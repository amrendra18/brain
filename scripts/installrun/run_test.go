package installrun

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"brain/internal/corpus"
	"brain/internal/platform"
	"brain/internal/schedule"
	"brain/internal/skill"
)

type fixture struct {
	installer                                 *installer
	home, root, state, config, source, binary string
	calls                                     [][]string
	fail                                      string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{home: filepath.Join(base, "home with spaces"), root: filepath.Join(base, "checkout"), state: filepath.Join(base, "private data"), source: filepath.Join(base, "source notes")}
	f.config = filepath.Join(f.state, "config.json")
	f.binary = filepath.Join(f.home, ".local", "bin", "brain")
	for _, dir := range []string{f.home, filepath.Join(f.root, ".local"), f.source} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", f.home)
	t.Setenv("USERPROFILE", f.home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("APPDATA", filepath.Join(f.home, "AppData", "Roaming"))
	f.installer = &installer{root: f.root, home: f.home, system: runtime.GOOS, run: f.run}
	return f
}
func (f *fixture) run(binary string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{binary}, args...))
	verb := args[0]
	if verb == f.fail {
		return nil, errors.New("test command failure: " + verb)
	}
	value := func(name string) string {
		for n, a := range args {
			if a == name && n+1 < len(args) {
				return args[n+1]
			}
		}
		return ""
	}
	if verb == "build" {
		return nil, os.WriteFile(value("-o"), []byte("candidate binary"), 0700)
	}
	if verb == "--version" {
		return []byte(`{"version":"test"}`), nil
	}
	if verb == "settings" {
		return json.Marshal(map[string]any{"selection": selection{State: f.state, Config: f.config}, "saved": false, "registration_allowed": true})
	}
	if verb == "init" {
		config := corpus.Default(f.source, []string{f.source}, nil)
		_, _, err := corpus.Initialize(f.config, config)
		return []byte(`{}`), err
	}
	if verb == "setup" {
		item, _ := skill.Setup(args[1], runtime.GOOS, f.home, binary, f.state, f.config)
		if err := os.MkdirAll(filepath.Dir(item.SkillFile), 0700); err != nil {
			return nil, err
		}
		return []byte(`{}`), os.WriteFile(item.SkillFile, []byte("new skill"), 0600)
	}
	return []byte(`{}`), nil
}
func (f *fixture) install(t *testing.T, args ...string) (receipt, int) {
	t.Helper()
	var out bytes.Buffer
	code := f.installer.runInstall(args, &out)
	var result receipt
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("not JSON: %s", out.String())
	}
	return result, code
}
func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}
func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
func (f *fixture) existing(t *testing.T) {
	t.Helper()
	write(t, f.binary, "old binary")
	if err := platform.PrepareState(f.state); err != nil {
		t.Fatal(err)
	}
	config := corpus.Default(f.source, []string{f.source}, nil)
	config.Exclude = append(config.Exclude, "keep-rule")
	if _, _, err := corpus.Initialize(f.config, config); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(f.state, "notes", "kept.md"), "saved note")
}

func TestInstallFirstRun(t *testing.T) {
	f := newFixture(t)
	result, code := f.install(t, "--source", f.source, "--skills", "codex,claude", "--time", "01:35", "--write")
	if code != 0 {
		t.Fatalf("%+v", result)
	}
	if result.Status != "installed" || read(t, f.binary) != "candidate binary" {
		t.Fatalf("%+v", result)
	}
	want := []string{"binary", "init", "settings", "index", "skill-codex", "skill-claude", "schedule"}
	if !reflect.DeepEqual(result.Completed, want) {
		t.Fatalf("steps %v", result.Completed)
	}
	if _, err := corpus.LoadFile(f.config, f.state); err != nil {
		t.Fatal(err)
	}
	for _, call := range f.calls {
		if call[1] == "index" || call[1] == "setup" || call[1] == "schedule" {
			if call[0] != f.binary {
				t.Fatalf("not durable executable: %v", call)
			}
			if !strings.Contains(strings.Join(call, "|"), "--state-dir|"+f.state+"|--config|"+f.config) {
				t.Fatalf("selection unpinned: %v", call)
			}
		}
	}
	last := f.calls[len(f.calls)-1]
	if !strings.Contains(strings.Join(last, "|"), "--time|01:35") {
		t.Fatal(last)
	}
	if !strings.Contains(read(t, filepath.Join(result.Backup, "receipt.json")), `"status": "installed"`) {
		t.Fatal("receipt incomplete")
	}
}

