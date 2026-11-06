package cli

import (
	"brain/internal/chunk"
	"brain/internal/index"
	"brain/internal/notes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestLongNoteOneEntryHasSearchableChunks(t *testing.T) {
	_, folder, run := freshnessFixture(t)
	token := "gh" + "p_" + strings.Repeat("Q", 36)
	text := "firstlongnotemarker " + strings.Repeat("a", chunk.Cap-len("firstlongnotemarker ")-6) + " " + token + "\n"
	text += strings.Repeat("Ordinary project context remains searchable.\n", 200) + "lastlongnotemarker"
	code, out := run("note", text, "--project", "longnote")
	var saved struct {
		EntryID string `json:"entry_id"`
		Indexed bool   `json:"indexed"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &saved) != nil || !saved.Indexed || saved.EntryID == "" {
		t.Fatal(code, out)
	}
	data, err := os.ReadFile(filepath.Join(folder, "notes", "longnote.md"))
	if err != nil || strings.Count(string(data), "## ") != 1 || strings.Contains(string(data), token) || !strings.Contains(string(data), "lastlongnotemarker") {
		t.Fatal("long note lost text, repeated an entry, or exposed a token", err)
	}
	for _, marker := range []string{"firstlongnotemarker", "lastlongnotemarker"} {
		code, out = run("search", marker, "--kind", "note")
		var found struct {
			Count int `json:"count"`
		}
		if code != 0 || json.Unmarshal([]byte(out), &found) != nil || found.Count != 1 {
			t.Fatal(marker, code, out)
		}
	}
	if code, out = run("show", saved.EntryID, "--full"); code != 0 || !strings.Contains(out, "firstlongnotemarker") {
		t.Fatal("entry id does not open the first text chunk", code, out)
	}
	db, err := index.OpenStored(folder)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	err = db.QueryRow("SELECT count(*) FROM places JOIN files ON files.id=places.file WHERE files.namespace='notes' AND files.path='longnote.md'").Scan(&count)
	db.Close()
	if err != nil || count < 3 {
		t.Fatal("long note did not create multiple searchable chunks", count, err)
	}
	if code, out = run("index"); code != 0 {
		t.Fatal(code, out)
	}
	after, err := os.ReadFile(filepath.Join(folder, "notes", "longnote.md"))
	if err != nil || string(after) != string(data) {
		t.Fatal("index changed or duplicated the saved note", err)
	}
	privateStateBytes(t, folder, token)
}

func TestNoteRawByteLimit(t *testing.T) {
	for _, unicode := range []bool{false, true} {
		t.Run(map[bool]string{false: "ascii", true: "utf8"}[unicode], func(t *testing.T) {
			_, folder, run := freshnessFixture(t)
			text := strings.Repeat("x", notes.LimitBytes)
			if unicode {
				text = strings.Repeat("é", notes.LimitBytes/2)
			}
			if code, out := run("note", text, "--project", "boundary"); code != 0 || !strings.Contains(out, `"indexed":true`) {
				t.Fatal("exact limit refused", code, out)
			}
			name := filepath.Join(folder, "notes", "boundary.md")
			before, err := os.ReadFile(name)
			if err != nil || !utf8.Valid(before) || !strings.Contains(string(before), text) {
				t.Fatal("accepted note lost bytes", err)
			}
			for _, input := range []string{text + "x", " " + text} {
				code, out := run("note", input, "--project", "boundary")
				var failure refusal
				if code != 2 || json.Unmarshal([]byte(out), &failure) != nil || failure.Error.Code != "note-too-long" || len(failure.Error.NextArgv) != 1 {
					t.Fatal("raw input beyond limit accepted", code, out)
				}
				if strings.Contains(out, text[:100]) {
					t.Fatal("refusal echoed input")
				}
			}
			after, err := os.ReadFile(name)
			if err != nil || string(after) != string(before) {
				t.Fatal("rejected note changed saved entries", err)
			}
		})
	}
}

func TestLongNoteOverlappingPrivateValues(t *testing.T) {
	source, folder, run := freshnessFixture(t)
	values := make([]string, 128)
	for i := range values {
		values[i] = strings.Repeat("X", i+1)
	}
	setPrivateValues(t, folder, filepath.Join(source, "private-values.json"), values...)
	if code, out := run("index"); code != 0 {
		t.Fatal(code, out)
	}
	input := strings.Repeat("X", notes.LimitBytes)
	if code, out := run("note", input, "--project", "overlap"); code != 0 || !strings.Contains(out, `"indexed":true`) {
		t.Fatal(code, out)
	}
	name := filepath.Join(folder, "notes", "overlap.md")
	before, err := os.ReadFile(name)
	if err != nil || strings.Count(string(before), "[redacted: private-value]") != 1 || strings.Contains(string(before), "X") {
		t.Fatal("overlapping literals were not removed", err, string(before))
	}
	code, out := run("note", input+"X", "--project", "overlap")
	var failure refusal
	if code != 2 || json.Unmarshal([]byte(out), &failure) != nil || failure.Error.Code != "note-too-long" {
		t.Fatal("sanitization bypassed the raw limit", code, out)
	}
	after, err := os.ReadFile(name)
	if err != nil || string(after) != string(before) {
		t.Fatal("oversized redacted note changed stored note", err)
	}
}
