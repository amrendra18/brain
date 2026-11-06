package index

import (
	"brain/internal/chunk"
	"brain/internal/corpus"
	"database/sql"
	"testing"
	"time"
)

func legacyTextIndex(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`ALTER TABLE places DROP COLUMN text;
DROP TRIGGER chunks_update;
CREATE TRIGGER chunks_update AFTER UPDATE ON chunks
 WHEN old.heading IS NOT new.heading OR old.project IS NOT new.project OR old.kind IS NOT new.kind BEGIN
 INSERT INTO chunk_fts(chunk_fts,rowid,heading,text,project,kind) VALUES('delete',old.row,old.heading,old.text,old.project,old.kind);
 INSERT INTO chunk_fts(rowid,heading,text,project,kind) VALUES(new.row,new.heading,new.text,new.project,new.kind);
END;`); err != nil {
		t.Fatal(err)
	}
}

func TestExistingIndexTextUpgrade(t *testing.T) {
	for _, mode := range []string{"ordinary", "full", "inline", "interrupted"} {
		t.Run(mode, func(t *testing.T) {
			source, state, db, config := fixture(t)
			write(t, source, "a.md", "The Ꟁkeeper returns.")
			write(t, source, "b.md", "THE ꟀKEEPER RETURNS.")
			refresh(t, db, state, config, false, 0)
			legacyTextIndex(t, db)
			before := "The Ꟁkeeper returns."
			id := chunk.ID(chunk.DocumentKey("source:0:", "a.md", before))
			stored, err := OpenRead(state, true)
			if err != nil {
				t.Fatal(err)
			}
			var text string
			if err = stored.QueryRow("SELECT text FROM chunks WHERE id=?", id).Scan(&text); err != nil || text != before {
				t.Fatal("legacy read-only text", text, err)
			}
			stored.Close()
			if needed, err := needsTextUpgrade(db); err != nil || !needed {
				t.Fatal("read-only open changed the old schema", needed, err)
			}
			// A large refresh remains pending without changing the old index.
			if report := refresh(t, db, state, config, false, 1); report.Pending != 2 {
				t.Fatal(report)
			}
			if needed, err := needsTextUpgrade(db); err != nil || !needed {
				t.Fatal("pending refresh changed the schema", needed, err)
			}
			if mode == "interrupted" {
				if err = upgradeText(db); err != nil {
					t.Fatal(err)
				}
				found, _, err := corpus.Scan(config)
				if err != nil {
					t.Fatal(err)
				}
				items, _, _, err := changes(db, found, false)
				if err != nil || len(items) != 2 {
					t.Fatal(items, err)
				}
				// Commit one file, then close the database as an interrupted run would.
				if _, err = apply(db, []work{read(items[0], state, config, false)}, nil, float64(time.Now().Unix()), 0); err != nil {
					t.Fatal(err)
				}
				if err = db.QueryRow("SELECT text FROM chunks WHERE id=?", id).Scan(&text); err != nil || text != before {
					t.Fatal("partial upgrade lost text", text, err)
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
			var pending int
			if err = db.QueryRow("SELECT count(*) FROM files WHERE chunker_version<>?", chunk.Version).Scan(&pending); err != nil || pending != 0 {
				t.Fatal("unchanged sources remain pending", pending, err)
			}
			for path, want := range map[string]string{"a.md": before, "b.md": "THE ꟀKEEPER RETURNS."} {
				if err = db.QueryRow("SELECT COALESCE(places.text,chunks.text) FROM places JOIN files ON files.id=places.file JOIN chunks ON chunks.id=places.chunk WHERE files.path=?", path).Scan(&text); err != nil || text != want {
					t.Fatal("unchanged source text was not filled", path, text, err)
				}
			}
			// The old trigger must now update tokens when normalized identity stays fixed.
			after := "The ꟁkeeper returns."
			write(t, source, "a.md", after)
			refresh(t, db, state, config, true, 0)
			if err = db.QueryRow("SELECT text FROM chunks WHERE id=?", id).Scan(&text); err != nil || text != after {
				t.Fatal("upgraded text", text, err)
			}
			for term, want := range map[string]int{"ꟁkeeper": 1, "Ꟁkeeper": 1} {
				var count int
				if err = db.QueryRow("SELECT count(*) FROM chunk_fts WHERE chunk_fts MATCH ?", term).Scan(&count); err != nil || count != want {
					t.Fatal("upgraded search tokens", term, count, err)
				}
			}
			if _, err = db.Exec(FTSCheck); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOccurrenceTextStoredOnce(t *testing.T) {
	source, state, db, config := fixture(t)
	write(t, source, "a.md", "# Shared\n\nThe keeper returns.\n\n# Shared\n\nTHE KEEPER RETURNS.")
	refresh(t, db, state, config, false, 0)
	var variants int
	if err := db.QueryRow("SELECT count(*) FROM places WHERE text IS NOT NULL").Scan(&variants); err != nil || variants != 1 {
		t.Fatal(variants, err)
	}
	write(t, source, "a.md", "# Shared\n\nTHE KEEPER RETURNS.\n\n# Shared\n\nThe keeper returns.")
	refresh(t, db, state, config, true, 0)
	var text string
	if err := db.QueryRow("SELECT text FROM places WHERE text IS NOT NULL").Scan(&text); err != nil || text != "# Shared\n\nThe keeper returns." {
		t.Fatal(text, err)
	}
}

func TestTextUpgradeFailures(t *testing.T) {
	_, state, db, _ := fixture(t)
	legacyTextIndex(t, db)
	reader, err := Open(state, false, false, true)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if err = upgradeText(reader); err == nil {
		t.Fatal("read-only migration accepted")
	}
	db.Close()
	if err = upgradeText(db); err == nil {
		t.Fatal("closed database accepted")
	}
}
