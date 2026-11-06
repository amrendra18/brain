package testutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestOwnedUnavailableSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "README.md"), []byte("owned content"), 0600); err != nil {
		t.Fatal(err)
	}
	restore := UnavailableSource(t, path)
	if runtime.GOOS == "windows" {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("source was not moved")
		}
	} else if _, err := os.ReadFile(filepath.Join(path, "README.md")); err == nil {
		t.Fatal("unavailable source remained readable")
	}
	restore()
	if data, err := os.ReadFile(filepath.Join(path, "README.md")); err != nil || string(data) != "owned content" {
		t.Fatal(string(data), err)
	}
}
