package host

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/gif"
	"image/png"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/fogleman/gg"
	"github.com/golang/freetype/truetype"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	_ "golang.org/x/image/webp" // register WebP decoder

	_ "image/jpeg" // register JPEG decoder
)

type canvasResult int32

const (
	canvasSuccess         canvasResult = 0
	canvasInvalidContext  canvasResult = -1
	canvasInvalidImagePtr canvasResult = -2
	canvasInvalidImage    canvasResult = -3
	canvasInvalidSrcRect  canvasResult = -4
	canvasInvalidResult   canvasResult = -5
	canvasInvalidBounds   canvasResult = -6
	canvasInvalidPath     canvasResult = -7
	canvasInvalidStyle    canvasResult = -8
	canvasInvalidString   canvasResult = -9
	canvasInvalidFont     canvasResult = -10
	canvasInvalidData     canvasResult = -11
	canvasFontLoadFailed  canvasResult = -12
)

// Canvas implements the `canvas` namespace (Imports/Canvas.swift): 2D
// drawing used by a minority of sources to descramble/reassemble split
// page images. Backed by github.com/fogleman/gg (pure Go, no cgo) instead
// of CoreGraphics. gg's coordinate system is already top-left/y-down, so
// unlike Canvas.swift we don't need to flip before drawing.
type Canvas struct {
	Store *Store

	// HTTPClient is used by load_font for http(s) URLs. Defaults to
	// http.DefaultClient.
	HTTPClient *http.Client
}

func (c *Canvas) client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

// DecodeImage implements the callback Net expects for its `get_image`
// binding.
func (c *Canvas) DecodeImage(data []byte) (int32, bool) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return 0, false
	}
	return c.Store.Store(img), true
}

func (c *Canvas) fetchImage(descriptor int32) image.Image {
	switch v := c.Store.Fetch(descriptor).(type) {
	case image.Image:
		return v
	case []byte:
		img, _, err := image.Decode(bytes.NewReader(v))
		if err != nil {
			return nil
		}
		return img
	default:
		return nil
	}
}

// maxCanvasDimension caps a single new_context axis against a guest
// passing a deliberately huge or garbage dimension. A source descrambling
// a scanned page never needs anywhere near this; the cap exists to bound
// gg.NewContext's image.NewRGBA allocation (4 bytes/pixel).
const maxCanvasDimension = 8192

// validCanvasDimension rejects non-finite and out-of-range new_context
// dimensions. NaN compares false against every relation, including <= 0
// and > maxCanvasDimension, so it has to be checked explicitly -- it would
// otherwise slip through both of those and reach int(v) downstream.
func validCanvasDimension(v float32) bool {
	if math.IsNaN(float64(v)) {
		return false
	}
	return v > 0 && v <= maxCanvasDimension
}

