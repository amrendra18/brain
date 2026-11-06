//go:build !windows

package corpus

import (
	"path/filepath"
	"syscall"
	"testing"
)

func TestNamedPipeNeverRead(t *testing.T) {
	root := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(root, "fifo.md"), 0600); err != nil {
		t.Fatal(err)
	}
	found, _, err := Scan(Default(root, []string{root}, nil))
	if err != nil || len(found) != 0 {
		t.Fatal(found, err)
	}
}
