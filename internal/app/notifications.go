package app

// Adapted from chapar ui/panels.go NotificationHistory
// (github.com/chapar-rest/chapar@2342e52a).

import (
	"time"

	"github.com/mirzakhany/yoga/icons"
	"github.com/mirzakhany/yoga/ui"
)

// Notifications keeps recent messages for the footer's notification center.
type Notifications struct {
	items  []notification
	unread int
}

type notification struct {
	text string
	kind ui.ToastVariant
	at   time.Time
}

const maxNotifications = 100

// Add records a message.
func (n *Notifications) Add(text string, kind ui.ToastVariant) {
	n.items = append(n.items, notification{text: text, kind: kind, at: time.Now()})
	if len(n.items) > maxNotifications {
		n.items = n.items[len(n.items)-maxNotifications:]
	}
	n.unread++
}

// MarkRead clears the unread count, when the center is opened.
func (n *Notifications) MarkRead() { n.unread = 0 }

// Unread is the number of messages since the center was last opened.
func (n *Notifications) Unread() int { return n.unread }

func (n *Notifications) Layout(c *ui.Ctx) ui.View {
	th := c.Theme()
	rows := []ui.View{}
	for i := len(n.items) - 1; i >= 0; i-- {
		item := n.items[i]
		style := ui.Spec{}.TextColor(ui.TokenForegroundMuted)
		switch item.kind {
		case ui.ToastError:
			style = ui.Spec{}.TextColor(ui.TokenError)
		case ui.ToastWarning:
			style = ui.Spec{}.TextColor(ui.TokenWarning)
		}
		rows = append(rows, ui.Row(
			ui.Caption(item.at.Format(time.Kitchen)).Width(56),
			ui.Paragraph(item.text).Style(style).Grow(1),
		).Align(ui.AlignStart).Gap(th.Spacing.S))
	}
	if len(rows) == 0 {
		rows = append(rows, ui.Muted("No notifications"))
	}
	return ui.Column(
		ui.Row(
			ui.Strong("Notifications"),
			ui.Spacer(),
			ui.IconButton("notif-clear", icons.Trash2).Tooltip("Clear").OnClick(func() {
				n.items = nil
				n.unread = 0
			}),
		).Margin(th.Spacing.S),
		ui.Scroll("notif-scroll", ui.Column(rows...).Gap(th.Spacing.S).PaddingXY(th.Spacing.S, 0)).Grow(1),
	).Gap(th.Spacing.S).Grow(1)
}
