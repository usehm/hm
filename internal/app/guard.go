// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"os"
	"path/filepath"

	"github.com/mirzakhany/yoga/icons"
	"github.com/mirzakhany/yoga/ui"

	"github.com/usehm/hm/internal/agent/host"
)

func userGuardPath() string { return host.UserGuardPath() }

func projectGuardPath(root string) string { return host.ProjectGuardPath(root) }

// guardTemplate starts a new guard.yaml.
const guardTemplate = `# Rules for what hm's agent may do, checked before it asks you anything.
# deny blocks a call outright; ask makes you approve it every time; allow
# lets a command run without asking. A project's .hm/guard.yaml can only add
# deny and ask rules. Changes apply to the next message.

paths:
  # No reading or writing. A name without a slash matches at any depth;
  # ** is any number of folders; ~ is your home folder.
  deny: [".env", "*.pem", "~/.ssh", "~/.aws"]
  # Read, but never change or delete.
  read_only: []

commands:
  # Each pattern is matched against the whole command line and every
  # command in it (split at &&, ||, ;, | and in $(...)). * is any text;
  # start a pattern with re: for a regular expression.
  deny: ["rm -rf /*", "sudo *", "re:curl .*\\| *(ba)?sh"]
  ask: ["git push*", "git reset --hard*"]
  allow: []

network:
  # When set, the only hosts web fetches may reach.
  allow_hosts: []
  deny_hosts: []

tools:
  # Tool names; * is any text (mcp_*_delete_*).
  deny: []
  ask: []
`

// openGuard opens the user or project guard.yaml, creating it from the
// template.
func (a *App) openGuard(project bool) {
	path := userGuardPath()
	if project {
		path = projectGuardPath(a.ws.Root)
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			a.fail("Could not create guard.yaml", err)
			return
		}
		if err := os.WriteFile(path, []byte(guardTemplate), 0o644); err != nil {
			a.fail("Could not create guard.yaml", err)
			return
		}
	}
	a.focusGroup = 0
	a.openFile(path)
}

// securitySection is Settings → Agent Security: where the guard files are
// and whether they read.
func (a *App) securitySection(c *ui.Ctx) ui.View {
	th := c.Theme()
	status := ui.View(ui.Alert("The guard rules read fine.", ui.AlertSuccess))
	if _, err := a.agentHost().Guard(); err != nil {
		status = ui.Alert("The agent will not run until this is fixed: "+err.Error(), ui.AlertError)
	}
	row := func(id, title, path string, project bool) ui.View {
		label := "Create"
		if fileExists(path) {
			label = "Edit"
		}
		return ui.Row(
			ui.Column(ui.Text(title), ui.Caption(path).Ellipsis(ui.EllipsisMiddle)).Grow(1),
			ui.Button(id, ui.Text(label)).IconStart(icons.FileCode).OnClick(func() { a.openGuard(project) }),
		).Gap(th.Spacing.S)
	}
	return ui.Column(
		ui.Paragraph("guard.yaml rules are checked before the agent's mode or your earlier answers: "+
			"deny blocks files, commands, hosts or tools outright, ask makes you approve every time, and "+
			"allow lets listed commands run without asking. A project's rules can only add deny and ask."),
		status,
		row("guard-user", "Your rules", userGuardPath(), false),
		row("guard-project", "This project's rules", projectGuardPath(a.ws.Root), true),
	).Gap(th.Spacing.M)
}
