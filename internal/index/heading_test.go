package index

import (
	"brain/internal/chunk"
	"brain/internal/corpus"
	"strings"
	"testing"
)

func TestHeadingVersionRefresh(t *testing.T) {
	source, state, db, config := fixture(t)
	text := "# " + strings.Repeat("x", chunk.Cap*3) + " tailmarker\n\nbodymarker"
	write(t, source, "long.md", text)
	write(t, source, "ordinary.md", "# Ordinary\n\nStable body.")
	refresh(t, db, state, config, false, 0)
	var ordinaryID string
	if err := db.QueryRow("SELECT chunks.id FROM chunks JOIN files ON files.id=chunks.file WHERE path='ordinary.md'").Scan(&ordinaryID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE chunks SET heading=? WHERE file=(SELECT id FROM files WHERE path='long.md')", strings.Repeat("x", chunk.Cap*3)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE places SET heading=? WHERE file=(SELECT id FROM files WHERE path='long.md')", strings.Repeat("x", chunk.Cap*3)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE files SET chunker_version=5"); err != nil {
		t.Fatal(err)
	}
	found, _, err := corpus.Scan(config)
	if err != nil {
		t.Fatal(err)
	}
	if pending, err := Pending(db, found, nil); err != nil || pending != 2 {
		t.Fatal("old headings were not pending", pending, err)
	}
	if result := refresh(t, db, state, config, false, 0); result.Updated != 2 {
		t.Fatal(result)
	}
	for _, table := range []string{"chunks", "places", "chunk_fts"} {
		var size int
		if err := db.QueryRow("SELECT max(length(heading)) FROM " + table).Scan(&size); err != nil || size > chunk.Cap {
			t.Fatal(table, size, err)
		}
	}
	var afterID string
	if err := db.QueryRow("SELECT chunks.id FROM chunks JOIN files ON files.id=chunks.file WHERE path='ordinary.md'").Scan(&afterID); err != nil || afterID != ordinaryID {
		t.Fatal("ordinary citation changed", afterID, err)
	}
	for _, term := range []string{"tailmarker", "bodymarker"} {
		var matches int
		if err := db.QueryRow("SELECT count(*) FROM chunk_fts WHERE chunk_fts MATCH ?", term).Scan(&matches); err != nil || matches == 0 {
			t.Fatal("source tail or body is not searchable", term, matches, err)
		}
	}
	if pending, err := Pending(db, found, nil); err != nil || pending != 0 {
		t.Fatal(pending, err)
	}
}
