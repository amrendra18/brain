package cli

import (
	"brain/internal/platform"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestStatelessErrorsOmitPrivateArguments(t *testing.T) {
	source, folder, run := freshnessFixture(t)
	value := "CedarPrivateArgumentFixture"
	setPrivateValues(t, folder, filepath.Join(source, "private-values.json"), value)
	t.Setenv("BRAIN_HOME", folder)
	t.Setenv("BRAIN_CONFIG", filepath.Join(folder, "config.json"))
	for _, test := range []struct {
		name string
		args []string
		code string
	}{
		{"verb", []string{value}, "unknown-command"},
		{"flag", []string{"--" + value}, "unknown-flag"},
		{"flag-value", []string{"--unknown=" + value}, "unknown-flag"},
		{"help-topic", []string{"help", value}, "unknown-command"},
		{"help-second-topic", []string{"help", "search", value}, "unknown-command"},
		{"help-flag", []string{"help", "--" + value}, "unknown-flag"},
		{"help-format", []string{"help", "--format", value}, "invalid-value"},
		{"help-format-equals", []string{"help", "--format=" + value}, "invalid-value"},
		{"help-state", []string{"help", "doctor", "--state-dir", value}, "invalid-value"},
		{"version-argument", []string{"--version", value}, "unexpected-argument"},
		{"version-flag", []string{"--version", "--" + value}, "unknown-flag"},
		{"version-format", []string{"--version", "--format", value}, "invalid-value"},
		{"version-format-equals", []string{"--version", "--format=" + value}, "invalid-value"},
		{"root-help-argument", []string{"--help", value}, "unexpected-argument"},
		{"absolute-argument", []string{filepath.Join(folder, value)}, "unknown-command"},
		{"recovery-state", []string{value, "--state-dir", filepath.Join(folder, value)}, "unknown-command"},
		{"recovery-config", []string{value, "--config=" + filepath.Join(folder, value)}, "unknown-command"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			if code := Run(test.args, &output); code != 2 {
				t.Fatalf("exit = %d; want 2", code)
			}
			if strings.Contains(output.String(), value) {
				t.Fatal("stateless refusal revealed the protected fixture")
			}
			var reply refusal
			if err := json.Unmarshal(output.Bytes(), &reply); err != nil {
				t.Fatal(err)
			}
			if reply.Error.Code != test.code || strings.Contains(reply.Error.Message, folder) {
				t.Fatal(reply.Error)
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			wantArgv := []string{executable, "help"}
			if !reflect.DeepEqual(reply.Error.NextArgv, [][]string{wantArgv}) || !reflect.DeepEqual(reply.Error.Next, []string{platform.Command(defaultShell(), wantArgv)}) {
				t.Fatal("stateless recovery repeated scope or lost shell quoting", reply.Error)
			}
			output.Reset()
			if code := Run(reply.Error.NextArgv[0][1:], &output); code != 0 {
				t.Fatalf("recovery exit = %d; want 0", code)
			}
		})
	}
	// Stateful validation must still load the selected policy and sanitize its reply.
	code, out := run("search", "lighthouse", "--"+value)
	if code != 2 || strings.Contains(out, value) || !strings.Contains(out, "[redacted: private-value]") {
		t.Fatalf("scoped search did not sanitize its refusal: exit %d", code)
	}
}

func TestStatelessRecoveryOmitsEnvironmentScope(t *testing.T) {
	root := t.TempDir()
	value := "CedarPrivateScopeFixture"
	t.Setenv("BRAIN_HOME", filepath.Join(root, value))
	t.Setenv("BRAIN_CONFIG", filepath.Join(root, value, "config.json"))
	var output bytes.Buffer
	if code := Run([]string{"unknown"}, &output); code != 2 || strings.Contains(output.String(), value) {
		t.Fatal("stateless recovery revealed an environment scope")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("stateless error wrote state", entries, err)
	}
}

func TestVersionHelpMatchesJSON(t *testing.T) {
	t.Setenv("BRAIN_HOME", filepath.Join(t.TempDir(), "absent-state"))
	var expected map[string]any
	for _, args := range [][]string{{"--version"}, {"--version", "--format=json"}, {"--version", "--format=text"}} {
		var output bytes.Buffer
		if code := Run(args, &output); code != 0 {
			t.Fatalf("version exit = %d", code)
		}
		var result map[string]any
		if err := json.Unmarshal(output.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if expected == nil {
			expected = result
		}
		if len(result) != 3 || !reflect.DeepEqual(result, expected) {
			t.Fatal("version formats returned different facts", result)
		}
	}
	for _, format := range []string{"text", "json", "markdown"} {
		args := []string{"help", "--version"}
		if format != "text" {
			args = append(args, "--format", format)
		}
		var output bytes.Buffer
		if code := Run(args, &output); code != 0 || !strings.Contains(output.String(), "One JSON object with version, revision and modified.") || !strings.Contains(output.String(), "Both return JSON") {
			t.Fatal("version help does not describe the actual output", output.String())
		}
	}
}

func TestScheduleHelpIncludesUnchangedExit(t *testing.T) {
	t.Setenv("BRAIN_HOME", filepath.Join(t.TempDir(), "absent-state"))
	for _, args := range [][]string{{"schedule", "--help"}, {"help", "schedule", "--format=json"}, {"help", "schedule", "--format=markdown"}} {
		var output bytes.Buffer
		if code := Run(args, &output); code != 0 || !strings.Contains(output.String(), "3 identical healthy job already installed.") {
			t.Fatal("schedule help omitted unchanged exit 3", output.String())
		}
	}
}
