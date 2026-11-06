package platform

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPrivateStateRoot(t *testing.T) {
	parent := t.TempDir()
	state := filepath.Join(parent, "private")
	if err := CheckPrivateState(state); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if err := PrepareState(state); err != nil {
		t.Fatal(err)
	}
	if err := PrepareState(state); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(parent, "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := CheckPrivateState(file); !errors.Is(err, ErrStateUnsafe) {
		t.Fatal("file root", err)
	}
	link := filepath.Join(parent, "link")
	if err := os.Symlink(state, link); err != nil {
		t.Fatal(err)
	}
	if err := PrepareState(link); !errors.Is(err, ErrStateUnsafe) {
		t.Fatal("linked root", err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	for _, mode := range []os.FileMode{0755, 0770, 0705} {
		if err := os.Chmod(state, mode); err != nil {
			t.Fatal(err)
		}
		if err := PrepareState(state); !errors.Is(err, ErrStateUnsafe) {
			t.Fatal(mode, err)
		}
		info, _ := os.Stat(state)
		if info.Mode().Perm() != mode {
			t.Fatal("shared root changed")
		}
	}
	if err := os.Chmod(state, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(state, 0700)
	if err := CheckPrivateState(state); err != nil {
		t.Fatal("safe readonly root", err)
	}
}

func TestPrivateFileBoundary(t *testing.T) {
	state := t.TempDir()
	if err := os.Chmod(state, 0700); err != nil {
		t.Fatal(err)
	}
	if err := PreparePrivateFile(state, "missing", false); err != nil {
		t.Fatal(err)
	}
	if err := PreparePrivateFile(state, "private", true); err != nil {
		t.Fatal(err)
	}
	if err := PreparePrivateFile(state, "private", false); err != nil {
		t.Fatal(err)
	}
	if err := PreparePrivateFile(state, "../outside", true); err == nil {
		t.Fatal("escape accepted")
	}
	if err := os.Mkdir(filepath.Join(state, "directory"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := PreparePrivateFile(state, "directory", false); !errors.Is(err, ErrStateUnsafe) {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(state, "private"), filepath.Join(state, "link")); err != nil {
		t.Fatal(err)
	}
	if err := PreparePrivateFile(state, "link", false); err == nil {
		t.Fatal("symlink accepted")
	}
	if err := os.Link(filepath.Join(state, "private"), filepath.Join(state, "hardlink")); err != nil {
		t.Fatal(err)
	}
	if err := PreparePrivateFile(state, "hardlink", false); !errors.Is(err, ErrStateUnsafe) {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	if err := os.WriteFile(filepath.Join(state, "legacy"), []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := PreparePrivateFile(state, "legacy", false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(state, "legacy"))
	if err != nil || string(data) != "keep" {
		t.Fatal(string(data), err)
	}
	info, _ := os.Stat(filepath.Join(state, "legacy"))
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
}

func TestReadOnlyPrivateFileValidation(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	if err := PrepareState(state); err != nil {
		t.Fatal(err)
	}
	if err := CheckPrivateFile(state, "absent"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(state, "legacy")
	if err := os.WriteFile(path, []byte("private text"), 0644); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckPrivateFile(state, "legacy"); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if before.Mode() != after.Mode() {
		t.Fatal("read validation changed permissions")
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.Link(path, outside); err != nil {
		t.Fatal(err)
	}
	if err := CheckPrivateFile(state, "legacy"); !errors.Is(err, ErrStateUnsafe) {
		t.Fatal("linked file accepted", err)
	}
	if err := os.Mkdir(filepath.Join(state, "directory"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := CheckPrivateFile(state, "directory"); !errors.Is(err, ErrStateUnsafe) {
		t.Fatal("directory accepted", err)
	}
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != "private text" {
		t.Fatal(string(data), err)
	}
	after, err = os.Stat(outside)
	if err != nil || before.Mode() != after.Mode() {
		t.Fatal("outside mode changed", err)
	}
}
