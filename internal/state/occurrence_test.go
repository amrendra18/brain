package state

import (
	"brain/internal/index"
	"os"
	"path/filepath"
	"testing"
)

func TestProjectPurposeOccurrence(t *testing.T) {
	source, folder, config, db := fixture(t)
	canonical := "# Alpha\n\nFirst sentence. Second sentence.\n"
	exact := "# Alpha\n\nFirst sentence.\n\nSecond sentence.\n"
	for name, text := range map[string]string{"alpha/README.md": exact, "alpha/A/README.md": canonical} {
		path := filepath.Join(source, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := index.Refresh(db, folder, config, false, 0); err != nil {
		t.Fatal(err)
	}

	if _, err := db.Exec("UPDATE chunks SET text=? WHERE kind='readme' AND project='alpha'", canonical); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE places SET text=? WHERE file IN (SELECT id FROM files WHERE path='alpha/README.md')", exact); err != nil {
		t.Fatal(err)
	}

	projects, err := Projects(db, config)
	if err != nil || projects[0].Purpose != "First sentence." {
		t.Fatal(projects, err)
	}
	if _, err := db.Exec("ALTER TABLE places DROP COLUMN text"); err != nil {
		t.Fatal(err)
	}
	reader, err := index.OpenRead(folder, true)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	projects, err = Projects(reader.DB, config)
	if err != nil || projects[0].Purpose != "First sentence. Second sentence." {
		t.Fatal(projects, err)
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM pragma_table_info('places') WHERE name='text'").Scan(&count); err != nil || count != 0 {
		t.Fatal("read changed legacy schema", count, err)
	}
}
