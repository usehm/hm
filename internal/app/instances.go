// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"

	"github.com/yogaui/yoga"

	"github.com/usehm/hm/internal/config"
)

// Each hm window is its own process, and macOS keeps ⌘` for cycling one
// process's windows: the app never sees it. So every running hm leaves a
// file named by its pid under the state folder, and window.next and
// window.prev (⌘⌥` by default) bring the next one to the front.

func instancesDir() string { return filepath.Join(config.StateDir(), "instances") }

// registerInstance records this process until the returned func runs.
func registerInstance() func() {
	dir := instancesDir()
	path := filepath.Join(dir, strconv.Itoa(os.Getpid()))
	if os.MkdirAll(dir, 0o755) != nil || os.WriteFile(path, nil, 0o644) != nil {
		return func() {}
	}
	return func() { _ = os.Remove(path) }
}

// instanceSwitching reports whether window.next and window.prev do
// anything here.
func instanceSwitching() bool { return runtime.GOOS == "darwin" }

// instances lists the recorded pids in order.
func instances() []int {
	entries, _ := os.ReadDir(instancesDir())
	var pids []int
	for _, e := range entries {
		if pid, err := strconv.Atoi(e.Name()); err == nil {
			pids = append(pids, pid)
		}
	}
	slices.Sort(pids)
	return pids
}

// nextInstances is the other pids in switching order from self: the
// higher ones then the lower going forward (dir 1), the reverse going back.
func nextInstances(pids []int, self, dir int) []int {
	var after, before []int
	for _, p := range pids {
		switch {
		case p > self:
			after = append(after, p)
		case p < self:
			before = append(before, p)
		}
	}
	out := append(after, before...)
	if dir < 0 {
		slices.Reverse(out)
	}
	return out
}

// switchInstance brings the next (dir 1) or previous (dir -1) hm window to
// the front. Records of processes that are gone are removed on the way.
func (a *App) switchInstance(dir int) {
	for _, pid := range nextInstances(instances(), os.Getpid(), dir) {
		if yoga.ActivateInstance(pid) {
			return
		}
		_ = os.Remove(filepath.Join(instancesDir(), strconv.Itoa(pid)))
	}
}