func TestInstallPreviewNoWrites(t *testing.T) {
	f := newFixture(t)
	result, code := f.install(t, "--source", f.source, "--skills", "codex,claude")
	if code != 0 || result.Status != "preview" {
		t.Fatalf("%+v", result)
	}
	entries, err := os.ReadDir(f.home)
	if err != nil || len(entries) != 0 {
		t.Fatalf("home changed: %v %v", entries, err)
	}
	if _, err = os.Stat(f.state); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("state created")
	}
	if len(f.calls) != 3 {
		t.Fatalf("preview called %v", f.calls)
	}
	entries, err = os.ReadDir(filepath.Join(f.root, ".local"))
	if err != nil || len(entries) != 0 {
		t.Fatal("candidate leaked")
	}
}

func TestInstallUpgrade(t *testing.T) {
	f := newFixture(t)
	f.existing(t)
	config := read(t, f.config)
	saved := f.savedSchedule(t, 4, 20)
	before := read(t, saved)
	result, code := f.install(t, "--write")
	if code != 0 {
		t.Fatalf("%+v", result)
	}
	if read(t, f.config) != config || read(t, filepath.Join(f.state, "notes", "kept.md")) != "saved note" {
		t.Fatal("upgrade changed stored content")
	}
	if read(t, filepath.Join(result.Backup, "brain.previous")) != "old binary" {
		t.Fatal("backup lost")
	}
	for _, call := range f.calls {
		if call[1] == "init" {
			t.Fatal("upgrade called init")
		}
		if call[1] == "schedule" && strings.Contains(strings.Join(call, "|"), "--time") {
			t.Fatal("time reset")
		}
	}
	if read(t, saved) != before {
		t.Fatal("saved schedule changed")
	}
}

func TestInstallFailureRecovery(t *testing.T) {
	for _, verb := range []string{"build", "--version", "settings", "init", "index", "setup", "schedule"} {
		t.Run(verb, func(t *testing.T) {
			f := newFixture(t)
			write(t, f.binary, "old binary")
			f.fail = verb
			result, code := f.install(t, "--source", f.source, "--skills", "codex", "--write")
			if code != 1 {
				t.Fatalf("unexpected outcome %+v", result)
			}
			if verb == "build" || verb == "--version" || verb == "settings" {
				if read(t, f.binary) != "old binary" {
					t.Fatal("binary changed before validation")
				}
			} else {
				if read(t, f.binary) != "candidate binary" {
					t.Fatal("candidate rolled back after state mutation")
				}
				if read(t, filepath.Join(result.Backup, "brain.previous")) != "old binary" {
					t.Fatal("backup missing")
				}
				if !strings.Contains(read(t, filepath.Join(result.Backup, "receipt.json")), "test command failure") {
					t.Fatal("failure receipt missing")
				}
				f.fail = ""
				args := []string{"--source", f.source, "--skills", "codex", "--write"}
				configBefore := ""
				if verb != "init" {
					configBefore = read(t, f.config)
				}
				write(t, filepath.Join(f.state, "notes", "retry.md"), "note during partial install")
				retry, code := f.install(t, args...)
				if code != 0 {
					t.Fatalf("retry %+v", retry)
				}
				if configBefore != "" && read(t, f.config) != configBefore {
					t.Fatal("retry changed config")
				}
				if read(t, filepath.Join(f.state, "notes", "retry.md")) != "note during partial install" {
					t.Fatal("retry lost note")
				}
				if read(t, filepath.Join(result.Backup, "brain.previous")) != "old binary" {
					t.Fatal("retry replaced recovery backup")
				}
			}
		})
	}
}

