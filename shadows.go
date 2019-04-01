package canvas

import (
	"image"

	"github.com/beta/canvas/backend/backendbase"
)

func (cv *Canvas) drawShadow2(pts [][2]float64, mask *image.Alpha) {
	if cv.state.shadowColor.A == 0 {
		return
	}
	if cv.state.shadowOffsetX == 0 && cv.state.shadowOffsetY == 0 {
		return
	}

	if cv.shadowBuf == nil || cap(cv.shadowBuf) < len(pts) {
		cv.shadowBuf = make([][2]float64, 0, len(pts)+1000)
	}
	cv.shadowBuf = cv.shadowBuf[:0]

	for _, pt := range pts {
		cv.shadowBuf = append(cv.shadowBuf, [2]float64{
			pt[0] + cv.state.shadowOffsetX,
			pt[1] + cv.state.shadowOffsetY,
		})
	}

	style := backendbase.FillStyle{Color: cv.state.shadowColor, Blur: cv.state.shadowBlur}
	if mask != nil {
		if len(cv.shadowBuf) != 4 {
			panic("invalid number of points to fill with mask, must be 4")
		}
		cv.b.FillImageMask(&style, mask, cv.shadowBuf)
	} else {
		cv.b.Fill(&style, cv.shadowBuf)
	}
}
