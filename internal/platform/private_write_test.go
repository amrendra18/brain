package platform

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWritePrivateFileIfUnchanged(t *testing.T) {
	folder := t.TempDir()
	name := filepath.Join(folder, "job")
	data := []byte("original")
	hash := sha256.Sum256(data)
	digest := hex.EncodeToString(hash[:])
	if err := WritePrivateFileIfUnchanged(folder, "job", data, nil, ""); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := WritePrivateFileIfUnchanged(folder, "job", []byte("new"), before, digest); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(name)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("owned update replaced the checked inode")
	}
	if err := WritePrivateFileIfUnchanged(folder, "job", []byte("foreign"), before, digest); !errors.Is(err, ErrFileChanged) {
		t.Fatal("changed contents accepted", err)
	}
	current, _ := os.Stat(name)
	if err := os.Rename(name, name+".previous"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	newHash := sha256.Sum256([]byte("new"))
	if err := WritePrivateFileIfUnchanged(folder, "job", []byte("foreign"), current, hex.EncodeToString(newHash[:])); !errors.Is(err, ErrFileChanged) {
		t.Fatal("changed inode accepted", err)
	}
	if err := os.Remove(name + ".previous"); err != nil {
		t.Fatal(err)
	}
	if err := WritePrivateFileIfUnchanged(folder, "job", []byte("foreign"), nil, ""); !errors.Is(err, ErrFileChanged) {
		t.Fatal("existing destination replaced", err)
	}
	actual, err := os.ReadFile(name)
	if err != nil || string(actual) != "new" {
		t.Fatal("foreign update changed owned data")
	}
	entries, err := os.ReadDir(folder)
	if err != nil || len(entries) != 1 {
		t.Fatal("conditional publication left temporary artifacts")
	}
	if err := WritePrivateFileIfUnchanged(folder, "../escape", data, nil, ""); err == nil {
		t.Fatal("escape accepted")
	}
	if err := WritePrivateFileIfUnchanged(folder, "missing/job", data, nil, ""); err == nil {
		t.Fatal("missing parent accepted")
	}
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	if err := WritePrivateFileIfUnchanged(folder, "job", data, before, digest); !errors.Is(err, ErrFileChanged) {
		t.Fatal("missing owned file recreated", err)
	}
}

func TestWritePrivateDescriptorPreservesReplacement(t *testing.T) {
	folder := t.TempDir()
	name := filepath.Join(folder, "job")
	if err := os.WriteFile(name, []byte("owned"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(folder)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	hash := sha256.Sum256([]byte("owned"))
	file, err := openPrivateUpdate(root, "job", before, hex.EncodeToString(hash[:]))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := os.Rename(name, name+".owned"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte("foreign replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writePrivateInPlace(root, "job", file, before, []byte("completed owned update")); !errors.Is(err, ErrFileChanged) {
		t.Fatal("late replacement was not reported", err)
	}
	foreign, err := os.ReadFile(name)
	if err != nil || string(foreign) != "foreign replacement" {
		t.Fatal("late foreign replacement was overwritten")
	}
	owned, err := os.ReadFile(name + ".owned")
	if err != nil || string(owned) != "completed owned update" {
		t.Fatal("write did not stay on its verified inode")
	}
}

func TestWritePrivateUpdateFailure(t *testing.T) {
	folder := t.TempDir()
	if err := os.WriteFile(filepath.Join(folder, "job"), []byte("owned"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(folder)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	file, err := root.Open("job")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if err := writePrivateInPlace(root, "job", file, before, []byte("new")); err == nil {
		t.Fatal("read-only descriptor accepted a write")
	}
	data, err := os.ReadFile(filepath.Join(folder, "job"))
	if err != nil || string(data) != "owned" {
		t.Fatal("failed update changed existing data")
	}
}
