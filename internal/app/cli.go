package app

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/mirzakhany/yoga/ui"
)

// cliName is the shell command that opens a folder in hm: `hm .`.
const cliName = "hm"

// bundleID matches id in yoga.toml; the launcher falls back to it when the
// app moved after the command was installed.
const bundleID = "me.mirzakhani.hm"

// cliSupported reports whether this platform gets the shell command.
// Windows users run hm.exe directly.
func cliSupported() bool { return runtime.GOOS != "windows" }

// cliDirs are where the command goes, best first: /usr/local/bin is on
// every default PATH, ~/.local/bin needs no admin rights.
func cliDirs() []string {
	dirs := []string{"/usr/local/bin"}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".local", "bin"))
	}
	return dirs
}

// appBundle is the .app this process runs from, or "" outside a bundle
// (go run, Linux).
func appBundle(exe string) string {
	macos := filepath.Dir(exe)
	contents := filepath.Dir(macos)
	app := filepath.Dir(contents)
	if filepath.Base(macos) != "MacOS" || filepath.Base(contents) != "Contents" || filepath.Ext(app) != ".app" {
		return ""
	}
	return app
}

// cliScript is the launcher: it turns its argument into an absolute path
// (a file opens its folder) and starts a new hm window on it. On macOS it
// goes through `open` so the app gets its Dock icon and bundle; anywhere
// else it starts the binary detached from the terminal. `hm agent ...`
// runs the binary in the foreground instead, so it keeps the terminal's
// standard input, output and exit code.
func cliScript(exe, bundle, bundleID string) string {
	var launch string
	if bundle != "" {
		launch = fmt.Sprintf(`app=%s
if [ -d "$app" ]; then
	exec open -n -a "$app" --args "$@"
fi
# The app moved since the command was installed; find it by bundle id.
exec open -n -b %s --args "$@"`, shellQuote(bundle), shellQuote(bundleID))
	} else {
		launch = fmt.Sprintf(`nohup %s "$@" >/dev/null 2>&1 &`, shellQuote(exe))
	}
	return `#!/bin/sh
# hm — open a folder in hm. Installed by hm's "Install 'hm' Command in PATH".
#
#   hm .          open the current folder
#   hm PATH       open PATH (a file opens its folder)
#   hm            reopen the last session
#   hm agent ...  run the agent in this terminal (hm ./agent opens a folder named agent)
set -e
if [ "${1-}" = agent ]; then
	exe=` + shellQuote(exe) + `
	if [ ! -x "$exe" ]; then
		echo "hm: $exe: not found; reinstall the hm command from the app" >&2
		exit 1
	fi
	exec "$exe" "$@"
fi
if [ $# -gt 0 ]; then
	target=$1
	shift
	if [ -f "$target" ]; then
		target=$(dirname -- "$target")
	fi
	if [ ! -d "$target" ]; then
		echo "hm: $target: no such folder" >&2
		exit 1
	fi
	set -- "$(cd -- "$target" && pwd -P)" "$@"
fi
` + launch + "\n"
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// installCLI writes the launcher into the first writable PATH directory.
// On macOS, when /usr/local/bin needs admin rights, it asks for them the
// way installers do instead of falling back to a folder that is not on
// PATH. It returns where the command went.
func installCLI() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	script := cliScript(exe, appBundle(exe), bundleID)

	var lastErr error
	for _, dir := range cliDirs() {
		dest := filepath.Join(dir, cliName)
		if err := writeCLI(dir, dest, script); err == nil {
			return dest, nil
		} else {
			lastErr = err
		}
		if runtime.GOOS == "darwin" && dir == "/usr/local/bin" {
			if err := writeCLIAdmin(dir, dest, script); err == nil {
				return dest, nil
			} else {
				lastErr = err
			}
		}
	}
	return "", lastErr
}

func writeCLI(dir, dest, script string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".hm-cli-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(script); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dest)
}

// writeCLIAdmin copies the script into dir with the macOS admin prompt.
func writeCLIAdmin(dir, dest, script string) error {
	tmp, err := os.CreateTemp("", "hm-cli-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(script); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	sh := fmt.Sprintf("mkdir -p %s && install -m 755 %s %s", shellQuote(dir), shellQuote(tmp.Name()), shellQuote(dest))
	return runAdmin(sh)
}

// uninstallCLI removes every launcher hm installed. It leaves alone an
// `hm` that some other program put there.
func uninstallCLI() (int, error) {
	n := 0
	for _, dir := range cliDirs() {
		dest := filepath.Join(dir, cliName)
		b, err := os.ReadFile(dest)
		if err != nil || !strings.Contains(string(b), "Installed by hm's") {
			continue
		}
		err = os.Remove(dest)
		if errors.Is(err, os.ErrPermission) && runtime.GOOS == "darwin" {
			err = runAdmin("rm -f " + shellQuote(dest))
		}
		if err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// runAdmin runs sh as root behind the standard macOS password prompt.
func runAdmin(sh string) error {
	esc := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(sh)
	out, err := exec.Command("osascript", "-e", `do shell script "`+esc+`" with administrator privileges`).CombinedOutput()
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return errors.New(msg)
		}
		return err
	}
	return nil
}

// InstallCLI installs the `hm` command for main's -install-cli flag and
// reports where it went on w.
func InstallCLI(w io.Writer) error {
	dest, err := installCLI()
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "installed", dest)
	if dir := filepath.Dir(dest); !onPath(dir) {
		fmt.Fprintf(w, "%s is not on your PATH; add it to run `hm .`\n", dir)
	}
	return nil
}

// onPath reports whether dir is in $PATH.
func onPath(dir string) bool {
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if filepath.Clean(p) == dir {
			return true
		}
	}
	return false
}

func (a *App) installCLICommand() {
	go func() {
		dest, err := installCLI()
		a.onUI(func() {
			if err != nil {
				a.toast("Could not install the hm command: "+err.Error(), ui.ToastError)
				return
			}
			msg := "Installed " + dest + ". Run `hm .` in a terminal to open a folder."
			if dir := filepath.Dir(dest); !onPath(dir) {
				msg = "Installed " + dest + ". Add " + dir + " to your PATH to run `hm .`."
			}
			a.toast(msg, ui.ToastInfo)
		})
	}()
}

func (a *App) uninstallCLICommand() {
	go func() {
		n, err := uninstallCLI()
		a.onUI(func() {
			switch {
			case err != nil:
				a.toast("Could not remove the hm command: "+err.Error(), ui.ToastError)
			case n == 0:
				a.toast("The hm command is not installed", ui.ToastInfo)
			default:
				a.toast("Removed the hm command", ui.ToastInfo)
			}
		})
	}()
}
