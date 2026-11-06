package cli

import (
	"brain/internal/corpus"
	"brain/internal/notes"
	"brain/tests/testutil"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNoteReplies(t *testing.T) {
	base := t.TempDir()
	source, folder := filepath.Join(base, "corpus"), filepath.Join(base, "state")
	os.MkdirAll(filepath.Join(source, "CaseProject"), 0700)
	os.WriteFile(filepath.Join(source, "CaseProject", "README.md"), []byte("# Project\n\nA harbour."), 0600)
	t.Setenv("BRAIN_HOME", folder)
	var output bytes.Buffer
	run := func(args ...string) int { output.Reset(); return Run(args, &output) }
	if run("init", "--source", source) != 0 || run("index") != 0 || run("note", "The keeper returns a lease", "--project", "caseproject", "--format", "json") != 0 {
		t.Fatal(output.String())
	}
	var raw map[string]any
	json.Unmarshal(output.Bytes(), &raw)
	if raw["indexed"] != true || raw["path"] != filepath.Join(folder, "notes", "CaseProject.md") {
		t.Fatal(raw)
	}
	for _, args := range [][]string{{"note", "Plain reply", "--project", "new"}, {"note", "General reply"}, {"help", "note"}} {
		if run(args...) != 0 {
			t.Fatal(output.String())
		}
		if Run(args, closedWriter{}) != 1 {
			t.Fatal("closed note output accepted")
		}
	}
	for _, args := range [][]string{{"note"}, {"note", "  "}, {"note", "Bad project", "--project", "../other"}, {"note", strings.Repeat("é", notes.LimitBytes/2+1)}} {
		if run(args...) != 2 {
			t.Fatal(args, output.String())
		}
	}
	if run("note", "unsafe\x00text") != 2 || !strings.Contains(output.String(), "null character") {
		t.Fatal(output.String())
	}
	testutil.Permissions(t, folder, 0500)
	if run("note", "Unsaved") != 1 || !strings.Contains(output.String(), "state-unwritable") {
		t.Fatal(output.String())
	}
	testutil.Permissions(t, folder, 0700)
	testutil.Permissions(t, filepath.Join(folder, "notes"), 0500)
	if run("note", "Unsaved") != 1 || !strings.Contains(output.String(), "notes-unwritable") {
		t.Fatal(output.String())
	}
	testutil.Permissions(t, filepath.Join(folder, "notes"), 0700)
	os.Rename(source, source+"-moved")
	if run("note", "Saved with unavailable sources") != 0 {
		t.Fatal(output.String())
	}
	os.Rename(source+"-moved", source)
	config := corpus.Default(source, []string{source}, nil)
	t.Chdir(filepath.Join(source, "CaseProject"))
	if here(config) != "CaseProject" {
		t.Fatal(here(config))
	}
	config.Sources = append(config.Sources, corpus.Source{Path: filepath.Join(source, "*"), Project: "Named"})
	if here(config) != "Named" {
		t.Fatal(here(config))
	}
	t.Chdir(source)
	if here(corpus.Default(source, []string{source}, nil)) != "corpus" {
		t.Fatal("root project")
	}
}

func TestBracketedSourceAndStateJourney(t *testing.T) {
	base := t.TempDir()
	var err error
	base, err = filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(base, "year[2026]", "notes[2026]")
	sibling := filepath.Join(base, "year[2026]", "notes2")
	folder := filepath.Join(base, "state[note]")
	for path, content := range map[string]string{
		filepath.Join(source, "README.md"):  "# Chosen\n\nliteralmarker",
		filepath.Join(sibling, "README.md"): "# Other\n\nunselectedmarker",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("BRAIN_HOME", folder)
	t.Setenv("HOME", base)
	t.Setenv("USERPROFILE", base)
	var output bytes.Buffer
	run := func(args ...string) map[string]any {
		t.Helper()
		output.Reset()
		code := Run(append(args, "--format", "json"), &output)
		if code != 0 && !(len(args) > 0 && args[0] == "doctor" && code == 3) {
			t.Fatal(args, code, output.String())
		}
		var reply map[string]any
		if err := json.Unmarshal(output.Bytes(), &reply); err != nil {
			t.Fatal(args, err, output.String())
		}
		return reply
	}
	run("init", "--source", source)
	indexed := run("index")
	if indexed["new"] != float64(1) {
		t.Fatal(indexed)
	}
	chosen := run("search", "literalmarker", "--no-refresh")
	if chosen["count"] != float64(1) || chosen["results"].([]any)[0].(map[string]any)["path"] != filepath.Join(source, "README.md") {
		t.Fatal(chosen)
	}
	if other := run("search", "unselectedmarker", "--no-refresh"); other["count"] != float64(0) {
		t.Fatal("unselected sibling was indexed", other)
	}
	if doctor := run("doctor"); doctor["count"] != float64(0) {
		t.Fatal(doctor)
	}
	projects := run("projects")
	if projects["count"] != float64(1) || projects["projects"].([]any)[0].(map[string]any)["path"] != source {
		t.Fatal(projects)
	}
	config := corpus.Default(source, []string{source}, nil)
	t.Chdir(source)
	if got := here(config); got != "notes[2026]" {
		t.Fatal(got)
	}
	saved := run("note", "savedmarker", "--project", "general")
	if saved["indexed"] != true {
		t.Fatal(saved)
	}
	id := saved["entry_id"].(string)
	if shown := run("show", id, "--no-refresh"); shown["id"] != id {
		t.Fatal(shown)
	}
	output.Reset()
	if code := Run([]string{"index", "--full", "--format", "json"}, &output); code != 3 {
		t.Fatal("full index", code, output.String())
	}
	if found := run("search", "savedmarker", "--kind", "note", "--no-refresh"); found["count"] != float64(1) || found["results"].([]any)[0].(map[string]any)["id"] != id {
		t.Fatal(found)
	}
}
