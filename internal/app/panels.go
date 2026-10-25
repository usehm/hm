package app

import (
	"github.com/mirzakhany/yoga/icons"
	"github.com/mirzakhany/yoga/theme"
	"github.com/mirzakhany/yoga/ui"

	"github.com/mirzakhany/hm/internal/workspace"
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
			OnSelectItem(func(i int, _ string) { a.panel = panelKind(i) }).
			Height(22),
		ui.Spacer(),
		ui.IconButton("panel-close", icons.X).Height(22).Tooltip("Close").OnClick(func() { a.panelOpen = false }),
	).Height(30).PaddingLeft(th.Spacing.S).PaddingRight(th.Spacing.XS)

	var body ui.View
	switch a.panel {
	case panelTerminal:
		body = ui.EmptyState("Terminal", "The interactive terminal arrives in a later milestone.").EmptyIcon(icons.SquareTerminal)
	case panelGit:
		body = ui.EmptyState("Git", "Changes, history and branches arrive in a later milestone.").EmptyIcon(icons.GitBranch)
	case panelProblems:
		body = ui.EmptyState("No problems", "Diagnostics from language servers show here.").EmptyIcon(icons.TriangleAlert)
	default:
		body = ui.EmptyState("No output", "Formatter and task output shows here.").EmptyIcon(icons.ScrollText)
	}
	return ui.Column(header, ui.ViewOf(body).Grow(1)).Grow(1).Background(ui.TokenChrome).
		BorderTop(ui.TokenBorder, th.Stroke.Thin)
}

func (a *App) agentView(c *ui.Ctx) ui.View {
	th := c.Theme()
	return ui.Column(
		ui.Row(
			ui.Strong("Agent"),
			ui.Spacer(),
			ui.IconButton("agent-close", icons.X).Tooltip("Close").OnClick(func() { a.agentOpen = false }),
		).Height(30).PaddingLeft(th.Spacing.M).PaddingRight(th.Spacing.XS),
		ui.ViewOf(ui.EmptyState("Agent", "The coding agent arrives in a later milestone.").EmptyIcon(icons.Bot)).Grow(1),
	).Grow(1).Background(ui.TokenChrome).BorderLeft(ui.TokenBorder, th.Stroke.Thin)
}

func (a *App) openSettings() { a.ws.OpenPage(workspace.KindSettings) }

// settingsPage is the Settings tab. Config-backed settings replace it.
func (a *App) settingsPage(c *ui.Ctx) ui.View {
	th := c.Theme()
	names := theme.Names()
	opts := make([]ui.SelectOption, len(names))
	sel := 0
	for i, n := range names {
		opts[i] = ui.SelectOption{Label: n, Value: n}
		if n == theme.Selected() {
			sel = i
		}
	}
	return ui.Scroll("settings-scroll", ui.Column(
		ui.Title("Settings"),
		ui.Form("settings-form",
			ui.FormHeading("Appearance", ""),
			ui.FormSelect("set-theme", "Theme", "Color theme for the whole window", opts, sel, func(v string) { theme.Use(v) }),
		).Grouped(),
	).Gap(th.Spacing.M).Padding(th.Spacing.XL).Width(720)).Grow(1)
}
