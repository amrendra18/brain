//go:build !windows

package platform

import (
	"path/filepath"
	"syscall"
	"testing"
)

func TestReadPrivateFileRefusesPipe(t *testing.T) {
	folder := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(folder, "pipe"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPrivateFile(folder, "pipe", 100); err == nil {
		t.Fatal("pipe accepted")
	}
}
