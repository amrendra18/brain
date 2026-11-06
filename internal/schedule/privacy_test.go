package schedule

import (
	"brain/internal/platform"
	"brain/internal/redact"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func schedulePolicy(t *testing.T, values ...string) *redact.Policy {
	t.Helper()
	path := filepath.Join(t.TempDir(), "values.json")
	data, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	policy, err := redact.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func TestSchedulePrivacyIdentities(t *testing.T) {
	for _, system := range []string{"darwin", "linux", "windows"} {
		t.Run(system, func(t *testing.T) {
			root := "/allowed"
			if system == "windows" {
				root = `C:\allowed`
			}
			p, err := Preview(system, Spec{Executable: root + "/tool", Config: root + "/selected.json", State: root + "/state", Home: root + "/home", Hour: 3})
			if err != nil {
				t.Fatal(err)
			}
			if err := CheckPrivacy(p, redact.Default()); err != nil {
				t.Fatal(err)
			}
			for _, value := range []string{p.Spec.Executable, p.Spec.Config, p.Spec.State, p.Spec.Home, p.ID, p.Artifacts[0].Path, p.Arguments[0], p.Actions[0].Executable} {
				if err := CheckPrivacy(p, schedulePolicy(t, value)); !errors.Is(err, ErrProtected) {
					t.Fatalf("protected identity accepted: %v", err)
				}
			}
			// These deliberate test inputs require JSON decoding before matching.
			p.Spec.Home = root + `/Private<&"Fixture`
			if err := CheckPrivacy(p, schedulePolicy(t, `Private<&"Fixture`)); !errors.Is(err, ErrProtected) {
				t.Fatal("escaped identity accepted", err)
			}
		})
	}
}

func TestSchedulePrivacyReceiptPreservesOwnership(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix stand-in adapter")
	}
	p := fixture(t, "darwin")
	adapter := &adapter{t: t, p: p}
	if _, err := Install(p, adapter.run); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(p.Spec.State, receiptName))
	if err != nil {
		t.Fatal(err)
	}
	calls := len(adapter.calls)
	if err := CheckStoredPrivacy(p.Spec.State, schedulePolicy(t, p.Spec.Home)); !errors.Is(err, ErrProtected) {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(p.Spec.State, receiptName))
	if err != nil || !reflect.DeepEqual(before, after) || len(adapter.calls) != calls || !adapter.registered {
		t.Fatal("privacy check modified ownership or native registration", err)
	}
	if err := CheckStoredPrivacy(p.Spec.State, redact.Default()); err != nil {
		t.Fatal(err)
	}
	if changed, err := Install(p, adapter.run); err != nil || changed {
		t.Fatal("preserved ownership lost idempotent installation", changed, err)
	}
}

func TestSchedulePrivacyReceiptBounds(t *testing.T) {
	state := t.TempDir()
	if err := os.Chmod(state, 0700); err != nil {
		t.Fatal(err)
	}
	policy := schedulePolicy(t, "PrivateReceiptFixture")
	if err := CheckStoredPrivacy(state, policy); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, data string
		want       error
	}{
		{"escaped", `{"unknown":"\u0050rivateReceiptFixture"}`, ErrProtected},
		{"key", `{"PrivateReceiptFixture":true}`, ErrProtected},
		{"invalid", "invalid json", ErrFiles},
		{"invalid-spec", `{"outcome":"installed"}`, ErrCollision},
		{"oversize", strings.Repeat("x", processLimit+1), platform.ErrSourceOversize},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := platform.WriteState(state, receiptName, []byte(test.data)); err != nil {
				t.Fatal(err)
			}
			if err := CheckStoredPrivacy(state, policy); !errors.Is(err, test.want) {
				t.Fatal(err)
			}
		})
	}
}
