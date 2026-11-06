package index

import (
	"brain/internal/chunk"
	"brain/internal/corpus"
	"brain/internal/platform"
	"brain/tests/testutil"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func fixture(t *testing.T) (string, string, *sql.DB, corpus.Config) {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join(root, "corpus")
	state := filepath.Join(root, "state")
	os.MkdirAll(source, 0700)
	os.MkdirAll(state, 0700)
	db, err := Open(state, true, false, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return source, state, db, corpus.Default(source, []string{source}, nil)
}
func write(t *testing.T, root, name, text string) string {
	t.Helper()
	file := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	return file
}
func refresh(t *testing.T, db *sql.DB, state string, config corpus.Config, full bool, limit int) Report {
	t.Helper()
	result, err := Refresh(db, state, config, full, limit)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func TestDedupe(t *testing.T) {
	source, state, db, config := fixture(t)
	write(t, source, "alpha/README.md", "# Dock\n\nReturn the lease.")
	write(t, source, "beta/README.md", "# Dock\n\nReturn the lease.")
	result := refresh(t, db, state, config, false, 0)
	files, chunks, folded, err := Totals(db)
	if err != nil || result.New != 2 || files != 2 || chunks != 2 || folded != 0 {
		t.Fatal(result, files, chunks, folded, err)
	}
	var also string
	if err = db.QueryRow("SELECT also_in FROM chunks").Scan(&also); err != nil || also != "[]" {
		t.Fatal(also, err)
	}
	os.Remove(filepath.Join(source, "beta/README.md"))
	if result = refresh(t, db, state, config, false, 0); result.Removed != 1 {
		t.Fatal(result)
	}
	if err = db.QueryRow("SELECT also_in FROM chunks").Scan(&also); err != nil || also != "[]" {
		t.Fatal(also, err)
	}
	os.Remove(filepath.Join(source, "alpha/README.md"))
	refresh(t, db, state, config, false, 0)
	db.Exec("UPDATE files SET deleted_at=deleted_at-8*86400")
	if result = refresh(t, db, state, config, true, 0); result.Purged != 2 {
		t.Fatal(result)
	}
	var retired int
	db.QueryRow("SELECT count(*) FROM retired").Scan(&retired)
	if retired != 2 {
		t.Fatal(retired)
	}
}

func TestPendingUnavailableSource(t *testing.T) {
	source, state, db, config := fixture(t)
	write(t, source, "alpha/README.md", "# Alpha\n\nA keeper.")
	write(t, source, "beta/README.md", "# Beta\n\nA beacon.")
	refresh(t, db, state, config, false, 0)
	found, _, err := corpus.Scan(config)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := Pending(db, found, nil); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	os.Remove(filepath.Join(source, "beta/README.md"))
	write(t, source, "new.md", "# New\n\nA harbour.")
	found, _, err = corpus.Scan(config)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := Pending(db, found, []string{filepath.Join(source, "absent")}); err != nil || count != 2 {
		t.Fatal(count, err)
	}
	if count, err := Pending(db, found, []string{filepath.Join(source, "beta")}); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if count, err := Pending(db, nil, []string{source}); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	if count, err := Pending(db, nil, []string{source + string(filepath.Separator)}); err != nil || count != 0 {
		t.Fatal("trailing slash counted unavailable files as removed", count, err)
	}
	db.Close()
	if _, err := Pending(db, nil, nil); err == nil {
		t.Fatal("closed index accepted")
	}
}
func TestSourceRootTransitions(t *testing.T) {
	source, state, db, config := fixture(t)
	write(t, source, "alpha/README.md", "# Keep\n\nA stable harbour.")
	refresh(t, db, state, config, false, 0)
	for _, full := range []bool{false, true} {
		for _, root := range []string{filepath.Join(source, "alpha"), source} {
			config.Sources = []corpus.Source{{Path: root}}
			var oldID string
			if err := db.QueryRow("SELECT id FROM chunks").Scan(&oldID); err != nil {
				t.Fatal(err)
			}
			refresh(t, db, state, config, full, 0)
			var oldCount int
			if err := db.QueryRow("SELECT count(*) FROM chunks WHERE id=?", oldID).Scan(&oldCount); err != nil || oldCount != 0 {
				t.Fatal("old source-boundary ID remains active", oldCount, err)
			}
			var count int
			var path, stored, after string
			db.QueryRow("SELECT count(*) FROM files").Scan(&count)
			db.QueryRow("SELECT path,source FROM files").Scan(&path, &stored)
			db.QueryRow("SELECT id FROM chunks").Scan(&after)
			want := "alpha/README.md"
			if root != source {
				want = "README.md"
			}
			if count != 1 || stored != root || path != want || after != chunk.ID(chunk.DocumentKey("source:0:", want, "# Keep\n\nA stable harbour.")) {
				t.Fatal(count, stored, path, after)
			}
		}
	}
}
func TestRefreshStates(t *testing.T) {
	source, state, db, config := fixture(t)
	file := write(t, source, "alpha/a.md", "# A\n\nStable text.")
	write(t, source, "alpha/big.md", "oversized text that will be skipped")
	write(t, source, "alpha/talk.rtf", "unsupported text")
	locked := write(t, source, "alpha/locked.md", "cannot read")
	if runtime.GOOS == "windows" {
		os.WriteFile(locked, []byte(strings.Repeat("oversized ", 5)), 0600)
	}
	testutil.Permissions(t, locked, 0)
	t.Cleanup(func() { testutil.Permissions(t, locked, 0600) })
	config.Options.MaxFileBytes = 30
	result := refresh(t, db, state, config, false, 0)
	if result.New != 1 || len(result.Skipped) != 3 {
		t.Fatal(result)
	}
	if result = refresh(t, db, state, config, false, 200); result.Pending != 0 {
		t.Fatal(result)
	}
	info, _ := os.Stat(file)
	os.Chtimes(file, info.ModTime().Add(-1000000000), info.ModTime().Add(-1000000000))
	if result = refresh(t, db, state, config, false, 0); result.Updated != 0 {
		t.Fatal(result)
	}
	write(t, source, "alpha/a.md", "# A\n\nNew heron.")
	write(t, source, "alpha/b.md", "# B\n\nNew heron.")
	if result = refresh(t, db, state, config, false, 2); result.Pending < 2 {
		t.Fatal(result)
	}
	lock, err := platform.Acquire(state, "index", false)
	if err != nil {
		t.Fatal(err)
	}
	if result = refresh(t, db, state, config, false, 200); result.Pending == 0 {
		t.Fatal(result)
	}
	lock.Close()
	refresh(t, db, state, config, false, 0)
	if last, err := ScannedAt(db); err != nil || last != 0 {
		t.Fatal(last, err)
	}
	if Bytes(state) == 0 {
		t.Fatal("no database bytes")
	}
	config.Sources = []corpus.Source{{Path: filepath.Join(source, "missing")}}
	if _, err := Refresh(db, state, config, false, 0); err == nil {
		t.Fatal("missing source accepted")
	}
}
func TestSqliteVersionCheck(t *testing.T) {
	for _, version := range []string{"3.43.0", "3.53.4", "4.0.0"} {
		if !VersionOK(version) {
			t.Fatal(version)
		}
	}
	for _, version := range []string{"3.42.9", "2.99.9", "broken"} {
		if VersionOK(version) {
			t.Fatal(version)
		}
	}
	if Locked(os.ErrClosed) || Locked(nil) {
		t.Fatal("lock classification")
	}
	source, state, db, config := fixture(t)
	write(t, source, "a.md", "harbour")
	if last, err := ScannedAt(db); err != nil || last != 0 {
		t.Fatal(last, err)
	}
	refresh(t, db, state, config, false, 0)
	db.Exec("UPDATE meta SET value=99 WHERE key='schema'")
	db.Close()
	if _, err := Open(state, false, false, false); err == nil {
		t.Fatal("wrong schema accepted")
	}
	rebuilt, err := Open(state, true, true, false)
	if err != nil {
		t.Fatal(err)
	}
	rebuilt.Close()
	if _, err := Open(filepath.Join(state, "absent"), false, false, false); err == nil {
		t.Fatal("missing index accepted")
	}
}
func TestDatabaseFailures(t *testing.T) {
	source, state, db, config := fixture(t)
	write(t, source, "a.md", "harbour")
	db.Close()
	if _, err := Refresh(db, state, config, false, 0); err == nil {
		t.Fatal("closed database accepted")
	}
	if _, _, _, err := Totals(db); err == nil {
		t.Fatal("closed totals accepted")
	}
	if _, err := ScannedAt(db); err == nil {
		t.Fatal("closed scan accepted")
	}
	if _, err := apply(db, nil, nil, 0, 0); err == nil {
		t.Fatal("closed transaction accepted")
	}
	if err := os.WriteFile(filepath.Join(state, "index.sqlite"), []byte("broken SQLite"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(state, false, false, false); err == nil {
		t.Fatal("corrupt database accepted")
	}
	rebuilt, err := Open(state, true, true, false)
	if err != nil {
		t.Fatal(err)
	}
	rebuilt.Close()
}
