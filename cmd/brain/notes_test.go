package main

import (
	"brain/tests/testutil"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestNoteRoundTrip(t *testing.T) {
	j := newJourney(t)
	j.put(t, "CaseProject/README.md", "# Project\n\nA harbour plan.")
	if code, reply := j.run(t, "init", "--source", j.source); code != 0 {
		t.Fatal(code, reply)
	}
	if code, reply := j.run(t, "index"); code != 0 {
		t.Fatal(code, reply)
	}
	code, reply := j.run(t, "note", "Decided", "the lease timeout stays at 30 minutes", "--project", "caseproject")
	path := filepath.Join(j.state, "notes", "CaseProject.md")
	if code != 0 || len(reply) != 3 || reply["path"] != path || reply["indexed"] != true {
		t.Fatal(code, reply)
	}
	id := reply["entry_id"].(string)
	data, err := os.ReadFile(path)
	if err != nil || !regexp.MustCompile(`^## \d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ\n\nDecided the lease timeout stays at 30 minutes\n$`).Match(data) {
		t.Fatal(string(data), err)
	}
	code, reply = j.run(t, "search", "lease timeout minutes", "--no-refresh", "--project", "CaseProject", "--kind", "note")
	if code != 0 || reply["total"] != float64(1) || result(t, reply, 0)["id"] != id || result(t, reply, 0)["path"] != path {
		t.Fatal(code, reply)
	}
	code, reply = j.run(t, "show", id, "--no-refresh")
	if code != 0 || !strings.HasSuffix(reply["text"].(string), "\n\nDecided the lease timeout stays at 30 minutes") {
		t.Fatal(code, reply)
	}
	if code, reply = j.run(t, "note", "Second note\n# not a heading\n  ```\nstill text  ", "--project", "CaseProject"); code != 0 || reply["indexed"] != true {
		t.Fatal(code, reply)
	}
	data, _ = os.ReadFile(path)
	if !strings.HasSuffix(string(data), "\n\nSecond note\n\\# not a heading\n  \\```\nstill text\n") || bytes.Count(data, []byte("\n## ")) != 1 {
		t.Fatal(string(data))
	}
	if code, reply = j.run(t, "index"); code != 3 {
		t.Fatal(code, reply)
	}
	code, reply = j.run(t, "projects")
	project := reply["projects"].([]any)[0].(map[string]any)
	if code != 0 || project["name"] != "CaseProject" || project["files"] != float64(2) || project["path"] != filepath.Join(j.source, "CaseProject") {
		t.Fatal(code, reply)
	}
	code, reply = j.run(t, "recent")
	if code != 0 || reply["total"] != float64(2) || reply["documents"].([]any)[0].(map[string]any)["kind"] != "note" {
		t.Fatal(code, reply)
	}
	for _, fixture := range []struct{ marker, text string }{
		{"longbodymarker", "Searching longbodymarker " + strings.Repeat("lease ", 300)},
		{"longunicodemarker", "Searching longunicodemarker é " + strings.Repeat("lease ", 300)},
	} {
		code, reply = j.run(t, "note", fixture.text, "--project", "CaseProject")
		if code != 0 || reply["indexed"] != true {
			t.Fatal(code, reply)
		}
		identifier := reply["entry_id"].(string)
		code, shown := j.run(t, "show", identifier, "--full", "--no-refresh")
		if code != 0 || !strings.Contains(shown["text"].(string), fixture.marker) {
			t.Fatal("entry id points away from note text", code, shown)
		}
		code, found := j.run(t, "search", fixture.marker, "--no-refresh")
		if code != 0 || found["count"] != float64(1) || result(t, found, 0)["id"] != identifier {
			t.Fatal(code, found)
		}
	}
}

func TestNoteLockAcrossProcesses(t *testing.T) {
	j := newJourney(t)
	j.put(t, "alpha/README.md", "# Alpha\n\nOne harbour.")
	j.ready(t)
	writers := make([]*exec.Cmd, 4)
	outputs, errors := make([]bytes.Buffer, 4), make([]bytes.Buffer, 4)
	for i := range writers {
		command := exec.Command(testBinary, "note", fmt.Sprintf("Parallel note number %d", i), "--project", "alpha", "--format", "json")
		command.Dir, command.Env, command.Stdout, command.Stderr = j.base, j.environment, &outputs[i], &errors[i]
		writers[i] = command
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
	}
	for i, writer := range writers {
		if err := writer.Wait(); err != nil || errors[i].Len() != 0 {
			t.Fatal(err, outputs[i].String(), errors[i].String())
		}
		var reply map[string]any
		if err := json.Unmarshal(outputs[i].Bytes(), &reply); err != nil || reply["entry_id"] == "" {
			t.Fatal(err, reply)
		}
	}
	data, err := os.ReadFile(filepath.Join(j.state, "notes", "alpha.md"))
	if err != nil {
		t.Fatal(err)
	}
	for i := range writers {
		if strings.Count(string(data), fmt.Sprintf("Parallel note number %d", i)) != 1 {
			t.Fatal("lost or duplicate note", string(data))
		}
	}
	code, reply := j.run(t, "search", "parallel note number")
	if code != 0 || reply["total"] != float64(4) {
		t.Fatal(code, reply)
	}
}

func TestNoteProjectAndRefusals(t *testing.T) {
	j := newJourney(t)
	j.put(t, "alpha/README.md", "# Alpha\n\nA lighthouse.")
	if code, reply := j.run(t, "note", "Too early"); code != 1 || reply["error"].(map[string]any)["code"] != "state-uninitialised" {
		t.Fatal(code, reply)
	}
	j.ready(t)
	for _, fixture := range []struct{ cwd, project string }{{j.source, "corpus"}, {filepath.Join(j.source, "alpha"), "alpha"}, {j.base, "general"}} {
		command := exec.Command(testBinary, "note", "Current folder decision", "--format", "json")
		command.Dir, command.Env = fixture.cwd, j.environment
		data, err := command.Output()
		var reply map[string]any
		json.Unmarshal(data, &reply)
		if err != nil || reply["path"] != filepath.Join(j.state, "notes", fixture.project+".md") || reply["indexed"] != true {
			t.Fatal(err, reply)
		}
	}
	for _, fixture := range []struct {
		words []string
		exit  int
		code  string
	}{
		{[]string{"note"}, 2, "missing-argument"},
		{[]string{"note", "A decision", "--project", "../other"}, 2, "invalid-value"},
		{[]string{"note", "A decision", "--project", "CON"}, 2, "invalid-value"},
	} {
		code, reply := j.run(t, fixture.words...)
		if code != fixture.exit || reply["error"].(map[string]any)["code"] != fixture.code {
			t.Fatal(code, reply)
		}
	}
	code, reply := j.run(t, "note", strings.Repeat("lease ", 500), "--project", "longnote")
	if code != 0 || reply["indexed"] != true {
		t.Fatal(code, reply)
	}
	data, err := os.ReadFile(filepath.Join(j.state, "notes", "longnote.md"))
	if err != nil || strings.Count(string(data), "lease") != 500 || strings.Count(string(data), "## ") != 1 {
		t.Fatal("long note lost text or split into entries", err)
	}
	testutil.Permissions(t, filepath.Join(j.state, "notes"), 0500)
	t.Cleanup(func() { testutil.Permissions(t, filepath.Join(j.state, "notes"), 0700) })
	if code, reply = j.run(t, "note", "Unsaved"); code != 1 || reply["error"].(map[string]any)["code"] != "notes-unwritable" {
		t.Fatal(code, reply)
	}
	testutil.Permissions(t, filepath.Join(j.state, "notes"), 0700)
	testutil.Permissions(t, j.state, 0500)
	t.Cleanup(func() { testutil.Permissions(t, j.state, 0700) })
	if code, reply = j.run(t, "note", "Unsaved"); code != 1 || reply["error"].(map[string]any)["code"] != "state-unwritable" {
		t.Fatal(code, reply)
	}
}
