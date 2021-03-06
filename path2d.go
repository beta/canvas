package canvas

import (
	"math"
)

type Path2D struct {
	p     []pathPoint
	move  vec
	cwSum float64
}

type pathPoint struct {
	pos   vec
	next  vec
	flags pathPointFlag
}

type pathPointFlag uint8

const (
	pathMove pathPointFlag = 1 << iota
	pathAttach
	pathIsRect
	pathIsConvex
	pathIsClockwise
	pathSelfIntersects
)

// NewPath2D creates a new Path2D and returns it
func NewPath2D() *Path2D {
	return &Path2D{p: make([]pathPoint, 0, 20)}
}

// func (p *Path2D) AddPath(p2 *Path2D) {
// }

// MoveTo (see equivalent function on canvas type)
func (p *Path2D) MoveTo(x, y float64) {
	if len(p.p) > 0 && isSamePoint(p.p[len(p.p)-1].pos, vec{x, y}, 0.1) {
		return
	}
	p.p = append(p.p, pathPoint{pos: vec{x, y}, flags: pathMove}) // todo more flags probably
	p.cwSum = 0
	p.move = vec{x, y}
}

// LineTo (see equivalent function on canvas type)
func (p *Path2D) LineTo(x, y float64) {
	p.lineTo(x, y, true)
}

func (p *Path2D) lineTo(x, y float64, checkSelfIntersection bool) {
	count := len(p.p)
	if count > 0 && isSamePoint(p.p[len(p.p)-1].pos, vec{x, y}, 0.1) {
		return
	}
	if count == 0 {
		p.MoveTo(x, y)
		return
	}
	prev := &p.p[count-1]
	prev.next = vec{x, y}
	prev.flags |= pathAttach
	p.p = append(p.p, pathPoint{pos: vec{x, y}})
	newp := &p.p[count]

	px, py := prev.pos[0], prev.pos[1]
	p.cwSum += (x - px) * (y + py)
	cwTotal := p.cwSum
	cwTotal += (p.move[0] - x) * (p.move[1] + y)
	if cwTotal <= 0 {
		newp.flags |= pathIsClockwise
	}

	if prev.flags&pathSelfIntersects > 0 {
		newp.flags |= pathSelfIntersects
	}

	if len(p.p) < 4 || Performance.AssumeConvex {
		newp.flags |= pathIsConvex
	} else if prev.flags&pathIsConvex > 0 {
		cuts := false
		if checkSelfIntersection && !Performance.IgnoreSelfIntersections {
			b0, b1 := prev.pos, vec{x, y}
			for i := 1; i < count; i++ {
				a0, a1 := p.p[i-1].pos, p.p[i].pos
				_, r1, r2 := lineIntersection(a0, a1, b0, b1)
				if r1 > 0 && r1 < 1 && r2 > 0 && r2 < 1 {
					cuts = true
					break
				}
			}
		}
		if cuts {
			newp.flags |= pathSelfIntersects
		} else {
			prev2 := &p.p[len(p.p)-3]
			cw := (newp.flags & pathIsClockwise) > 0

			ln := prev.pos.sub(prev2.pos)
			lo := vec{ln[1], -ln[0]}
			dot := newp.pos.sub(prev2.pos).dot(lo)

			if (cw && dot <= 0) || (!cw && dot >= 0) {
				newp.flags |= pathIsConvex
			}
		}
	}
}

// Arc (see equivalent function on canvas type)
func (p *Path2D) Arc(x, y, radius, startAngle, endAngle float64, anticlockwise bool) {
	checkSelfIntersection := len(p.p) > 0

	lastWasMove := len(p.p) == 0 || p.p[len(p.p)-1].flags&pathMove != 0

	if endAngle == startAngle {
		s, c := math.Sincos(endAngle)
		p.lineTo(x+radius*c, y+radius*s, checkSelfIntersection)

		if lastWasMove {
			p.p[len(p.p)-1].flags |= pathIsConvex
		}

		return
	}

	if (!anticlockwise && endAngle < startAngle) || (anticlockwise && endAngle > startAngle) {
		endAngle, startAngle = startAngle, endAngle
	}

	if !anticlockwise {
		diff := endAngle - startAngle
		if diff >= math.Pi*4 {
			diff = math.Mod(diff, math.Pi*2) + math.Pi*2
			endAngle = startAngle + diff
		}
	} else {
		diff := startAngle - endAngle
		if diff >= math.Pi*4 {
			diff = math.Mod(diff, math.Pi*2)
			endAngle = startAngle - diff
		}
	}

	const step = math.Pi * 2 / 90
	if !anticlockwise {
		for a := startAngle; a < endAngle; a += step {
			s, c := math.Sincos(a)
			p.lineTo(x+radius*c, y+radius*s, checkSelfIntersection)
		}
	} else {
		for a := startAngle; a > endAngle; a -= step {
			s, c := math.Sincos(a)
			p.lineTo(x+radius*c, y+radius*s, checkSelfIntersection)
		}
	}
	s, c := math.Sincos(endAngle)
	p.lineTo(x+radius*c, y+radius*s, checkSelfIntersection)

	if lastWasMove {
		p.p[len(p.p)-1].flags |= pathIsConvex
	}
}