func TestInstallPreservesSkills(t *testing.T) {
	f := newFixture(t)
	f.existing(t)
	codex, _ := skill.Setup("codex", runtime.GOOS, f.home, f.binary, f.state, f.config)
	claude, _ := skill.Setup("claude", runtime.GOOS, f.home, f.binary, f.state, f.config)
	write(t, codex.SkillFile, "custom codex")
	write(t, claude.SkillFile, "custom claude")
	sibling := filepath.Join(filepath.Dir(codex.SkillFile), "notes.md")
	write(t, sibling, "keep sibling")
	hook := filepath.Join(f.home, ".codex", "hooks.json")
	write(t, hook, "keep hooks")
	result, code := f.install(t, "--skills", "codex", "--no-schedule", "--write")
	if code != 0 {
		t.Fatalf("%+v", result)
	}
	if read(t, filepath.Join(result.Backup, "codex.SKILL.md")) != "custom codex" {
		t.Fatal("skill backup missing")
	}
	if read(t, codex.SkillFile) != "new skill" || read(t, claude.SkillFile) != "custom claude" || read(t, sibling) != "keep sibling" || read(t, hook) != "keep hooks" {
		t.Fatal("unselected content changed")
	}
	for _, call := range f.calls {
		if call[1] == "schedule" {
			t.Fatal("--no-schedule registered job")
		}
	}
}

func TestInstallRefusals(t *testing.T) {
	t.Run("source required", func(t *testing.T) {
		f := newFixture(t)
		result, code := f.install(t, "--write")
		if code != 1 || !strings.Contains(result.Message, "explicit --source") {
			t.Fatalf("%+v", result)
		}
	})
	t.Run("upgrade source", func(t *testing.T) {
		f := newFixture(t)
		f.existing(t)
		other := filepath.Join(f.home, "other source")
		if err := os.Mkdir(other, 0700); err != nil {
			t.Fatal(err)
		}
		result, code := f.install(t, "--source", other, "--write")
		if code != 1 || read(t, f.binary) != "old binary" || !strings.Contains(result.Message, "omit --source") {
			t.Fatalf("%+v", result)
		}
	})
	t.Run("invalid config", func(t *testing.T) {
		f := newFixture(t)
		write(t, f.config, "invalid")
		result, code := f.install(t, "--write")
		if code != 1 || !strings.Contains(result.Message, "config is invalid") {
			t.Fatalf("%+v", result)
		}
	})
	for _, kind := range []string{"directory", "symlink", "hardlink", "permissions"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			f.existing(t)
			if err := os.Remove(f.binary); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "directory":
				if err := os.Mkdir(f.binary, 0700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(f.config, f.binary); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(f.config, f.binary); err != nil {
					t.Fatal(err)
				}
			case "permissions":
				write(t, f.binary, "unsafe")
				if err := os.Chmod(f.binary, 0666); err != nil {
					t.Fatal(err)
				}
			}
			result, code := f.install(t, "--write")
			if code != 1 || len(f.calls) != 0 {
				t.Fatalf("%+v calls %v", result, f.calls)
			}
		})
	}
}

func TestInstallConcurrentRefusal(t *testing.T) {
	f := newFixture(t)
	f.existing(t)
	backup := filepath.Join(f.home, ".local", "share", "brain-install-backups")
	if err := os.MkdirAll(backup, 0700); err != nil {
		t.Fatal(err)
	}
	lock, err := platform.Acquire(backup, "install.lock", false)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	result, code := f.install(t, "--write")
	if code != 1 || !strings.Contains(result.Message, "another installer") || read(t, f.binary) != "old binary" {
		t.Fatalf("%+v", result)
	}
}

