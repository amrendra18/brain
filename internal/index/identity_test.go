package index

import (
	"brain/internal/chunk"
	"brain/internal/corpus"
	"path/filepath"
	"testing"
	"time"
)

func TestSourceNamespaceStability(t *testing.T) {
	var portable string
	for copy := 0; copy < 2; copy++ {
		source, state, db, config := fixture(t)
		write(t, source, "beta/memory/README.md", "A stable keeper.")
		config.Sources = []corpus.Source{{Path: filepath.Join(source, "*", "memory")}}
		refresh(t, db, state, config, false, 0)
		var before string
		if err := db.QueryRow("SELECT id FROM chunks").Scan(&before); err != nil {
			t.Fatal(err)
		}
		if portable != "" && portable != before {
			t.Fatal("moving the corpus changed its ID", portable, before)
		}
		portable = before
		write(t, source, "alpha/memory/README.md", "A stable keeper.")
		write(t, source, "extra/README.md", "A stable keeper.")
		config.Sources = append(config.Sources, corpus.Source{Path: filepath.Join(source, "extra")})
		refresh(t, db, state, config, false, 0)
		var after string
		if err := db.QueryRow("SELECT chunks.id FROM chunks JOIN files ON files.id=chunks.file WHERE files.source=?", filepath.Join(source, "beta", "memory")).Scan(&after); err != nil || after != before {
			t.Fatal("adding a match or source changed its ID", before, after, err)
		}
		_, chunks, folded, err := Totals(db)
		if err != nil || chunks != 3 || folded != 0 {
			t.Fatal(chunks, folded, err)
		}
		config.Sources[0], config.Sources[1] = config.Sources[1], config.Sources[0]
		refresh(t, db, state, config, false, 0)
		if err := db.QueryRow("SELECT chunks.id FROM chunks JOIN files ON files.id=chunks.file WHERE files.source=?", filepath.Join(source, "beta", "memory")).Scan(&after); err != nil || after == before {
			t.Fatal("source reordering did not refresh its identity", before, after, err)
		}
	}
}

