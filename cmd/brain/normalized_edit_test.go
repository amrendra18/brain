package main

import (
	"brain/internal/index"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func currentText(t *testing.T, j *journey, term, path, text, id string) string {
	t.Helper()
	code, reply := j.run(t, "search", term, "--no-refresh", "--full")
	if code != 0 || reply["count"].(float64) < 1 {
		t.Fatal("search", code, reply)
	}
	var item map[string]any
	for _, value := range reply["results"].([]any) {
		candidate := value.(map[string]any)
		if candidate["path"] == path {
			item = candidate
			break
		}
	}
	if item == nil {
		t.Fatal("missing document", path, reply)
	}
	if item["path"] != path || item["snippet"] != text || item["bytes"] != float64(len(text)) || len(item["also_in"].([]any)) != 0 {
		t.Fatal("search text or citation", item)
	}
	gotID := item["id"].(string)
	if id != "" && gotID != id {
		t.Fatal("normalized edit changed id", id, gotID)
	}
	code, reply = j.run(t, "show", gotID, "--full")
	if code != 0 || reply["path"] != path || reply["text"] != text || reply["bytes"] != float64(len(text)) {
		t.Fatal("show text or citation", code, reply)
	}
	db, err := sql.Open("sqlite", filepath.Join(j.state, "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(index.FTSCheck); err != nil {
		t.Fatal("search table differs from chunk text", err)
	}
	return gotID
}

func putChanged(t *testing.T, j *journey, name, text string) string {
	t.Helper()
	path := j.put(t, name, text)
	// A different timestamp makes same-size edits visible on every filesystem.
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCaseOnlyRename(t *testing.T) {
	for _, changed := range []bool{false, true} {
		name := "same text"
		if changed {
			name = "changed text"
		}
		t.Run(name, func(t *testing.T) {
			j := newJourney(t)
			oldText := "# Keeper\n\nThe old beacon shines."
			oldPath := j.put(t, "Guide.md", oldText)
			j.ready(t)
			oldID := currentText(t, j, "beacon", oldPath, oldText, "")
			newPath := filepath.Join(j.source, "guide.md")
			if err := os.Rename(oldPath, newPath); err != nil {
				t.Fatal(err)
			}
			text, term, id := oldText, "beacon", ""
			if changed {
				text, term, id = "# Keeper\n\nThe new lantern shines.", "lantern", ""
				j.put(t, "guide.md", text)
			}
			if code, reply := j.run(t, "index"); code != 0 || reply["removed"] != float64(1) {
				t.Fatal("rename refresh", code, reply)
			}
			currentText(t, j, term, newPath, text, id)
			db, err := sql.Open("sqlite", filepath.Join(j.state, "index.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var active, retired, places int
			if err = db.QueryRow("SELECT count(*) FROM files WHERE path='Guide.md' AND deleted_at IS NULL").Scan(&active); err != nil {
				t.Fatal(err)
			}
			if err = db.QueryRow("SELECT count(*) FROM files WHERE path='Guide.md' AND deleted_at IS NOT NULL").Scan(&retired); err != nil {
				t.Fatal(err)
			}
			if err = db.QueryRow("SELECT count(*) FROM places JOIN files ON files.id=places.file WHERE files.path='guide.md' AND files.deleted_at IS NULL").Scan(&places); err != nil {
				t.Fatal(err)
			}
			if active != 0 || retired != 1 || places != 1 {
				t.Fatal("exact stored filenames", active, retired, places)
			}
			if changed {
				if code, reply := j.run(t, "search", "beacon", "--no-refresh"); code != 0 || reply["count"] != float64(0) {
					t.Fatal("old distinct text remains searchable", code, reply)
				}
				if code, reply := j.run(t, "show", oldID); code != 1 {
					t.Fatal("old distinct chunk remains active", code, reply)
				}
			}
		})
	}
}

func TestNormalizedEditRefreshesText(t *testing.T) {
	for _, mode := range []string{"ordinary", "full", "search", "show"} {
		for _, edit := range []struct{ name, before, after, term string }{
			{"case", "The keeper returns.", "THE KEEPER RETURNS.", "keeper"},
			{"whitespace", "The keeper returns.", "The\tkeeper   returns.", "keeper"},
			{"unicode tokens", "The Ꟁkeeper returns.", "The ꟁkeeper returns.", "ꟁkeeper"},
		} {
			t.Run(mode+"/"+edit.name, func(t *testing.T) {
				j := newJourney(t)
				path := j.put(t, "a.md", edit.before)
				j.ready(t)
				db, err := sql.Open("sqlite", filepath.Join(j.state, "index.sqlite"))
				if err != nil {
					t.Fatal(err)
				}
				var id string
				err = db.QueryRow("SELECT id FROM chunks").Scan(&id)
				db.Close()
				if err != nil {
					t.Fatal(err)
				}
				putChanged(t, j, "a.md", edit.after)
				args := []string{"index"}
				switch mode {
				case "full":
					args = append(args, "--full")
				case "search":
					args = []string{"search", edit.term}
				case "show":
					args = []string{"show", id}
				}
				if code, reply := j.run(t, args...); code != 0 {
					t.Fatal(code, reply)
				}
				if mode == "search" || mode == "show" {
					if code, reply := j.run(t, "show", id); code != 0 || reply["text"] != edit.before {
						t.Fatal("reading changed stored text before explicit indexing", code, reply)
					}
					if code, reply := j.run(t, "index"); code != 0 {
						t.Fatal(code, reply)
					}
				}
				currentText(t, j, edit.term, path, edit.after, id)
			})
		}
	}
}

func TestNormalizedDuplicateOwnership(t *testing.T) {
	for _, full := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "full"}[full], func(t *testing.T) {
			j := newJourney(t)
			original, other := "The keeper returns.", "THE KEEPER RETURNS."
			owner := j.put(t, "a.md", original)
			copy := j.put(t, "b.md", other)
			j.ready(t)
			id := currentText(t, j, "keeper", owner, original, "")
			copyID := currentText(t, j, "keeper", copy, other, "")
			args := []string{"index"}
			if full {
				args = append(args, "--full")
			}
			for _, change := range []struct{ a, b string }{
				{original, "THE\tKEEPER RETURNS."},
				{"The keeper   returns.", "the keeper returns."},
			} {
				putChanged(t, j, "a.md", change.a)
				putChanged(t, j, "b.md", change.b)
				if code, reply := j.run(t, args...); code != 0 {
					t.Fatal(code, reply)
				}
				currentText(t, j, "keeper", owner, change.a, id)
				other = change.b
			}
			if err := os.Remove(owner); err != nil {
				t.Fatal(err)
			}
			// Only the removed owner changes. The remaining copy is not re-read.
			if code, reply := j.run(t, "index"); code != 0 {
				t.Fatal(code, reply)
			}
			currentText(t, j, "keeper", copy, other, copyID)
		})
	}
}

func TestNormalizedOwnerSuperseded(t *testing.T) {
	j := newJourney(t)
	owner := j.put(t, "a.md", "The keeper returns.")
	other := "THE KEEPER RETURNS."
	copy := j.put(t, "b.md", other)
	j.ready(t)
	currentText(t, j, "keeper", owner, "The keeper returns.", "")
	id := currentText(t, j, "keeper", copy, other, "")
	archive := filepath.Join(j.source, "archive")
	if err := os.Mkdir(archive, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(owner, filepath.Join(archive, "a.md")); err != nil {
		t.Fatal(err)
	}
	if code, reply := j.run(t, "index"); code != 0 {
		t.Fatal(code, reply)
	}
	currentText(t, j, "keeper", copy, other, id)
}

func TestNormalizedCanonicalTextToggles(t *testing.T) {
	j := newJourney(t)
	lower, upper := "The keeper returns.", "THE KEEPER RETURNS."
	owner := j.put(t, "a.md", lower)
	copy := j.put(t, "b.md", lower)
	variant := j.put(t, "c.md", upper)
	j.ready(t)
	id := currentText(t, j, "keeper", owner, lower, "")
	copyID := currentText(t, j, "keeper", copy, lower, "")
	variantID := currentText(t, j, "keeper", variant, upper, "")
	for _, text := range []string{upper, lower} {
		putChanged(t, j, "a.md", text)
		if code, reply := j.run(t, "index"); code != 0 || reply["unchanged"] != float64(2) {
			t.Fatal("unchanged copies were re-read", code, reply)
		}
		currentText(t, j, "keeper", owner, text, id)

		currentText(t, j, "keeper", copy, lower, copyID)
		currentText(t, j, "keeper", variant, upper, variantID)
	}
	for _, step := range []struct {
		remove, path, text string
		id                 string
	}{
		{owner, copy, lower, copyID}, {copy, variant, upper, variantID},
	} {
		if err := os.Remove(step.remove); err != nil {
			t.Fatal(err)
		}
		if code, reply := j.run(t, "index"); code != 0 {
			t.Fatal(code, reply)
		}
		currentText(t, j, "keeper", step.path, step.text, step.id)
	}
}
