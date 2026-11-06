package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestScheduleProtectedCanonicalHome(t *testing.T) {
	for _, alias := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "alias"}[alias], func(t *testing.T) {
			_, state, run := freshnessFixture(t)
			value := "PrivateScheduleHomeFixture"
			root := t.TempDir()
			home := filepath.Join(root, value)
			if err := os.Mkdir(home, 0700); err != nil {
				t.Fatal(err)
			}
			selected := home
			if alias {
				selected = filepath.Join(root, "home-alias")
				if err := os.Symlink(home, selected); err != nil {
					t.Skip("symlinks unavailable", err)
				}
			}
			t.Setenv("HOME", selected)
			t.Setenv("USERPROFILE", selected)
			t.Setenv("PATH", t.TempDir())
			setPrivateValues(t, state, filepath.Join(t.TempDir(), "values.json"), value)
			before := scheduleFiles(t, state, home)
			for _, args := range [][]string{{"schedule"}, {"schedule", "--write"}} {
				code, out := run(args...)
				if code != 1 || !strings.Contains(out, "privacy-protected-path") || strings.Contains(out, value) {
					t.Fatal(code, out)
				}
				if after := scheduleFiles(t, state, home); !reflect.DeepEqual(before, after) {
					t.Fatal("protected schedule changed state or home")
				}
			}
		})
	}
}

func TestScheduleProtectedGeneratedIdentity(t *testing.T) {
	_, state, run := freshnessFixture(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PATH", t.TempDir())
	code, out := run("schedule")
	var preview struct{ Schedule struct{ ID string } }
	if code != 0 || json.Unmarshal([]byte(out), &preview) != nil || preview.Schedule.ID == "" {
		t.Fatal(code, out)
	}
	value := preview.Schedule.ID
	setPrivateValues(t, state, filepath.Join(t.TempDir(), "values.json"), value)
	before := scheduleFiles(t, state, home)
	if code, out = run("schedule", "--write"); code != 1 || !strings.Contains(out, "privacy-protected-path") || strings.Contains(out, value) {
		t.Fatal(code, out)
	}
	if after := scheduleFiles(t, state, home); !reflect.DeepEqual(before, after) {
		t.Fatal("protected identity changed state or home")
	}
}

func TestPrivacyLegacyScheduleCLIRefusal(t *testing.T) {
	_, state, run := freshnessFixture(t)
	value := "PrivateScheduleHomeFixture"
	setPrivateValues(t, state, filepath.Join(t.TempDir(), "values.json"), value)
	if code, out := run("index"); code != 0 {
		t.Fatal(code, out)
	}
	receipt := []byte(`{"legacy":"` + value + `"}`)
	path := filepath.Join(state, "schedule.json")
	if err := os.WriteFile(path, receipt, 0600); err != nil {
		t.Fatal(err)
	}
	before := scheduleFiles(t, state)
	for _, args := range [][]string{{"index"}, {"search", "skylark"}, {"note", "Allowed note", "--project", "source"}} {
		code, out := run(args...)
		if code != 1 || !strings.Contains(out, "privacy-protected-path") || !strings.Contains(out, "Keep the receipt") || strings.Contains(out, value) {
			t.Fatal(code, out)
		}
	}
	after := scheduleFiles(t, state)
	for path := range before {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.IsDir() {
			delete(before, path)
			delete(after, path)
		}
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("refusal changed existing state files")
	}
}

func TestSchedulePrivacyRecoveryHelp(t *testing.T) {
	root := t.TempDir()
	value := "PrivateRecoveryHomeFixture"
	t.Setenv("BRAIN_HOME", filepath.Join(root, value))
	for _, args := range [][]string{{"help", "schedule"}, {"help", "schedule", "--format=json"}, {"help", "schedule", "--format=markdown"}} {
		var output bytes.Buffer
		if code := Run(args, &output); code != 0 {
			t.Fatal(code, output.String())
		}
		for _, needed := range []string{"schedule.json", "spec.state", "spec.config", "spec.executable", "native job", "owned", "owned:null", "definitions directly", "prior bytes", "Either", "SHA-256", "trusted receipt", "same config and state"} {
			if !strings.Contains(output.String(), needed) {
				t.Fatalf("missing recovery guidance %q", needed)
			}
		}
		if strings.Contains(output.String(), value) {
			t.Fatal("help exposed private scope")
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("recovery help changed state", err)
	}
}
