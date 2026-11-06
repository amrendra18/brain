package index

import (
	"testing"
)

func TestDocumentText(t *testing.T) {
	source, folder, db, config := fixture(t)
	first := "## Work\n\n- [ ] first\n- [ ] second"
	second := "## Work\n\n- [ ] first - [ ] second"
	write(t, source, "PLAN.md", first+"\n\n"+second)
	refresh(t, db, folder, config, false, 0)
	var file int64
	if err := db.QueryRow("SELECT id FROM files WHERE path='PLAN.md'").Scan(&file); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ name, sql, want string }{
		{"exact", "", first + "\n\n" + second},
		{"null fallback", "UPDATE places SET text=NULL", first + "\n\n" + first},
		{"legacy read-only", "ALTER TABLE places DROP COLUMN text", first + "\n\n" + first},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.sql != "" {
				if _, err := db.Exec(test.sql); err != nil {
					t.Fatal(err)
				}
			}
			reader, err := OpenRead(folder, true)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			got, err := DocumentText(reader.DB, file)
			if err != nil || got != test.want {
				t.Fatalf("got %q, %v; want %q", got, err, test.want)
			}
			if got, err = DocumentText(reader.DB, -1); err != nil || got != "" {
				t.Fatal(got, err)
			}
		})
	}
	if needed, err := needsTextUpgrade(db); err != nil || !needed {
		t.Fatal("reader changed legacy schema", needed, err)
	}
	db.Close()
	if _, err := DocumentText(db, file); err == nil {
		t.Fatal("closed index accepted")
	}
}
