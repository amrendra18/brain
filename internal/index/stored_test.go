package index

import (
	"brain/internal/platform"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStoredLockLifetime(t *testing.T) {
	source, state, db, config := fixture(t)
	write(t, source, "alpha/README.md", "# Alpha\n\nA stored keeper.")
	refresh(t, db, state, config, false, 0)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	stored, err := OpenStored(state)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stored.Close() })
	if lock, err := platform.Acquire(state, "index", false); !errors.Is(err, platform.ErrLocked) {
		if lock != nil {
			lock.Close()
		}
		t.Fatal("the checkpointed reader did not retain the writer lock", err)
	}
	if err := stored.Close(); err != nil {
		t.Fatal(err)
	}
	lock, err := platform.Acquire(state, "index", false)
	if err != nil {
		t.Fatal("closing SQLite did not release the lock", err)
	}
	lock.Close()
	if err := stored.Close(); err != nil {
		t.Fatal(err)
	}
	live, err := Open(state, false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	if _, err := live.Exec("UPDATE meta SET value=value-60 WHERE key='scanned_at'"); err != nil {
		t.Fatal(err)
	}
	stored, err = OpenStored(state)
	if err != nil {
		t.Fatal(err)
	}
	defer stored.Close()
	lock, err = platform.Acquire(state, "index", false)
	if err != nil {
		t.Fatal("the normal live-WAL reader blocked the writer lock", err)
	}
	lock.Close()
}

func TestReadResourceLifetime(t *testing.T) {
	source, state, writer, config := fixture(t)
	write(t, source, "alpha/README.md", "# Alpha\n\nA stored keeper.")
	refresh(t, writer, state, config, false, 0)
	for _, readOnly := range []bool{false, true} {
		reader, err := OpenRead(state, readOnly)
		if err != nil {
			t.Fatal(err)
		}
		if scanned, err := ScannedAt(reader); err != nil || scanned == 0 {
			reader.Close()
			t.Fatal(scanned, err)
		}
		lock, err := platform.Acquire(state, "index", false)
		if err != nil {
			reader.Close()
			t.Fatal("a normal live-WAL reader blocked the writer", err)
		}
		lock.Close()
		if err := reader.Close(); err != nil {
			t.Fatal(err)
		}
	}
	writer.Close()
	// A damaged immutable fallback must release its guard on the refusal path.
	if err := os.WriteFile(filepath.Join(state, "index.sqlite"), []byte("damaged"), 0600); err != nil {
		t.Fatal(err)
	}
	if reader, err := OpenRead(state, true); err == nil {
		reader.Close()
		t.Fatal("damaged index accepted")
	}
	lock, err := platform.Acquire(state, "index", false)
	if err != nil {
		t.Fatal("failed read retained its lock", err)
	}
	lock.Close()
}