// LinkCanvas registers the `canvas` namespace onto the given host module
// builder.
func LinkCanvas(builder wazero.HostModuleBuilder, c *Canvas) wazero.HostModuleBuilder {
	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, width, height float32) int32 {
			if !validCanvasDimension(width) || !validCanvasDimension(height) {
				return int32(canvasInvalidBounds)
			}
			gc := gg.NewContext(int(width), int(height))
			return c.Store.Store(gc)
		}).
		Export("new_context")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, contextPtr int32, translateX, translateY, scaleX, scaleY, rotateAngle float32) int32 {
			gc, ok := c.Store.Fetch(contextPtr).(*gg.Context)
			if !ok {
				return int32(canvasInvalidContext)
			}
			gc.Identity()
			gc.Translate(float64(translateX), float64(translateY))
			gc.Scale(float64(scaleX), float64(scaleY))
			gc.Rotate(float64(rotateAngle))
			return int32(canvasSuccess)
		}).
		Export("set_transform")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, contextPtr, imagePtr int32, dstX, dstY, dstWidth, dstHeight float32) int32 {
			gc, ok := c.Store.Fetch(contextPtr).(*gg.Context)
			if !ok {
				return int32(canvasInvalidContext)
			}
			img := c.fetchImage(imagePtr)
			if img == nil {
				return int32(canvasInvalidImagePtr)
			}
			drawScaled(gc, img, float64(dstX), float64(dstY), float64(dstWidth), float64(dstHeight))
			return int32(canvasSuccess)
		}).
		Export("draw_image")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(
			ctx context.Context,
			contextPtr, imagePtr int32,
			srcX, srcY, srcWidth, srcHeight, dstX, dstY, dstWidth, dstHeight float32,
		) int32 {
			gc, ok := c.Store.Fetch(contextPtr).(*gg.Context)
			if !ok {
				return int32(canvasInvalidContext)
			}
			img := c.fetchImage(imagePtr)
			if img == nil {
				return int32(canvasInvalidImagePtr)
			}
			srcRect := image.Rect(int(srcX), int(srcY), int(srcX+srcWidth), int(srcY+srcHeight))
			if !srcRect.In(img.Bounds()) {
				return int32(canvasInvalidSrcRect)
			}
			cropped := image.NewRGBA(image.Rect(0, 0, srcRect.Dx(), srcRect.Dy()))
			for y := 0; y < srcRect.Dy(); y++ {
				for x := 0; x < srcRect.Dx(); x++ {
					cropped.Set(x, y, img.At(srcRect.Min.X+x, srcRect.Min.Y+y))
				}
			}
			drawScaled(gc, cropped, float64(dstX), float64(dstY), float64(dstWidth), float64(dstHeight))
			return int32(canvasSuccess)
		}).
		Export("copy_image")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, contextPtr, pathPtr int32, r, g, b, a float32) int32 {
			gc, ok := c.Store.Fetch(contextPtr).(*gg.Context)
			if !ok {
				return int32(canvasInvalidContext)
			}
			data, ok := readGuestBuffer(m, pathPtr)
			if !ok {
				return int32(canvasInvalidPath)
			}
			path, err := decodeCanvasPath(data)
			if err != nil {
				return int32(canvasInvalidPath)
			}
			gc.ClearPath()
			applyPath(gc, path)
			gc.SetRGBA(float64(r), float64(g), float64(b), float64(a))
			gc.Fill()
			return int32(canvasSuccess)
		}).
		Export("fill")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, contextPtr, pathPtr, stylePtr int32) int32 {
			gc, ok := c.Store.Fetch(contextPtr).(*gg.Context)
			if !ok {
				return int32(canvasInvalidContext)
			}
			pathData, ok := readGuestBuffer(m, pathPtr)
			if !ok {
				return int32(canvasInvalidPath)
			}
			path, err := decodeCanvasPath(pathData)
			if err != nil {
				return int32(canvasInvalidPath)
			}
			styleData, ok := readGuestBuffer(m, stylePtr)
			if !ok {
				return int32(canvasInvalidStyle)
			}
			style, err := decodeCanvasStrokeStyle(styleData)
			if err != nil {
				return int32(canvasInvalidStyle)
			}
			gc.ClearPath()
			applyPath(gc, path)
			applyStrokeStyle(gc, style)
			gc.Stroke()
			return int32(canvasSuccess)
		}).
		Export("stroke")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(
			ctx context.Context, m api.Module,
			contextPtr, textPtr, textLen int32,
			size, x, y float32,
			fontPtr int32,
			r, g, b, a float32,
		) int32 {
			gc, ok := c.Store.Fetch(contextPtr).(*gg.Context)
			if !ok {
				return int32(canvasInvalidContext)
			}
			ttFont, ok := c.Store.Fetch(fontPtr).(*truetype.Font)
			if !ok {
				return int32(canvasInvalidFont)
			}
			text, ok := readMemString(m, textPtr, textLen)
			if !ok {
				return int32(canvasInvalidString)
			}
			face := truetype.NewFace(ttFont, &truetype.Options{Size: float64(size), DPI: 72, Hinting: font.HintingFull})
			gc.SetFontFace(face)
			gc.SetRGBA(float64(r), float64(g), float64(b), float64(a))
			gc.DrawString(text, float64(x), float64(y)+float64(size))
			return int32(canvasSuccess)
		}).
		Export("draw_text")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, contextPtr int32) int32 {
			gc, ok := c.Store.Fetch(contextPtr).(*gg.Context)
			if !ok {
				return int32(canvasInvalidContext)
			}
			return c.Store.Store(gc.Image())
		}).
		Export("get_image")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, namePtr, nameLen int32) int32 {
			// No system font lookup is available on a headless Kindle
			// target; fall back to the embedded default, same as
			// system_font. The requested name is ignored.
			return c.storeEmbeddedFont(false)
		}).
		Export("new_font")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, weight int32) int32 {
			return c.storeEmbeddedFont(weight >= 5) // semibold(5)+ -> bold face
		}).
		Export("system_font")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, urlPtr, urlLen int32) int32 {
			urlStr, ok := readMemString(m, urlPtr, urlLen)
			if !ok {
				return int32(canvasInvalidString)
			}
			data, err := c.loadBytes(urlStr)
			if err != nil {
				return int32(canvasFontLoadFailed)
			}
			parsed, err := truetype.Parse(data)
			if err != nil {
				return int32(canvasFontLoadFailed)
			}
			return c.Store.Store(parsed)
		}).
		Export("load_font")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, dataPtr, dataLen int32) int32 {
			data, ok := m.Memory().Read(uint32(dataPtr), uint32(dataLen))
			if !ok {
				return int32(canvasInvalidData)
			}
			img, _, err := image.Decode(bytes.NewReader(data))
			if err != nil {
				return int32(canvasInvalidImage)
			}
			return c.Store.Store(img)
		}).
		Export("new_image")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, imagePtr int32) int32 {
			switch v := c.Store.Fetch(imagePtr).(type) {
			case image.Image:
				var buf bytes.Buffer
				if err := png.Encode(&buf, v); err != nil {
					return int32(canvasInvalidImage)
				}
				return c.Store.Store(buf.Bytes())
			case []byte:
				cp := make([]byte, len(v))
				copy(cp, v)
				return c.Store.Store(cp)
			default:
				return int32(canvasInvalidImagePtr)
			}
		}).
		Export("get_image_data")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, imagePtr int32) float32 {
			img := c.fetchImage(imagePtr)
			if img == nil {
				return float32(canvasInvalidImagePtr)
			}
			return float32(img.Bounds().Dx())
		}).
		Export("get_image_width")

	builder = builder.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, imagePtr int32) float32 {
			img := c.fetchImage(imagePtr)
			if img == nil {
				return float32(canvasInvalidImagePtr)
			}
			return float32(img.Bounds().Dy())
		}).
		Export("get_image_height")

	return builder
}

