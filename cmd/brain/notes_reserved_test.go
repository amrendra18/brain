package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReservedNoteProjects(t *testing.T) {
	j := newJourney(t)
	j.put(t, "alpha/README.md", "# Alpha\n\nA harbour.")
	j.ready(t)
	for _, project := range []string{"COM¹", "COM²", "COM³", "LPT¹", "LPT²", "LPT³", "CONIN$", "conout$", "COM¹.md", "lpt²", "LPT2", "AUX", "NUL.md", "COM1 ", "AUX .md"} {
		code, reply := j.run(t, "note", "Must not create a device note", "--project", project)
		if code != 2 || reply["error"].(map[string]any)["code"] != "invalid-value" {
			t.Fatal(project, code, reply)
		}
	}
	if _, err := os.Lstat(filepath.Join(j.state, "notes")); !os.IsNotExist(err) {
		t.Fatal("reserved project created a notes folder", err)
	}
}
