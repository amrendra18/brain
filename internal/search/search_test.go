package search

import (
	"brain/internal/corpus"
	"brain/internal/index"
	"brain/internal/query"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func prepared(t *testing.T) (*sql.DB, string) {
	t.Helper()
	root := t.TempDir()
	source, state := filepath.Join(root, "corpus"), filepath.Join(root, "state")
	os.Mkdir(source, 0700)
	os.Mkdir(state, 0700)
	for name, text := range map[string]string{"alpha/README.md": "# Dock\n\nThe keeper returns a lease.\n\n## Long\n\n" + strings.Repeat("é lantern ", 170) + "\n\n## End\n\nFinished.", "beta/PRD.md": "# Boats\n\nThe boats gather at the harbour."} {
		path := filepath.Join(source, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(path), 0700)
		os.WriteFile(path, []byte(text), 0600)
	}
	db, err := index.Open(state, true, false, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err = index.Refresh(db, state, corpus.Default(source, []string{source}, nil), false, 0); err != nil {
		t.Fatal(err)
	}
	return db, state
}
func TestSearchAndShow(t *testing.T) {
	db, _ := prepared(t)
	projects, err := Projects(db)
	if err != nil || len(projects) != 2 {
		t.Fatal(projects, err)
	}
	for _, test := range []struct {
		typed, project, kind, mode string
		full                       bool
		count                      int
	}{
		{"lease", "", "", "and", false, 1},
		{"lease OR boats", "", "", "or", false, 2},
		{"lease AND keeper", "", "", "and", false, 1},
		{"lease NOT keeper", "", "", "expression", false, 0},
		{"lease OR boats AND harbour", "", "", "expression", false, 2},
		{`"OR"`, "", "", "and", false, 0}, {"lease missingword", "", "", "or", false, 1}, {"alpha lease", "alpha", "readme", "and", false, 1}, {"boats", "beta", "prd", "and", false, 1}, {"absentword", "", "", "and", false, 0}, {"lantern", "", "", "and", true, 2},
	} {
		got, err := Find(db, query.Make(test.typed, projects), test.project, test.kind, 8, test.full)
		if err != nil || got.Query.Mode != test.mode || got.Count != test.count {
			t.Fatal(test, got, err)
		}
	}
	found, err := Find(db, query.Make("lantern", projects), "", "", 1, false)
	if err != nil || !found.Results[0].Truncated || found.Total != 2 || len(found.Results[0].Snippet) > 600 {
		t.Fatal(found, err)
	}
	shown, err := Show(db, found.Results[0].ID, false)
	if err != nil || shown == nil || !shown.Truncated || len(shown.Text) > 1500 || shown.Prev == nil || shown.NextChunk == nil {
		t.Fatal(shown, err)
	}
	whole, err := Show(db, shown.ID, true)
	if err != nil || whole.Truncated || len(whole.Text) <= 1500 {
		t.Fatal(whole, err)
	}
	if got, err := Show(db, "missing", false); err != nil || got != nil {
		t.Fatal(got, err)
	}
	if LastHeading(db, shown.ID) == "" || LastHeading(db, "missing") != "" {
		t.Fatal("last heading lookup")
	}
	if Clip("éé", 3) != "é" || Clip("abc", 6) != "abc" {
		t.Fatal("UTF-8 clipping")
	}
}

func TestShowNavigatesDistinctChunks(t *testing.T) {
	for _, test := range []struct {
		name, text string
		want       []string
		places     int
	}{
		{"adjacent repeats", "# A\n\nrepeatmark\n\n# A\n\nrepeatmark\n\n# B\n\ntailmark\n", []string{"A", "B"}, 3},
		{"separated repeats", "# A\n\nfirstmark\n\n# B\n\nsecondmark\n\n# A\n\nfirstmark\n\n# C\n\nfinalmark\n", []string{"A", "B", "C"}, 4},
		{"unique sections", "# One\n\nuniquemark\n\n# Two\n\nendmark\n", []string{"One", "Two"}, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			source, state := filepath.Join(root, "source"), filepath.Join(root, "state")
			if err := os.Mkdir(source, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(state, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(source, "doc.md"), []byte(test.text), 0600); err != nil {
				t.Fatal(err)
			}
			db, err := index.Open(state, true, false, false)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := index.Refresh(db, state, corpus.Default(source, []string{source}, nil), false, 0); err != nil {
				t.Fatal(err)
			}
			var count, distinct int
			if err := db.QueryRow("SELECT count(*),count(DISTINCT chunk) FROM places").Scan(&count, &distinct); err != nil {
				t.Fatal(err)
			}
			if count != test.places || distinct != len(test.want) {
				t.Fatalf("fixture has %d places and %d IDs; want %d and %d", count, distinct, test.places, len(test.want))
			}
			var id string
			if err := db.QueryRow("SELECT chunk FROM places ORDER BY ordinal LIMIT 1").Scan(&id); err != nil {
				t.Fatal(err)
			}
			seen := map[string]bool{}
			ids := []string{}
			for position, heading := range test.want {
				shown, err := Show(db, id, true)
				if err != nil || shown == nil || shown.Heading != heading || seen[id] {
					t.Fatalf("forward position %d: id %q, chunk %+v, error %v", position, id, shown, err)
				}
				seen[id] = true
				ids = append(ids, id)
				if position == len(test.want)-1 {
					if shown.NextChunk != nil {
						t.Fatalf("last chunk points to %q", *shown.NextChunk)
					}
				} else {
					if shown.NextChunk == nil {
						t.Fatalf("forward navigation stopped at %q", heading)
					}
					id = *shown.NextChunk
				}
			}
			id = ids[len(ids)-1]
			for position := len(ids) - 1; position >= 0; position-- {
				shown, err := Show(db, id, true)
				if err != nil || shown == nil || shown.ID != ids[position] {
					t.Fatalf("backward position %d: chunk %+v, error %v", position, shown, err)
				}
				if position == 0 {
					if shown.Prev != nil {
						t.Fatalf("first chunk points backward to %q", *shown.Prev)
					}
				} else if shown.Prev == nil || *shown.Prev != ids[position-1] {
					t.Fatalf("backward position %d points to %v, want %q", position, shown.Prev, ids[position-1])
				} else {
					id = *shown.Prev
				}
			}
		})
	}
}

func TestSearchFailures(t *testing.T) {
	db, _ := prepared(t)
	if _, err := Find(db, &query.Plan{First: `"`}, "", "", 8, false); err == nil {
		t.Fatal("invalid FTS query accepted")
	}
	db.Exec("UPDATE chunks SET also_in='broken JSON'")
	if _, err := Find(db, query.Make("lease", nil), "", "", 8, false); err == nil {
		t.Fatal("broken copy metadata accepted")
	}
	db.Close()
	if _, err := Find(db, query.Make("lease", nil), "", "", 8, false); err == nil {
		t.Fatal("closed search accepted")
	}
	if _, err := Show(db, "id", false); err == nil {
		t.Fatal("closed show accepted")
	}
	if _, err := Projects(db); err == nil {
		t.Fatal("closed project lookup accepted")
	}
}
