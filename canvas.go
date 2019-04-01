// Package canvas provides an API that tries to closely mirror that
// of the HTML5 canvas API, using OpenGL to do the rendering.
package canvas

import (
	"fmt"
	"image/color"
	"os"

	"github.com/golang/freetype/truetype"
	"github.com/beta/canvas/backend/backendbase"
	"golang.org/x/image/font"
)

//go:generate go run make_shaders.go
//go:generate go fmt

// Canvas represents an area on the viewport on which to draw
// using a set of functions very similar to the HTML5 canvas
type Canvas struct {
	b backendbase.Backend

	path   Path2D
	convex bool
	rect   bool

	state      drawState
	stateStack []drawState

	images map[interface{}]*Image

	shadowBuf [][2]float64
}

type drawState struct {
	transform     mat
	fill          drawStyle
	stroke        drawStyle
	font          *Font
	fontSize      float64
	fontMetrics   font.Metrics
	textAlign     textAlign
	textBaseline  textBaseline
	lineAlpha     float64
	lineWidth     float64
	lineJoin      lineJoin
	lineCap       lineCap
	miterLimitSqr float64
	globalAlpha   float64

	lineDash       []float64
	lineDashPoint  int
	lineDashOffset float64

	clip Path2D

	shadowColor   color.RGBA
	shadowOffsetX float64
	shadowOffsetY float64
	shadowBlur    float64

	/*
		The current transformation matrix.
		The current clipping region.
		The current dash list.
		The current values of the following attributes: strokeStyle, fillStyle, globalAlpha,
			lineWidth, lineCap, lineJoin, miterLimit, lineDashOffset, shadowOffsetX,
			shadowOffsetY, shadowBlur, shadowColor, globalCompositeOperation, font,
			textAlign, textBaseline, direction, imageSmoothingEnabled
	*/
}

type drawStyle struct {
	color          color.RGBA
	radialGradient *RadialGradient
	linearGradient *LinearGradient
	imagePattern   *ImagePattern
}

type lineJoin uint8
type lineCap uint8

// Line join and end constants for SetLineJoin and SetLineCap
const (
	Miter = iota
	Bevel
	Round
	Square
	Butt
)

type textAlign uint8

// Text alignment constants for SetTextAlign
const (
	Left = iota
	Center
	Right
	Start
	End
)

type textBaseline uint8

// Text baseline constants for SetTextBaseline
const (
	Alphabetic = iota
	Top
	Hanging
	Middle
	Ideographic
	Bottom
)

// Performance is a nonstandard setting to improve the
// performance of the rendering in some circumstances.
// Disabling self intersections will lead to incorrect
// rendering of self intersecting polygons, but will
// yield better performance when not using the polygons
// are not self intersecting. Assuming convex polygons
// will break concave polygons, but improve performance
// even further
var Performance = struct {
	IgnoreSelfIntersections bool
	AssumeConvex            bool
}{}

// New creates a new canvas with the given viewport coordinates.
// While all functions on the canvas use the top left point as
// the origin, since GL uses the bottom left coordinate, the
// coordinates given here also use the bottom left as origin
func New(backend backendbase.Backend) *Canvas {
	cv := &Canvas{
		b:          backend,
		stateStack: make([]drawState, 0, 20),
		images:     make(map[interface{}]*Image),
	}
	cv.state.lineWidth = 1
	cv.state.lineAlpha = 1
	cv.state.miterLimitSqr = 100
	cv.state.globalAlpha = 1
	cv.state.fill.color = color.RGBA{A: 255}
	cv.state.stroke.color = color.RGBA{A: 255}
	cv.state.transform = matIdentity()
	return cv
}

// Width returns the internal width of the canvas
func (cv *Canvas) Width() int {
	w, _ := cv.b.Size()
	return w
}

// Height returns the internal height of the canvas
func (cv *Canvas) Height() int {
	_, h := cv.b.Size()
	return h
}

// Size returns the internal width and height of the canvas
func (cv *Canvas) Size() (int, int) { return cv.b.Size() }

func (cv *Canvas) tf(v vec) vec {
	return v.mulMat(cv.state.transform)
}

const alphaTexSize = 2048

type offscreenBuffer struct {
	tex              uint32
	w                int
	h                int
	renderStencilBuf uint32
	frameBuf         uint32
	alpha            bool
}

type gaussianShader struct {
	id          uint32
	vertex      uint32
	texCoord    uint32
	canvasSize  int32
	kernelScale int32
	image       int32
	kernel      int32
}

