// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

//go:build !nogpu

// Command hm is an editor and AI coding agent in one window.
package main

import (
	"flag"
	"log"
	"os"

	"github.com/usehm/hm/internal/app"
	"github.com/usehm/hm/internal/cli/agentcmd"
	"github.com/usehm/hm/internal/gitx"
)

func main() {
	// git runs hm as its askpass helper to get a remote's credentials.
	if os.Getenv(gitx.AskPassEnv) == "1" {
		os.Exit(gitx.AskPass(os.Args[1:], os.Stdout))
	}
	// hm agent runs the agent on the command line, with no window.
	if len(os.Args) > 1 && os.Args[1] == "agent" {
		os.Exit(agentcmd.Main(os.Args[2:]))
	}
	installCLI := flag.Bool("install-cli", false, "install the `hm` shell command and exit")
	flag.Parse()
	if *installCLI {
		if err := app.InstallCLI(os.Stdout); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := app.Run(app.Options{Root: flag.Arg(0)}); err != nil {
		log.Fatal(err)
	}
}
