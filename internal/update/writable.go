// Copied from chapar internal/update/writable.go (github.com/chapar-rest/chapar@2342e52a).

//go:build darwin || windows

package update

import "os"

// dirWritable reports whether files can be created in dir, which a swap needs.
func dirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".hm-write-test-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}
