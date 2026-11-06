package agentcmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mirzakhany/hm/internal/agent/agenttest"
)

// TestPythonExample runs examples/rpc_client.py against a built hm and the
// fake model: it prompts, approves the shell call and prints the answer.
func TestPythonExample(t *testing.T) {
	if testing.Short() {
		t.Skip("builds hm")
	}
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3")
	}
	root, model := agenttest.Workspace(t, []agenttest.Reply{shellCall(), {Text: "the sum is 3345"}}, "")
	// The child process has no fake keychain: give it the key in the
	// environment instead.
	cfgPath := filepath.Join(root, ".hm", "config.yaml")
	cfg, _ := os.ReadFile(cfgPath)
	agenttest.WriteFile(t, cfgPath, strings.ReplaceAll(string(cfg), `"keychain:test"`, `"${HM_TEST_KEY}"`))

	bin := filepath.Join(t.TempDir(), "hm")
	build := exec.Command("go", "build", "-tags", "nogpu", "-o", bin, "../../../cmd/hm")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	home := t.TempDir()
	for _, interactive := range []bool{false, true} {
		args := []string{"-I", "../../../examples/rpc_client.py", "--hm", bin}
		if !interactive {
			args = append(args, "--yes")
		}
		args = append(args, "run the sum", "--", "--cwd", root, "--no-session")
		cmd := exec.Command(py, args...)
		cmd.Env = append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"), "HM_TEST_KEY=sk-test")
		if interactive {
			cmd.Stdin = strings.NewReader("y\n")
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("interactive %v: %v\n%s", interactive, err, out)
		}
		if !strings.Contains(string(out), "the sum is 3345") || !strings.Contains(string(out), "→ shell") {
			t.Fatalf("interactive %v:\n%s", interactive, out)
		}
		if interactive && !strings.Contains(string(out), "Allow shell") {
			t.Fatalf("not asked:\n%s", out)
		}
		model.Reset([]agenttest.Reply{shellCall(), {Text: "the sum is 3345"}})
	}
	if !model.Sent("3345") {
		t.Fatal("the approved call did not run")
	}
}
