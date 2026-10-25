//go:build !nogpu

// Command hm is an editor and AI coding agent in one window.
package main

import (
	"flag"
	"log"
	"os"

	"github.com/mirzakhany/hm/internal/app"
	"github.com/mirzakhany/hm/internal/gitx"
)

func main() {
	// git runs hm as its askpass helper to get a remote's credentials.
	if os.Getenv(gitx.AskPassEnv) == "1" {
		os.Exit(gitx.AskPass(os.Args[1:], os.Stdout))
	}
	flag.Parse()
	if err := app.Run(app.Options{Root: flag.Arg(0)}); err != nil {
		log.Fatal(err)
	}
}
