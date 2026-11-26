// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package gitui

import (
	"os/exec"
	"runtime"

	codeeditor "github.com/yogaui/editor"
	"github.com/yogaui/editor/highlight"
	"github.com/yogaui/yoga/icons"
	"github.com/yogaui/yoga/theme"
	"github.com/yogaui/yoga/ui"
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
func replaceEditor(old *codeeditor.Editor, data []byte, hl highlight.Highlighter) *codeeditor.Editor {
	if old != nil {
		old.Close()
	}
	ed := codeeditor.NewEditor(data, hl, codeeditor.WithReadOnly(), codeeditor.WithSharedContent())
	ed.SetMinimap(false) // a diff side is read beside the other, not navigated
	return ed
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
