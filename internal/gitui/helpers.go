package gitui

import (
	"os/exec"
	"runtime"

	"github.com/mirzakhany/yoga/highlight"
	"github.com/mirzakhany/yoga/icons"
	"github.com/mirzakhany/yoga/theme"
	"github.com/mirzakhany/yoga/ui"
)

// Helpers chapar keeps in its container and pages packages.

func mutedParagraph(s string) *ui.Node {
	return ui.Paragraph(s).Style(ui.Spec{}.TextColor(ui.TokenForegroundMuted))
}

func emptyMessage(th *theme.Theme, icon icons.Icon, title, detail string) ui.View {
	return ui.Column(
		ui.Row(ui.Icon(icon, th.Metrics.IconSizeMD*2, th.ForegroundMuted)).Justify(ui.JustifyCenter),
		ui.Row(ui.Subtitle(title)).Justify(ui.JustifyCenter),
		ui.Paragraph(detail).TextAlign(ui.AlignCenter).
			Style(ui.Spec{}.TextColor(ui.TokenForegroundMuted)),
	).Gap(th.Spacing.M).PaddingXY(th.Spacing.XXL, th.Spacing.L).
		Justify(ui.JustifyCenter).Grow(1)
}

func paneCard(th *theme.Theme, children ...ui.View) ui.View {
	return ui.Column(
		ui.Column(children...).
			Radius(th.Radius.Medium).
			Border(ui.TokenBorder, th.Stroke.Thin).
			Margin(th.Spacing.XS).
			Padding(th.Spacing.S).
			Gap(th.Spacing.XS).
			Grow(1),
	)
}

// replaceEditor swaps a read-only diff editor for one showing data.
func replaceEditor(old *ui.Editor, data []byte, hl highlight.Highlighter) *ui.Editor {
	if old != nil {
		old.Close()
	}
	return ui.NewEditor(data, hl, ui.WithReadOnly(), ui.WithSharedContent())
}

// secretStoreName is what the platform calls its secret store.
func secretStoreName() string {
	switch runtime.GOOS {
	case "darwin":
		return "the macOS Keychain"
	case "windows":
		return "Windows Credential Manager"
	}
	return "the system keyring"
}

// openURL opens url in the default browser.
func openURL(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
