package platform

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStateLease(t *testing.T) {
	state := t.TempDir()
	os.Chmod(state, 0700)
	if _, err := StateLease(filepath.Join(state, "missing"), true, true); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if _, err := StateLease(state, false, false); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	first, err := StateLease(state, false, true)
	if err != nil {
		t.Fatal(err)
	}
	second, err := StateLease(state, false, false)
	if err != nil {
		t.Fatal(err)
	}
	second.Close()
	ready := make(chan *Lock, 1)
	failure := make(chan error, 1)
	go func() {
		lock, err := StateLease(state, true, false)
		if err != nil {
			failure <- err
		} else {
			ready <- lock
		}
	}()
	select {
	case lock := <-ready:
		lock.Close()
		t.Fatal("exclusive lease ignored writer")
	case err := <-failure:
		t.Fatal(err)
	case <-time.After(30 * time.Millisecond):
	}
	first.Close()
	select {
	case lock := <-ready:
		lock.Close()
	case err := <-failure:
		t.Fatal(err)
	case <-time.After(6 * time.Second):
		t.Fatal("lease never released")
	}
	lease, err := StateLease(state, true, true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = StateLease(state, false, false)
	lease.Close()
	if !errors.Is(err, ErrLocked) {
		t.Fatal("lock timeout", err)
	}
	if err = os.Remove(filepath.Join(state, "locks", "mutation.lock")); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(state, "locks", "mutation.lock"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = StateLease(state, false, true); err == nil {
		t.Fatal("directory lock accepted")
	}
	if _, err = StateLease(state, false, false); err == nil {
		t.Fatal("directory lock read accepted")
	}
}
func TestStateLeaseUnsafe(t *testing.T) {
	state := t.TempDir()
	os.Chmod(state, 0700)
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(state, "locks")); err != nil {
		t.Skip(err)
	}
	if _, err := StateLease(state, true, true); err == nil {
		t.Fatal("linked lock accepted")
	}
	os.Remove(filepath.Join(state, "locks"))
	os.WriteFile(filepath.Join(state, "locks"), []byte("block directory"), 0600)
	if _, err := StateLease(state, true, true); err == nil {
		t.Fatal("file lock parent accepted")
	}
}
