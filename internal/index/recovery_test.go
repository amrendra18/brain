package index

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"modernc.org/sqlite"
)

func TestRebuildPreservesHealthyReadOnlyDatabase(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes")
	}
	for _, readonly := range []bool{false, true} {
		t.Run(map[bool]string{false: "writable", true: "read-only"}[readonly], func(t *testing.T) {
			source, state, db, config := fixture(t)
			text := "# Keeper\n\npreservedcontrol old source text.\n"
			write(t, source, "README.md", text)
			note := write(t, state, "notes/keeper.md", "saved note remains")
			refresh(t, db, state, config, false, 0)
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(state, "index.sqlite")
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			hidden := source + "-unavailable"
			if err = os.Rename(source, hidden); err != nil {
				t.Fatal(err)
			}
			if readonly {
				if err = os.Chmod(path, 0400); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.Chmod(path, 0600) })
			}
			opened, problem := Open(state, true, true, false)
			if problem == nil {
				_, problem = Refresh(opened, state, config, true, 0)
				opened.Close()
			}
			if problem == nil {
				t.Fatal("full indexing unexpectedly succeeded")
			}
			if readonly {
				var engine *sqlite.Error
				if !errors.As(problem, &engine) || engine.Code()&0xff != 8 {
					t.Fatal("read-only database did not cause a write refusal", problem)
				}
			}
			t.Logf("full index refusal: %T %v", problem, problem)
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("healthy database changed", err)
			}
			if readonly {
				info, err := os.Stat(path)
				if err != nil || info.Mode().Perm() != 0400 {
					t.Fatal("database permissions changed", info, err)
				}
			}
			stored, err := OpenRead(state, true)
			if err != nil {
				t.Fatal(err)
			}
			defer stored.Close()
			var retained string
			if err = stored.QueryRow("SELECT text FROM chunk_fts WHERE chunk_fts MATCH 'preservedcontrol'").Scan(&retained); err != nil || !strings.Contains(retained, "old source text") {
				t.Fatal("stored answer lost", retained, err)
			}
			for path, want := range map[string]string{filepath.Join(hidden, "README.md"): text, note: "saved note remains"} {
				got, err := os.ReadFile(path)
				if err != nil || string(got) != want {
					t.Fatal("authoritative file changed", path, err)
				}
			}
		})
	}
}

func TestRecoveryDriverErrors(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cases := []struct {
		name, sql string
		code      int
	}{
		{"missing-table", "SELECT value FROM absent", 1},
		{"create", "CREATE TABLE keeper(value); PRAGMA query_only=ON", 0},
		{"read-only", "INSERT INTO keeper VALUES(1)", 8},
	}
	for _, item := range cases {
		_, err = db.Exec(item.sql)
		if item.code == 0 {
			if err != nil {
				t.Fatal(err)
			}
			continue
		}
		var engine *sqlite.Error
		if !errors.As(err, &engine) || engine.Code() != item.code {
			t.Fatal(item.name, err)
		}
		t.Logf("%s: code=%d error=%v", item.name, engine.Code(), err)
	}
	path := filepath.Join(t.TempDir(), "index.sqlite")
	if err = os.WriteFile(path, []byte("deliberately invalid SQLite bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	damaged, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer damaged.Close()
	_, err = damaged.Exec("PRAGMA quick_check")
	var engine *sqlite.Error
	if !errors.As(err, &engine) || engine.Code() != 26 {
		t.Fatal(err)
	}
	t.Logf("not-database: code=%d error=%v", engine.Code(), err)
	source, state, index, config := fixture(t)
	write(t, source, "README.md", "# Keeper\n\nsearchable keeper")
	refresh(t, index, state, config, false, 0)
	if _, err = index.Exec("INSERT INTO chunk_fts(chunk_fts) VALUES('delete-all')"); err != nil {
		t.Fatal(err)
	}
	_, err = index.Exec(FTSCheck)
	if !errors.As(err, &engine) || engine.Code() != 267 {
		t.Fatal("FTS mismatch", err)
	}
	t.Logf("FTS corruption: code=%d error=%v", engine.Code(), err)
	index.Close()
	repaired, err := Open(state, true, true, false)
	if err != nil {
		t.Fatal(err)
	}
	defer repaired.Close()
	refresh(t, repaired, state, config, true, 0)
	if _, err = repaired.Exec(FTSCheck); err != nil {
		t.Fatal("FTS repair failed", err)
	}
}

func TestSchemaReadErrorsRetainDatabase(t *testing.T) {
	source, state, db, config := fixture(t)
	write(t, source, "README.md", "# Keeper\n\nunchanged stored answer")
	refresh(t, db, state, config, false, 0)
	if _, err := db.Exec("ALTER TABLE meta RENAME COLUMN value TO incompatible_value"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(state, "index.sqlite")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, rebuild := range []bool{false, true} {
		opened, err := Open(state, rebuild, rebuild, false)
		if opened != nil {
			opened.Close()
		}
		var engine *sqlite.Error
		if !errors.As(err, &engine) || engine.Code() != 1 {
			t.Fatal("schema execution error lost", err)
		}
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("unrecognized schema was replaced", err)
		}
	}
}

func TestMissingSchemaRecovery(t *testing.T) {
	for _, statement := range []string{"DROP TABLE meta", "DELETE FROM meta WHERE key='schema'"} {
		t.Run(statement, func(t *testing.T) {
			source, state, db, config := fixture(t)
			write(t, source, "README.md", "# Keeper\n\nrestored answer")
			refresh(t, db, state, config, false, 0)
			if _, err := db.Exec(statement); err != nil {
				t.Fatal(err)
			}
			db.Close()
			if opened, err := Open(state, false, false, false); !errors.Is(err, ErrMissing) {
				if opened != nil {
					opened.Close()
				}
				t.Fatal("missing schema was not identified", err)
			}
			rebuilt, err := Open(state, true, true, false)
			if err != nil {
				t.Fatal(err)
			}
			rebuilt.Close()
			if _, err = MigrateLocked(state, config); err != nil {
				t.Fatal(err)
			}
			rebuilt, err = Open(state, false, false, false)
			if err != nil {
				t.Fatal(err)
			}
			defer rebuilt.Close()
			var text string
			if err = rebuilt.QueryRow("SELECT text FROM chunks").Scan(&text); err != nil || !strings.Contains(text, "restored answer") {
				t.Fatal(text, err)
			}
		})
	}
}
