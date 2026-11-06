package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTextResultNavigation(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(source, "README.md")
	if err := os.WriteFile(path, []byte("# First\n\nAmber telescope first.\n\n## Second\n\nAmber telescope second.\n\n## Third\n\nAmber telescope third."), 0600); err != nil {
		t.Fatal(err)
	}
	path, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("BRAIN_HOME", filepath.Join(root, "state"))
	var output bytes.Buffer
	run := func(args ...string) string {
		t.Helper()
		output.Reset()
		if code := Run(args, &output); code != 0 {
			t.Fatalf("%v: exit %d: %s", args, code, output.String())
		}
		return output.String()
	}
	run("init", "--source", source)
	run("index")
	var found searchReply
	if err := json.Unmarshal([]byte(run("search", "amber telescope", "--no-refresh", "--format", "json")), &found); err != nil {
		t.Fatal(err)
	}
	if len(found.Results) != 3 {
		t.Fatal(found)
	}
	text := run("search", "amber telescope", "--no-refresh")
	middle := 0
	for _, result := range found.Results {
		if !strings.Contains(text, result.ID) {
			t.Errorf("text search omitted result %s: %s", result.ID, text)
		}
		var shown showReply
		if err := json.Unmarshal([]byte(run("show", result.ID, "--no-refresh", "--format", "json")), &shown); err != nil {
			t.Fatal(err)
		}
		if shown.ID != result.ID || shown.Path != path || !strings.Contains(shown.Text, "Amber telescope") {
			t.Fatal(shown)
		}
		if shown.Prev != nil && shown.NextChunk != nil {
			middle++
			text := run("show", result.ID, "--no-refresh")
			for _, id := range []string{*shown.Prev, *shown.NextChunk} {
				if !strings.Contains(text, id) {
					t.Errorf("text show omitted neighbour %s: %s", id, text)
				}
			}
		}
	}
	if middle != 1 {
		t.Fatalf("wanted one middle chunk, got %d", middle)
	}
}
