package cli

import (
	"brain/tests/testutil"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestIntegrationReplies(t *testing.T) {
	home := t.TempDir()
	state := filepath.Join(home, "state")
	source := filepath.Join(home, "source")
	os.Mkdir(source, 0700)
	os.WriteFile(filepath.Join(source, "README.md"), []byte("# Source\n\nKeeper lease."), 0600)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("BRAIN_HOME", state)
	var output bytes.Buffer
	run := func(args ...string) int { output.Reset(); return Run(args, &output) }
	for _, args := range [][]string{{"help", "--skill"}, {"help", "--brief"}, {"help", "setup"}, {"--brief"}, {"--brief", "--format", "copilot"}, {"--brief", "--format", "vscode"}, {"--brief", "--format", "json"}, {"setup"}, {"setup", "all", "--format", "json"}, {"setup", "all", "--write", "--format", "json"}} {
		if run(args...) != 0 {
			t.Fatal(args, output.String())
		}
		if Run(args, closedWriter{}) != 1 {
			t.Fatal("closed output accepted", args)
		}
	}
	for _, args := range [][]string{{"setup", "other"}, {"setup", "claude", "extra"}, {"setup", "--bad"}, {"setup", "--write=yes"}, {"--brief", "now"}, {"--brief", "--format", "xml"}, {"--brief", "--format"}} {
		if run(args...) != 2 {
			t.Fatal(args, output.String())
		}
	}
	for _, stage := range []string{"init", "index"} {
		args := []string{stage}
		if stage == "init" {
			args = append(args, "--source", source)
		}
		if run(args...) != 0 {
			t.Fatal(output.String())
		}
		if run("--brief") != 0 {
			t.Fatal(output.String())
		}
	}
	testutil.Permissions(t, state, 0500)
	if run("--brief") != 0 || !strings.Contains(output.String(), "doctor") || !briefKeepsCommands(t, output.String(), state) {
		testutil.Permissions(t, state, 0700)
		t.Fatal(output.String())
	}
	testutil.Permissions(t, state, 0700)
	os.WriteFile(filepath.Join(state, "index.sqlite"), []byte("broken"), 0600)
	if run("--brief") != 0 || !strings.Contains(output.String(), "--full") {
		t.Fatal(output.String())
	}
	if runtime.GOOS != "windows" {
		if run("--brief", "--state-dir", "/Network/fixture-brain") != 0 || !strings.Contains(output.String(), "local state") {
			t.Fatal(output.String())
		}
		os.Remove(filepath.Join(home, ".claude", "skills", "brain", "SKILL.md"))
		os.Mkdir(filepath.Join(home, ".claude", "skills", "brain", "SKILL.md"), 0700)
		// The existing skill file is a directory, so copying must refuse safely.
		if run("setup", "claude", "--write") != 1 {
			t.Fatal(output.String())
		}
	}
	var reply refusal
	if run("setup", "/private/fixture") != 2 || json.Unmarshal(output.Bytes(), &reply) != nil || strings.Contains(reply.Error.Message, "private") {
		t.Fatal(output.String())
	}
}

func briefKeepsCommands(t *testing.T, text, state string) bool {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(lines) != 3 || len([]rune(text)) >= 1000 {
		return false
	}
	head, line := len([]rune(lines[0]))+len([]rune(lines[1]))+3, lines[2]
	if strings.Contains(line, "Doctor for ") {
		return true
	}
	for _, kind := range []string{"write", "repair"} {
		suffix := ". The doctor command cannot fit this brief. Run the binary's help command for " + kind + " guidance."
		search, found := strings.CutPrefix(line, "Search stored data: ")
		if !found || !strings.HasSuffix(search, suffix) {
			continue
		}
		search = strings.TrimSuffix(search, suffix)
		doctor := strings.Replace(strings.Replace(search, `"search" "your words"`, `"doctor"`, 1), `'search' 'your words'`, `'doctor'`, 1)
		return doctor != search && strings.Contains(search, state) && head+len([]rune("Search stored data: "+search+". Doctor for "+kind+" guidance: "+doctor)) >= 1000
	}
	return false
}
