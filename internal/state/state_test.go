package state

import (
	"brain/internal/corpus"
	"brain/internal/index"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) (string, string, corpus.Config, *sql.DB) {
	t.Helper()
	base := t.TempDir()
	source, folder := filepath.Join(base, "corpus"), filepath.Join(base, "state")
	for _, name := range []string{"alpha/README.md", "alpha/PLAN.md", "beta/PLAN.md"} {
		path := filepath.Join(source, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(path), 0700)
		os.WriteFile(path, []byte("# "+name+"\n\n"+name+" returns a lease.\n"), 0600)
	}
	os.Mkdir(folder, 0700)
	config := corpus.Default(source, []string{source}, nil)
	config.Sources = append(config.Sources, corpus.Source{Path: filepath.Join(folder, "notes"), Kind: "note"})
	db, err := index.Open(folder, true, false, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err = index.Refresh(db, folder, config, false, 0); err != nil {
		t.Fatal(err)
	}
	return source, folder, config, db
}
func TestStateFacts(t *testing.T) {
	source, folder, config, db := fixture(t)
	before, _, err := Snapshot(folder, config, nil)
	if err != nil || before.Index.Present || before.Index.Pending != 0 || before.Index.PendingKnown || before.Index.IndexAgeS != nil {
		t.Fatal(before, err)
	}
	view, missing, err := Snapshot(folder, config, db)
	if err != nil || len(missing) != 0 || view.Index.Files != 3 || view.Index.Pending != 0 || !view.Index.Present {
		t.Fatal(view, missing, err)
	}
	projects, err := Projects(db, config)
	if err != nil || len(projects) != 2 || projects[0].Name != "alpha" || projects[0].Path != filepath.Join(source, "alpha") || projects[0].Purpose != "alpha/README.md returns a lease." || projects[0].Files != 2 {
		t.Fatal(projects, err)
	}
	documents, total, err := Recent(db, 7, 1)
	if err != nil || len(documents) != 1 || total != 3 {
		t.Fatal(documents, total, err)
	}
	past := time.Now().Add(-10 * 24 * time.Hour)
	os.Chtimes(filepath.Join(source, "beta/PLAN.md"), past, past)
	if _, err = index.Refresh(db, folder, config, false, 0); err != nil {
		t.Fatal(err)
	}
	_, total, err = Recent(db, 7, 10)
	if err != nil || total != 2 {
		t.Fatal(total, err)
	}
	os.WriteFile(filepath.Join(source, "alpha/PLAN.md"), []byte("changed"), 0600)
	os.Remove(filepath.Join(source, "beta/PLAN.md"))
	view, _, err = Snapshot(folder, config, db)
	if err != nil || view.Index.Pending != 0 || view.Index.PendingKnown {
		t.Fatal(view, err)
	}
	os.Rename(source, source+"-moved")
	view, missing, err = Snapshot(folder, config, db)
	if err != nil || view.Index.Pending != 0 || len(missing) != 0 || view.Index.Files != 3 {
		t.Fatal(view, missing, err)
	}
	db.Close()
	if _, _, err = Snapshot(folder, config, db); err == nil {
		t.Fatal("closed index accepted")
	}
	if _, err = Projects(db, config); err == nil {
		t.Fatal("closed index accepted")
	}
	if _, _, err = Recent(db, 7, 20); err == nil {
		t.Fatal("closed index accepted")
	}
}
func TestProjectOwners(t *testing.T) {
	source, folder, config, db := fixture(t)
	config.Sources[0].Project = "combined"
	config.Sources = append(config.Sources, corpus.Source{Path: filepath.Join(source, "*"), Project: "group"})
	os.Mkdir(filepath.Join(folder, "notes"), 0700)
	os.WriteFile(filepath.Join(folder, "notes", "notes-only.md"), []byte("# Only\n\nA note."), 0600)
	if _, err := index.Refresh(db, folder, config, false, 0); err != nil {
		t.Fatal(err)
	}
	noteRoot, err := filepath.EvalSymlinks(filepath.Join(folder, "notes"))
	if err != nil {
		t.Fatal(err)
	}
	projects, err := Projects(db, config)
	if err != nil || len(projects) != 2 || projects[0].Path != filepath.Join(source, "alpha") || projects[1].Path != noteRoot {
		t.Fatal(projects, err)
	}
	config.Sources = nil
	projects, err = Projects(db, config)
	if err != nil || projects[1].Path != noteRoot {
		t.Fatal(projects, err)
	}
}
func TestPurpose(t *testing.T) {
	for _, test := range []struct{ text, want string }{
		{"# Title\n\nFirst paragraph\nwraps here.\n\nSecond.", "First paragraph wraps here."},
		{"---\ntitle: x\n---\n# T\n\n![badge](u)\n\n<!-- c -->\n\n| a | b |\n| - | - |\n\nProse.", "Prose."},
		{"```sh\n\nbrain search\n```\n\nAfter the code.", "After the code."},
		{"```md\n~~~\nExample purpose.\n```\n\nReal purpose.", "Real purpose."},
		{"````md\n```\nExample purpose.\n````\n\nReal purpose.", "Real purpose."},
		{"~~~md\n~~~ not a closer\nExample purpose.\n~~~~ \t\n\nReal purpose.", "Real purpose."},
		{"Text before a fence\n```\ncode\n```", "Text before a fence"},
		{"***\n\n Spaced   out\ttext ", "Spaced out text"},
		{"# Only a heading\n", ""}, {"---\nfront matter that never closes", ""},
		{strings.Repeat("é", 300), strings.Repeat("é", 200)},
	} {
		if got := Purpose(test.text); got != test.want {
			t.Fatalf("%q: %q", test.text, got)
		}
	}
	if !reflect.DeepEqual(iso(0), "1970-01-01T00:00:00Z") {
		t.Fatal(iso(0))
	}
}
