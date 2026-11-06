package platform

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPrivacyLease(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	if err := PrepareState(state); err != nil {
		t.Fatal(err)
	}
	if _, err := PrivacyLock(state, false, false); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	first, err := PrivacyLock(state, false, true)
	if err != nil {
		t.Fatal(err)
	}
	second, err := PrivacyLock(state, false, false)
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan *Lock, 1)
	failed := make(chan error, 1)
	go func() {
		lock, err := PrivacyLock(state, true, false)
		if err != nil {
			failed <- err
		} else {
			entered <- lock
		}
	}()
	select {
	case lock := <-entered:
		lock.Close()
		t.Fatal("exclusive lease entered while readers held leases")
	case err := <-failed:
		t.Fatal(err)
	case <-time.After(60 * time.Millisecond):
	}
	first.Close()
	select {
	case lock := <-entered:
		lock.Close()
		t.Fatal("exclusive lease ignored second reader")
	case err := <-failed:
		t.Fatal(err)
	case <-time.After(60 * time.Millisecond):
	}
	second.Close()
	var exclusive *Lock
	select {
	case exclusive = <-entered:
	case err := <-failed:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("exclusive lease never entered")
	}
	if _, err := PrivacyLock(state, false, false); !errors.Is(err, ErrLocked) {
		t.Fatal("reader did not time out under exclusive lease", err)
	}
	exclusive.Close()
	reader, err := PrivacyLock(state, false, false)
	if err != nil {
		t.Fatal(err)
	}
	reader.Close()
	if _, err := PrivacyLock(filepath.Join(state, "missing"), false, true); err == nil {
		t.Fatal("missing state accepted")
	}
	if err := os.Remove(filepath.Join(state, "locks", "privacy.lock")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "outside"), filepath.Join(state, "locks", "privacy.lock")); err == nil {
		if _, err := PrivacyLock(state, false, true); err == nil {
			t.Fatal("linked lease accepted")
		}
	}
}
