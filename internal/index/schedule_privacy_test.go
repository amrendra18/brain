package index

import (
	"brain/internal/platform"
	"brain/internal/redact"
	"brain/internal/schedule"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func legacyScheduleFixture(t *testing.T, state, homeName string) (schedule.Plan, []byte) {
	t.Helper()
	home := filepath.Join(t.TempDir(), homeName)
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := schedule.Preview(runtime.GOOS, schedule.Spec{Executable: executable, Config: filepath.Join(state, "config.json"), State: state, Home: home, Hour: 3})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := json.Marshal(map[string]any{"id": plan.ID, "system": plan.System, "spec": plan.Spec, "outcome": "installed"})
	if err != nil {
		t.Fatal(err)
	}
	if err := platform.WriteState(state, "schedule.json", receipt); err != nil {
		t.Fatal(err)
	}
	for _, artifact := range plan.Artifacts {
		if err := os.MkdirAll(filepath.Dir(artifact.Path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(artifact.Path, []byte(artifact.Data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return plan, receipt
}

func assertLegacySchedulePreserved(t *testing.T, plan schedule.Plan, receipt []byte) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(plan.Spec.State, "schedule.json"))
	if err != nil || !bytes.Equal(data, receipt) {
		t.Fatal("receipt ownership changed", err)
	}
	current, exists, err := schedule.Current(plan.Spec.State)
	if err != nil || !exists || !reflect.DeepEqual(current, plan) {
		t.Fatal("saved ownership no longer usable", err)
	}
	for _, artifact := range plan.Artifacts {
		data, err := os.ReadFile(artifact.Path)
		if err != nil || string(data) != artifact.Data {
			t.Fatal("native definition changed", err)
		}
	}
}

func TestPrivacyLegacyScheduleSamePolicy(t *testing.T) {
	testLegacyScheduleRefusal(t, true)
}

func TestPrivacyLegacyScheduleChangedPolicy(t *testing.T) {
	testLegacyScheduleRefusal(t, false)
}

func testLegacyScheduleRefusal(t *testing.T, same bool) {
	t.Helper()
	source, state, db, config := fixture(t)
	value := "PrivateScheduleHomeFixture"
	if same {
		config.Policy = privacyPolicy(t, value)
	}
	write(t, source, "README.md", "Allowed lighthouse")
	refresh(t, db, state, config, false, 0)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	config.Policy = privacyPolicy(t, value)
	plan, receipt := legacyScheduleFixture(t, state, value)
	checks := []func(string, *redact.Policy) (bool, error){PrivacyRequired, PrivacyRequiredStored, func(s string, p *redact.Policy) (bool, error) { return PrivacyRequiredForIndex(s, p, false) }, func(s string, p *redact.Policy) (bool, error) { return PrivacyRequiredForIndex(s, p, true) }}
	for _, check := range checks {
		if _, err := check(state, config.Privacy()); !errors.Is(err, ErrPrivacySchedule) {
			t.Fatal("protected legacy receipt accepted", err)
		}
	}
	if _, err := MigrateLocked(state, config); !errors.Is(err, ErrPrivacySchedule) {
		t.Fatal("migration claimed refresh", err)
	}
	if _, err := os.Stat(filepath.Join(state, privacyMarker)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("refused migration changed marker", err)
	}
	assertLegacySchedulePreserved(t, plan, receipt)
}

func TestPrivacyLegacyScheduleInterruptedRefresh(t *testing.T) {
	for _, boundary := range []string{"marked", "database-published"} {
		t.Run(boundary, func(t *testing.T) {
			source, state, db, config := fixture(t)
			value := "PrivateScheduleHomeFixture"
			write(t, source, "README.md", "Allowed lighthouse")
			refresh(t, db, state, config, false, 0)
			db.Close()
			plan, receipt := legacyScheduleFixture(t, state, value)
			config.Policy = privacyPolicy(t, "OtherProtectedFixture")
			interrupted := errors.New("test interruption")
			_, err := migrateLocked(state, config, func(at string) error {
				if at == boundary {
					return interrupted
				}
				return nil
			})
			if !errors.Is(err, interrupted) {
				t.Fatal(err)
			}
			marker, err := os.ReadFile(filepath.Join(state, privacyMarker))
			if err != nil {
				t.Fatal(err)
			}
			config.Policy = privacyPolicy(t, value)
			if _, err := MigrateLocked(state, config); !errors.Is(err, ErrPrivacySchedule) {
				t.Fatal("unsafe retry accepted", err)
			}
			after, err := os.ReadFile(filepath.Join(state, privacyMarker))
			if err != nil || !bytes.Equal(marker, after) {
				t.Fatal("unsafe retry changed interruption marker", err)
			}
			assertLegacySchedulePreserved(t, plan, receipt)
			// A policy that allows this operational identity can resume without changing ownership.
			config.Policy = privacyPolicy(t, "OtherProtectedFixture")
			if _, err := MigrateLocked(state, config); err != nil {
				t.Fatal(err)
			}
			assertLegacySchedulePreserved(t, plan, receipt)
			if required, err := PrivacyRequired(state, config.Privacy()); err != nil || required {
				t.Fatal(required, err)
			}
		})
	}
}

func TestPrivacyAllowedScheduleRefresh(t *testing.T) {
	source, state, db, config := fixture(t)
	value := "PrivateDocumentFixture"
	write(t, source, "README.md", "Allowed lighthouse "+value)
	refresh(t, db, state, config, false, 0)
	db.Close()
	plan, receipt := legacyScheduleFixture(t, state, "allowed-home")
	config.Policy = privacyPolicy(t, value)
	if _, err := MigrateLocked(state, config); err != nil {
		t.Fatal(err)
	}
	assertPrivateBytes(t, state, value)
	assertLegacySchedulePreserved(t, plan, receipt)
	if required, err := PrivacyRequired(state, config.Privacy()); err != nil || required {
		t.Fatal(required, err)
	}
}
