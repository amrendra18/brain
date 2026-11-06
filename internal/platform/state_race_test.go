package platform

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateFileRemovedAfterOpen(t *testing.T) {
	for _, writable := range []bool{false, true} {
		state := t.TempDir()
		if err := os.Chmod(state, 0700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(state, "index.sqlite-shm")
		if err := os.WriteFile(path, []byte("private sidecar"), 0600); err != nil {
			t.Fatal(err)
		}
		root, err := os.OpenRoot(state)
		if err != nil {
			t.Fatal(err)
		}
		before, err := root.Lstat("index.sqlite-shm")
		if err != nil {
			t.Fatal(err)
		}
		file, err := root.Open("index.sqlite-shm")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err = checkOpenedPrivateFile(root, "index.sqlite-shm", before, file, writable); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("removed sidecar must retry: %v", err)
		}
		file.Close()
		root.Close()
		if err = privateFile(state, "index.sqlite-shm", false, writable); err != nil {
			t.Fatal("absent sidecar refused", err)
		}
	}
}

func TestPrivateFileReplacementAfterOpen(t *testing.T) {
	for _, replacement := range []string{"regular", "symlink", "hardlink", "directory"} {
		t.Run(replacement, func(t *testing.T) {
			state := t.TempDir()
			if err := os.Chmod(state, 0700); err != nil {
				t.Fatal(err)
			}
			name := "index.sqlite-wal"
			path := filepath.Join(state, name)
			if err := os.WriteFile(path, []byte("original"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, 0644); err != nil {
				t.Fatal(err)
			}
			root, err := os.OpenRoot(state)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			before, err := root.Lstat(name)
			if err != nil {
				t.Fatal(err)
			}
			file, err := root.Open(name)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			original := filepath.Join(state, "original")
			if err = os.Rename(path, original); err != nil {
				t.Fatal(err)
			}
			switch replacement {
			case "regular":
				err = os.WriteFile(path, []byte("replacement"), 0600)
			case "symlink":
				err = os.Symlink(original, path)
			case "hardlink":
				err = os.Link(original, path)
			case "directory":
				err = os.Mkdir(path, 0700)
			}
			if err != nil {
				t.Fatal(err)
			}
			want := ErrStateUnsafe
			if replacement == "regular" {
				want = ErrLocked
			}
			if err = checkOpenedPrivateFile(root, name, before, file, true); !errors.Is(err, want) {
				t.Fatalf("replacement check = %v, want %v", err, want)
			}
			for _, writable := range []bool{false, true} {
				err = privateFile(state, name, false, writable)
				if (err == nil) != (replacement == "regular") {
					t.Fatalf("fresh check writable=%v: %v", writable, err)
				}
			}
			after, err := os.Stat(original)
			if err != nil || after.Mode() != before.Mode() {
				t.Fatal("original permissions changed", err)
			}
			if data, err := os.ReadFile(original); err != nil || string(data) != "original" {
				t.Fatal("original changed", string(data), err)
			}
		})
	}
}

func TestPrivateFileConcurrentRemoval(t *testing.T) {
	state := t.TempDir()
	if err := os.Chmod(state, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(state, "index.sqlite-shm")
	done := make(chan error, 1)
	go func() {
		for n := 0; n < 2000; n++ {
			if err := os.WriteFile(path, nil, 0600); err != nil {
				done <- err
				return
			}
			if err := os.Remove(path); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	var failure error
	for n := 0; n < 2000; n++ {
		if err := privateFile(state, "index.sqlite-shm", false, n%2 == 0); err != nil {
			failure = err
			break
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if failure != nil {
		t.Fatal("ordinary sidecar removal refused", failure)
	}
}
