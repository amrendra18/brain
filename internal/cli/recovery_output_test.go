package cli

import (
	"brain/internal/corpus"
	"brain/tests/testutil"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
)

func TestInvalidConfigRecoveryPreservesBytes(t *testing.T) {
	for _, fixture := range []struct{ name, data string }{
		{"syntax", `{"sources": ["private-marker"],}`},
		{"values", `{"sources":[],"exclude":["private-marker"]}`},
		{"null", `null`},
		{"unreadable", "private-marker"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			root := t.TempDir()
			source, folder := filepath.Join(root, "source"), filepath.Join(root, "state")
			os.Mkdir(source, 0700)
			os.Mkdir(folder, 0700)
			path := filepath.Join(folder, "config.json")
			os.WriteFile(path, []byte(fixture.data), 0600)
			if fixture.name == "unreadable" {
				testutil.Permissions(t, path, 0000)
				t.Cleanup(func() { testutil.Permissions(t, path, 0600) })
			}
			for _, args := range [][]string{{"search", "marker"}, {"init", "--source", source}} {
				var out bytes.Buffer
				code := Run(append(args, "--state-dir", folder, "--format", "json"), &out)
				var reply refusal
				if json.Unmarshal(out.Bytes(), &reply) != nil || code != 1 || reply.Error.Code != "config-invalid" {
					t.Errorf("%v: exit %d, %s", args, code, out.String())
					continue
				}
				if strings.Contains(reply.Error.Message, "private-marker") || strings.Contains(reply.Error.Message, folder) {
					t.Fatal("private config in refusal")
				}
				if len(reply.Error.NextArgv) != 1 || reply.Error.NextArgv[0][1] != "help" {
					t.Fatal(reply)
				}
				var help bytes.Buffer
				if Run(reply.Error.NextArgv[0][1:], &help) != 0 {
					t.Fatal(help.String())
				}
			}
			var brief bytes.Buffer
			if Run([]string{"--brief", "--state-dir", folder}, &brief) != 0 || !strings.Contains(brief.String(), "repair") {
				t.Errorf("brief: %s", brief.String())
			}
			if fixture.name == "unreadable" {
				testutil.Permissions(t, path, 0600)
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != fixture.data {
				t.Fatalf("existing config changed: %q, %v", got, err)
			}
		})
	}
}

func TestRepairedConfigKeepsExclusions(t *testing.T) {
	root := t.TempDir()
	source, folder := filepath.Join(root, "source"), filepath.Join(root, "state")
	os.Mkdir(source, 0700)
	os.WriteFile(filepath.Join(source, "secret.md"), []byte("privatecanary"), 0600)
	var out bytes.Buffer
	run := func(args ...string) int {
		out.Reset()
		return Run(append(args, "--state-dir", folder, "--format", "json"), &out)
	}
	if run("init", "--source", source) != 0 || run("init") != 3 {
		t.Fatal(out.String())
	}
	cfg := corpus.Default(source, []string{source}, nil)
	cfg.Exclude = append(cfg.Exclude, "secret.md")
	good, _ := json.Marshal(cfg)
	path := filepath.Join(folder, "config.json")
	os.WriteFile(path, append(append([]byte{}, good...), ','), 0600)
	if run("init") != 1 {
		t.Fatal("invalid config was replaced", out.String())
	}
	os.WriteFile(path, good, 0600)
	if code := run("index"); code != 0 && code != 3 {
		t.Fatal(out.String())
	}
	if run("search", "privatecanary") != 0 {
		t.Fatal(out.String())
	}
	var reply searchReply
	if json.Unmarshal(out.Bytes(), &reply) != nil || reply.Count != 0 {
		t.Fatal(out.String())
	}
}

func unsafeText(text string) bool {
	for _, r := range text {
		if (unicode.IsControl(r) && r != '\n' && r != '\t') || (r == '\u061c' || r == '\u200e' || r == '\u200f' || r >= '\u202a' && r <= '\u202e' || r >= '\u2066' && r <= '\u2069') || r == '\u2028' || r == '\u2029' {
			return true
		}
	}
	return false
}

