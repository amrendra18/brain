package index

import (
	"brain/internal/corpus"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSourceReplacementNeverReadsExcluded(t *testing.T) {
	for _, component := range []string{"file", "parent", "source", "ancestor", "inside"} {
		t.Run(component, func(t *testing.T) {
			source, state, db, config := fixture(t)
			if component == "ancestor" {
				source = filepath.Join(source, "nested")
				config.Sources[0].Path = source
			}
			path := write(t, source, "docs/README.md", "Ordinary replacement candidate.")
			const marker = "SYNTHETIC_EXCLUDED_AUDIT_TOKEN"
			excluded := t.TempDir()
			secret := write(t, excluded, ".local/nested/docs/README.md", marker)
			if component == "inside" {
				secret = write(t, source, ".local/private.md", marker)
			}
			found, _, err := corpus.Scan(config)
			if err != nil || len(found) != 1 {
				t.Fatal(found, err)
			}
			from, to := path, secret
			switch component {
			case "parent":
				from, to = filepath.Dir(path), filepath.Dir(secret)
			case "source":
				from, to = source, filepath.Dir(filepath.Dir(secret))
			case "ancestor":
				from, to = filepath.Dir(source), filepath.Join(excluded, ".local")
			}
			if err = os.Rename(from, from+"-saved"); err != nil {
				t.Fatal(err)
			}
			if err = os.Symlink(to, from); err != nil {
				t.Skip("symbolic links unavailable: " + err.Error())
			}
			item := read(work{Entry: found[path]}, state, config, false)
			if _, err = apply(db, []work{item}, nil, 1, 0); err != nil {
				t.Fatal(err)
			}
			var count int
			if err = db.QueryRow("SELECT count(*) FROM chunks WHERE text LIKE ?", "%"+marker+"%").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 || item.Status != "unreadable" {
				t.Fatalf("excluded markers indexed: %d; file status: %s", count, item.Status)
			}
		})
	}
}

func TestSourceReplacementSizeAndType(t *testing.T) {
	for _, replacement := range []string{"oversize", "directory", "missing", "ordinary"} {
		t.Run(replacement, func(t *testing.T) {
			source, state, _, config := fixture(t)
			path := write(t, source, "README.md", "short")
			config.Options.MaxFileBytes = 32
			found, _, err := corpus.Scan(config)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.Remove(path); err != nil {
				t.Fatal(err)
			}
			want := "unreadable"
			switch replacement {
			case "oversize":
				write(t, source, "README.md", strings.Repeat("x", 33))
				want = "oversize"
			case "directory":
				if err = os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "ordinary":
				write(t, source, "README.md", "Ordinary readable replacement.")
				want = "current"
			}
			item := read(work{Entry: found[path]}, state, config, false)
			if item.Status != want || want != "current" && len(item.Pieces) != 0 {
				t.Fatal(item.Status, want, item.Pieces)
			}
		})
	}
}

func TestOwnedNoteGrowsAfterScan(t *testing.T) {
	source, state, _, config := fixture(t)
	path := write(t, source, "notes.md", "old note")
	config.Sources[0].Kind = "note"
	config.Options.MaxFileBytes = 8
	found, _, err := corpus.Scan(config)
	if err != nil || found[path].Kind != "note" {
		t.Fatal(found, err)
	}
	write(t, source, "notes.md", "old note\n\nA new note appended while the index lock was held.")
	item := read(work{Entry: found[path]}, state, config, false)
	if item.Status != "current" || len(item.Pieces) == 0 || !strings.Contains(item.Pieces[0].Text, "new note appended") {
		t.Fatal("note growth lost contents", item.Status, item.Pieces)
	}
}
