package index

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestFTS5Available(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`
		CREATE VIRTUAL TABLE documents USING fts5(body, tokenize='porter unicode61');
		INSERT INTO documents(body) VALUES
		('The lighthouse keeper moors boats.'),
		('The harbour keeper moors boats and keeps boats.');
	`)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(`SELECT bm25(documents) FROM documents WHERE documents MATCH 'mooring' ORDER BY bm25(documents)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count, previous := 0, -1.0
	for rows.Next() {
		var score float64
		if err := rows.Scan(&score); err != nil {
			t.Fatal(err)
		}
		if score >= 0 || (count > 0 && score < previous) {
			t.Fatalf("unexpected match order: previous %g, current %g", previous, score)
		}
		count++
		previous = score
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("porter tokenizer matched %d rows, want 2", count)
	}
}