// ArcTo (see equivalent function on canvas type)
func (p *Path2D) ArcTo(x1, y1, x2, y2, radius float64) {
	if len(p.p) == 0 {
		return
	}
	p0, p1, p2 := p.p[len(p.p)-1].pos, vec{x1, y1}, vec{x2, y2}
	v0, v1 := p0.sub(p1).norm(), p2.sub(p1).norm()
	angle := math.Acos(v0.dot(v1))
	// should be in the range [0-pi]. if parallel, use a straight line
	if angle <= 0 || angle >= math.Pi {
		p.LineTo(x2, y2)
		return
	}
	// cv0 and cv1 are vectors that point to the center of the circle
	cv0 := vec{-v0[1], v0[0]}
	cv1 := vec{v1[1], -v1[0]}
	x := cv1.sub(cv0).div(v0.sub(v1))[0] * radius
	if x < 0 {
		cv0 = cv0.mulf(-1)
		cv1 = cv1.mulf(-1)
	}
	center := p1.add(v0.mulf(math.Abs(x))).add(cv0.mulf(radius))
	a0, a1 := cv0.mulf(-1).atan2(), cv1.mulf(-1).atan2()
	if x > 0 {
		if a1-a0 > 0 {
			a0 += math.Pi * 2
		}
	} else {
		if a0-a1 > 0 {
			a1 += math.Pi * 2
		}
	}
	p.Arc(center[0], center[1], radius, a0, a1, x > 0)
}

// QuadraticCurveTo (see equivalent function on canvas type)
func (p *Path2D) QuadraticCurveTo(x1, y1, x2, y2 float64) {
	if len(p.p) == 0 {
		return
	}
	p0 := p.p[len(p.p)-1].pos
	p1 := vec{x1, y1}
	p2 := vec{x2, y2}
	v0 := p1.sub(p0)
	v1 := p2.sub(p1)

	const step = 0.01

	for r := 0.0; r < 1; r += step {
		i0 := v0.mulf(r).add(p0)
		i1 := v1.mulf(r).add(p1)
		pt := i1.sub(i0).mulf(r).add(i0)
		p.LineTo(pt[0], pt[1])
	}
	p.LineTo(x2, y2)
}

// BezierCurveTo (see equivalent function on canvas type)
func (p *Path2D) BezierCurveTo(x1, y1, x2, y2, x3, y3 float64) {
	if len(p.p) == 0 {
		return
	}
	p0 := p.p[len(p.p)-1].pos
	p1 := vec{x1, y1}
	p2 := vec{x2, y2}
	p3 := vec{x3, y3}
	v0 := p1.sub(p0)
	v1 := p2.sub(p1)
	v2 := p3.sub(p2)

	const step = 0.01

	for r := 0.0; r < 1; r += step {
		i0 := v0.mulf(r).add(p0)
		i1 := v1.mulf(r).add(p1)
		i2 := v2.mulf(r).add(p2)
		iv0 := i1.sub(i0)
		iv1 := i2.sub(i1)
		j0 := iv0.mulf(r).add(i0)
		j1 := iv1.mulf(r).add(i1)
		pt := j1.sub(j0).mulf(r).add(j0)
		p.LineTo(pt[0], pt[1])
	}
	p.LineTo(x3, y3)
}

// ClosePath (see equivalent function on canvas type)
func (p *Path2D) ClosePath() {
	if len(p.p) < 2 {
		return
	}
	if isSamePoint(p.p[len(p.p)-1].pos, p.p[0].pos, 0.1) {
		return
	}
	closeIdx := 0
	for i := len(p.p) - 1; i >= 0; i-- {
		if p.p[i].flags&pathMove != 0 {
			closeIdx = i
			break
		}
	}
	p.LineTo(p.p[closeIdx].pos[0], p.p[closeIdx].pos[1])
	p.p[len(p.p)-1].next = p.p[closeIdx].next
	p.p[len(p.p)-1].flags |= pathAttach
}

// Rect (see equivalent function on canvas type)
func (p *Path2D) Rect(x, y, w, h float64) {
	lastWasMove := len(p.p) == 0 || p.p[len(p.p)-1].flags&pathMove != 0
	p.MoveTo(x, y)
	p.LineTo(x+w, y)
	p.LineTo(x+w, y+h)
	p.LineTo(x, y+h)
	p.LineTo(x, y)
	if lastWasMove {
		p.p[len(p.p)-1].flags |= pathIsRect
		p.p[len(p.p)-1].flags |= pathIsConvex
	}
}

// func (p *Path2D) Ellipse(...) {
// }
