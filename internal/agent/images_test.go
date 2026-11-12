package agent

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usehm/hm/internal/agent/provider"
)

func testPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func testImage(t *testing.T) provider.ImageData {
	data := testPNG(t, 4, 2)
	return provider.ImageData{MediaType: "image/png", Data: data, Hash: provider.HashOf(data), Width: 4, Height: 2}
}

// picturesIn counts the pictures a request carries.
func picturesIn(req provider.Request) int {
	n := 0
	for _, m := range req.Messages {
		for _, b := range m.Blocks {
			if b.Type == provider.Image {
				n++
			}
			n += len(b.Images)
		}
	}
	return n
}

func TestReadGivesTheModelAnImage(t *testing.T) {
	h := newAgent(t, "auto",
		[]provider.Block{call("c1", "read", `{"path":"shot.png"}`)},
		[]provider.Block{say("a blank picture")},
	)
	if err := os.WriteFile(filepath.Join(h.root, "shot.png"), testPNG(t, 3000, 1500), 0o644); err != nil {
		t.Fatal(err)
	}
	h.run(t, "look at shot.png")
	res := h.results()
	if len(res) != 1 || res[0].IsError || len(res[0].Images) != 1 {
		t.Fatalf("results %+v", res)
	}
	im := res[0].Images[0]
	if im.Width != 1568 || im.Height != 784 || im.MediaType != "image/png" {
		t.Fatalf("image %s %dx%d", im.MediaType, im.Width, im.Height)
	}
	if want := "Image shot.png, 3000×1500 (sent scaled to 1568×784)"; res[0].Text != want {
		t.Fatalf("text %q, want %q", res[0].Text, want)
	}
	if n := picturesIn(h.prov.requests[1]); n != 1 {
		t.Fatalf("second request carries %d pictures", n)
	}
	var shown bool
	for _, e := range h.events {
		shown = shown || e.Kind == EvToolResult && len(e.Result.Images) == 1
	}
	if !shown {
		t.Fatal("the tool result event has no image")
	}
}

func TestReadBinaryIsNotAnError(t *testing.T) {
	h := newAgent(t, "auto",
		[]provider.Block{call("c1", "read", `{"path":"blob.bin"}`)},
		[]provider.Block{say("binary")},
	)
	if err := os.WriteFile(filepath.Join(h.root, "blob.bin"), []byte{0, 1, 2, 0}, 0o644); err != nil {
		t.Fatal(err)
	}
	h.run(t, "read blob.bin")
	if res := h.results(); len(res) != 1 || res[0].IsError || res[0].Text != "blob.bin is a binary file (4 bytes)" || res[0].Images != nil {
		t.Fatalf("results %+v", res)
	}
}

func TestNoVisionPlaceholder(t *testing.T) {
	tests := []struct {
		name     string
		noVision bool
		pictures int // in each request
		warnings int
	}{
		{"vision", false, 1, 0},
		{"no vision", true, 0, 1}, // warned once across two turns
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newAgent(t, "auto", []provider.Block{say("one")}, []provider.Block{say("two")})
			h.agent.NoVision = tt.noVision
			if err := h.agent.Run(context.Background(), h.session, "what is this?", testImage(t)); err != nil {
				t.Fatal(err)
			}
			h.run(t, "and now?")
			warnings := 0
			for _, e := range h.events {
				if e.Kind == EvWarning {
					warnings++
				}
			}
			if warnings != tt.warnings {
				t.Fatalf("%d warnings, want %d", warnings, tt.warnings)
			}
			for i, req := range h.prov.requests {
				if n := picturesIn(req); n != tt.pictures {
					t.Fatalf("request %d carries %d pictures, want %d", i, n, tt.pictures)
				}
				placeholder := strings.Contains(req.Messages[0].Blocks[1].Text, provider.NoVision)
				if placeholder != tt.noVision {
					t.Fatalf("request %d placeholder %v", i, placeholder)
				}
			}
			// The session keeps the picture either way.
			if m := h.session.Snapshot()[0]; m.Blocks[1].Image == nil {
				t.Fatal("the session lost the picture")
			}
		})
	}
}

func TestSteerWithImage(t *testing.T) {
	h := newAgent(t, "auto",
		[]provider.Block{call("c1", "read", `{"path":"a.go"}`)},
		[]provider.Block{say("I see it")},
	)
	queue := []Input{{Text: "this one", Images: []provider.ImageData{testImage(t)}}}
	h.agent.Hooks.Steer = func() []Input { q := queue; queue = nil; return q }
	h.run(t, "read a")
	if n := picturesIn(h.prov.requests[1]); n != 1 {
		t.Fatalf("the steered picture reached %d times", n)
	}
}

func TestStoreKeepsImagesInFiles(t *testing.T) {
	state := t.TempDir()
	st := NewStore(state)
	im := testImage(t)
	s := NewSession("auto", "p")
	s.addUser(Input{Text: "look", Images: []provider.ImageData{im}})
	s.append(provider.Message{Role: provider.Assistant, Blocks: []provider.Block{call("c1", "read", `{}`)}})
	s.append(provider.Message{Role: provider.User, Blocks: []provider.Block{{Type: provider.ToolResult, ToolID: "c1",
		Text: "Image", Images: []provider.ImageData{im}}}})
	if err := st.Save(s); err != nil {
		t.Fatal(err)
	}
	line, err := os.ReadFile(st.file(s.ID))
	if err != nil {
		t.Fatal(err)
	}
	if b64 := im.Base64(); bytes.Contains(line, []byte(b64[:16])) {
		t.Fatal("the session file holds the picture's data")
	}
	files, _ := os.ReadDir(filepath.Join(state, "sessions", "images"))
	if len(files) != 1 || files[0].Name() != im.Hash+".png" {
		t.Fatalf("image files %v, want one, shared", files)
	}
	if m := s.Snapshot()[0]; len(m.Blocks[1].Image.Data) == 0 {
		t.Fatal("saving took the data out of the session in memory")
	}

	got, err := st.Load(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	msgs := got.Snapshot()
	if !bytes.Equal(msgs[0].Blocks[1].Image.Data, im.Data) || !bytes.Equal(msgs[2].Blocks[0].Images[0].Data, im.Data) {
		t.Fatal("the pictures did not load back")
	}

	// A picture whose file is gone loads as a note.
	if err := os.Remove(filepath.Join(state, "sessions", "images", files[0].Name())); err != nil {
		t.Fatal(err)
	}
	got, err = NewStore(state).Load(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	msgs = got.Snapshot()
	if b := msgs[0].Blocks[1]; b.Type != provider.Text || b.Text != imageGone {
		t.Fatalf("missing picture became %+v", b)
	}
	if b := msgs[2].Blocks[0]; len(b.Images) != 0 || b.Text != "Image\n"+imageGone {
		t.Fatalf("missing tool picture became %+v", b)
	}
}

func TestPromptTextMarksImages(t *testing.T) {
	m := provider.Message{Role: provider.User, Blocks: []provider.Block{{Type: provider.Image, Image: &provider.ImageData{}}}}
	if got := PromptText(m); got != ImageMark {
		t.Fatalf("PromptText = %q", got)
	}
}
