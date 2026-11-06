package index

import (
	"brain/internal/corpus"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIgnoreChangeRemovesStoredText(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		t.Run(map[bool]string{false: "current", true: "deleted"}[deleted], func(t *testing.T) {
			source, state, db, config := fixture(t)
			write(t, source, "allowed.md", "# Allowed\n\nAllowed sibling lantern.")
			hidden := write(t, source, "private.rtf", `{\rtf1\ansi\b HiddenHeading\b0\par excludedmarker\par}`)
			refresh(t, db, state, config, false, 0)
			var id string
			if err := db.QueryRow("SELECT chunks.id FROM chunks JOIN files ON files.id=chunks.file WHERE files.path='private.rtf' LIMIT 1").Scan(&id); err != nil {
				t.Fatal(err)
			}
			if deleted {
				if err := os.Remove(hidden); err != nil {
					t.Fatal(err)
				}
				refresh(t, db, state, config, false, 0)
			}
			write(t, source, ".brainignore", "private.rtf\n")
			result := refresh(t, db, state, config, false, 0)
			if result.Purged != 1 {
				t.Fatal(result)
			}
			for _, table := range []string{"files", "places", "chunks", "chunk_fts", "retired"} {
				rows, err := db.Query("SELECT * FROM " + table)
				if err != nil {
					t.Fatal(err)
				}
				cols, _ := rows.Columns()
				for rows.Next() {
					values := make([]any, len(cols))
					ptrs := make([]any, len(cols))
					for i := range ptrs {
						ptrs[i] = &values[i]
					}
					if err := rows.Scan(ptrs...); err != nil {
						t.Fatal(err)
					}
					for _, v := range values {
						if text, ok := v.(string); ok && (strings.Contains(text, "excludedmarker") || strings.Contains(text, "HiddenHeading") || strings.Contains(text, "private.rtf")) {
							t.Fatalf("%s retains %q", table, text)
						}
					}
				}
				rows.Close()
			}
			var heading string
			db.QueryRow("SELECT heading FROM chunks WHERE id=? UNION ALL SELECT heading FROM retired WHERE id=? LIMIT 1", id, id).Scan(&heading)
			if heading != "" {
				t.Fatal("excluded heading retained", heading)
			}
			files, err := os.ReadDir(filepath.Join(state, "converted"))
			if err != nil || len(files) != 0 {
				t.Fatal(files, err)
			}
			var allowed int
			if err := db.QueryRow("SELECT count(*) FROM chunks WHERE text LIKE '%Allowed sibling lantern%'").Scan(&allowed); err != nil || allowed != 1 {
				t.Fatal("allowed control", allowed, err)
			}
		})
	}
}

func TestIgnorePolicyClearsRetiredHeading(t *testing.T) {
	source, state, db, config := fixture(t)
	file := write(t, source, "private.md", "# RetiredProtectedHeading\n\nmarker")
	write(t, source, "allowed.md", "allowed")
	refresh(t, db, state, config, false, 0)
	os.Remove(file)
	refresh(t, db, state, config, false, 0)
	db.Exec("UPDATE files SET deleted_at=deleted_at-8*86400 WHERE path='private.md'")
	refresh(t, db, state, config, true, 0)
	var before int
	db.QueryRow("SELECT count(*) FROM retired").Scan(&before)
	if before == 0 {
		t.Fatal("positive control has no retired heading")
	}
	write(t, source, ".brainignore", "private.md\n")
	refresh(t, db, state, config, false, 0)
	var after int
	db.QueryRow("SELECT count(*) FROM retired").Scan(&after)
	if after != 0 {
		t.Fatal("retired headings retained", after)
	}
}

