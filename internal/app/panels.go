// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"github.com/mirzakhany/yoga/icons"
	"github.com/mirzakhany/yoga/ui"
)

type panelKind int

const (
	panelTerminal panelKind = iota
	panelProblems
	panelOutput
)

var panelTabs = []ui.SegmentItem{
	{Label: "Terminal", Value: "terminal"},
	{Label: "Problems", Value: "problems"},
	{Label: "Output", Value: "output"},
}

// showPanel opens the bottom panel on k.
func (a *App) showPanel(k panelKind) {
	if k == panelTerminal {
		a.term.focus = true
	}
	a.panel, a.panelOpen = k, true
}

// togglePanel closes the panel when it already shows k, else shows k.
func (a *App) togglePanel(k panelKind) {
	if a.panelOpen && a.panel == k {
		a.panelOpen = false
		return
	}
	a.showPanel(k)
}

func (a *App) panelView(c *ui.Ctx) ui.View {
	th := c.Theme()
	header := ui.Row(
		ui.Segmented("panel-tabs", panelTabs...).
			Selected(int(a.panel)).
			OnSelectItem(func(i int, _ string) { a.showPanel(panelKind(i)) }).
			Height(22),
		ui.Spacer(),
		ui.IconButton("panel-close", icons.X).Height(22).Tooltip("Close").OnClick(func() { a.panelOpen = false }),
	).Height(30).PaddingLeft(th.Spacing.S).PaddingRight(th.Spacing.XS)

	var body ui.View
	switch a.panel {
	case panelTerminal:
		// The drawer builds its panel while closed too; a hidden terminal
		// must neither start a shell nor hold keyboard focus.
		body = ui.Spacer()
		if a.panelOpen {
			body = a.terminalView(c)
		}
	case panelProblems:
		body = a.problemsView(c)
	default:
		body = a.outputView(c)
	}
	return ui.Column(header, ui.ViewOf(body).Grow(1)).Grow(1).Background(ui.TokenChrome).
		BorderTop(ui.TokenBorder, th.Stroke.Thin)
}
