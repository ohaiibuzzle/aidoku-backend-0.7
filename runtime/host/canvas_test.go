package host

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/fogleman/gg"
)

func TestCanvasFillPath(t *testing.T) {
	gc := gg.NewContext(10, 10)
	path := &canvasPath{Ops: []canvasPathOp{
		{Kind: pathOpMoveTo, P1: canvasPoint{X: 0, Y: 0}},
		{Kind: pathOpLineTo, P1: canvasPoint{X: 10, Y: 0}},
		{Kind: pathOpLineTo, P1: canvasPoint{X: 10, Y: 10}},
		{Kind: pathOpLineTo, P1: canvasPoint{X: 0, Y: 10}},
		{Kind: pathOpClose},
	}}
	applyPath(gc, path)
	gc.SetRGBA(1, 0, 0, 1)
	gc.Fill()

	img := gc.Image()
	r, g, b, a := img.At(5, 5).RGBA()
	if r>>8 != 255 || g>>8 != 0 || b>>8 != 0 || a>>8 != 255 {
		t.Fatalf("expected opaque red at (5,5), got r=%d g=%d b=%d a=%d", r>>8, g>>8, b>>8, a>>8)
	}
}

func TestCanvasGetImageDataProducesValidPNG(t *testing.T) {
	gc := gg.NewContext(4, 4)
	gc.SetRGBA(0, 1, 0, 1)
	gc.DrawRectangle(0, 0, 4, 4)
	gc.Fill()

	var buf bytes.Buffer
	if err := png.Encode(&buf, gc.Image()); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	decoded, err := png.Decode(&buf)
	if err != nil {
		t.Fatalf("png.Decode: %v", err)
	}
	if decoded.Bounds().Dx() != 4 || decoded.Bounds().Dy() != 4 {
		t.Fatalf("unexpected decoded bounds: %v", decoded.Bounds())
	}
}

func TestCanvasDrawScaled(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			src.Set(x, y, color.RGBA{B: 255, A: 255})
		}
	}
	gc := gg.NewContext(8, 8)
	drawScaled(gc, src, 0, 0, 8, 8)

	_, _, b, a := gc.Image().At(4, 4).RGBA()
	if b>>8 != 255 || a>>8 != 255 {
		t.Fatalf("expected scaled blue fill at center, got b=%d a=%d", b>>8, a>>8)
	}
}