func TestIgnoreUnreadablePreservesIndex(t *testing.T) {
	source, state, db, config := fixture(t)
	write(t, source, "allowed.md", "allowed marker")
	refresh(t, db, state, config, false, 0)
	target := write(t, source, "control-target.md", "*.md")
	if err := os.Symlink(target, filepath.Join(source, ".brainignore")); err != nil {
		t.Fatal(err)
	}
	if _, err := Refresh(db, state, config, false, 0); err != corpus.ErrIgnoreUnreadable {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM chunks WHERE text LIKE '%allowed marker%'").Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}

func TestExcludedRemovalDamagedState(t *testing.T) {
	source, state, db, config := fixture(t)
	write(t, source, "allowed.md", "allowed")
	refresh(t, db, state, config, false, 0)
	scanned, err := corpus.ScanWithPolicy(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DROP TABLE places"); err != nil {
		t.Fatal(err)
	}
	if _, err := removeExcluded(db, scanned, false, nil); err == nil {
		t.Fatal("damaged index accepted")
	}
	var count int
	db.QueryRow("SELECT count(*) FROM files").Scan(&count)
	if count != 1 {
		t.Fatal("damaged index partially modified", count)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := removeExcluded(db, scanned, false, nil); err == nil {
		t.Fatal("closed index accepted")
	}
}

func TestUnchangedIgnorePolicyKeepsDeletionGuidance(t *testing.T) {
	source, state, db, config := fixture(t)
	file := write(t, source, "vanished.md", "# OrdinaryHeading\n\nordinary text")
	write(t, source, "allowed.md", "allowed")
	write(t, source, ".brainignore", "unrelated.md\n")
	refresh(t, db, state, config, false, 0)
	os.Remove(file)
	refresh(t, db, state, config, false, 0)
	db.Exec("UPDATE files SET deleted_at=deleted_at-8*86400 WHERE path='vanished.md'")
	refresh(t, db, state, config, true, 0)
	refresh(t, db, state, config, false, 0)
	var count int
	if err := db.QueryRow("SELECT count(*) FROM retired WHERE heading='OrdinaryHeading'").Scan(&count); err != nil || count != 1 {
		t.Fatal("ordinary guidance lost", count, err)
	}
}

func TestIgnoreRemovalKeepsSharedOccurrence(t *testing.T) {
	source, state, db, config := fixture(t)
	write(t, source, "private.md", "# Shared\n\nAllowed shared marker")
	write(t, source, "allowed.md", "# Shared\n\nAllowed shared marker")
	refresh(t, db, state, config, false, 0)
	var shared, discarded string
	if err := db.QueryRow("SELECT chunks.id FROM chunks JOIN files ON files.id=chunks.file WHERE files.path='private.md'").Scan(&shared); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT chunks.id FROM chunks JOIN files ON files.id=chunks.file WHERE files.path='allowed.md'").Scan(&discarded); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE places SET chunk=? WHERE chunk=?", shared, discarded); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DELETE FROM chunks WHERE id=?", discarded); err != nil {
		t.Fatal(err)
	}
	write(t, source, ".brainignore", "private.md\n")
	refresh(t, db, state, config, false, 0)
	var path, text string
	if err := db.QueryRow("SELECT files.path,chunks.text FROM chunks JOIN files ON files.id=chunks.file WHERE chunks.id=?", shared).Scan(&path, &text); err != nil || path != "allowed.md" || !strings.Contains(text, "Allowed shared marker") {
		t.Fatal(path, text, err)
	}
}

func TestIgnoreCacheCleanupRetry(t *testing.T) {
	source, state, db, config := fixture(t)
	write(t, source, "allowed.md", "Allowed retry control")
	write(t, source, "private.rtf", `{\rtf1\ansi Excluded cache retry marker.}`)
	refresh(t, db, state, config, false, 0)
	folder := filepath.Join(state, "converted")
	before, err := os.ReadDir(folder)
	if err != nil || len(before) != 1 {
		t.Fatal("missing positive cache control", before, err)
	}
	write(t, source, ".brainignore", "private.rtf\n")
	blocked := filepath.Join(state, "held-converted")
	if err := os.Rename(folder, blocked); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(folder, []byte("deliberately blocked cache folder"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Refresh(db, state, config, false, 0); err == nil {
		t.Fatal("blocked cache cleanup accepted")
	}
	if err := os.Remove(folder); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(blocked, folder); err != nil {
		t.Fatal(err)
	}
	refresh(t, db, state, config, false, 0)
	after, err := os.ReadDir(folder)
	if err != nil || len(after) != 0 {
		t.Fatal("retry retained excluded cache", after, err)
	}
	var allowed int
	if err := db.QueryRow("SELECT count(*) FROM chunks WHERE text LIKE '%Allowed retry control%'").Scan(&allowed); err != nil || allowed != 1 {
		t.Fatal("allowed sibling lost", allowed, err)
	}
}