// SetFillStyle sets the color, gradient, or image for any fill calls. To set a
// color, there are several acceptable formats: 3 or 4 int values for RGB(A) in
// the range 0-255, 3 or 4 float values for RGB(A) in the range 0-1, hex strings
// in the format "#AABBCC", "#AABBCCDD", "#ABC", or "#ABCD"
func (cv *Canvas) SetFillStyle(value ...interface{}) {
	cv.state.fill = cv.parseStyle(value...)
}

// SetStrokeStyle sets the color, gradient, or image for any line drawing calls.
// To set a color, there are several acceptable formats: 3 or 4 int values for
// RGB(A) in the range 0-255, 3 or 4 float values for RGB(A) in the range 0-1,
// hex strings in the format "#AABBCC", "#AABBCCDD", "#ABC", or "#ABCD"
func (cv *Canvas) SetStrokeStyle(value ...interface{}) {
	cv.state.stroke = cv.parseStyle(value...)
}

var imagePatterns = make(map[interface{}]*ImagePattern)

func (cv *Canvas) parseStyle(value ...interface{}) drawStyle {
	var style drawStyle
	if len(value) == 1 {
		switch v := value[0].(type) {
		case *LinearGradient:
			style.linearGradient = v
			return style
		case *RadialGradient:
			style.radialGradient = v
			return style
		}
	}
	c, ok := parseColor(value...)
	if ok {
		style.color = c
	} else if len(value) == 1 {
		switch v := value[0].(type) {
		case *Image, string:
			if _, ok := imagePatterns[v]; !ok {
				imagePatterns[v] = cv.CreatePattern(v, "")
			}
			style.imagePattern = imagePatterns[v]
		case *ImagePattern:
			style.imagePattern = v
		}
	}
	return style
}

func (cv *Canvas) backendFillStyle(s *drawStyle, alpha float64) backendbase.FillStyle {
	stl := backendbase.FillStyle{Color: s.color}
	alpha *= cv.state.globalAlpha
	if lg := s.linearGradient; lg != nil {
		lg.load()
		stl.LinearGradient = lg.grad
		from := cv.tf(lg.from)
		to := cv.tf(lg.to)
		stl.Gradient.X0 = from[0]
		stl.Gradient.Y0 = from[1]
		stl.Gradient.X1 = to[0]
		stl.Gradient.Y1 = to[1]
	} else if rg := s.radialGradient; rg != nil {
		rg.load()
		from := cv.tf(rg.from)
		to := cv.tf(rg.to)
		stl.Gradient.X0 = from[0]
		stl.Gradient.Y0 = from[1]
		stl.Gradient.X1 = to[0]
		stl.Gradient.Y1 = to[1]
		stl.Gradient.RadFrom = rg.radFrom
		stl.Gradient.RadTo = rg.radTo
		stl.RadialGradient = rg.grad
	} else if ip := s.imagePattern; ip != nil {
		if ip.ip == nil {
			stl.Color = color.RGBA{}
		} else {
			stl.ImagePattern = ip.ip
		}
	} else {
		alpha *= float64(s.color.A) / 255
	}
	stl.Color.A = uint8(alpha * 255)
	return stl
}

// SetLineWidth sets the line width for any line drawing calls
func (cv *Canvas) SetLineWidth(width float64) {
	if width < 0 {
		cv.state.lineWidth = 1
		cv.state.lineAlpha = 0
	} else if width < 1 {
		cv.state.lineWidth = 1
		cv.state.lineAlpha = width
	} else {
		cv.state.lineWidth = width
		cv.state.lineAlpha = 1
	}
}

// SetFont sets the font and font size. The font parameter can be a font loaded
// with the LoadFont function, a filename for a font to load (which will be
// cached), or nil, in which case the first loaded font will be used
func (cv *Canvas) SetFont(src interface{}, size float64) {
	if src == nil {
		cv.state.font = defaultFont
	} else {
		switch v := src.(type) {
		case *Font:
			cv.state.font = v
		case *truetype.Font:
			cv.state.font = &Font{font: v}
		case string:
			if f, ok := fonts[v]; ok {
				cv.state.font = f
			} else {
				f, err := cv.LoadFont(v)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error loading font %s: %v\n", v, err)
					fonts[v] = nil
				} else {
					fonts[v] = f
					cv.state.font = f
				}
			}
		}
	}
	cv.state.fontSize = size

	fontFace := truetype.NewFace(cv.state.font.font, &truetype.Options{Size: size})
	cv.state.fontMetrics = fontFace.Metrics()
}

// SetTextAlign sets the text align for any text drawing calls.
// The value can be Left, Center, Right, Start, or End
func (cv *Canvas) SetTextAlign(align textAlign) {
	cv.state.textAlign = align
}

