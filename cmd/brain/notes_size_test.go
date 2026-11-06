package main

import (
	"brain/internal/notes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOwnedNotesBeyondSourceLimit(t *testing.T) {
	j := newJourney(t)
	j.put(t, "alpha/README.md", "# Alpha\n\nA harbour.")
	j.put(t, "alpha/large.md", strings.Repeat("z", 1048577))
	if code, reply := j.run(t, "init", "--source", j.source); code != 0 {
		t.Fatal(code, reply)
	}
	if code, reply := j.run(t, "index"); code != 1 || reply["count"] != float64(1) {
		t.Fatal(code, reply)
	}
	path := filepath.Join(j.state, "notes", "alpha.md")
	os.Mkdir(filepath.Dir(path), 0700)
	stamp := "2026-01-06T20:00:00Z"
	old := notes.Entry("oldercapmarker retained decision", stamp)
	for i := 0; ; i++ {
		block := notes.Entry(fmt.Sprintf("Existing decision %d %s", i, strings.Repeat("x", 1750)), stamp)
		if len(old)+len(block)+1 > 1048516 {
			break
		}
		old += "\n" + block
	}
	old += strings.Repeat(" ", 1048516-len(old))
	if err := os.WriteFile(path, []byte(old), 0600); err != nil {
		t.Fatal(err)
	}
	if code, reply := j.run(t, "index"); code != 1 {
		t.Fatal(code, reply)
	}
	code, found := j.run(t, "search", "oldercapmarker")
	if code != 0 || found["count"] != float64(1) {
		t.Fatal(code, found)
	}
	code, reply := j.run(t, "note", "newcapmarker "+strings.Repeat("y", 1800), "--project", "alpha")
	if code != 0 || reply["indexed"] != true {
		t.Fatal(code, reply)
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) <= 1048576 || !strings.HasPrefix(string(data), old) || strings.Count(string(data), "newcapmarker") != 1 {
		t.Fatal("note file changed or lost text", len(data), err)
	}
	for _, full := range []bool{false, true} {
		if full {
			code, reply = j.run(t, "index", "--full")
			if code != 1 || reply["count"] != float64(1) || reply["skipped"].([]any)[0].(map[string]any)["path"] != filepath.Join(j.source, "alpha", "large.md") {
				t.Fatal(code, reply)
			}
		}
		for _, marker := range []string{"oldercapmarker", "newcapmarker"} {
			code, found = j.run(t, "search", marker, "--no-refresh")
			if code != 0 || found["count"] != float64(1) {
				t.Fatal(marker, code, found)
			}
		}
	}
	files, _ := os.ReadDir(filepath.Dir(path))
	if len(files) != 1 {
		t.Fatal("notes split into extra files", files)
	}
	// An external source cannot claim the owned-note size exception in configuration.
	configPath := filepath.Join(j.state, "config.json")
	encoded, _ := os.ReadFile(configPath)
	var config map[string]any
	json.Unmarshal(encoded, &config)
	config["sources"].([]any)[0].(map[string]any)["kind"] = "note"
	encoded, _ = json.Marshal(config)
	os.WriteFile(configPath, encoded, 0600)
	code, reply = j.run(t, "index", "--full")
	if code != 1 || reply["count"] != float64(1) {
		t.Fatal("configured source bypassed size cap", code, reply)
	}
	t.Logf("owned notes grew from %d to %d bytes; both markers stayed searchable; repository oversize remained skipped", len(old), len(data))
}
