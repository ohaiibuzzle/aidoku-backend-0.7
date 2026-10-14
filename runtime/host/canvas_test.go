package host

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"strings"
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

func TestValidCanvasDimension(t *testing.T) {
	cases := []struct {
		name string
		v    float32
		want bool
	}{
		{"typical page width", 1200, true},
		{"zero", 0, false},
		{"negative", -1, false},
		{"at cap", maxCanvasDimension, true},
		{"over cap", maxCanvasDimension + 1, false},
		{"NaN", float32(math.NaN()), false},
		{"positive infinity", float32(math.Inf(1)), false},
		{"negative infinity", float32(math.Inf(-1)), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := validCanvasDimension(c.v); got != c.want {
				t.Errorf("validCanvasDimension(%v) = %v, want %v", c.v, got, c.want)
			}
		})
	}
}

// TestLoadBytesRejectsLocalPaths guards against a guest-controlled font
// location (canvas.load_font) reading arbitrary local files: aidoku-run
// runs unsandboxed as the invoking user, so a bare path or "file://" URL
// used to let any source read anything that user can read (SSH keys,
// other app data, ...).
func TestLoadBytesRejectsLocalPaths(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "secret-*.txt")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	if _, err := f.WriteString("do not read me"); err != nil {
		t.Fatalf("WriteString: %v", err)
	}
	f.Close()

	c := &Canvas{Store: NewStore()}
	for _, loc := range []string{f.Name(), "file://" + f.Name(), "/etc/passwd"} {
		if _, err := c.loadBytes(loc); err == nil {
			t.Errorf("loadBytes(%q): expected an error, got nil", loc)
		}
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

func TestValidDashes(t *testing.T) {
	for _, in := range [][]float32{nil, {0}, {0, 0}, {0.1, 0.1}, {-1, 5}, {float32(math.NaN()), 5}, {float32(math.Inf(1))}} {
		if _, ok := validDashes(in); ok {
			t.Errorf("validDashes(%v) accepted", in)
		}
	}
	if d, ok := validDashes([]float32{4, 2}); !ok || len(d) != 2 {
		t.Errorf("validDashes([4 2]) = %v, %v", d, ok)
	}
}

func TestDecodeImageBoundedRejectsHugeDeclaredSize(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	if _, err := decodeImageBounded(data); err != nil {
		t.Fatalf("1x1 PNG rejected: %v", err)
	}
	// Patch IHDR width/height (bytes 16..23) to 20000x20000 and fix up its
	// CRC: a tiny file whose header claims 1.6GB of RGBA.
	huge := bytes.Clone(data)
	for i, v := range []byte{0, 0, 0x4e, 0x20, 0, 0, 0x4e, 0x20} {
		huge[16+i] = v
	}
	binary.BigEndian.PutUint32(huge[29:], crc32.ChecksumIEEE(huge[12:29]))
	if _, err := decodeImageBounded(huge); err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Fatalf("20000x20000 PNG: err = %v, want out-of-range", err)
	}
}
