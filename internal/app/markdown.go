// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/mirzakhany/yoga/ui"
)

// mdMode is how a Markdown tab shows its file.
type mdMode int

const (
	mdSource mdMode = iota
	mdSplit
	mdPreview
)

var mdModeValues = []string{"source", "split", "preview"}

// mdDoc is the preview state of one Markdown file. The text is copied out
// of the editor only when its revision moves.
type mdDoc struct {
	mode mdMode
	rev  uint64
	text string
	read bool
}

func isMarkdown(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown":
		return true
	}
	return false
}

func (a *App) mdDoc(path string) *mdDoc {
	if a.mdDocs == nil {
		a.mdDocs = map[string]*mdDoc{}
	}
	d := a.mdDocs[path]
	if d == nil {
		d = &mdDoc{}
		a.mdDocs[path] = d
	}
	return d
}

// cycleMarkdownPreview steps the active Markdown tab through source, split
// and preview.
func (a *App) cycleMarkdownPreview() {
	ed := a.ws.ActiveEditor()
	if ed == nil || !isMarkdown(ed.Path) {
		return
	}
	d := a.mdDoc(ed.Path)
	d.mode = (d.mode + 1) % mdMode(len(mdModeValues))
}

// markdownMode is the source/split/preview switch, sized to sit at the end
// of the breadcrumb row.
func (a *App) markdownMode(path string) ui.View {
	d := a.mdDoc(path)
	return ui.Segmented("md-mode",
		ui.SegmentItem{Label: "Source", Value: mdModeValues[mdSource]},
		ui.SegmentItem{Label: "Split", Value: mdModeValues[mdSplit]},
		ui.SegmentItem{Label: "Preview", Value: mdModeValues[mdPreview]},
	).Selected(int(d.mode)).OnChange(func(v string) {
		for i, m := range mdModeValues {
			if m == v {
				d.mode = mdMode(i)
			}
		}
	}).Height(22).Background(ui.TokenSurface).Border(ui.TokenSurface, 1)
}

// markdownView wraps a Markdown file's editor with the rendered preview.
func (a *App) markdownView(c *ui.Ctx, ed *ui.Editor, editor ui.View) ui.View {
	th := c.Theme()
	path := ed.Path
	d := a.mdDoc(path)
	if d.mode == mdSource {
		return editor
	}
	if rev := ed.Revision(); !d.read || rev != d.rev {
		d.rev, d.text, d.read = rev, string(ed.Bytes()), true
	}
	base := filepath.Dir(path)
	preview := ui.Scroll("md-scroll:"+path,
		ui.Markdown("md:"+path, d.text).OnLink(func(u string) { a.openLink(base, u) }).
			Padding(th.Spacing.XL),
	).Grow(1).Background(ui.TokenSurface)
	var body ui.View = preview
	if d.mode == mdSplit {
		body = ui.Splitter("md-split", ui.Horizontal, editor, preview).Percents(50, 50).MinSizes(200, 200).Grow(1)
	}
	return body
}

// openLink follows a link clicked in rendered Markdown. Web and mail links
// open in the browser; anything else is taken as a file path, relative to
// base, and opened in a tab. Other schemes are ignored rather than handed
// to the OS, since agent replies are not trusted to launch apps.
func (a *App) openLink(base, link string) {
	if strings.HasPrefix(link, "#") || link == "" {
		return
	}
	if u, err := url.Parse(link); err == nil && u.Scheme != "" && len(u.Scheme) > 1 {
		switch strings.ToLower(u.Scheme) {
		case "http", "https", "mailto":
			if err := openURL(link); err != nil {
				a.fail("Could not open link", err)
			}
		case "file":
			a.openLinkedFile(u.Path)
		}
		return
	}
	p := link
	if i := strings.IndexAny(p, "#?"); i >= 0 {
		p = p[:i]
	}
	if unescaped, err := url.PathUnescape(p); err == nil {
		p = unescaped
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(base, p)
	}
	a.openLinkedFile(p)
}

func (a *App) openLinkedFile(p string) {
	fi, err := os.Stat(p)
	switch {
	case err != nil:
		a.fail("Could not open link", err)
	case fi.IsDir():
		a.sideOpen = true
		a.tree.Reveal(p)
	default:
		a.openFile(p)
	}
}
