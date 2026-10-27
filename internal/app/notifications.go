package app

// Adapted from chapar ui/panels.go NotificationHistory
// (github.com/chapar-rest/chapar@2342e52a).

import (
	"strconv"
	"time"

	"github.com/mirzakhany/yoga/icons"
	"github.com/mirzakhany/yoga/ui"
)

// Notifications keeps recent messages for the footer's notification center.
// Clicking one opens it in full, with the actions it was recorded with.
type Notifications struct {
	items  []notification
	unread int
	// Close hides the notification center, before a picked one opens.
	Close func()
}

type notification struct {
	text    string
	kind    ui.ToastVariant
	at      time.Time
	actions []notifAction
}

// notifAction is a button on an opened notification, such as "Open File".
type notifAction struct {
	label string
	run   func()
}

const maxNotifications = 100

// Add records a message, with optional actions to offer when it is opened.
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
		line := ui.Row(
			ui.Caption(item.at.Format(time.Kitchen)).Width(56),
			ui.Paragraph(item.text).Style(style).Grow(1),
		).Align(ui.AlignStart).Gap(th.Spacing.S).Grow(1)
		if len(item.actions) > 0 {
			line = ui.Row(line, ui.Icon(icons.ChevronRight, 14, th.ForegroundMuted)).Align(ui.AlignStart).Grow(1)
		}
		rows = append(rows, ui.Button("notif-"+strconv.FormatInt(item.at.UnixNano(), 36), line).
			Ghost().HoverFill().
			OnClick(func() { n.open(c, item) }))
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
		ui.Scroll("notif-scroll", ui.Column(rows...).Gap(th.Spacing.XXS).PaddingXY(th.Spacing.XS, 0)).Grow(1),
	).Gap(th.Spacing.S).Grow(1)
}

// open shows item in full in a dialog, with Copy and the item's actions.
func (n *Notifications) open(c *ui.Ctx, item notification) {
	if n.Close != nil {
		n.Close()
	}
	title, sev := "Notification", ui.DialogSeverityInfo
	switch item.kind {
	case ui.ToastError:
		title, sev = "Error", ui.DialogSeverityError
	case ui.ToastWarning:
		title, sev = "Warning", ui.DialogSeverityWarning
	case ui.ToastSuccess:
		title = "Done"
	}
	actions := []ui.DialogAction{
		{Label: "Copy", OnClick: func() {
			if clip := c.Clipboard(); clip != nil {
				clip.Set(item.text)
			}
		}},
	}
	for i, act := range item.actions {
		actions = append(actions, ui.DialogAction{Label: act.label, Primary: i == len(item.actions)-1, OnClick: act.run})
	}
	if len(item.actions) == 0 {
		actions = append(actions, ui.DialogAction{Label: "Close", Primary: true})
	}
	c.Dialogs().Show(ui.DialogOpts{
		Title:      title,
		Width:      480,
		FitContent: true,
		Severity:   sev,
		Body: func(*ui.Ctx) ui.View {
			return ui.Column(
				ui.Caption(item.at.Format("Jan 2, 15:04:05")),
				ui.Paragraph(item.text),
			).Gap(c.Theme().Spacing.S)
		},
		Actions: actions,
	})
}
