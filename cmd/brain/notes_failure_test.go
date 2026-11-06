package main

import (
	"brain/internal/platform"
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSavedNoteSurvivesHeldIndex(t *testing.T) {
	j := newJourney(t)
	j.put(t, "alpha/README.md", "# Alpha\n\nA harbour.")
	j.ready(t)
	observer, err := sql.Open("sqlite", filepath.Join(j.state, "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close()
	var before float64
	if err = observer.QueryRow("SELECT value FROM meta WHERE key='scanned_at'").Scan(&before); err != nil {
		t.Fatal(err)
	}
	indexLock, err := platform.Acquire(j.state, "index", true)
	if err != nil {
		t.Fatal(err)
	}
	defer indexLock.Close()
	text := "Saved while the index was held\n" + strings.Repeat("ordinary context ", 512)
	command := exec.Command(testBinary, "note", text, "--project", "alpha", "--format", "json")
	command.Dir, command.Env = j.base, j.environment
	var output, stderr bytes.Buffer
	command.Stdout, command.Stderr = &output, &stderr
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if command.ProcessState == nil {
			command.Process.Kill()
			command.Wait()
		}
	})
	if err = command.Wait(); err != nil || stderr.Len() != 0 {
		t.Fatal(err, stderr.String(), output.String())
	}
	indexLock.Close()
	var reply map[string]any
	if err = json.Unmarshal(output.Bytes(), &reply); err != nil || reply["indexed"] != false {
		t.Fatal(err, reply)
	}
	data, err := os.ReadFile(filepath.Join(j.state, "notes", "alpha.md"))
	if err != nil || strings.Count(string(data), "Saved while the index was held") != 1 || strings.Count(string(data), "## ") != 1 || !strings.Contains(string(data), strings.TrimSpace(text)) {
		t.Fatal(err, string(data))
	}
	var after float64
	if err = observer.QueryRow("SELECT value FROM meta WHERE key='scanned_at'").Scan(&after); err != nil || after != before {
		t.Fatal(before, after, err)
	}
	if code, found := j.run(t, "search", "index was held"); code != 0 || found["total"] != float64(0) {
		t.Fatal(code, found)
	}
	if code, found := j.run(t, "index"); code != 0 {
		t.Fatal(code, found)
	}
	code, found := j.run(t, "search", "index was held")
	if code != 0 || found["total"] != float64(1) || result(t, found, 0)["id"] != reply["entry_id"] {
		t.Fatal(code, found)
	}
}