// SetTextBaseline sets the text baseline for any text drawing calls.
// The value can be Alphabetic (default), Top, Hanging, Middle,
// Ideographic, or Bottom
func (cv *Canvas) SetTextBaseline(baseline textBaseline) {
	cv.state.textBaseline = baseline
}

// SetLineJoin sets the style of line joints for rendering a path with Stroke.
// The value can be Miter, Bevel, or Round
func (cv *Canvas) SetLineJoin(join lineJoin) {
	cv.state.lineJoin = join
}

// SetLineCap sets the style of line endings for rendering a path with Stroke
// The value can be Butt, Square, or Round
func (cv *Canvas) SetLineCap(cap lineCap) {
	cv.state.lineCap = cap
}

// SetLineDash sets the line dash style
func (cv *Canvas) SetLineDash(dash []float64) {
	l := len(dash)
	if l%2 == 0 {
		d2 := make([]float64, l)
		copy(d2, dash)
		cv.state.lineDash = d2
	} else {
		d2 := make([]float64, l*2)
		copy(d2[:l], dash)
		copy(d2[l:], dash)
		cv.state.lineDash = d2
	}
	cv.state.lineDashPoint = 0
	cv.state.lineDashOffset = 0
}

func (cv *Canvas) SetLineDashOffset(offset float64) {
	cv.state.lineDashOffset = offset
}

// GetLineDash gets the line dash style
func (cv *Canvas) GetLineDash() []float64 {
	result := make([]float64, len(cv.state.lineDash))
	copy(result, cv.state.lineDash)
	return result
}

// SetMiterLimit sets the limit for how far a miter line join can be extend.
// The fallback is a bevel join
func (cv *Canvas) SetMiterLimit(limit float64) {
	cv.state.miterLimitSqr = limit * limit
}

// SetGlobalAlpha sets the global alpha value
func (cv *Canvas) SetGlobalAlpha(alpha float64) {
	cv.state.globalAlpha = alpha
}

// Save saves the current draw state to a stack
func (cv *Canvas) Save() {
	cv.stateStack = append(cv.stateStack, cv.state)
}

// Restore restores the last draw state from the stack if available
func (cv *Canvas) Restore() {
	l := len(cv.stateStack)
	if l <= 0 {
		return
	}
	cv.b.ClearClip()
	for _, st := range cv.stateStack {
		if len(st.clip.p) > 0 {
			cv.clip(&st.clip)
		}
	}
	cv.state = cv.stateStack[l-1]
	cv.stateStack = cv.stateStack[:l-1]
}

// Scale updates the current transformation with a scaling by the given values
func (cv *Canvas) Scale(x, y float64) {
	cv.state.transform = matScale(vec{x, y}).mul(cv.state.transform)
}

// Translate updates the current transformation with a translation by the given values
func (cv *Canvas) Translate(x, y float64) {
	cv.state.transform = matTranslate(vec{x, y}).mul(cv.state.transform)
}

// Rotate updates the current transformation with a rotation by the given angle
func (cv *Canvas) Rotate(angle float64) {
	cv.state.transform = matRotate(angle).mul(cv.state.transform)
}

// Transform updates the current transformation with the given matrix
func (cv *Canvas) Transform(a, b, c, d, e, f float64) {
	cv.state.transform = mat{a, b, c, d, e, f}.mul(cv.state.transform)
}

// SetTransform replaces the current transformation with the given matrix
func (cv *Canvas) SetTransform(a, b, c, d, e, f float64) {
	cv.state.transform = mat{a, b, c, d, e, f}
}

// SetShadowColor sets the color of the shadow. If it is fully transparent (default)
// then no shadow is drawn
func (cv *Canvas) SetShadowColor(color ...interface{}) {
	if c, ok := parseColor(color...); ok {
		cv.state.shadowColor = c
	}
}

// SetShadowOffsetX sets the x offset of the shadow
func (cv *Canvas) SetShadowOffsetX(offset float64) {
	cv.state.shadowOffsetX = offset
}

// SetShadowOffsetY sets the y offset of the shadow
func (cv *Canvas) SetShadowOffsetY(offset float64) {
	cv.state.shadowOffsetY = offset
}

// SetShadowOffset sets the offset of the shadow
func (cv *Canvas) SetShadowOffset(x, y float64) {
	cv.state.shadowOffsetX = x
	cv.state.shadowOffsetY = y
}

// SetShadowBlur sets the gaussian blur radius of the shadow
// (0 for no blur)
func (cv *Canvas) SetShadowBlur(r float64) {
	cv.state.shadowBlur = r
}
