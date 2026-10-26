package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mirzakhany/yoga/input"
)

func TestMarkdownPreviewFollowsEdits(t *testing.T) {
	root := project(t)
	readme := filepath.Join(root, "README.md")
	if err := os.WriteFile(readme, []byte("# Demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, root, false)
	h.app.openFile(readme)
	h.frame()
	if d := h.app.mdDoc(readme); d.mode != mdSource || d.read {
		t.Fatalf("a Markdown tab opens on its source, got mode %d read %v", d.mode, d.read)
	}

	h.press(input.KeyV, input.ModSuper|input.ModShift)
	d := h.app.mdDoc(readme)
	if d.mode != mdSplit || d.text != "# Demo\n" {
		t.Fatalf("⌘⇧V should split with a preview, got mode %d text %q", d.mode, d.text)
	}
	h.app.ws.ActiveEditor().SetText("# Changed\n")
	h.frame()
	if d.text != "# Changed\n" {
		t.Fatalf("preview text %q did not follow the edit", d.text)
	}
	h.app.cycleMarkdownPreview()
	h.frame()
	if d.mode != mdPreview {
		t.Fatalf("mode %d, want preview", d.mode)
	}
}

func TestOpenLink(t *testing.T) {
	root := project(t)
	h := newHarness(t, root, false)
	h.app.openLink(root, "main.go#L1")
	if ed := h.app.ws.ActiveEditor(); ed == nil || ed.Path != filepath.Join(root, "main.go") {
		t.Fatal("a relative link should open the file")
	}
	h.app.openLink(filepath.Join(root, "util"), "./strings.go")
	if ed := h.app.ws.ActiveEditor(); ed == nil || ed.Path != filepath.Join(root, "util", "strings.go") {
		t.Fatal("a link resolves against its base folder")
	}
	tabs := len(h.app.ws.Tabs)
	for _, l := range []string{"#heading", "javascript:alert(1)", "vscode://x", "missing.go"} {
		h.app.openLink(root, l)
	}
	if len(h.app.ws.Tabs) != tabs {
		t.Fatal("anchors, other schemes and missing files must not open tabs")
	}
}