func TestInstallOptions(t *testing.T) {
	for _, args := range [][]string{{"--wat"}, {"extra"}, {"--time", "25:00"}, {"--time", "1:00"}, {"--skills", "all"}, {"--skills", "none,codex"}, {"--skills", "codex,codex"}, {"--source", "/missing-install-source"}} {
		if _, err := parse(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	o, err := parse([]string{"--state-dir", ".", "--config", "config.json", "--bin-dir", "bin"})
	if err != nil || !filepath.IsAbs(o.state) || !filepath.IsAbs(o.config) || !filepath.IsAbs(o.bin) {
		t.Fatalf("%+v %v", o, err)
	}
	s := sources{"one", "two"}
	if s.String() != "one,two" {
		t.Fatal(s.String())
	}
	f := newFixture(t)
	var out bytes.Buffer
	if f.installer.runInstall([]string{"--help"}, &out) != 0 || !strings.Contains(out.String(), "usage:") {
		t.Fatal(out.String())
	}
	if f.installer.runInstall([]string{"--bad"}, io.Discard) != 2 {
		t.Fatal("usage exit")
	}
}

func TestInstallCommandExitCodes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell exit fixture")
	}
	for _, code := range []string{"0", "3", "1"} {
		out, err := execute("/bin/sh", "-c", "echo command-output; exit "+code)
		if code == "1" {
			if err == nil || strings.Contains(err.Error(), "command-output") {
				t.Fatal(err)
			}
		} else if err != nil || string(out) != "command-output\n" {
			t.Fatalf("%s %v", out, err)
		}
	}
}

func TestInstallRechecksSelection(t *testing.T) {
	f := newFixture(t)
	f.existing(t)
	calls := 0
	run := f.installer.run
	f.installer.run = func(binary string, args ...string) ([]byte, error) {
		if args[0] == "settings" {
			calls++
			if calls == 2 {
				return json.Marshal(map[string]any{"selection": selection{State: f.state, Config: f.config}, "registration_allowed": false})
			}
		}
		return run(binary, args...)
	}
	result, code := f.install(t, "--write")
	if code != 1 || calls != 2 || read(t, f.binary) != "old binary" {
		t.Fatalf("%+v calls %d", result, calls)
	}
	if read(t, filepath.Join(result.Backup, "brain.previous")) != "old binary" {
		t.Fatal("backup missing")
	}
}

func TestInstallExternalConfig(t *testing.T) {
	f := newFixture(t)
	f.config = filepath.Join(f.home, "external config.json")
	result, code := f.install(t, "--source", f.source, "--state-dir", f.state, "--config", f.config, "--bin-dir", filepath.Join(f.home, "my binaries"), "--no-schedule", "--write")
	if code != 0 {
		t.Fatalf("%+v", result)
	}
	if result.Selection.Config != f.config || result.Binary != filepath.Join(f.home, "my binaries", "brain") {
		t.Fatalf("%+v", result)
	}
	if _, err := os.Stat(filepath.Join(f.state, "config.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("wrote unselected config")
	}
}

type changeList struct {
	Status       string            `json:"status"`
	SkillFiles   map[string]string `json:"skill_files"`
	BackupsDir   string            `json:"backups_dir"`
	SettingsFile string            `json:"settings_file"`
	DailyJob     *struct {
		Install bool     `json:"install"`
		Name    string   `json:"name"`
		Time    string   `json:"time"`
		Files   []string `json:"files"`
	} `json:"daily_job"`
}

func (f *fixture) changes(t *testing.T, args ...string) changeList {
	t.Helper()
	var out bytes.Buffer
	if code := f.installer.runInstall(args, &out); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	var result changeList
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("not JSON: %s", out.String())
	}
	return result
}

