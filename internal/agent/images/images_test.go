package images

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math/rand/v2"
	"os"
	"testing"
)

func pngOf(t *testing.T, w, h int, noise bool) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	r := rand.New(rand.NewPCG(1, 2))
	for y := range h {
		for x := range w {
			c := color.RGBA{uint8(x), uint8(y), 128, 255}
			if noise {
				c = color.RGBA{uint8(r.IntN(256)), uint8(r.IntN(256)), uint8(r.IntN(256)), 255}
			}
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestLoad(t *testing.T) {
	small := pngOf(t, 40, 20, false)
	var jpg bytes.Buffer
	_ = jpeg.Encode(&jpg, image.NewRGBA(image.Rect(0, 0, 10, 10)), nil)
	var anim bytes.Buffer
	pal := color.Palette{color.Black, color.White}
	frame := image.NewPaletted(image.Rect(0, 0, 2000, 1000), pal)
	_ = gif.EncodeAll(&anim, &gif.GIF{Image: []*image.Paletted{frame, frame}, Delay: []int{1, 1}})

	tests := []struct {
		name     string
		data     []byte
		wantType string
		wantW    int
		wantH    int
		wantSame bool // kept byte for byte
		wantErr  error
		anyErr   bool
	}{
		{name: "small png kept", data: small, wantType: "image/png", wantW: 40, wantH: 20, wantSame: true},
		{name: "jpeg kept", data: jpg.Bytes(), wantType: "image/jpeg", wantW: 10, wantH: 10, wantSame: true},
		{name: "wide png scaled", data: pngOf(t, 3136, 1000, false), wantType: "image/png", wantW: 1568, wantH: 500},
		{name: "tall png scaled", data: pngOf(t, 500, 2000, false), wantType: "image/png", wantW: 392, wantH: 1568},
		{name: "animated gif first frame", data: anim.Bytes(), wantType: "image/png", wantW: 1568, wantH: 784},
		{name: "noisy png falls back to jpeg", data: pngOf(t, 1568, 1568, true), wantType: "image/jpeg", wantW: 1568, wantH: 1568},
		{name: "text", data: []byte("hello, world"), wantErr: ErrUnsupported},
		{name: "truncated png", data: small[:30], anyErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Load(tt.data)
			if tt.wantErr != nil || tt.anyErr {
				if err == nil || (tt.wantErr != nil && !errors.Is(err, tt.wantErr)) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.MediaType != tt.wantType || got.Width != tt.wantW || got.Height != tt.wantH {
				t.Fatalf("got %s %dx%d, want %s %dx%d", got.MediaType, got.Width, got.Height, tt.wantType, tt.wantW, tt.wantH)
			}
			if same := bytes.Equal(got.Data, tt.data); same != tt.wantSame {
				t.Fatalf("kept byte for byte = %v, want %v", same, tt.wantSame)
			}
			if len(got.Data) > MaxBytes {
				t.Fatalf("%d bytes, over MaxBytes", len(got.Data))
			}
			cfg, _, err := image.DecodeConfig(bytes.NewReader(got.Data))
			if err != nil || cfg.Width != got.Width || cfg.Height != got.Height {
				t.Fatalf("encoded %dx%d (%v), says %dx%d", cfg.Width, cfg.Height, err, got.Width, got.Height)
			}
			orig, _, _ := image.DecodeConfig(bytes.NewReader(tt.data))
			if got.OrigWidth != orig.Width || got.OrigHeight != orig.Height ||
				got.Resized() != (orig.Width != tt.wantW) {
				t.Fatalf("orig %dx%d, Resized %v", got.OrigWidth, got.OrigHeight, got.Resized())
			}
		})
	}
}

func TestThumb(t *testing.T) {
	th, err := Thumb(pngOf(t, 400, 100, false), 64)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(th))
	if err != nil || cfg.Width != 64 || cfg.Height != 16 {
		t.Fatalf("thumb %dx%d, %v; want 64x16", cfg.Width, cfg.Height, err)
	}
}

func TestIsImagePath(t *testing.T) {
	for path, want := range map[string]bool{"a.png": true, "B.JPG": true, "c.jpeg": true, "d.gif": true,
		"e.webp": true, "f.svg": false, "g.go": false, "png": false} {
		if got := IsImagePath(path); got != want {
			t.Errorf("IsImagePath(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestTokens(t *testing.T) {
	if got := Tokens(1500, 750); got != 1500 {
		t.Fatalf("Tokens = %d, want 1500", got)
	}
	if got := Tokens(0, 0); got != 1000 {
		t.Fatalf("Tokens unknown = %d, want 1000", got)
	}
}

func TestLoadWebP(t *testing.T) {
	data, err := os.ReadFile("testdata/gopher.webp") // from golang.org/x/image
	if err != nil {
		t.Fatal(err)
	}
	got, err := Load(data)
	if err != nil || got.MediaType != "image/webp" || got.Width == 0 || !bytes.Equal(got.Data, data) {
		t.Fatalf("Load = %s %dx%d, %v; want the webp kept", got.MediaType, got.Width, got.Height, err)
	}
	if _, err := Thumb(data, 32); err != nil {
		t.Fatalf("Thumb: %v", err)
	}
}
