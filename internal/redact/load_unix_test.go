//go:build !windows

package redact

import (
	"path/filepath"
	"syscall"
	"testing"
)

func TestPrivateValuesRefusePipe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pipe")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != ErrPolicyInvalid {
		t.Fatal("pipe accepted")
	}
}