func TestExistingIndexIdentityUpgrade(t *testing.T) {
	for _, mode := range []string{"ordinary", "full", "inline", "interrupted"} {
		t.Run(mode, func(t *testing.T) {
			source, state, db, config := fixture(t)
			text := "A shared keeper."
			write(t, source, "alpha/README.md", text)
			write(t, source, "beta/README.md", text)
			refresh(t, db, state, config, false, 0)
			oldID := chunk.ID(chunk.Key(text))
			if _, err := db.Exec("UPDATE places SET chunk=?; DELETE FROM chunks WHERE row<>(SELECT min(row) FROM chunks); UPDATE chunks SET id=?,dedupe_key=?; UPDATE files SET chunker_version=2; ALTER TABLE files DROP COLUMN namespace", oldID, oldID, chunk.Key(text)); err != nil {
				t.Fatal(err)
			}
			stored, err := OpenRead(state, true)
			if err != nil {
				t.Fatal(err)
			}
			var got string
			err = stored.QueryRow("SELECT text FROM chunks WHERE id=?", oldID).Scan(&got)
			stored.Close()
			if err != nil || got != text {
				t.Fatal("old index unreadable", got, err)
			}
			if report := refresh(t, db, state, config, false, 1); report.Pending != 2 {
				t.Fatal(report)
			}
			if needed, err := needsIdentityUpgrade(db); err != nil || !needed {
				t.Fatal("pending read changed schema", needed, err)
			}
			if mode == "interrupted" {
				if err := upgradeIdentity(db); err != nil {
					t.Fatal(err)
				}
				found, _, err := corpus.Scan(config)
				if err != nil {
					t.Fatal(err)
				}
				items, _, _, err := changes(db, found, false)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = apply(db, []work{read(items[0], state, config, false)}, nil, float64(time.Now().Unix()), 0); err != nil {
					t.Fatal(err)
				}
				if err = db.QueryRow("SELECT text FROM chunks WHERE id=?", oldID).Scan(&got); err != nil || got != text {
					t.Fatal("partial migration lost old text", got, err)
				}
				db.Close()
				db, err = Open(state, false, false, false)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
			}
			limit := 0
			if mode == "inline" {
				limit = 200
			}
			refresh(t, db, state, config, mode == "full", limit)
			files, chunks, folded, err := Totals(db)
			if err != nil || files != 2 || chunks != 2 || folded != 0 {
				t.Fatal(files, chunks, folded, err)
			}
			var old int
			if err = db.QueryRow("SELECT count(*) FROM chunks WHERE id=?", oldID).Scan(&old); err != nil || old != 0 {
				t.Fatal(old, err)
			}
			if _, err = db.Exec(FTSCheck); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDistinctSourcesWithEqualNames(t *testing.T) {
	source, state, db, config := fixture(t)
	for _, parent := range []string{"left", "right"} {
		write(t, source, parent+"/project/README.md", "A shared lighthouse.")
	}
	config.Sources = []corpus.Source{{Path: filepath.Join(source, "left", "project")}, {Path: filepath.Join(source, "right", "project")}}
	refresh(t, db, state, config, false, 0)
	var count int
	if err := db.QueryRow("SELECT count(DISTINCT chunks.id) FROM chunks JOIN files ON files.id=chunks.file WHERE files.project='project' AND files.path='README.md'").Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
}

func TestIdentityUpgradeFailures(t *testing.T) {
	_, state, db, _ := fixture(t)
	if _, err := db.Exec("ALTER TABLE files DROP COLUMN namespace"); err != nil {
		t.Fatal(err)
	}
	reader, err := Open(state, false, false, true)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if err = upgradeIdentity(reader); err == nil {
		t.Fatal("read-only migration accepted")
	}
	db.Close()
	if err = upgradeIdentity(db); err == nil {
		t.Fatal("closed database accepted")
	}
}

func TestWildcardFixedPrefix(t *testing.T) {
	for _, pattern := range []string{"*/memory", "pro*/memory", "[ap]*/memory", "?roalpha/memory"} {
		source, state, db, config := fixture(t)
		write(t, source, "proalpha/memory/note.md", "A keeper remembers.")
		config.Sources = []corpus.Source{{Path: filepath.Join(source, filepath.FromSlash(pattern))}}
		refresh(t, db, state, config, false, 0)
		var namespace string
		if err := db.QueryRow("SELECT namespace FROM files").Scan(&namespace); err != nil || namespace != "source:0:proalpha/memory" {
			t.Fatal(pattern, namespace, err)
		}
	}
}

func TestSourceMoveKeepsIDs(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		source, state, db, config := fixture(t)
		old, new := filepath.Join(source, "alpha"), filepath.Join(source, "zeta")
		if reverse {
			old, new = new, old
		}
		write(t, old, "README.md", "The unchanged lighthouse.")
		id := uint64(17)
		config.Sources = []corpus.Source{{Path: old, Literal: true, SourceID: &id}}
		refresh(t, db, state, config, false, 0)
		var before string
		if err := db.QueryRow("SELECT id FROM chunks").Scan(&before); err != nil {
			t.Fatal(err)
		}
		write(t, new, "README.md", "The unchanged lighthouse.")
		config.Sources[0].Path = new
		refresh(t, db, state, config, false, 0)
		var after, path, root string
		err := db.QueryRow("SELECT chunks.id,files.path,files.source FROM chunks JOIN files ON files.id=chunks.file WHERE files.deleted_at IS NULL").Scan(&after, &path, &root)
		if err != nil || before != after || root != new || path != "README.md" {
			t.Fatal(reverse, before, after, root, path, err)
		}
		var active int
		if err := db.QueryRow("SELECT count(*) FROM chunks").Scan(&active); err != nil || active != 1 {
			t.Fatal(active, err)
		}
	}
}

func TestRemoveSourceKeepsOtherIDs(t *testing.T) {
	source, state, db, config := fixture(t)
	first, second := filepath.Join(source, "first"), filepath.Join(source, "second")
	write(t, first, "README.md", "The first keeper.")
	write(t, second, "README.md", "The second keeper.")
	zero, one := uint64(0), uint64(1)
	config.Sources = []corpus.Source{{Path: first, SourceID: &zero}, {Path: second, SourceID: &one}}
	refresh(t, db, state, config, false, 0)
	var before string
	if err := db.QueryRow("SELECT chunks.id FROM chunks JOIN files ON files.id=chunks.file WHERE files.source=?", second).Scan(&before); err != nil {
		t.Fatal(err)
	}
	config.Sources = config.Sources[1:]
	refresh(t, db, state, config, false, 0)
	var after string
	if err := db.QueryRow("SELECT chunks.id FROM chunks JOIN files ON files.id=chunks.file WHERE files.source=?", second).Scan(&after); err != nil || before != after {
		t.Fatal(before, after, err)
	}
}