func TestTextControlsEscaped(t *testing.T) {
	root := t.TempDir()
	source, folder := filepath.Join(root, "source"), filepath.Join(root, "state")
	os.Mkdir(source, 0700)
	payload := "lease café 😀 👩‍💻\x1b]52;c;secret\a\b\u009b31m\u202eunsafe\u2066text"
	os.WriteFile(filepath.Join(source, "README.md"), []byte("# Heading\n\n"+payload+"\n"), 0600)
	var out bytes.Buffer
	run := func(args ...string) int { out.Reset(); return Run(append(args, "--state-dir", folder), &out) }
	if run("init", "--source", source) != 0 || run("index") != 0 || run("search", "lease", "--format", "json") != 0 {
		t.Fatal(out.String())
	}
	var reply searchReply
	if json.Unmarshal(out.Bytes(), &reply) != nil || len(reply.Results) != 1 || !strings.Contains(reply.Results[0].Snippet, payload) {
		t.Fatal("JSON changed source bytes", out.String())
	}
	id := reply.Results[0].ID
	t.Chdir(source)
	for _, args := range [][]string{{"search", "lease"}, {"show", id}, {"projects"}, {"map", "source"}, {"--brief"}} {
		if run(args...) != 0 {
			t.Fatal(args, out.String())
		}
		if unsafeText(out.String()) {
			t.Errorf("raw controls in %v: %q", args, out.String())
		}
		if !strings.Contains(out.String(), "café 😀 👩‍💻") {
			t.Errorf("ordinary Unicode changed in %v: %q", args, out.String())
		}
	}
}

type shortWriter struct{}

func (shortWriter) Write(data []byte) (int, error) { return len(data) - 1, nil }
func TestOutputShortWrites(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"help", "note"}, {"help", "--skill"}, {"--version"}, {"unknown"}} {
		if code := Run(args, shortWriter{}); code != 1 {
			t.Errorf("%v: exit %d for truncated output", args, code)
		}
	}
}

func TestBulletNoteRecovery(t *testing.T) {
	root := t.TempDir()
	source, folder := filepath.Join(root, "source"), filepath.Join(root, "state")
	os.Mkdir(source, 0700)
	var out bytes.Buffer
	run := func(args ...string) int { out.Reset(); return Run(append(args, "--state-dir", folder), &out) }
	if run("init", "--source", source) != 0 {
		t.Fatal(out.String())
	}
	if run("note", "- exact bullet", "--project", "alpha") != 2 {
		t.Fatal(out.String())
	}
	var reply refusal
	if json.Unmarshal(out.Bytes(), &reply) != nil || len(reply.Error.NextArgv) != 1 {
		t.Fatal(out.String())
	}
	out.Reset()
	if Run(reply.Error.NextArgv[0][1:], &out) != 0 || !strings.Contains(out.String(), `note " - exact bullet"`) {
		t.Fatal("help lacks runnable bullet form", out.String())
	}
	if run("note", " - exact bullet", "--project", "alpha") != 0 {
		t.Fatal(out.String())
	}
	data, err := os.ReadFile(filepath.Join(folder, "notes", "alpha.md"))
	if err != nil || !strings.Contains(string(data), "\n- exact bullet\n") || strings.Contains(string(data), "\n - exact bullet") {
		t.Fatal(string(data), err)
	}
	if run("search", "exact bullet", "--format", "json") != 0 {
		t.Fatal(out.String())
	}
	var found searchReply
	if json.Unmarshal(out.Bytes(), &found) != nil || len(found.Results) != 1 || !strings.Contains(found.Results[0].Snippet, "- exact bullet") {
		t.Fatal(out.String())
	}
}

func TestTextAndJSONControlBoundaries(t *testing.T) {
	exact := "ordinary café 😀 👩‍💻\n\t\x00\x1b\a\b\r\v\f\u007f\u0085\u009b\u2028\u2029\u202e\u2066"
	var out bytes.Buffer
	if _, err := writeText(&out, exact); err != nil || unsafeText(out.String()) || !strings.HasPrefix(out.String(), "ordinary café 😀 👩‍💻\n\t") {
		t.Fatal(out.String(), err)
	}
	if !strings.Contains(out.String(), `\x1b`) || !strings.Contains(out.String(), `\r`) {
		t.Fatal(out.String())
	}
	out.Reset()
	want := Failure{Code: "test", Message: exact, Next: []string{exact}, NextArgv: [][]string{{exact}}}
	if WriteJSON(&out, want) != 0 {
		t.Fatal("JSON writer failed")
	}
	var got Failure
	if json.Unmarshal(out.Bytes(), &got) != nil || got.Message != exact || got.Next[0] != exact || got.NextArgv[0][0] != exact {
		t.Fatal("JSON changed bytes", out.String())
	}
}

func TestTextCommandRecoveryGuidance(t *testing.T) {
	exact := "brain search lease --state-dir state\x1b\a"
	if got := textCommand("brain search lease"); got != "brain search lease" {
		t.Fatal(got)
	}
	got := textCommand(exact)
	if unsafeText(got) || !strings.Contains(got, "--format json") || !strings.Contains(got, "next_argv") || strings.Contains(got, "state") {
		t.Fatal(got)
	}
	var out bytes.Buffer
	c := &call{state: "state\x1b\a", shell: "sh", output: &out, flags: map[string]string{}}
	if c.printNext([]string{exact}) != 0 || unsafeText(out.String()) || !strings.Contains(out.String(), "control characters") {
		t.Fatal(out.String())
	}
}
