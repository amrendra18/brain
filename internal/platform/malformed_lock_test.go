package platform

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestIndexLocksRefuseMalformedFiles(t *testing.T) {
	for _, kind := range []string{"directory", "symlink", "hardlink"} {
		t.Run(kind, func(t *testing.T) {
			state := t.TempDir()
			if err := os.Mkdir(filepath.Join(state, "locks"), 0700); err != nil {
				t.Fatal(err)
			}
			name := filepath.Join(state, "locks", "index.lock")
			target := filepath.Join(state, "target")
			if err := os.WriteFile(target, []byte("unchanged"), 0600); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "directory":
				err = os.Mkdir(name, 0700)
			case "symlink":
				err = os.Symlink(target, name)
			case "hardlink":
				err = os.Link(target, name)
			}
			if err != nil {
				if kind != "directory" {
					t.Skipf("cannot create %s test input: %v", kind, err)
				}
				t.Fatal(err)
			}
			for _, acquire := range []func() (*Lock, error){
				func() (*Lock, error) { return ReadLock(state) },
				func() (*Lock, error) { return Acquire(state, "index", false) },
			} {
				lock, err := acquire()
				if lock != nil {
					lock.Close()
				}
				if err == nil || errors.Is(err, ErrLocked) {
					t.Fatalf("%s treated as an ordinary lock: %v", kind, err)
				}
			}
			if content, err := os.ReadFile(target); err != nil || string(content) != "unchanged" {
				t.Fatal("malformed lock changed target", err)
			}
		})
	}
}
