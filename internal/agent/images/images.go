// Package images turns pictures from the clipboard, a drop or a file into
// what model APIs take: PNG, JPEG, GIF or WebP, at most MaxSide pixels on
// the long side and MaxBytes in size.
package images

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/gif" // decoder; Decode gives an animation's first frame
	"image/jpeg"
	"image/png"
	"net/http"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mirzakhany/hm/internal/agent/provider"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // decoder
)

const (
	// MaxSide is the long side APIs scale larger pictures down to anyway;
	// sending more only costs bytes.
	MaxSide = 1568
	// MaxBytes keeps a picture under the 5 MB the APIs accept, with room
	// for base64 framing.
	MaxBytes = 5_000_000
)

// ErrUnsupported is returned for data that is not a picture hm can send.
var ErrUnsupported = errors.New("not a PNG, JPEG, GIF or WebP image")

// Types are the media types the APIs accept.
var Types = []string{"image/png", "image/jpeg", "image/gif", "image/webp"}

// IsImagePath reports whether a path names a picture by its extension.
func IsImagePath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	}
	return false
}

// Sniff returns data's media type from its first bytes, or "" when it is
// none of Types.
func Sniff(data []byte) string {
	if t := http.DetectContentType(data); slices.Contains(Types, t) {
		return t
	}
	return ""
}

// Image is a picture ready to send.
type Image struct {
	MediaType     string
	Data          []byte
	Width, Height int
	// OrigWidth and OrigHeight are the size before it was scaled down.
	OrigWidth, OrigHeight int
}

// Prepare is Load, giving the picture as a message holds it.
func Prepare(data []byte) (provider.ImageData, error) {
	im, err := Load(data)
	if err != nil {
		return provider.ImageData{}, err
	}
	return im.Message(), nil
}

// Message is the picture as a message holds it.
func (im Image) Message() provider.ImageData {
	return provider.ImageData{MediaType: im.MediaType, Data: im.Data, Hash: provider.HashOf(im.Data),
		Width: im.Width, Height: im.Height}
}

// Resized reports whether the picture was scaled down.
func (im Image) Resized() bool { return im.Width != im.OrigWidth || im.Height != im.OrigHeight }

// Load checks data is a picture and fits it to MaxSide and MaxBytes. A
// picture that already fits is kept byte for byte; one that does not is
// scaled and encoded again, as PNG, or JPEG when the PNG is too big. An
// animated GIF keeps its first frame then.
func Load(data []byte) (Image, error) {
	mt := Sniff(data)
	if mt == "" {
		return Image{}, ErrUnsupported
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Image{}, fmt.Errorf("decode %s: %w", mt, err)
	}
	out := Image{MediaType: mt, Data: data, Width: cfg.Width, Height: cfg.Height,
		OrigWidth: cfg.Width, OrigHeight: cfg.Height}
	if len(data) <= MaxBytes && max(cfg.Width, cfg.Height) <= MaxSide {
		return out, nil
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Image{}, fmt.Errorf("decode %s: %w", mt, err)
	}
	side := min(max(cfg.Width, cfg.Height), MaxSide)
	for side >= 16 {
		w, h := scaled(cfg.Width, cfg.Height, side)
		dst := scale(src, w, h)
		if enc, mt, ok := encode(dst); ok {
			out.MediaType, out.Data, out.Width, out.Height = mt, enc, w, h
			return out, nil
		}
		side = side * 3 / 4
	}
	return Image{}, errors.New("image too large to send")
}

// encode returns the smallest acceptable encoding of img: PNG when it
// fits MaxBytes, else JPEG at falling quality.
func encode(img image.Image) ([]byte, string, bool) {
	var b bytes.Buffer
	if err := png.Encode(&b, img); err == nil && b.Len() <= MaxBytes {
		return b.Bytes(), "image/png", true
	}
	for _, q := range []int{85, 70, 50} {
		b.Reset()
		if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: q}); err == nil && b.Len() <= MaxBytes {
			return b.Bytes(), "image/jpeg", true
		}
	}
	return nil, "", false
}

// scaled is w×h with the long side side, keeping the aspect ratio.
func scaled(w, h, side int) (int, int) {
	if w >= h {
		return side, max(1, h*side/w)
	}
	return max(1, w*side/h), side
}

func scale(src image.Image, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
	return dst
}

// Thumb returns a PNG of data at most side pixels on the long side, for
// showing in the window, which draws only PNG and JPEG.
func Thumb(data []byte, side int) ([]byte, error) {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if max(w, h) > side {
		w, h = scaled(w, h, side)
	}
	var out bytes.Buffer
	if err := png.Encode(&out, scale(src, w, h)); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// Tokens estimates what a w×h picture costs in a prompt: about one token
// per 750 pixels, as Anthropic documents; other APIs are close.
func Tokens(w, h int) int64 {
	if w <= 0 || h <= 0 {
		return 1000 // size unknown: a mid-sized screenshot
	}
	return int64(w) * int64(h) / 750
}
