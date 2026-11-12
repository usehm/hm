// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package terminal

import (
	"image/color"

	"github.com/charmbracelet/x/ansi"
	"github.com/mirzakhany/yoga/render"
	"github.com/mirzakhany/yoga/theme"
)

// Palette is what cells are drawn with: the theme's text and surface for
// default colors, and an ANSI set tuned for a light or dark surface.
type Palette struct {
	Fg, Bg, Cursor, Selection render.Color
	ANSI                      [16]render.Color
}

// ANSI colors are terminal content, not chrome, so they are fixed sets:
// the theme only decides light or dark.
var (
	darkANSI = [16]render.Color{
		rgb(0x1e, 0x1e, 0x1e), rgb(0xf1, 0x4c, 0x4c), rgb(0x23, 0xd1, 0x8b), rgb(0xf5, 0xf5, 0x43),
		rgb(0x3b, 0x8e, 0xea), rgb(0xd6, 0x70, 0xd6), rgb(0x29, 0xb8, 0xdb), rgb(0xcc, 0xcc, 0xcc),
		rgb(0x76, 0x76, 0x76), rgb(0xf1, 0x4c, 0x4c), rgb(0x23, 0xd1, 0x8b), rgb(0xf5, 0xf5, 0x43),
		rgb(0x3b, 0x8e, 0xea), rgb(0xd6, 0x70, 0xd6), rgb(0x29, 0xb8, 0xdb), rgb(0xe5, 0xe5, 0xe5),
	}
	lightANSI = [16]render.Color{
		rgb(0x00, 0x00, 0x00), rgb(0xcd, 0x31, 0x31), rgb(0x00, 0xbc, 0x00), rgb(0x94, 0x98, 0x00),
		rgb(0x04, 0x51, 0xa5), rgb(0xbc, 0x05, 0xbc), rgb(0x05, 0x98, 0xbc), rgb(0x55, 0x55, 0x55),
		rgb(0x66, 0x66, 0x66), rgb(0xcd, 0x31, 0x31), rgb(0x14, 0xce, 0x14), rgb(0xb5, 0xba, 0x00),
		rgb(0x04, 0x51, 0xa5), rgb(0xbc, 0x05, 0xbc), rgb(0x05, 0x98, 0xbc), rgb(0xa5, 0xa5, 0xa5),
	}
)

func rgb(r, g, b uint8) render.Color { return render.RGBA8(r, g, b, 0xff) }

// PaletteFor builds the palette for th.
func PaletteFor(th *theme.Theme) Palette {
	p := Palette{Fg: th.Foreground, Bg: th.Surface, Cursor: th.Foreground, Selection: th.Selection, ANSI: lightANSI}
	if th.Dark {
		p.ANSI = darkANSI
	}
	return p
}

// color resolves a cell color; nil is the default def.
func (p *Palette) color(c color.Color, def render.Color) render.Color {
	switch c := c.(type) {
	case nil:
		return def
	case ansi.BasicColor:
		return p.ANSI[c&15]
	case ansi.IndexedColor:
		if c < 16 {
			return p.ANSI[c]
		}
	}
	r, g, b, a := c.RGBA()
	return render.RGBA8(uint8(r>>8), uint8(g>>8), uint8(b>>8), uint8(a>>8))
}
