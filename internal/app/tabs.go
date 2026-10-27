package app

import (
	"fmt"
	"path/filepath"

	"github.com/mirzakhany/yoga/ui"

	"github.com/mirzakhany/hm/internal/workspace"
)

// closeOthers closes every tab but i.
func (a *App) closeOthers(i int) {
	a.closeTabs(func(j int) bool { return j != i })
}

// closeRight closes the tabs after i.
func (a *App) closeRight(i int) {
	a.closeTabs(func(j int) bool { return j > i })
}

// closeSaved closes the tabs without unsaved changes.
func (a *App) closeSaved() {
	a.closeTabs(func(j int) bool { return !a.ws.Modified(j) })
}

func (a *App) closeAllTabs() {
	a.closeTabs(func(int) bool { return true })
}

// closeTabs closes the tabs sel picks, asking once about any with unsaved
// changes. The active tab stays active when it is kept.
func (a *App) closeTabs(sel func(i int) bool) {
	var picked []workspace.Tab
	var dirty []string
	for i, t := range a.ws.Tabs {
		if sel(i) {
			picked = append(picked, t)
			if a.ws.Modified(i) {
				dirty = append(dirty, t.Path)
			}
		}
	}
	if len(picked) == 0 {
		return
	}
	closeAll := func() {
		var active *workspace.Tab
		if t := a.ws.ActiveTab(); t != nil {
			v := *t // a copy: closing shifts the slice
			active = &v
		}
		for _, t := range picked {
			for i, u := range a.ws.Tabs {
				if u == t {
					a.ws.Close(i)
					break
				}
			}
		}
		if active != nil {
			for i, u := range a.ws.Tabs {
				if u == *active {
					a.ws.Active = i
				}
			}
		}
	}
	if len(dirty) == 0 {
		closeAll()
		return
	}
	title := "Save changes to " + filepath.Base(dirty[0]) + "?"
	if len(dirty) > 1 {
		title = fmt.Sprintf("Save changes to %d files?", len(dirty))
	}
	a.ctx.Dialogs().Show(ui.DialogOpts{
		Title:      title,
		Width:      420,
		FitContent: true,
		Severity:   ui.DialogSeverityWarning,
		Body: func(*ui.Ctx) ui.View {
			return ui.Paragraph("Your changes will be lost if you don't save them.")
		},
		Actions: []ui.DialogAction{
			{Label: "Cancel"},
			{Label: "Don't Save", OnClick: closeAll},
			{Label: "Save All", Primary: true, OnClick: func() {
				for _, p := range dirty {
					if err := a.saveFileNow(p); err != nil {
						a.fail("Save failed", err)
						return
					}
				}
				closeAll()
			}},
		},
	})
}
