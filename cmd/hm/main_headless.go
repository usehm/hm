//go:build nogpu

// Command hm, built with -tags nogpu, lays out one frame without a window and
// prints what it drew. CI uses it as a smoke test.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/mirzakhany/yoga"
	"github.com/mirzakhany/yoga/input"
	"github.com/mirzakhany/yoga/layout"
	"github.com/mirzakhany/yoga/render"
	"github.com/mirzakhany/yoga/shape"
	"github.com/mirzakhany/yoga/ui"

	"github.com/mirzakhany/hm/internal/app"
	"github.com/mirzakhany/hm/internal/gitx"
)

func main() {
	// git runs hm as its askpass helper to get a remote's credentials.
	if os.Getenv(gitx.AskPassEnv) == "1" {
		os.Exit(gitx.AskPass(os.Args[1:], os.Stdout))
	}
	flag.Parse()
	const w, h = 1280, 820
	text, err := shape.NewEngine(1, false)
	if err != nil {
		log.Fatal(err)
	}
	clip := &input.MemClipboard{}
	sheet := render.NewSpriteSheet(text.Atlas)
	yoga.SetResources(text, sheet, clip)

	a := app.Build(app.Options{Root: flag.Arg(0), NoSession: true, KeepPath: true, NoUpdates: true})
	defer a.Close()

	c := ui.New(text, ui.NewFocusScope(), nil)
	c.SetIcons(sheet)
	c.SetClipboard(clip)
	mouse, kb := &input.Mouse{}, &input.Keyboard{}
	root := ui.BuildFrame(c, a.Body, w, h, mouse, kb)
	dl := &render.DrawList{}
	layout.Paint(root, dl, text)
	fmt.Printf("hm headless frame: %d vertices, %d indices\n", len(dl.Vertices), len(dl.Indices))
}
