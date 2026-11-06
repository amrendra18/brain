package index

import (
	"brain/internal/platform"
	"bytes"
	"database/sql"
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestOrphanSnapshotAfterWALRemoval(t *testing.T) {
	source, state, writer, config := fixture(t)
	write(t, source, "README.md", "# Keeper\n\nA stored keeper.")
	refresh(t, writer, state, config, false, 0)
	walPath := filepath.Join(state, "index.sqlite-wal")
	if info, err := os.Stat(walPath); err != nil || info.Size() == 0 {
		t.Fatal("the writer must start with committed WAL pages", info, err)
	}
	// Model a writer finishing after orphan detection but before snapshot reads.
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(walPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the writer did not remove its WAL", err)
	}
	before, err := os.ReadFile(filepath.Join(state, "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if db, err := orphanSnapshot(state); !errors.Is(err, platform.ErrLocked) {
		if db != nil {
			db.Close()
		}
		t.Fatal("a disappearing WAL needs retry guidance", err)
	}
	stored, err := OpenStored(state)
	if err != nil {
		t.Fatal("retry could not read the completed index", err)
	}
	files, chunks, _, err := Totals(stored)
	stored.Close()
	if err != nil || files != 1 || chunks != 1 {
		t.Fatal("retry lost stored facts", files, chunks, err)
	}
	after, err := os.ReadFile(filepath.Join(state, "index.sqlite"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("stored reads changed the database", err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(filepath.Join(state, "index.sqlite"+suffix)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("stored reads recreated a sidecar", suffix, err)
		}
	}
	if err := os.WriteFile(walPath, []byte("corrupt WAL"), 0600); err != nil {
		t.Fatal(err)
	}
	if db, err := orphanSnapshot(state); !errors.Is(err, ErrMissing) {
		if db != nil {
			db.Close()
		}
		t.Fatal("corrupt WAL was hidden by retry guidance", err)
	}
}

func TestCommittedWALSnapshot(t *testing.T) {
	if state := os.Getenv("BRAIN_INDEX_ORPHAN_WAL"); state != "" {
		db, err := sql.Open("sqlite", filepath.Join(state, "index.sqlite"))
		if os.Getenv("BRAIN_INDEX_UNCOMMITTED") != "" {
			if err == nil {
				_, err = db.Exec("PRAGMA journal_mode=WAL; PRAGMA cache_size=1; BEGIN; CREATE TABLE spill(content BLOB); INSERT INTO spill VALUES(zeroblob(500000)); UPDATE meta SET value=999 WHERE key='scanned_at'")
			}
			if err != nil {
				t.Fatal(err)
			}
			os.Exit(0)
		}
		if err == nil {
			_, err = db.Exec("PRAGMA journal_mode=WAL; UPDATE meta SET value=100 WHERE key='scanned_at'")
		}
		if err == nil {
			_, err = db.Exec("UPDATE meta SET value=123456789 WHERE key='scanned_at'")
		}
		if err != nil {
			t.Fatal(err)
		}
		os.Exit(0)
	}
	source, state, db, config := fixture(t)
	write(t, source, "README.md", "# Keeper\n\nA keeper lease.")
	refresh(t, db, state, config, false, 0)
	baseline, err := ScannedAt(db)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	helper := exec.Command(os.Args[0], "-test.run=^TestCommittedWALSnapshot$")
	helper.Env = append(os.Environ(), "BRAIN_INDEX_ORPHAN_WAL="+state)
	if data, err := helper.CombinedOutput(); err != nil {
		t.Fatal(err, string(data))
	}
	os.Remove(filepath.Join(state, "index.sqlite-shm"))
	database, err := os.ReadFile(filepath.Join(state, "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	wal, err := os.ReadFile(filepath.Join(state, "index.sqlite-wal"))
	if err != nil {
		t.Fatal(err)
	}
	opened, err := OpenStored(state)
	if err != nil {
		t.Fatal(err)
	}
	last, err := ScannedAt(opened)
	if err != nil || last != 123456789 {
		t.Fatal("committed WAL was lost", last, err)
	}
	if _, err = opened.Exec("UPDATE meta SET value=1 WHERE key='scanned_at'"); err == nil {
		t.Fatal("private snapshot is writable")
	}
	opened.Close()
	if _, err = os.Stat(filepath.Join(state, "index.sqlite-shm")); !os.IsNotExist(err) {
		t.Fatal("snapshot created shared memory")
	}
	for name, wanted := range map[string][]byte{"index.sqlite": database, "index.sqlite-wal": wal} {
		got, _ := os.ReadFile(filepath.Join(state, name))
		if !bytes.Equal(got, wanted) {
			t.Fatal("snapshot altered source", name)
		}
	}
	holder, err := platform.Acquire(state, "index", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = OpenStored(state); !errors.Is(err, platform.ErrLocked) {
		t.Fatal("competing lock ignored", err)
	}
	holder.Close()
	pageSize := int(binary.BigEndian.Uint32(wal[8:12]))
	frameSize := pageSize + 24
	if len(wal) != 32+2*frameSize {
		t.Fatal("fixture needs two committed WAL frames", len(wal))
	}
	for _, mutation := range []struct {
		name   string
		change func([]byte) []byte
		want   float64
	}{
		{"complete", func(data []byte) []byte { return data }, 123456789},
		{"incomplete-tail", func(data []byte) []byte { return data[:len(data)-4] }, 100},
		{"corrupt-tail", func(data []byte) []byte { data[len(data)-1] ^= 1; return data }, 100},
		{"old-salt-tail", func(data []byte) []byte { data[32+frameSize+8] ^= 1; return data }, 100},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			changed := mutation.change(bytes.Clone(wal))
			image, err := committedPages(database, changed)
			if err != nil {
				t.Fatal(err)
			}
			memory, _ := sql.Open("sqlite", ":memory:")
			memory.SetMaxOpenConns(1)
			defer memory.Close()
			connection, err := memory.Conn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			err = connection.Raw(func(raw any) error { return raw.(interface{ Deserialize([]byte) error }).Deserialize(image) })
			connection.Close()
			last, problem := ScannedAt(memory)
			if err != nil || problem != nil || last != mutation.want {
				t.Fatal(last, err, problem)
			}
		})
	}
	for _, damage := range []struct{ db, wal []byte }{
		{nil, wal}, {database, nil}, {database, wal[:31]},
		{bytes.Repeat([]byte{0}, len(database)), wal},
	} {
		if _, err := committedPages(damage.db, damage.wal); err == nil {
			t.Fatal("damaged header accepted")
		}
	}
	for _, offset := range []int{0, 4, 8, 24} {
		bad := bytes.Clone(wal)
		bad[offset] ^= 1
		if _, err := committedPages(database, bad); err == nil {
			t.Fatal("invalid WAL header accepted", offset)
		}
	}
	for _, offset := range []int{18, 19} {
		bad := bytes.Clone(database)
		bad[offset] = 3
		if _, err := committedPages(bad, wal); err == nil {
			t.Fatal("unsupported database format accepted", offset)
		}
	}
	if image, err := committedPages(database, wal[:32+frameSize-1]); err != nil || !bytes.Equal(image[20:], database[20:]) {
		t.Fatal("incomplete first frame changed committed base", err)
	}
	if err = os.Remove(filepath.Join(state, "locks", "index.lock")); err != nil {
		t.Fatal(err)
	}
	if _, err = OpenStored(state); err == nil {
		t.Fatal("missing read lock was created")
	}
	os.WriteFile(filepath.Join(state, "locks", "index.lock"), nil, 0600)
	os.WriteFile(filepath.Join(state, "index.sqlite-wal"), []byte("broken WAL"), 0600)
	if _, err = OpenStored(state); err == nil {
		t.Fatal("malformed WAL answered")
	}
	os.WriteFile(filepath.Join(state, "index.sqlite"), database, 0600)
	os.Remove(filepath.Join(state, "index.sqlite-wal"))
	os.Remove(filepath.Join(state, "index.sqlite-shm"))
	helper = exec.Command(os.Args[0], "-test.run=^TestCommittedWALSnapshot$")
	helper.Env = append(os.Environ(), "BRAIN_INDEX_ORPHAN_WAL="+state, "BRAIN_INDEX_UNCOMMITTED=1")
	if data, err := helper.CombinedOutput(); err != nil {
		t.Fatal(err, string(data))
	}
	os.Remove(filepath.Join(state, "index.sqlite-shm"))
	spill, err := os.ReadFile(filepath.Join(state, "index.sqlite-wal"))
	if err != nil || len(spill) <= 32 {
		t.Fatal("no real spill frames", err)
	}
	for offset := 32; offset+frameSize <= len(spill); offset += frameSize {
		if binary.BigEndian.Uint32(spill[offset+4:offset+8]) != 0 {
			t.Fatal("spill fixture unexpectedly committed")
		}
	}
	opened, err = OpenStored(state)
	if err != nil {
		t.Fatal(err)
	}
	last, err = ScannedAt(opened)
	opened.Close()
	if err != nil || last != baseline {
		t.Fatal("uncommitted spill changed scan age", last, baseline, err)
	}
	if _, err = os.Stat(filepath.Join(state, "index.sqlite-shm")); !os.IsNotExist(err) {
		t.Fatal("spill snapshot created a file")
	}
}