func (c *Canvas) storeEmbeddedFont(bold bool) int32 {
	src := goregular.TTF
	if bold {
		src = gobold.TTF
	}
	parsed, err := truetype.Parse(src)
	if err != nil {
		return int32(canvasFontLoadFailed)
	}
	return c.Store.Store(parsed)
}

// loadBytes fetches a font for canvas.load_font. Only http(s) is
// supported: location is a guest-controlled string (a manga source's own
// wasm code), and aidoku-run runs unsandboxed as the invoking user with
// real filesystem access -- a local-file fallback here would let any
// source read arbitrary files readable by that user (SSH keys, other
// app data, ...) by passing a bare path or "file://" URL. There's no
// legitimate reason a source needs that: every real-world source
// references a remote CDN font URL.
func (c *Canvas) loadBytes(location string) ([]byte, error) {
	if !strings.HasPrefix(location, "http://") && !strings.HasPrefix(location, "https://") {
		return nil, fmt.Errorf("canvas: unsupported font location %q (only http/https are allowed)", location)
	}
	client := c.client()
	req, err := http.NewRequest(http.MethodGet, location, nil)
	if err != nil {
		return nil, err
	}
	httpClient := client
	if httpClient.Timeout == 0 {
		httpClient = &http.Client{Timeout: 30 * time.Second, Transport: client.Transport, Jar: client.Jar, CheckRedirect: client.CheckRedirect}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func drawScaled(gc *gg.Context, img image.Image, dstX, dstY, dstWidth, dstHeight float64) {
	bounds := img.Bounds()
	if bounds.Dx() == 0 || bounds.Dy() == 0 || dstWidth <= 0 || dstHeight <= 0 {
		return
	}
	gc.Push()
	gc.Translate(dstX, dstY)
	gc.Scale(dstWidth/float64(bounds.Dx()), dstHeight/float64(bounds.Dy()))
	gc.DrawImage(img, 0, 0)
	gc.Pop()
}

func applyPath(gc *gg.Context, path *canvasPath) {
	for _, op := range path.Ops {
		switch op.Kind {
		case pathOpMoveTo:
			gc.MoveTo(float64(op.P1.X), float64(op.P1.Y))
		case pathOpLineTo:
			gc.LineTo(float64(op.P1.X), float64(op.P1.Y))
		case pathOpQuadTo:
			gc.QuadraticTo(float64(op.P1.X), float64(op.P1.Y), float64(op.P2.X), float64(op.P2.Y))
		case pathOpCubicTo:
			gc.CubicTo(float64(op.P1.X), float64(op.P1.Y), float64(op.P2.X), float64(op.P2.Y), float64(op.P3.X), float64(op.P3.Y))
		case pathOpArc:
			// Approximates CoreGraphics' addArc(clockwise:); gg's DrawArc
			// always sweeps from angle1 to angle2 in increasing-angle
			// order, so we don't replicate the clockwise flag precisely.
			gc.DrawArc(float64(op.P1.X), float64(op.P1.Y), float64(op.Radius), float64(op.StartAngle), float64(op.StartAngle+op.Sweep))
		case pathOpClose:
			gc.ClosePath()
		}
	}
}

func applyStrokeStyle(gc *gg.Context, style *canvasStrokeStyle) {
	gc.SetRGBA(float64(style.Color.R), float64(style.Color.G), float64(style.Color.B), float64(style.Color.A))
	gc.SetLineWidth(float64(style.Width))
	switch style.Cap {
	case lineCapRound:
		gc.SetLineCapRound()
	case lineCapSquare:
		gc.SetLineCapSquare()
	case lineCapButt:
		gc.SetLineCapButt()
	}
	switch style.Join {
	case lineJoinRound:
		gc.SetLineJoinRound()
	case lineJoinBevel:
		gc.SetLineJoinBevel()
	case lineJoinMiter:
		// gg has no true miter join; round is the closest available.
		gc.SetLineJoinRound()
	}
	if len(style.DashArray) > 0 {
		dashes := make([]float64, len(style.DashArray))
		for i, d := range style.DashArray {
			dashes[i] = float64(d)
		}
		gc.SetDash(dashes...)
		gc.SetDashOffset(float64(style.DashOffset))
	}
}
