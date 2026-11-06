package settings

import (
	"brain/internal/platform"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRelocationInitialMarkerRecovery(t *testing.T) {
	root, source, dest := fixture(t)
	tx, err := PreviewRelocation(root, source, dest)
	if err != nil {
		t.Fatal(err)
	}
	if err = freeze(tx); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(CheckMutation(source.State), ErrPending) {
		t.Fatal("initial marker did not freeze writers")
	}
	if _, ok, err := ReadRelocation(root); err != nil || ok {
		t.Fatal("fixture unexpectedly has a journal", err)
	}
	result, err := Relocate(root, source, dest, Hooks{})
	if err != nil || result.Phase != Complete {
		t.Fatal(result, err)
	}
}

func TestRelocationPreparedRetiresLegacyState(t *testing.T) {
	root, source, dest := fixture(t)
	stop := errors.New("interrupted")
	_, err := Relocate(root, source, dest, Hooks{After: func(phase Phase) error {
		if phase == Prepared {
			return stop
		}
		return nil
	}})
	if !errors.Is(err, stop) {
		t.Fatal(err)
	}
	if platform.Writable(source.State) || !errors.Is(CheckMutation(source.State), ErrPending) {
		t.Fatal("prepared state accepts writes")
	}
	if _, err = Relocate(root, source, dest, Hooks{}); err != nil {
		t.Fatal(err)
	}
}

func TestRelocationRecopiesUnpublishedLegacyWrites(t *testing.T) {
	for _, phase := range []Phase{Copied, Indexed, Scheduled} {
		t.Run(string(phase), func(t *testing.T) {
			root, source, dest := fixture(t)
			stop := errors.New("interrupted")
			_, err := Relocate(root, source, dest, Hooks{After: func(at Phase) error {
				if at == phase {
					return stop
				}
				return nil
			}})
			if !errors.Is(err, stop) {
				t.Fatal(err)
			}
			if err = os.Chmod(source.State, 0700); err != nil {
				t.Fatal(err)
			}
			note := filepath.Join(source.State, "notes", "saved.md")
			old, err := os.ReadFile(note)
			if err != nil {
				t.Fatal(err)
			}
			acknowledged := append(old, []byte("\nAcknowledged legacy saffron note.\n")...)
			if err = os.WriteFile(note, acknowledged, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err = Relocate(root, source, dest, Hooks{}); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(filepath.Join(dest, "notes", "saved.md"))
			if err != nil || !bytes.Equal(got, acknowledged) {
				t.Fatal("acknowledged legacy bytes missing", err)
			}
			if platform.Writable(source.State) {
				t.Fatal("retained state accepts legacy writes")
			}
		})
	}
}

func TestSettingsStateOverlapRefusesBeforeWrites(t *testing.T) {
	for _, kind := range []string{"same", "settings-inside-state", "state-inside-settings"} {
		t.Run(kind, func(t *testing.T) {
			_, scope, dest := fixture(t)
			root := scope.State
			if kind == "settings-inside-state" {
				root = filepath.Join(scope.State, "preferences")
			}
			if kind == "state-inside-settings" {
				root = filepath.Dir(scope.State)
			}
			before, err := os.ReadFile(scope.Config)
			if err != nil {
				t.Fatal(err)
			}
			if err = Registration(root, scope); err == nil {
				t.Fatal("overlap registered")
			}
			if _, err = PreviewRelocation(root, scope, dest); err == nil {
				t.Fatal("overlap preview accepted")
			}
			if _, err = Relocate(root, scope, dest, Hooks{}); err == nil {
				t.Fatal("overlap relocation accepted")
			}
			after, err := os.ReadFile(scope.Config)
			if err != nil || !bytes.Equal(before, after) || !platform.Writable(scope.State) {
				t.Fatal("overlap changed original data or permissions", err)
			}
			if _, err = os.Stat(filepath.Join(scope.State, "relocation.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("overlap froze original state", err)
			}
			if _, err = os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("overlap created destination", err)
			}
		})
	}
}

func TestSettingsPhysicalOverlapRefusesBeforeWrites(t *testing.T) {
	root, scope, _ := fixture(t)
	base := filepath.Dir(root)
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(base, alias); err != nil {
		t.Fatal(err)
	}
	root = filepath.Join(alias, "settings")
	dest := filepath.Join(base, "settings", "new-state")
	before, err := os.ReadFile(scope.Config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = PreviewRelocation(root, scope, dest); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err = Relocate(root, scope, dest, Hooks{}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	after, err := os.ReadFile(scope.Config)
	if err != nil || !bytes.Equal(before, after) || !platform.Writable(scope.State) {
		t.Fatal("original data or permissions changed", err)
	}
	for _, path := range []string{dest, filepath.Join(scope.State, "relocation.json")} {
		if _, err = os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("overlap wrote state", err)
		}
	}
}

func TestSettingsMissingAliasesOverlap(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(base, "real")
	if err = os.Mkdir(real, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "alias")
	if err = os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(real, "missing", "settings")
	state := filepath.Join(alias, "missing")
	scope := Scope{State: state, Config: filepath.Join(state, "config.json")}
	if err = Registration(root, scope); !errors.Is(err, ErrInvalid) {
		t.Fatal("overlap registered", err)
	}
	for _, path := range []string{root, state} {
		if _, err = os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("registration wrote state", err)
		}
	}
	if overlappingPaths(root, filepath.Join(alias, "other")) {
		t.Fatal("separate folders overlap")
	}
	loop := filepath.Join(base, "loop")
	if err = os.Symlink(loop, loop); err != nil {
		t.Fatal(err)
	}
	if !overlappingPaths(root, loop) {
		t.Fatal("unresolvable alias accepted")
	}
}

func TestRelocationRejectsCaseAliasInsideSource(t *testing.T) {
	root, source, _ := fixture(t)
	alias := filepath.Join(filepath.Dir(source.State), strings.ToUpper(filepath.Base(source.State)))
	original, err := os.Stat(source.State)
	if err != nil {
		t.Fatal(err)
	}
	alternate, err := os.Stat(alias)
	if err != nil || !os.SameFile(original, alternate) {
		t.Skip("filesystem distinguishes case")
	}
	before, err := os.ReadFile(source.Config)
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"nested", filepath.Join("locks", "moved")} {
		destination := filepath.Join(alias, suffix)
		if _, err = PreviewRelocation(root, source, destination); !errors.Is(err, ErrInvalid) {
			t.Fatal("nested preview accepted", err)
		}
		if _, err = Relocate(root, source, destination, Hooks{}); !errors.Is(err, ErrInvalid) {
			t.Fatal("nested move accepted", err)
		}
		if _, err = os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("nested destination created", err)
		}
	}
	after, err := os.ReadFile(source.Config)
	if err != nil || !bytes.Equal(before, after) || !platform.Writable(source.State) {
		t.Fatal("original changed", err)
	}
}

