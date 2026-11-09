package app

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mirzakhany/yoga/input"
)

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// sentImages counts the pictures in a request to the fake model: each
// names its media type once, however the API encodes it.
func sentImages(body map[string]any) int { return strings.Count(history(body), "image/png") }

func TestImagesReachTheModel(t *testing.T) {
	tests := []struct {
		name   string
		kind   string
		attach func(t *testing.T, h *harness, root string)
		text   string
	}{
		{"paste", "anthropic", func(t *testing.T, h *harness, _ string) {
			h.c.Clipboard().(*input.MemClipboard).SetImage(pngBytes(t, 8, 8))
			r := h.app.run()
			r.focusInput = true
			h.frame()
			if !agentInput(h.c, r).Focused() {
				t.Fatal("the message box has no focus")
			}
			h.press(input.KeyV, input.ModSuper)
			if got := r.input; got != "" {
				t.Fatalf("pasting a picture typed %q", got)
			}
		}, "what is this?"},
		{"drop", "openai", func(t *testing.T, h *harness, root string) {
			shot := filepath.Join(root, "shot.png")
			if err := os.WriteFile(shot, pngBytes(t, 8, 8), 0o644); err != nil {
				t.Fatal(err)
			}
			h.mouse.Drop([]string{shot, filepath.Join(root, "main.go")})
			h.frame()
			if got := h.app.run().input; got != "@main.go " {
				t.Fatalf("dropping main.go typed %q", got)
			}
		}, ""},
		{"mention", "google", func(t *testing.T, _ *harness, root string) {
			if err := os.WriteFile(filepath.Join(root, "shot.png"), pngBytes(t, 8, 8), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "what is in @shot.png"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := &fakeModel{}
			h, root := agentProject(t, tt.kind, model)
			tt.attach(t, h, root)
			r := h.app.run()
			if tt.name != "mention" && len(r.attached) != 1 {
				t.Fatalf("%d pictures in the message box, want 1", len(r.attached))
			}
			h.frame() // draws the thumbnail
			runTask(t, h, r.input+tt.text)
			h.until("turn end", 10*time.Second, func() bool { return !r.running })
			if len(r.attached) != 0 {
				t.Fatal("the message box kept the picture after sending")
			}
			if n := sentImages(model.requests[0]); n != 1 {
				t.Fatalf("the model got %d pictures:\n%s", n, history(model.requests[0]))
			}
			h.frame() // draws the picture in the chat
		})
	}
}

func TestPasteTextStillPastes(t *testing.T) {
	h, _ := agentProject(t, "anthropic", &fakeModel{})
	h.c.Clipboard().Set("hello")
	r := h.app.run()
	r.focusInput = true
	h.frame()
	h.press(input.KeyV, input.ModSuper)
	if len(r.attached) != 0 || r.input != "hello" {
		t.Fatalf("pasting text gave %q and %d pictures", r.input, len(r.attached))
	}
}

func TestEditAndResendKeepsImages(t *testing.T) {
	model := &fakeModel{}
	h, _ := agentProject(t, "anthropic", model)
	r := h.app.run()
	if !h.app.attachImage(r, pngBytes(t, 4, 4), "test") {
		t.Fatal("attach failed")
	}
	runTask(t, h, "look")
	h.until("turn end", 10*time.Second, func() bool { return !r.running })
	first, ok := r.session.Entry(r.session.MessageIDs()[0])
	if !ok {
		t.Fatal("no first message")
	}
	h.app.editAndResend(r, first)
	if r.input != "look" || len(r.attached) != 1 {
		t.Fatalf("message box %q with %d pictures", r.input, len(r.attached))
	}
}
