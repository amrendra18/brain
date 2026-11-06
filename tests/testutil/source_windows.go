//go:build windows

package testutil

import (
	"os"
	"testing"
)

// UnavailableSource moves an owned folder without changing its read permissions.
func UnavailableSource(t *testing.T, path string) func() {
	t.Helper()
	moved := path + "-unavailable"
	if err := os.Rename(path, moved); err != nil {
		t.Fatal(err)
	}
	restore := func() {
		if _, err := os.Stat(moved); err == nil {
			if err := os.Rename(moved, path); err != nil {
				t.Error(err)
			}
		}
	}
	t.Cleanup(restore)
	return restore
}
