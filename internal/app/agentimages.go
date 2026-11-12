// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mirzakhany/yoga/icons"
	"github.com/mirzakhany/yoga/input"
	"github.com/mirzakhany/yoga/ui"

	"github.com/usehm/hm/internal/agent/images"
	"github.com/usehm/hm/internal/agent/provider"
)

// Pictures reach the agent three ways: pasted into the message box,
// dropped on the window, or mentioned as @path. Each waits in the box as
// a thumbnail until the message is sent.

// maxImageFile is the largest picture file read; it is scaled down after.
const maxImageFile = 50 << 20

// loadImageFile reads a picture for a message.
func (a *App) loadImageFile(path string) (provider.ImageData, error) {
	st, err := os.Stat(path)
	if err != nil {
		return provider.ImageData{}, err
	}
	if st.Size() > maxImageFile {
		return provider.ImageData{}, fmt.Errorf("%s is too large (%d MB)", filepath.Base(path), st.Size()>>20)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return provider.ImageData{}, err
	}
	return images.Prepare(data)
}

// attachImage puts a picture in r's message box; what is not one is
// reported as name.
func (a *App) attachImage(r *agentRun, data []byte, name string) bool {
	im, err := images.Prepare(data)
	if err != nil {
		a.toast(name+": "+err.Error(), ui.ToastWarning)
		return false
	}
	r.attached = append(r.attached, im)
	r.focusInput = true
	return true
}

// pasteImage attaches the clipboard's picture when the message box has
// focus and the clipboard holds one; text pastes as usual.
func (a *App) pasteImage(c *ui.Ctx, k input.KeyEvent) bool {
	if k.Key != input.KeyV || !k.Mods.Primary() || k.Mods&(input.ModShift|input.ModAlt) != 0 {
		return false
	}
	r := a.run()
	if r == nil || !a.agentOpen || !agentInput(c, r).Focused() {
		return false
	}
	clip, ok := c.Clipboard().(input.ImageClipboard)
	if !ok {
		return false
	}
	data, ok := clip.GetImage()
	if !ok {
		return false
	}
	a.attachImage(r, data, "Pasted image")
	return true
}

// takeDrops handles files dropped on the window. With the agent open,
// pictures go in its message box and other files are mentioned in it;
// otherwise the files open in the editor.
func (a *App) takeDrops(c *ui.Ctx) {
	m := c.Mouse()
	if m == nil || len(m.Dropped) == 0 {
		return
	}
	paths := m.Dropped
	m.Dropped = nil
	r := a.run()
	if !a.agentOpen || r == nil {
		for _, p := range paths {
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				a.openFile(p)
			}
		}
		return
	}
	var mentions []string
	for _, p := range paths {
		if images.IsImagePath(p) {
			if im, err := a.loadImageFile(p); err == nil {
				r.attached = append(r.attached, im)
			} else {
				a.toast(filepath.Base(p)+": "+err.Error(), ui.ToastWarning)
			}
			continue
		}
		if rel, err := filepath.Rel(a.ws.Root, p); err == nil && !strings.HasPrefix(rel, "..") {
			p = filepath.ToSlash(rel)
		}
		mentions = append(mentions, "@"+p)
	}
	if len(mentions) > 0 {
		if r.input != "" && !strings.HasSuffix(r.input, " ") && !strings.HasSuffix(r.input, "\n") {
			r.input += " "
		}
		r.input += strings.Join(mentions, " ") + " "
	}
	r.focusInput = true
}

// thumbSize is how tall pictures show: in the message box, and in the
// chat.
const (
	thumbSmall = 56
	thumbLarge = 160
)

// thumb is a small PNG of a picture, made once per picture and size; the
// window draws only PNG and JPEG, and full-size pictures would fill the
// texture atlas.
func (a *App) thumb(im provider.ImageData, side int) []byte {
	key := fmt.Sprintf("%s@%d", im.Hash, side)
	if t, ok := a.agent.thumbs[key]; ok {
		return t
	}
	if a.agent.thumbs == nil {
		a.agent.thumbs = map[string][]byte{}
	}
	t, err := images.Thumb(im.Data, side*2) // sharp on 2x displays
	if err != nil {
		t = nil
	}
	a.agent.thumbs[key] = t
	return t
}

// imageView is a picture at height h, or a placeholder when it cannot be
// drawn.
func (a *App) imageView(c *ui.Ctx, id string, im provider.ImageData, h float32) ui.View {
	th := c.Theme()
	t := a.thumb(im, int(h))
	if t == nil {
		return ui.Row(ui.Icon(icons.Image, 14, th.ForegroundMuted), ui.Caption("image")).Gap(th.Spacing.XXS).
			Padding(th.Spacing.XS).Radius(th.Radius.Small).Border(ui.TokenBorder, th.Stroke.Thin)
	}
	tip := im.MediaType
	if im.Width > 0 {
		tip = fmt.Sprintf("%d×%d %s", im.Width, im.Height, strings.TrimPrefix(im.MediaType, "image/"))
	}
	return ui.ViewOf(ui.Image(id, t).Height(h)).Radius(th.Radius.Small).Border(ui.TokenBorder, th.Stroke.Thin).
		Tooltip(tip)
}

// imageRow shows pictures side by side, wrapping.
func (a *App) imageRow(c *ui.Ctx, id string, ims []provider.ImageData, h float32) ui.View {
	views := make([]ui.View, len(ims))
	for i, im := range ims {
		views[i] = a.imageView(c, fmt.Sprintf("%s-%d", id, i), im, h)
	}
	return ui.Row(views...).Gap(c.Theme().Spacing.XS).Wrap()
}

// attachedRow is the message box's pictures, each with a button to take
// it out; nil when there are none.
func (a *App) attachedRow(c *ui.Ctx, r *agentRun) ui.View {
	if len(r.attached) == 0 {
		return nil
	}
	th := c.Theme()
	views := make([]ui.View, len(r.attached))
	for i, im := range r.attached {
		id := r.wid(fmt.Sprintf("attached-%d", i))
		remove := ui.IconButton(id+"-x", icons.X).Tooltip("Remove").OnClick(func() {
			if i < len(r.attached) {
				r.attached = append(r.attached[:i:i], r.attached[i+1:]...)
			}
		})
		views[i] = ui.Row(a.imageView(c, id, im, thumbSmall), remove).Gap(th.Spacing.XXS).Align(ui.AlignStart)
	}
	return ui.Row(views...).Gap(th.Spacing.S).Wrap().PaddingXY(th.Spacing.S, th.Spacing.XXS)
}

// messageImages are the pictures of a user message.
func messageImages(m provider.Message) []provider.ImageData {
	var out []provider.ImageData
	for _, b := range m.Blocks {
		if b.Type == provider.Image && b.Image != nil {
			out = append(out, *b.Image)
		}
	}
	return out
}

// promptInput is what the user sent in m, to edit and send again: the
// text typed, without attached context, and the pictures.
func promptInput(m provider.Message) (string, []provider.ImageData) {
	var parts []string
	for _, b := range m.Blocks {
		if b.Type == provider.Text && strings.TrimSpace(b.Text) != "" {
			parts = append(parts, b.Text)
		}
	}
	return typedText(strings.Join(parts, "\n\n")), messageImages(m)
}
