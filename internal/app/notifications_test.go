package app

import (
	"path/filepath"
	"testing"

	"github.com/mirzakhany/yoga/ui"
)

func TestNotificationOpensWithItsAction(t *testing.T) {
	root := project(t)
	h := newHarness(t, root, false)
	path := filepath.Join(root, "main.go")
	h.app.notifs.Add("main.go changed", ui.ToastWarning, h.app.openFileAction(path))
	h.app.notifs.Add("plain", ui.ToastInfo, h.app.openFileAction("defaults"))
	if n := len(h.app.notifs.items[1].actions); n != 0 {
		t.Fatalf("non-file action kept: %d", n)
	}
	h.app.notifsOpen = true
	h.app.notifs.open(h.c, h.app.notifs.items[0])
	if h.app.notifsOpen || !h.c.Dialogs().Open {
		t.Fatal("opening a notification must close the center and show a dialog")
	}
	h.app.notifs.items[0].actions[0].run()
	if ed := h.app.ws.ActiveEditor(); ed == nil || ed.Path != path {
		t.Fatal("Open File action did not open the file")
	}
}
