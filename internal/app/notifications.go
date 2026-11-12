// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package app

// Adapted from chapar ui/panels.go NotificationHistory
// (github.com/chapar-rest/chapar@2342e52a).

import (
	"strconv"
	"time"

	"github.com/mirzakhany/yoga/icons"
	"github.com/mirzakhany/yoga/ui"
)

// Notifications keeps recent messages for the footer's notification center,
// each as a card with its actions.
type Notifications struct {
	items  []notification
	unread int
	// Close hides the notification center, before an action runs.
	Close func()
}

type notification struct {
	text    string
	kind    ui.ToastVariant
	at      time.Time
	actions []notifAction
}

// notifAction is a button on a notification's card, such as "Open File".
type notifAction struct {
	label string
	run   func()
}

const maxNotifications = 100

// Add records a message, with optional actions to offer on its card.
func (n *Notifications) Add(text string, kind ui.ToastVariant, actions ...notifAction) {
	var acts []notifAction
	for _, act := range actions {
		if act.run != nil {
			acts = append(acts, act)
		}
	}
	n.items = append(n.items, notification{text: text, kind: kind, at: time.Now(), actions: acts})
	if len(n.items) > maxNotifications {
		n.items = n.items[len(n.items)-maxNotifications:]
	}
	n.unread++
}

// MarkRead clears the unread count, when the center is opened.
func (n *Notifications) MarkRead() { n.unread = 0 }

// Unread is the number of messages since the center was last opened.
func (n *Notifications) Unread() int { return n.unread }

// remove drops the notification recorded at at.
func (n *Notifications) remove(at time.Time) {
	for i, it := range n.items {
		if it.at.Equal(at) {
			n.items = append(n.items[:i], n.items[i+1:]...)
			return
		}
	}
}

func (n *Notifications) Layout(c *ui.Ctx) ui.View {
	th := c.Theme()
	cards := []ui.View{}
	for i := len(n.items) - 1; i >= 0; i-- {
		cards = append(cards, n.card(c, n.items[i]))
	}
	if len(cards) == 0 {
		cards = append(cards, ui.Muted("No notifications").Margin(th.Spacing.S))
	}
	return ui.Column(
		ui.Row(
			ui.Strong("Notifications"),
			ui.Spacer(),
			ui.Button("notif-clear", ui.Caption("Clear all")).Ghost().HoverFill().
				Disabled(len(n.items) == 0).
				OnClick(func() {
					n.items = nil
					n.unread = 0
				}),
			ui.IconButton("notif-close", icons.ChevronDown).Tooltip("Close").OnClick(func() {
				if n.Close != nil {
					n.Close()
				}
			}),
		).Align(ui.AlignCenter).Gap(th.Spacing.XS).PaddingXY(th.Spacing.S, th.Spacing.XS).
			BorderBottom(ui.TokenBorder, th.Stroke.Thin),
		ui.Scroll("notif-scroll", ui.Column(cards...).Gap(th.Spacing.S).Padding(th.Spacing.S)).Grow(1),
	).Grow(1)
}

// card shows one notification: its icon, text and dismiss button, and a row
// of its actions when it has any.
func (n *Notifications) card(c *ui.Ctx, item notification) ui.View {
	th := c.Theme()
	key := strconv.FormatInt(item.at.UnixNano(), 36)
	icon, tok := item.kind.Icon()
	kids := []ui.View{
		ui.Row(
			ui.Icon(icon, th.Metrics.IconSizeMD, tok.Resolve(th)),
			ui.Column(
				ui.Paragraph(item.text).Selectable("notif-text-"+key),
				ui.Caption(item.at.Format(time.Kitchen)).Style(ui.Spec{}.TextColor(ui.TokenForegroundMuted)),
			).Gap(th.Spacing.XXS).Grow(1),
			ui.IconButton("notif-x-"+key, icons.X).Tooltip("Dismiss").OnClick(func() { n.remove(item.at) }),
		).Align(ui.AlignStart).Gap(th.Spacing.S),
	}
	if len(item.actions) > 0 {
		buttons := []ui.View{ui.Spacer()}
		for i, act := range item.actions {
			btn := ui.Button("notif-act-"+key+"-"+strconv.Itoa(i), ui.Text(act.label))
			if i == len(item.actions)-1 {
				btn = btn.Primary()
			}
			buttons = append(buttons, btn.OnClick(func() {
				if n.Close != nil {
					n.Close()
				}
				act.run()
			}))
		}
		kids = append(kids, ui.Row(buttons...).Gap(th.Spacing.S))
	}
	return ui.Column(kids...).Gap(th.Spacing.S).Padding(th.Spacing.M).
		Background(ui.TokenChrome).
		Style(ui.Spec{}.Radius(th.Radius.Large).Border(ui.TokenBorder, th.Stroke.Thin))
}
