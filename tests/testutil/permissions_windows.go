//go:build windows

package testutil

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type accessSnapshot struct{ file, tool, parent, sid string }

var accessMu sync.Mutex
var accessBefore = map[string]accessSnapshot{}

// Permissions denies writes only in owned folders, then restores the saved access.
func Permissions(t *testing.T, path string, mode os.FileMode) error {
	t.Helper()
	relative, err := filepath.Rel(os.TempDir(), path)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("permission fixture must be inside the temporary folder")
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return os.Chmod(path, mode)
	}
	accessMu.Lock()
	defer accessMu.Unlock()
	before, known := accessBefore[path]
	if known {
		if data, err := exec.Command(before.tool, before.parent, "/restore", before.file).CombinedOutput(); err != nil {
			return fmt.Errorf("restore owned fixture access: %w: %s", err, data)
		}
	}
	if mode&0200 != 0 {
		delete(accessBefore, path)
		return nil
	}
	if !known {
		caller, err := user.Current()
		if err != nil {
			return err
		}
		before = accessSnapshot{file: filepath.Join(t.TempDir(), "access.txt"), tool: filepath.Join(os.Getenv("SystemRoot"), "System32", "icacls.exe"), parent: filepath.Dir(path), sid: "*" + caller.Uid}
		if data, err := exec.Command(before.tool, path, "/save", before.file).CombinedOutput(); err != nil {
			return fmt.Errorf("save owned fixture access: %w: %s", err, data)
		}
		t.Cleanup(func() {
			if err := Permissions(t, path, 0700); err != nil {
				t.Error(err)
			}
		})
	}
	accessBefore[path] = before
	if data, err := exec.Command(before.tool, path, "/deny", before.sid+":(W)").CombinedOutput(); err != nil {
		return fmt.Errorf("deny owned fixture writes: %w: %s", err, data)
	}
	return nil
}
