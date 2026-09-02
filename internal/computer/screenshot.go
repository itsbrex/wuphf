package computer

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"strings"
	"time"
)

// Frame is one captured screen, ready to ride an SSE event or a stream line.
type Frame struct {
	DataURL string    `json:"data_url"`
	Mime    string    `json:"mime"`
	At      time.Time `json:"at"`
}

// PreviewWidth is the widest frame handed to the UI. The desktop is
// 1280 wide, so this is a 2:1 downscale that keeps text legible while
// cutting the bytes on the wire by roughly six times.
var PreviewWidth = 1024

// ScreenshotTimeout bounds the capture inside the container.
var ScreenshotTimeout = 30 * time.Second

const previewPath = "/tmp/gawkbot-preview.png"

// Screenshot asks Cua Driver for a desktop-state screenshot, reads it back
// as base64 over exec, verifies it is a whole image, downscales, and
// returns a JPEG data URL.
func Screenshot(ctx context.Context, run Runner, rt Runtime, target Target) (Frame, error) {
	args := CuaExecArgs([]string{"call", "get_desktop_state", "{}", "--socket", CuaSocket, "--screenshot-out-file", previewPath}, target.ContainerName, false)
	if _, _, err := run(ctx, string(rt), args, ScreenshotTimeout); err != nil {
		return Frame{}, err
	}
	stdout, _, err := run(ctx, string(rt), []string{"exec", target.ContainerName, "base64", "-w0", previewPath}, ScreenshotTimeout)
	if err != nil {
		return Frame{}, err
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(stdout))
	if err != nil {
		return Frame{}, fmt.Errorf("decode screenshot: %w", err)
	}
	return EncodePreview(raw, time.Now())
}

// EncodePreview turns raw PNG or JPEG bytes into the preview frame. A
// truncated transfer must never become a "successful" frame, so the image
// is fully decoded first.
func EncodePreview(raw []byte, at time.Time) (Frame, error) {
	if !wholeImage(raw) {
		return Frame{}, fmt.Errorf("screenshot transfer was incomplete")
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return Frame{}, fmt.Errorf("decode screenshot: %w", err)
	}
	img = downscale(img, PreviewWidth)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: 72}); err != nil {
		return Frame{}, err
	}
	return Frame{
		DataURL: "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(out.Bytes()),
		Mime:    "image/jpeg",
		At:      at,
	}, nil
}

func wholeImage(b []byte) bool {
	if len(b) < 512 {
		return false
	}
	if bytes.HasPrefix(b, []byte{0x89, 'P', 'N', 'G'}) {
		return bytes.Contains(b[max(0, len(b)-12):], []byte("IEND"))
	}
	if b[0] == 0xff && b[1] == 0xd8 {
		return bytes.Contains(b[max(0, len(b)-32):], []byte{0xff, 0xd9})
	}
	return false
}

// downscale is an integer-factor box filter. It keeps the standard library
// the only dependency and is plenty for a preview.
func downscale(img image.Image, maxWidth int) image.Image {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w <= maxWidth || maxWidth <= 0 {
		return img
	}
	factor := (w + maxWidth - 1) / maxWidth
	if factor < 2 {
		return img
	}
	nw, nh := w/factor, h/factor
	out := image.NewRGBA(image.Rect(0, 0, nw, nh))
	area := uint32(factor * factor)
	for y := 0; y < nh; y++ {
		for x := 0; x < nw; x++ {
			var r, g, b, a uint32
			for dy := 0; dy < factor; dy++ {
				for dx := 0; dx < factor; dx++ {
					pr, pg, pb, pa := img.At(bounds.Min.X+x*factor+dx, bounds.Min.Y+y*factor+dy).RGBA()
					r += pr >> 8
					g += pg >> 8
					b += pb >> 8
					a += pa >> 8
				}
			}
			i := out.PixOffset(x, y)
			out.Pix[i] = uint8(r / area)
			out.Pix[i+1] = uint8(g / area)
			out.Pix[i+2] = uint8(b / area)
			out.Pix[i+3] = uint8(a / area)
		}
	}
	return out
}

// pngDecoderRegistered keeps the png import alive for image.Decode.
var _ = png.Decode
