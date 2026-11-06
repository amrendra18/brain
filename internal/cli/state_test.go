package cli

import (
	"brain/internal/corpus"
	"brain/internal/index"
	"brain/internal/platform"
	"brain/tests/testutil"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStateCommands(t *testing.T) {
	root := t.TempDir()
	source, state := filepath.Join(root, "corpus"), filepath.Join(root, "state")
	os.Mkdir(source, 0700)
	os.WriteFile(filepath.Join(source, "README.md"), []byte("# Lease\n\nReturn a lease.\n\n## Later\n\nFinish the lease."), 0600)
	t.Setenv("BRAIN_HOME", state)
	var output bytes.Buffer
	for _, args := range [][]string{{"init", "--source", source}, {"index"}, {"search", "lease"}, {"search", "lease", "--format", "json"}} {
		output.Reset()
		if Run(args, &output) != 0 {
			t.Fatal(args, output.String())
		}
	}
	var found searchReply
	if err := json.Unmarshal(output.Bytes(), &found); err != nil {
		t.Fatal(err)
	}
	id := found.Results[0].ID
	for _, args := range [][]string{{"show", id}, {"show", id, "--format", "json"}, {"help", "search"}, {"init", "--source", source, "--format", "json"}, {"index", "--full", "--format", "json"}} {
		output.Reset()
		if code := Run(args, &output); code != 0 && code != 3 {
			t.Fatal(code, args, output.String())
		}
	}
	for _, args := range [][]string{{"init", "--source", source}, {"index"}, {"search", "lease"}, {"search", "lease", "--format", "json"}, {"show", id}, {"show", id, "--format", "json"}, {"help", "index"}} {
		if Run(args, closedWriter{}) != 1 {
			t.Fatal("closed output was accepted", args)
		}
	}
	output.Reset()
	if Run([]string{"search", "lease\x00id", "--format", "json"}, &output) != 2 {
		t.Fatal(output.String())
	}
	var failed refusal
	if err := json.Unmarshal(output.Bytes(), &failed); err != nil {
		t.Fatal(err)
	}
	for _, word := range failed.Error.NextArgv[0] {
		if strings.ContainsRune(word, 0) {
			t.Fatal("retry contains NUL")
		}
	}
	for _, args := range [][]string{{"show"}, {"show", id, "extra"}, {"init", "--source"}, {"init", "extra"}, {"search", "lease", "--format", "xml"}, {"search", "lease", "--limit", "bad"}, {"search", "lease", "--kind", "bad"}, {"search", "lease", "--shell", "bad"}, {"init", "--source", "absent"}, {"init", "--include", "../bad"}} {
		output.Reset()
		if Run(args, &output) != 2 {
			t.Fatal(args, output.String())
		}
	}
	for _, input := range []string{"unknown /private/tmp", `unknown C:\People\Ada`, `unknown \\server\share`, "--flag=/private/tmp"} {
		if strings.Contains(inputName(input), "private") || strings.Contains(inputName(input), "People") || strings.Contains(inputName(input), "server") {
			t.Fatal(inputName(input))
		}
	}
}

func TestStateRecoveryAndFreshness(t *testing.T) {
	root := t.TempDir()
	source, state := filepath.Join(root, "corpus"), filepath.Join(root, "state")
	os.Mkdir(source, 0700)
	file := filepath.Join(source, "README.md")
	os.WriteFile(file, []byte("# A\n\nInitial lighthouse.\n\n## B\n\nLater keeper."), 0600)
	t.Setenv("BRAIN_HOME", state)
	var output bytes.Buffer
	run := func(args ...string) int { output.Reset(); return Run(append(args, "--format", "json"), &output) }
	if run("init", "--source", source) != 0 || run("index") != 0 {
		t.Fatal(output.String())
	}
	if run("search", "lighthouse", "--project", "corpus", "--kind", "readme", "--shell", "powershell") != 0 {
		t.Fatal(output.String())
	}
	var initial searchReply
	json.Unmarshal(output.Bytes(), &initial)
	id := initial.Results[0].ID
	configPath := filepath.Join(state, "config.json")
	data, _ := os.ReadFile(configPath)
	var config corpus.Config
	json.Unmarshal(data, &config)
	config.Options.InlineRefreshLimit = 1
	data, _ = json.Marshal(config)
	os.WriteFile(configPath, data, 0600)
	os.WriteFile(file, []byte("# A\n\nNew heron."), 0600)
	if run("search", "lighthouse") != 0 {
		t.Fatal(output.String())
	}
	var stale searchReply
	json.Unmarshal(output.Bytes(), &stale)
	if stale.Count != 1 {
		t.Fatal(stale)
	}
	if run("index") != 0 {
		t.Fatal(output.String())
	}
	if run("show", id) != 1 || !strings.Contains(output.String(), "last known heading") {
		t.Fatal(output.String())
	}
	restoreSource := testutil.UnavailableSource(t, source)
	if run("search", "heron") != 0 || !strings.Contains(output.String(), `"count":1`) {
		t.Fatal(output.String())
	}
	restoreSource()
	testutil.Permissions(t, state, 0500)
	if run("search", "heron") != 0 || !strings.Contains(output.String(), `"read_only":true`) {
		testutil.Permissions(t, state, 0700)
		t.Fatal(output.String())
	}
	if run("index") != 1 || !strings.Contains(output.String(), "state-unwritable") {
		testutil.Permissions(t, state, 0700)
		t.Fatal(output.String())
	}
	testutil.Permissions(t, state, 0700)
	os.WriteFile(file, []byte("# Long\n\n"+strings.Repeat("é lantern ", 180)+"\n\n## End\n\nFinished."), 0600)
	if run("index") != 0 || run("search", "lantern") != 0 {
		t.Fatal(output.String())
	}
	var long searchReply
	json.Unmarshal(output.Bytes(), &long)
	if !long.Results[0].Truncated {
		t.Fatal(long)
	}
	if run("show", long.Results[0].ID) != 0 || !strings.Contains(output.String(), `"truncated":true`) {
		t.Fatal(output.String())
	}
	if _, err := os.Stat(filepath.Join(state, "index.sqlite-wal")); err == nil {
		t.Fatal("closed commands left an active WAL")
	}
	os.WriteFile(filepath.Join(state, "index.sqlite"), []byte("damaged database"), 0600)
	if run("search", "lantern") != 1 || !strings.Contains(output.String(), "index-missing") {
		t.Fatal(output.String())
	}
	if run("index", "--full") != 0 {
		t.Fatal(output.String())
	}
	for _, err := range []error{index.ErrOldSQLite, platform.ErrLocked, corpus.ErrUnreadable} {
		output.Reset()
		c := &call{command: "search", words: []string{"lantern"}, state: state, shell: "sh", flags: map[string]string{"--limit": "3", "--full": "true", "--no-refresh": "true"}, output: &output}
		if c.databaseError(err) != 1 {
			t.Fatal(output.String())
		}
		c.output = closedWriter{}
		if c.databaseError(err) != 1 {
			t.Fatal("closed refusal output")
		}
	}
}
