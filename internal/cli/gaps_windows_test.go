//go:build windows

package cli

import (
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
)

// gapFolder denies reading an owned folder while keeping its directory entry.
func gapFolder(t *testing.T, path string) func() {
	t.Helper()
	relative, err := filepath.Rel(os.TempDir(), path)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		t.Fatal("folder must be an owned temporary fixture")
	}
	caller, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	tool := filepath.Join(os.Getenv("SystemRoot"), "System32", "icacls.exe")
	saved := filepath.Join(t.TempDir(), "access.txt")
	run := func(args ...string) {
		t.Helper()
		if output, err := exec.Command(tool, args...).CombinedOutput(); err != nil {
			t.Fatal(err, string(output))
		}
	}
	run(path, "/save", saved)
	restored := false
	restore := func() {
		if restored {
			return
		}
		if output, err := exec.Command(tool, filepath.Dir(path), "/restore", saved).CombinedOutput(); err != nil {
			t.Error(err, string(output))
			return
		}
		restored = true
	}
	t.Cleanup(restore)
	run(path, "/deny", "*"+caller.Uid+":(RX)")
	if _, err := os.ReadDir(path); err == nil {
		t.Fatal("OS allowed reading the denied fixture")
	}
	return restore
}
