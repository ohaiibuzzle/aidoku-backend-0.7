package host

import (
	"fmt"

	"github.com/ohaiibuzzle/aidokurunner-go/postcard"
)

// This file mirrors the private drawing types at the bottom of
// AidokuRunner's Canvas.swift (Point, PathOp, Path, LineCap, LineJoin,
// Color, StrokeStyle). They're postcard-decode-only, read directly from
// guest memory (not the Store) via the same [length:u32][pad:u32][data]
// convention used by defaults.set — see readGuestBuffer.

type canvasPoint struct {
	X, Y float32
}

func (p *canvasPoint) decode(r *postcard.Reader) error {
	var err error
	if p.X, err = r.ReadF32(); err != nil {
		return err
	}
	if p.Y, err = r.ReadF32(); err != nil {
		return err
	}
	return nil
}

type canvasPathOpKind uint8

const (
	pathOpMoveTo canvasPathOpKind = iota
	pathOpLineTo
	pathOpQuadTo
	pathOpCubicTo
	pathOpArc
	pathOpClose
)

type canvasPathOp struct {
	Kind                      canvasPathOpKind
	P1, P2, P3                canvasPoint
	Radius, StartAngle, Sweep float32
}

func (op *canvasPathOp) decode(r *postcard.Reader) error {
	kind, err := r.ReadU8()
	if err != nil {
		return err
	}
	op.Kind = canvasPathOpKind(kind)
	switch op.Kind {
	case pathOpMoveTo, pathOpLineTo:
		return op.P1.decode(r)
	case pathOpQuadTo:
		if err := op.P1.decode(r); err != nil {
			return err
		}
		return op.P2.decode(r)
	case pathOpCubicTo:
		if err := op.P1.decode(r); err != nil {
			return err
		}
		if err := op.P2.decode(r); err != nil {
			return err
		}
		return op.P3.decode(r)
	case pathOpArc:
		if err := op.P1.decode(r); err != nil {
			return err
		}
		if op.Radius, err = r.ReadF32(); err != nil {
			return err
		}
		if op.StartAngle, err = r.ReadF32(); err != nil {
			return err
		}
		if op.Sweep, err = r.ReadF32(); err != nil {
			return err
		}
		return nil
	case pathOpClose:
		return nil
	default:
		return fmt.Errorf("canvas: unknown path op kind %d", kind)
	}
}

type canvasPath struct {
	Ops []canvasPathOp
}

func decodeCanvasPath(data []byte) (*canvasPath, error) {
	r := postcard.NewReader(data)
	n, err := r.ReadLen()
	if err != nil {
		return nil, err
	}
	p := &canvasPath{Ops: make([]canvasPathOp, n)}
	for i := 0; i < n; i++ {
		if err := p.Ops[i].decode(r); err != nil {
			return nil, err
		}
	}
	return p, nil
}

type canvasColor struct {
	R, G, B, A float32
}

func (c *canvasColor) decode(r *postcard.Reader) error {
	var err error
	if c.R, err = r.ReadF32(); err != nil {
		return err
	}
	if c.G, err = r.ReadF32(); err != nil {
		return err
	}
	if c.B, err = r.ReadF32(); err != nil {
		return err
	}
	if c.A, err = r.ReadF32(); err != nil {
		return err
	}
	return nil
}

type canvasLineCap uint8

const (
	lineCapRound canvasLineCap = iota
	lineCapSquare
	lineCapButt
)

type canvasLineJoin uint8

const (
	lineJoinRound canvasLineJoin = iota
	lineJoinBevel
	lineJoinMiter
)

type canvasStrokeStyle struct {
	Color      canvasColor
	Width      float32
	Cap        canvasLineCap
	Join       canvasLineJoin
	MiterLimit float32
	DashArray  []float32
	DashOffset float32
}

func decodeCanvasStrokeStyle(data []byte) (*canvasStrokeStyle, error) {
	r := postcard.NewReader(data)
	s := &canvasStrokeStyle{}
	if err := s.Color.decode(r); err != nil {
		return nil, err
	}
	var err error
	if s.Width, err = r.ReadF32(); err != nil {
		return nil, err
	}
	capByte, err := r.ReadU8()
	if err != nil {
		return nil, err
	}
	s.Cap = canvasLineCap(capByte)
	joinByte, err := r.ReadU8()
	if err != nil {
		return nil, err
	}
	s.Join = canvasLineJoin(joinByte)
	if s.MiterLimit, err = r.ReadF32(); err != nil {
		return nil, err
	}
	n, err := r.ReadLen()
	if err != nil {
		return nil, err
	}
	s.DashArray = make([]float32, n)
	for i := 0; i < n; i++ {
		if s.DashArray[i], err = r.ReadF32(); err != nil {
			return nil, err
		}
	}
	if s.DashOffset, err = r.ReadF32(); err != nil {
		return nil, err
	}
	return s, nil
}