func (f *fixture) settingsFile() string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(f.home, "Library", "Preferences", "Brain", "selection.json")
	case "windows":
		return filepath.Join(f.home, "AppData", "Roaming", "Brain", "selection.json")
	}
	return filepath.Join(f.home, ".config", "brain", "selection.json")
}

func (f *fixture) savedSchedule(t *testing.T, hour, minute int) string {
	t.Helper()
	plan, err := schedule.Preview(runtime.GOOS, schedule.Spec{Executable: f.binary, Config: f.config, State: f.state, Home: f.home, Hour: hour, Minute: minute})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"id": plan.ID, "system": plan.System, "spec": plan.Spec, "owned": map[string]string{}, "outcome": "installed"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.state, "schedule.json")
	write(t, path, string(data))
	return path
}

func TestInstallPreviewListsChanges(t *testing.T) {
	f := newFixture(t)
	result := f.changes(t, "--source", f.source, "--skills", "codex,claude", "--time", "01:35")
	codex, _ := skill.Setup("codex", runtime.GOOS, f.home, f.binary, f.state, f.config)
	claude, _ := skill.Setup("claude", runtime.GOOS, f.home, f.binary, f.state, f.config)
	if result.Status != "preview" || !reflect.DeepEqual(result.SkillFiles, map[string]string{"codex": codex.SkillFile, "claude": claude.SkillFile}) {
		t.Fatalf("skill files %+v", result)
	}
	if result.BackupsDir != filepath.Join(f.home, ".local", "share", "brain-install-backups") {
		t.Fatalf("backups folder %q", result.BackupsDir)
	}
	if result.SettingsFile != f.settingsFile() {
		t.Fatalf("settings file %q", result.SettingsFile)
	}
	job := result.DailyJob
	if job == nil || !job.Install || !strings.HasPrefix(job.Name, "brain-index-") || job.Time != "01:35" || len(job.Files) == 0 {
		t.Fatalf("daily job %+v", job)
	}
	for _, file := range job.Files {
		if !inside(f.home, file) && !inside(f.state, file) {
			t.Fatalf("job file outside home and state: %s", file)
		}
	}
	if runtime.GOOS == "darwin" && job.Files[0] != filepath.Join(f.home, "Library", "LaunchAgents", job.Name+".plist") {
		t.Fatalf("job file %s", job.Files[0])
	}
	entries, err := os.ReadDir(f.home)
	if err != nil || len(entries) != 0 {
		t.Fatalf("home changed: %v %v", entries, err)
	}

	if job := f.changes(t, "--source", f.source).DailyJob; job == nil || job.Time != "01:00" {
		t.Fatalf("default time %+v", job)
	}
	none := f.changes(t, "--source", f.source, "--no-schedule")
	if none.DailyJob == nil || none.DailyJob.Install || none.DailyJob.Name != "" || len(none.DailyJob.Files) != 0 || none.SkillFiles == nil || len(none.SkillFiles) != 0 {
		t.Fatalf("no daily job %+v", none)
	}
}

func TestInstallPreviewKeepsSavedTime(t *testing.T) {
	f := newFixture(t)
	f.existing(t)
	f.savedSchedule(t, 4, 20)
	if job := f.changes(t).DailyJob; job == nil || !job.Install || job.Time != "04:20" {
		t.Fatalf("saved time %+v", job)
	}
	if job := f.changes(t, "--time", "02:05").DailyJob; job == nil || job.Time != "02:05" {
		t.Fatalf("chosen time %+v", job)
	}
}

func TestInstallWriteListsChanges(t *testing.T) {
	f := newFixture(t)
	preview := f.changes(t, "--source", f.source, "--skills", "codex")
	written := f.changes(t, "--source", f.source, "--skills", "codex", "--write")
	if written.Status != "installed" {
		t.Fatalf("%+v", written)
	}
	preview.Status, written.Status = "", ""
	if !reflect.DeepEqual(preview, written) {
		t.Fatalf("preview %+v\nwrite %+v", preview, written)
	}
}
