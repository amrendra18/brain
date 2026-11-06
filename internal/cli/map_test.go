package cli

import (
	"brain/internal/notes"
	"brain/tests/testutil"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMapReplies(t *testing.T) {
	root := t.TempDir()
	source, folder := filepath.Join(root, "corpus"), filepath.Join(root, "state")
	for name, text := range map[string]string{"alpha/README.md": "# Alpha\n\nA harbour keeper.", "alpha/PLAN.md": "## Status\n" + strings.Repeat("A1 underway.\n", 30) + "## Work\n- [ ] Build it.", "beta/PLAN.md": "# Beta\n\nA plan with no status."} {
		path := filepath.Join(source, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(path), 0700)
		os.WriteFile(path, []byte(text), 0600)
	}
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("BRAIN_HOME", folder)
	t.Setenv("PATH", "")
	var output bytes.Buffer
	run := func(args ...string) int { output.Reset(); return Run(args, &output) }
	if run("map", "alpha") != 1 || !strings.Contains(output.String(), "state-uninitialised") {
		t.Fatal(output.String())
	}
	if run("init", "--source", source) != 0 || run("map", "alpha") != 1 || !strings.Contains(output.String(), "index-missing") {
		t.Fatal(output.String())
	}
	if run("index") != 0 {
		t.Fatal(output.String())
	}
	for n := 0; n < 5; n++ {
		if _, err := notes.Append(folder, "alpha", notes.Entry("A saved beacon.", "2026-01-05T09:00:00Z")); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"map", "alpha"}, {"map", "beta"}, {"map", "ALPHA", "--format", "json"}, {"help", "map"}} {
		if run(args...) != 0 {
			t.Fatal(args, output.String())
		}
		if Run(args, closedWriter{}) != 1 {
			t.Fatal("closed map output accepted", args)
		}
	}
	if run("map", "alpha", "--no-refresh", "--format", "json") != 0 {
		t.Fatal(output.String())
	}
	var page mapReply
	if json.Unmarshal(output.Bytes(), &page) != nil || page.Project != "alpha" || page.Milestones.Count != 20 || page.Milestones.Total != 30 || page.Notes.Count != 3 || page.Notes.Total != 5 || page.Unchecked["PLAN.md"] != 1 {
		t.Fatal(page)
	}
	for _, args := range [][]string{{"map"}, {"map", "alpha", "extra"}, {"map", "alpha", "--full"}} {
		if run(args...) != 2 {
			t.Fatal(args, output.String())
		}
	}
	if run("map", "unknown /private/fixture") != 1 || !strings.Contains(output.String(), "project-unknown") {
		t.Fatal(output.String())
	}
	testutil.Permissions(t, folder, 0500)
	if run("map", "alpha", "--format", "json") != 0 || !strings.Contains(output.String(), `"read_only":true`) {
		testutil.Permissions(t, folder, 0700)
		t.Fatal(output.String())
	}
	testutil.Permissions(t, folder, 0700)
	os.WriteFile(filepath.Join(folder, "index.sqlite"), []byte("broken"), 0600)
	if run("map", "alpha") != 1 || !strings.Contains(output.String(), "index-missing") {
		t.Fatal(output.String())
	}
}
