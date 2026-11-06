//go:build !windows

package testutil

import (
	"os"
	"testing"
)

// UnavailableSource removes Unix read/search access only from an owned folder.
func UnavailableSource(t *testing.T, path string) func() {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	originalMode := info.Mode().Perm()
	if err := os.Chmod(path, 0400); err != nil {
		t.Fatal(err)
	}
	restore := func() {
		if err := os.Chmod(path, originalMode); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(restore)
	return restore
}