func TestSettingsMissingCaseOverlap(t *testing.T) {
	root, scope, _ := fixture(t)
	base := filepath.Dir(root)
	root = filepath.Join(base, "preferences")
	destination := filepath.Join(base, "PREFERENCES", "state")
	newScope := Scope{State: destination, Config: filepath.Join(destination, "config.json")}
	if err := Registration(root, newScope); !errors.Is(err, ErrInvalid) {
		t.Fatal("case overlap registered", err)
	}
	if _, err := PreviewRelocation(root, scope, destination); !errors.Is(err, ErrInvalid) {
		t.Fatal("case overlap preview accepted", err)
	}
	if _, err := Relocate(root, scope, destination, Hooks{}); !errors.Is(err, ErrInvalid) {
		t.Fatal("case overlap move accepted", err)
	}
	for _, path := range []string{root, destination, filepath.Join(scope.State, "relocation.json")} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("case overlap wrote data", err)
		}
	}
}

func TestSettingsMissingUnicodeAliases(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(base, "caf\u00e9")
	second := filepath.Join(base, "cafe\u0301")
	if err = os.Mkdir(first, 0700); err != nil {
		t.Fatal(err)
	}
	a, err := os.Stat(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.Stat(second)
	if err != nil || !os.SameFile(a, b) {
		t.Skip("filesystem distinguishes Unicode normalization")
	}
	root := filepath.Join(first, "missing", "settings")
	state := filepath.Join(second, "missing")
	err = Registration(root, Scope{State: state, Config: filepath.Join(state, "config.json")})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("physically nested missing folders accepted: %v; overlappingPaths=%v", err, overlappingPaths(root, state))
	}
}

func TestRelocationUnicodeAliasRefusesBeforeWrites(t *testing.T) {
	oldRoot, source, _ := fixture(t)
	base := filepath.Dir(oldRoot)
	first := filepath.Join(base, "caf\u00e9")
	second := filepath.Join(base, "cafe\u0301")
	if err := os.Mkdir(first, 0700); err != nil {
		t.Fatal(err)
	}
	a, err := os.Stat(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.Stat(second)
	if err != nil || !os.SameFile(a, b) {
		t.Skip("filesystem distinguishes Unicode normalization")
	}
	root := filepath.Join(first, "missing", "settings")
	dest := filepath.Join(second, "missing")
	_, err = Relocate(root, source, dest, Hooks{})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected invalid overlap, got %v", err)
	}
	for _, path := range []string{root, dest} {
		if _, err = os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("refused overlap created %s: %v", path, err)
		}
	}
}
