package agent

import (
	"os/exec"
	"strings"
	"testing"
)

// TestNoUIToolkit checks that the headless agent and the wiring it shares
// with the app build without the UI toolkit, so they need no display.
func TestNoUIToolkit(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go command")
	}
	for _, pkg := range []string{"github.com/usehm/hm/pkg/hm/agent", "github.com/usehm/hm/internal/agent/host"} {
		out, err := exec.Command("go", "list", "-deps", pkg).CombinedOutput()
		if err != nil {
			t.Fatalf("go list %s: %v\n%s", pkg, err, out)
		}
		for _, dep := range strings.Fields(string(out)) {
			if strings.HasPrefix(dep, "github.com/mirzakhany/yoga") {
				t.Errorf("%s imports %s", pkg, dep)
			}
		}
	}
}
