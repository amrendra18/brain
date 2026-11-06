package settings

import (
	"brain/internal/corpus"
	"brain/internal/index"
	"brain/internal/platform"
	"brain/internal/schedule"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func fixture(t *testing.T) (string, Scope, string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "settings")
	source := filepath.Join(base, "old")
	t.Cleanup(func() { _ = os.Chmod(source, 0700) })
	t.Cleanup(func() { os.Chmod(source, 0700) })
	dest := filepath.Join(base, "new")
	for _, path := range []string{root, source, filepath.Join(base, "docs"), filepath.Join(source, "notes")} {
		if err = os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.WriteFile(filepath.Join(base, "docs", "one.md"), []byte("# Guide\n\nRelocation keeps original documents unchanged.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	config := corpus.Default(base, []string{filepath.Join(base, "docs")}, nil)
	data, _ := json.Marshal(config)
	scope := Scope{source, filepath.Join(source, "config.json")}
	if err = os.WriteFile(scope.Config, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(source, "notes", "saved.md"), []byte("# Saved\n\nAcknowledged amber note survives relocation.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = rebuild(scope); err != nil {
		t.Fatal(err)
	}
	return root, scope, dest
}
func TestSettingsSelection(t *testing.T) {
	root, scope, _ := fixture(t)
	t.Setenv("BRAIN_HOME", "")
	t.Setenv("BRAIN_CONFIG", scope.Config)
	if err := Save(root, Selected(scope)); err != nil {
		t.Fatal(err)
	}
	got, explicit, err := Resolve(root, "", "", false)
	if err != nil || got != scope || !explicit {
		t.Fatalf("resolve %v %v %v", got, explicit, err)
	}
	t.Setenv("BRAIN_HOME", filepath.Join(filepath.Dir(scope.State), "override"))
	got, _, err = Resolve(root, "", "", false)
	if err != nil || got.State == scope.State || got.Config != scope.Config {
		t.Fatalf("environment: %v %v", got, err)
	}
	if err = os.Unsetenv("BRAIN_CONFIG"); err != nil {
		t.Fatal(err)
	}
	got, explicit, err = Resolve(root, "", "", false)
	if err != nil || explicit || got.Config != filepath.Join(got.State, "config.json") {
		t.Fatalf("config inheritance: %v %v", got, err)
	}
	got, explicit, err = Resolve(root, scope.State, scope.Config, true)
	if err != nil || !explicit || got != scope {
		t.Fatalf("flags %v %v", got, err)
	}
	s, exists, err := Read(root)
	if err != nil || !exists || s.Resolved() != scope || s.Config != "" {
		t.Fatalf("saved %v %v %v", s, exists, err)
	}
}
func TestSettingsUnsafeSelection(t *testing.T) {
	root, scope, _ := fixture(t)
	for _, data := range []string{`{}`, `null`, `{"version":2,"state_dir":"/tmp"}`, `{"version":1,"state_dir":"relative"}`, `{"version":1,"state_dir":"/tmp","extra":true}`, `{} {}`} {
		if err := os.WriteFile(filepath.Join(root, "selection.json"), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := Read(root); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted %s: %v", data, err)
		}
	}
	if err := Save(root, Selected(scope)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "selection.json"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Read(root); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if err := Save(root, Selection{}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}
func TestRelocationPreservesNotes(t *testing.T) {
	root, scope, dest := fixture(t)
	note := filepath.Join(scope.State, "notes", "saved.md")
	before, _ := os.ReadFile(note)
	unknown := []byte("preserve unknown safe files\n")
	if err := os.WriteFile(filepath.Join(scope.State, "extra.txt"), unknown, 0600); err != nil {
		t.Fatal(err)
	}
	config, _ := os.ReadFile(scope.Config)
	preview, err := PreviewRelocation(root, scope, dest)
	if err != nil || preview.Phase != Prepared {
		t.Fatal(preview, err)
	}
	if _, err = os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("preview wrote destination", err)
	}
	tx, err := Relocate(root, scope, dest, Hooks{})
	if err != nil || tx.Phase != Complete {
		t.Fatal(tx, err)
	}
	for _, path := range []string{note, filepath.Join(dest, "notes", "saved.md")} {
		data, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(data, before) {
			t.Fatalf("note %s: %v", path, err)
		}
	}
	for name, want := range map[string][]byte{"config.json": config, "extra.txt": unknown} {
		data, err := os.ReadFile(filepath.Join(dest, name))
		if err != nil || !bytes.Equal(data, want) {
			t.Fatalf("preserved %s: %v", name, err)
		}
	}
	if !errors.Is(CheckMutation(scope.State), ErrPending) || CheckMutation(dest) != nil {
		t.Fatal("mutation markers")
	}
	db, err := index.OpenRead(dest, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var path string
	if err = db.QueryRow("SELECT path FROM files WHERE path LIKE '%saved.md'").Scan(&path); err != nil {
		t.Fatal(err)
	}
	if path != "saved.md" {
		t.Fatalf("noncanonical note path %q", path)
	}
	selected, _, err := Read(root)
	if err != nil || selected.State != dest {
		t.Fatal(selected, err)
	}
	if _, err = Relocate(root, selected.Resolved(), dest, Hooks{}); err != nil {
		t.Fatal("repeat", err)
	}
}
func TestRelocationInterrupted(t *testing.T) {
	for _, phase := range []Phase{Prepared, Copied, Indexed, Scheduled, Published, Complete} {
		t.Run(string(phase), func(t *testing.T) {
			root, scope, dest := fixture(t)
			interrupted := errors.New("boundary interruption")
			tx, err := Relocate(root, scope, dest, Hooks{After: func(got Phase) error {
				if got == phase {
					return interrupted
				}
				return nil
			}})
			if !errors.Is(err, interrupted) || tx.Phase != phase {
				t.Fatalf("interrupt %s: %v %v", phase, tx, err)
			}
			selected, exists, err := Read(root)
			if err != nil {
				t.Fatal(err)
			}
			if (phase == Published || phase == Complete) != exists {
				t.Fatalf("publication %s %v", phase, exists)
			}
			if exists && selected.State != dest {
				t.Fatal(selected)
			}
			if phase == Copied || phase == Indexed || phase == Scheduled || phase == Published {
				if !errors.Is(CheckRead(dest), ErrPending) {
					t.Fatal("partial destination readable")
				}
			}
			tx, err = Relocate(root, scope, dest, Hooks{})
			if err != nil || tx.Phase != Complete {
				t.Fatal("retry", tx, err)
			}
		})
	}
}
func TestRelocationExternalConfig(t *testing.T) {
	root, scope, dest := fixture(t)
	data, _ := os.ReadFile(scope.Config)
	external := filepath.Join(filepath.Dir(root), "external.json")
	if err := os.WriteFile(external, data, 0600); err != nil {
		t.Fatal(err)
	}
	scope.Config = external
	tx, err := Relocate(root, scope, dest, Hooks{})
	if err != nil || tx.Destination.Config != external {
		t.Fatal(tx, err)
	}
	after, _ := os.ReadFile(external)
	if !bytes.Equal(data, after) {
		t.Fatal("external config changed")
	}
	selected, _, err := Read(root)
	if err != nil || selected.Config != external {
		t.Fatal(selected, err)
	}
}
func TestRelocationUnsafeTargets(t *testing.T) {
	for _, kind := range []string{"occupied", "nested", "parent", "symlink", "hardlink", "permission", "source-unreadable", "private-values"} {
		t.Run(kind, func(t *testing.T) {
			root, scope, dest := fixture(t)
			switch kind {
			case "occupied":
				os.Mkdir(dest, 0700)
				os.WriteFile(filepath.Join(dest, "mine"), []byte("keep"), 0600)
			case "nested":
				dest = filepath.Join(scope.State, "nested")
			case "parent":
				dest = filepath.Dir(scope.State)
			case "symlink":
				os.Symlink(scope.State, dest)
			case "hardlink":
				os.Link(scope.Config, filepath.Join(scope.State, "linked"))
			case "permission":
				os.Chmod(filepath.Join(scope.State, "notes", "saved.md"), 0644)
			case "source-unreadable":
				os.Chmod(filepath.Join(scope.State, "notes", "saved.md"), 0000)
			case "private-values":
				os.Mkdir(dest, 0700)
				private := filepath.Join(dest, "values.txt")
				os.WriteFile(private, []byte("[\"confidential-phrase\"]"), 0600)
				var config map[string]any
				data, _ := os.ReadFile(scope.Config)
				json.Unmarshal(data, &config)
				config["private_values_file"] = private
				data, _ = json.Marshal(config)
				os.WriteFile(scope.Config, data, 0600)
			}
			if _, err := PreviewRelocation(root, scope, dest); err == nil {
				t.Fatal("unsafe target accepted")
			}
		})
	}
}
func TestRelocationScheduleFailure(t *testing.T) {
	root, scope, dest := fixture(t)
	plan, err := schedule.Preview("darwin", schedule.Spec{Executable: filepath.Join(filepath.Dir(root), "brain"), State: scope.State, Config: scope.Config, Home: filepath.Dir(root), Hour: 4, Minute: 12})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := PreviewRelocation(root, scope, dest)
	if err != nil {
		t.Fatal(err)
	}
	tx.Schedule = &plan
	if err = saveRelocation(root, tx); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("scheduler refused")
	tx, err = Relocate(root, scope, dest, Hooks{Schedule: func(schedule.Plan, Scope) error { return failure }})
	if !errors.Is(err, failure) || tx.Phase != Indexed {
		t.Fatal(tx, err)
	}
	if _, exists, _ := Read(root); exists {
		t.Fatal("published despite failed scheduler")
	}
	tx, err = Relocate(root, scope, dest, Hooks{Schedule: func(old schedule.Plan, next Scope) error {
		if old.Spec.Hour != 4 || old.Spec.Minute != 12 || next.State != dest {
			t.Fatal("time or scope changed")
		}
		return nil
	}})
	if err != nil || tx.Phase != Complete {
		t.Fatal(tx, err)
	}
}
func TestRelocationConcurrentWrites(t *testing.T) {
	root, scope, dest := fixture(t)
	started := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		_, err := Relocate(root, scope, dest, Hooks{After: func(phase Phase) error {
			if phase == Copied {
				close(started)
				<-release
			}
			return nil
		}})
		finished <- err
	}()
	<-started
	writer := make(chan error, 1)
	go func() {
		lease, err := platform.StateLease(scope.State, false, true)
		if err == nil {
			defer lease.Close()
			err = CheckMutation(scope.State)
		}
		writer <- err
	}()
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if err := <-writer; !errors.Is(err, ErrPending) {
		t.Fatal("stale writer accepted", err)
	}
}
func TestRelocationStalePrivacyRefuses(t *testing.T) {
	root, scope, dest := fixture(t)
	policy := filepath.Join(filepath.Dir(root), "private-values.json")
	os.WriteFile(policy, []byte(`["Acknowledged amber"]`), 0600)
	var config map[string]any
	data, _ := os.ReadFile(scope.Config)
	json.Unmarshal(data, &config)
	config["private_values_file"] = policy
	data, _ = json.Marshal(config)
	os.WriteFile(scope.Config, data, 0600)
	if _, err := Relocate(root, scope, dest, Hooks{}); !errors.Is(err, index.ErrPrivacyRefresh) {
		t.Fatal("stale notes copied", err)
	}
	if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("destination created", err)
	}
}
func TestSettingsRegistration(t *testing.T) {
	root, scope, _ := fixture(t)
	home := filepath.Dir(root)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("BRAIN_HOME", "")
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "default-data"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "default-data"))
	if err := Registration(root, scope); err != nil {
		t.Fatal(err)
	}
	if err := Save(root, Selected(scope)); err != nil {
		t.Fatal(err)
	}
	if err := Registration(root, scope); err != nil {
		t.Fatal(err)
	}
	if err := Registration(root, Scope{filepath.Join(home, "other"), scope.Config}); !errors.Is(err, ErrDifferent) {
		t.Fatal(err)
	}
	if err := marker(scope.State, "pending", false); err != nil {
		t.Fatal(err)
	}
	if err := Registration(root, scope); !errors.Is(err, ErrPending) {
		t.Fatal(err)
	}
}
func TestSettingsSavedExternalAndInvalidScope(t *testing.T) {
	root, scope, _ := fixture(t)
	t.Setenv("BRAIN_HOME", "")
	previous, present := os.LookupEnv("BRAIN_CONFIG")
	os.Unsetenv("BRAIN_CONFIG")
	t.Cleanup(func() {
		if present {
			os.Setenv("BRAIN_CONFIG", previous)
		} else {
			os.Unsetenv("BRAIN_CONFIG")
		}
	})
	external := filepath.Join(filepath.Dir(root), "external.json")
	if err := Save(root, Selected(Scope{scope.State, external})); err != nil {
		t.Fatal(err)
	}
	got, explicit, err := Resolve(root, "", "", false)
	if err != nil || !explicit || got.Config != external {
		t.Fatal(got, explicit, err)
	}
	got, explicit, err = Resolve(root, scope.State, "", false)
	if err != nil || explicit || got.Config != scope.Config {
		t.Fatal("state override inherited config", got, explicit, err)
	}
	for _, state := range []string{"relative"} {
		if _, _, err := Resolve(root, state, "", false); !errors.Is(err, ErrScope) {
			t.Fatal(err)
		}
	}
	if _, _, err := Resolve(root, scope.State, "", true); !errors.Is(err, ErrScope) {
		t.Fatal(err)
	}
	t.Setenv("HOME", filepath.Dir(root))
	t.Setenv("USERPROFILE", filepath.Dir(root))
	if value, err := Root(); err != nil || !filepath.IsAbs(value) {
		t.Fatal(value, err)
	}
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	if _, err := Root(); err == nil {
		t.Fatal("missing home accepted")
	}
}
func TestSettingsRegistrationInvalidDefault(t *testing.T) {
	root, scope, _ := fixture(t)
	home := filepath.Dir(root)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("BRAIN_HOME", "")
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "data"))
	defaults, err := platform.CurrentState("")
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(defaults, 0700)
	os.WriteFile(filepath.Join(defaults, "config.json"), []byte("invalid"), 0600)
	if err := Registration(root, scope); err == nil {
		t.Fatal("invalid initialized default replaced")
	}
	os.Remove(filepath.Join(defaults, "config.json"))
	os.WriteFile(filepath.Join(scope.State, "schedule.json"), []byte("invalid"), 0600)
	if err := Registration(root, scope); err == nil {
		t.Fatal("invalid receipt accepted")
	}
}
func TestRelocationUnreadableDocumentSource(t *testing.T) {
	root, scope, dest := fixture(t)
	docs := filepath.Join(filepath.Dir(root), "docs")
	if err := os.Rename(docs, docs+"-unavailable"); err != nil {
		t.Fatal(err)
	}
	tx, err := Relocate(root, scope, dest, Hooks{})
	if err == nil || tx.Phase != Copied {
		t.Fatal("incomplete source published", tx, err)
	}
	if _, exists, _ := Read(root); exists {
		t.Fatal("incomplete selection published")
	}
	if err = os.Rename(docs+"-unavailable", docs); err != nil {
		t.Fatal(err)
	}
	tx, err = Relocate(root, scope, dest, Hooks{})
	if err != nil || tx.Phase != Complete {
		t.Fatal("restored source retry", tx, err)
	}
}
func TestRelocationPublicationWindows(t *testing.T) {
	for _, window := range []string{"selection-before-journal", "marker-before-complete"} {
		t.Run(window, func(t *testing.T) {
			root, scope, dest := fixture(t)
			stop := errors.New("stop after publication")
			tx, err := Relocate(root, scope, dest, Hooks{After: func(p Phase) error {
				if p == Published {
					return stop
				}
				return nil
			}})
			if !errors.Is(err, stop) {
				t.Fatal(err)
			}
			if window == "selection-before-journal" {
				tx.Phase = Scheduled
				if err = saveRelocation(root, tx); err != nil {
					t.Fatal(err)
				}
			} else {
				if err = os.Remove(filepath.Join(dest, "relocation.json")); err != nil {
					t.Fatal(err)
				}
			}
			tx, err = Relocate(root, tx.Destination, dest, Hooks{})
			if err != nil || tx.Phase != Complete {
				t.Fatal(tx, err)
			}
		})
	}
}
