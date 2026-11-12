package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mirzakhany/yoga/ui"

	"github.com/usehm/hm/internal/agent/guard"
)

func TestNotificationActionAndDismiss(t *testing.T) {
	root := project(t)
	h := newHarness(t, root, false)
	path := filepath.Join(root, "main.go")
	h.app.notifs.items = nil // startup may have added some
	h.app.notifs.Add("main.go changed", ui.ToastWarning, h.app.openFileAction(path))
	h.app.notifs.Add("plain", ui.ToastInfo, h.app.openFileAction("defaults"))
	if n := len(h.app.notifs.items[1].actions); n != 0 {
		t.Fatalf("non-file action kept: %d", n)
	}
	h.app.notifs.items[0].actions[0].run()
	if ed := h.app.ws.ActiveEditor(); ed == nil || ed.Path != path {
		t.Fatal("Open File action did not open the file")
	}
	h.app.notifs.remove(h.app.notifs.items[0].at)
	if len(h.app.notifs.items) != 1 || h.app.notifs.items[0].text != "plain" {
		t.Fatalf("dismiss removed the wrong card: %+v", h.app.notifs.items)
	}
}

func TestGuardTemplateLoads(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "guard.yaml")
	if err := os.WriteFile(p, []byte(guardTemplate), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := guard.Load(p, "", dir); err != nil {
		t.Fatal(err)
	}
}
