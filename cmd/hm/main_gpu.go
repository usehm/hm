//go:build !nogpu

// Command hm is an editor and AI coding agent in one window.
package main

import (
	"flag"
	"log"

	"github.com/mirzakhany/hm/internal/app"
)

func main() {
	flag.Parse()
	if err := app.Run(app.Options{Root: flag.Arg(0)}); err != nil {
		log.Fatal(err)
	}
}
