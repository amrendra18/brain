package cli

import (
	"brain/internal/platform"
	"brain/internal/schedule"
	"brain/internal/settings"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func settingsFixture(t *testing.T) (string, string, string, func(...string) (int, string)) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("APPDATA", filepath.Join(home, "appdata"))
	t.Setenv("BRAIN_HOME", "")
	old, exists := os.LookupEnv("BRAIN_CONFIG")
	os.Unsetenv("BRAIN_CONFIG")
	t.Cleanup(func() {
		if exists {
			os.Setenv("BRAIN_CONFIG", old)
		} else {
			os.Unsetenv("BRAIN_CONFIG")
		}
	})
	source := filepath.Join(home, "docs")
	os.Mkdir(source, 0700)
	os.WriteFile(filepath.Join(source, "README.md"), []byte("# Memory\n\nAmber notes remain searchable.\n"), 0600)
	state := filepath.Join(home, "data")
	t.Cleanup(func() { _ = os.Chmod(state, 0700) })
	destination := filepath.Join(home, "moved")
	run := func(args ...string) (int, string) {
		var out bytes.Buffer
		code := Run(append(args, "--format", "json"), &out)
		return code, out.String()
	}
	if code, out := run("init", "--state-dir", state, "--source", source); code != 0 {
		t.Fatal(code, out)
	}
	if code, out := run("index", "--state-dir", state); code != 0 {
		t.Fatal(code, out)
	}
	return home, state, destination, run
}
func TestSettingsCommandSelection(t *testing.T) {
	home, state, dest, run := settingsFixture(t)
	if code, out := run("settings", "--state-dir", state); code != 0 {
		t.Fatal(code, out)
	}
	if code, out := run("settings", "--write", "--state-dir", state); code != 0 {
		t.Fatal(code, out)
	}
	if code, out := run("settings", "--write"); code != 3 {
		t.Fatal(code, out)
	}
	if code, out := run("settings"); code != 0 {
		t.Fatal(code, out)
	} else {
		var got struct {
			Selection           settings.Selection `json:"selection"`
			Saved               bool               `json:"saved"`
			RegistrationAllowed bool               `json:"registration_allowed"`
		}
		if json.Unmarshal([]byte(out), &got) != nil || !got.Saved || !got.RegistrationAllowed || got.Selection.State != state {
			t.Fatal(out)
		}
	}
	if code, out := run("settings", "--state-dir", dest, "--write"); code != 1 {
		t.Fatal("changed selection", code, out)
	}
	if code, out := run("settings", "--config", filepath.Join(home, "other.json"), "--write"); code != 1 {
		t.Fatal("changed config", code, out)
	}
	if code, out := run("settings", "relocate", dest); code != 0 {
		t.Fatal(code, out)
	}
	if code, out := run("note", "Retain the copper receipt."); code != 0 {
		t.Fatal(code, out)
	}
	if code, out := run("settings", "relocate", dest, "--write"); code != 0 {
		t.Fatal(code, out)
	}
	if code, out := run("search", "copper"); code != 0 {
		t.Fatal(code, out)
	}
	for _, args := range [][]string{{"note", "reject stale note"}, {"index"}, {"init"}, {"exclude", "add", "*.draft"}, {"schedule", "--write"}, {"setup", "codex", "--write"}} {
		args = append(args, "--state-dir", state)
		if code, out := run(args...); code != 1 {
			t.Fatal("old writer", args, code, out)
		}
	}
	if code, out := run("settings", "relocate", dest, "--write"); code != 0 {
		t.Fatal("idempotent recovery", code, out)
	}
}
func TestSettingsUnsafeCommand(t *testing.T) {
	_, _, _, run := settingsFixture(t)
	root, _ := settings.Root()
	platform.PrepareState(root)
	os.WriteFile(filepath.Join(root, "selection.json"), []byte("broken"), 0600)
	for _, args := range [][]string{{"settings"}, {"search", "amber"}, {"setup", "codex"}} {
		if code, out := run(args...); code != 1 {
			t.Fatal(args, code, out)
		}
	}
	var out bytes.Buffer
	if code := Run([]string{"help", "settings"}, &out); code != 0 {
		t.Fatal(code, out.String())
	}
	out.Reset()
	if code := Run([]string{"--version"}, &out); code != 0 {
		t.Fatal(code, out.String())
	}
}
func TestSettingsInitializedDefaultRefusesReplacement(t *testing.T) {
	home, state, _, run := settingsFixture(t)
	defaults, err := platform.CurrentState("")
	if err != nil {
		t.Fatal(err)
	}
	if code, out := run("init", "--source", filepath.Join(home, "docs")); code != 0 {
		t.Fatal(code, out)
	}
	if code, out := run("settings", "--state-dir", state, "--write"); code != 1 {
		t.Fatal(code, out)
	}
	if code, out := run("settings", "--state-dir", state); code != 0 {
		t.Fatal(code, out)
	} else {
		var got struct {
			Allowed bool `json:"registration_allowed"`
		}
		json.Unmarshal([]byte(out), &got)
		if got.Allowed {
			t.Fatal("default switch allowed", out)
		}
	}
	if code, out := run("settings", "--state-dir", defaults, "--write"); code != 0 {
		t.Fatal(code, out)
	}
}
func TestSettingsOwnedJobConfigRefusal(t *testing.T) {
	home, state, dest, run := settingsFixture(t)
	external := filepath.Join(home, "external.json")
	data, _ := os.ReadFile(filepath.Join(state, "config.json"))
	os.WriteFile(external, data, 0600)
	plan, err := schedule.Preview("darwin", schedule.Spec{Executable: filepath.Join(home, "brain"), State: state, Config: external, Home: home, Hour: 5, Minute: 17})
	if err != nil {
		t.Fatal(err)
	}
	receipt := map[string]any{"id": plan.ID, "system": plan.System, "spec": plan.Spec, "outcome": "installed", "status": map[string]any{}}
	data, _ = json.Marshal(receipt)
	os.WriteFile(filepath.Join(state, "schedule.json"), data, 0600)
	if code, out := run("settings", "--state-dir", state, "--write"); code != 1 {
		t.Fatal("job config switch", code, out)
	}
	if code, out := run("settings", "--state-dir", state, "--config", external, "--write"); code != 0 {
		t.Fatal(code, out)
	}
	if code, out := run("settings", "--state-dir", dest, "--write"); code != 1 {
		t.Fatal("state switch", code, out)
	}
	root, _ := settings.Root()
	failure := errors.New("scheduler unavailable")
	tx, err := settings.Relocate(root, settings.Scope{State: state, Config: external}, dest, settings.Hooks{Schedule: func(schedule.Plan, settings.Scope) error { return failure }})
	if !errors.Is(err, failure) || tx.Phase != settings.Indexed {
		t.Fatal(tx, err)
	}
	selected, _, err := settings.Read(root)
	if err != nil || selected.State != state {
		t.Fatal("published failed move", selected, err)
	}
}
func TestSettingsDestinationPrivacy(t *testing.T) {
	home, state, _, run := settingsFixture(t)
	private := filepath.Join(home, "private.txt")
	os.WriteFile(private, []byte("secret-folder-name\n"), 0600)
	config := filepath.Join(state, "config.json")
	data, _ := os.ReadFile(config)
	var doc map[string]any
	json.Unmarshal(data, &doc)
	doc["private_values_file"] = private
	data, _ = json.Marshal(doc)
	os.WriteFile(config, data, 0600)
	destination := filepath.Join(home, "secret-folder-name")
	if code, out := run("settings", "relocate", destination, "--state-dir", state, "--write"); code != 1 {
		t.Fatal(code, out)
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("private destination created", err)
	}
}

func TestExplicitStateWithoutHome(t *testing.T) {
	_, state, _, run := settingsFixture(t)
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	for _, environment := range []bool{false, true} {
		args := []string{"search", "amber"}
		if environment {
			t.Setenv("BRAIN_HOME", state)
		} else {
			args = append(args, "--state-dir", state)
		}
		if code, out := run(args...); code != 0 {
			t.Fatal(code, out)
		}
	}
	if code, out := run("settings"); code != 1 || !strings.Contains(out, "settings-invalid") {
		t.Fatal(code, out)
	}
	t.Setenv("BRAIN_HOME", "")
	if code, out := run("search", "amber"); code != 1 || !strings.Contains(out, "settings-invalid") {
		t.Fatal(code, out)
	}
}
