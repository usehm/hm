// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"fmt"
	"image"
	_ "image/gif" // decoders for the image viewer; yoga has PNG and JPEG
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yogaui/yoga/ui"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

// imageExts are the files hm shows as images instead of text.
var imageExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
	".bmp": true, ".tif": true, ".tiff": true,
}

func isImage(path string) bool { return imageExts[strings.ToLower(filepath.Ext(path))] }

// imageInfo is what the viewer says about an image file.
type imageInfo struct {
	format string
	w, h   int
	size   int64
	mod    time.Time
	err    error
}

// imageInfoFor reads path's header once per change; diskChanged drops it.
func (a *App) imageInfoFor(path string) imageInfo {
	if info, ok := a.images[path]; ok {
		return info
	}
	var info imageInfo
	f, err := os.Open(path)
	if err == nil {
		defer f.Close()
		var st os.FileInfo
		if st, err = f.Stat(); err == nil {
			info.size, info.mod = st.Size(), st.ModTime()
			var cfg image.Config
			if cfg, info.format, err = image.DecodeConfig(f); err == nil {
				info.w, info.h = cfg.Width, cfg.Height
			}
		}
	}
	info.err = err
	a.images[path] = info
	return info
}

// imageTab shows an image tab: the picture, shrunk to fit but never
// enlarged, with its format, size and dimensions in the breadcrumb row.
func (a *App) imageTab(c *ui.Ctx, path string) ui.View {
	info := a.imageInfoFor(path)
	if info.err != nil {
		return ui.Column(a.breadcrumb(c, path),
			ui.EmptyState("Could not show this image", info.err.Error()).Grow(1)).Grow(1)
	}
	th := c.Theme()
	caption := fmt.Sprintf("%s · %d × %d · %s", strings.ToUpper(info.format), info.w, info.h, byteSize(info.size))
	// The id changes with the file, so an edited image is read again.
	id := fmt.Sprintf("image-view:%s:%d", path, info.mod.UnixNano())
	return ui.Column(
		a.breadcrumb(c, path, ui.Caption(caption)),
		ui.Column(ui.ImageFile(id, path).Fit(ui.FitScaleDown).Grow(1)).
			Align(ui.AlignStretch).Grow(1).Padding(th.Spacing.M),
	).Grow(1)
}

// byteSize is n in B, KB or MB.
func byteSize(n int64) string {
	switch {
	case n < 1<<10:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
}
