package app

import (
	"github.com/mirzakhany/yoga/icons"
	"github.com/mirzakhany/yoga/ui"
)

type panelKind int

const (
	panelTerminal panelKind = iota
	panelGit
	panelProblems
	panelOutput
)

var panelTabs = []ui.SegmentItem{
	{Label: "Terminal", Value: "terminal"},
	{Label: "Git", Value: "git"},
	{Label: "Problems", Value: "problems"},
	{Label: "Output", Value: "output"},
}

// showPanel opens the bottom panel on k.
func (a *App) showPanel(k panelKind) {
	if k == panelGit && (!a.panelOpen || a.panel != panelGit) && a.gitst.page != nil {
		// The work tree changes outside hm; read it each time the panel opens.
		a.gitst.page.Refresh()
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
		body = ui.EmptyState("Terminal", "The interactive terminal arrives in a later milestone.").EmptyIcon(icons.SquareTerminal)
	case panelGit:
		body = a.gitst.page.Layout(c)
	case panelProblems:
		body = a.problemsView(c)
	default:
		body = a.outputView(c)
	}
	return ui.Column(header, ui.ViewOf(body).Grow(1)).Grow(1).Background(ui.TokenChrome).
		BorderTop(ui.TokenBorder, th.Stroke.Thin)
}
