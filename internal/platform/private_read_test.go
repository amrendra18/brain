package platform

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestReadPrivateFile(t *testing.T) {
	folder := t.TempDir()
	if err := os.Chmod(folder, 0755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(folder, "owned")
	if err := os.WriteFile(file, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	data, err := ReadPrivateFile(folder, "owned", 7)
	if err != nil || string(data) != "private" {
		t.Fatalf("private read: %q %v", data, err)
	}
	if _, err := ReadPrivateFile(folder, "owned", 6); !errors.Is(err, ErrSourceOversize) {
		t.Fatal("oversize file accepted")
	}
	if _, err := ReadPrivateFile(folder, "owned", -1); !errors.Is(err, ErrSourceOversize) {
		t.Fatal("negative limit accepted")
	}
	if _, err := ReadPrivateFile(folder, "missing", 100); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing file lost its cause")
	}
	if _, err := ReadPrivateFile(folder, "../escape", 100); err == nil {
		t.Fatal("parent escape accepted")
	}
	if _, err := ReadPrivateFile(folder, ".", 100); err == nil {
		t.Fatal("directory accepted")
	}
	if runtime.GOOS == "windows" {
		return
	}
	info, _ := os.Stat(folder)
	if info.Mode().Perm() != 0755 {
		t.Fatal("parent permissions changed")
	}
	if err := os.Chmod(file, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPrivateFile(folder, "owned", 100); err == nil {
		t.Fatal("public leaf accepted")
	}
	info, _ = os.Stat(file)
	if info.Mode().Perm() != 0644 {
		t.Fatal("read changed leaf permissions")
	}
	if err := os.Chmod(file, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(file, filepath.Join(folder, "linked")); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPrivateFile(folder, "linked", 100); err == nil {
		t.Fatal("symlink accepted")
	}
	if err := os.Link(file, filepath.Join(folder, "hard")); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPrivateFile(folder, "hard", 100); err == nil {
		t.Fatal("hardlink accepted")
	}
}
