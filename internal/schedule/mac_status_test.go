package schedule

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"testing"
)

func macStatusRunner(a *adapter, value string) Runner {
	return func(ctx context.Context, call Invocation) (ProcessResult, error) {
		result, err := a.run(ctx, call)
		if err == nil && reflect.DeepEqual(call, a.p.Queries[1]) {
			result.Output = "\n\tdisabled services = {\n\t\t\"unrelated-job\" => unknown\n"
			if value != "" {
				result.Output += fmt.Sprintf("\t\t\"%s\" => %s\n", a.p.ID, value)
			}
			result.Output += "\t}\n"
		}
		return result, err
	}
}

func TestMacStatusSpellings(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix adapter fixture")
	}
	for _, value := range []string{"enabled", "false", "disabled", "true", "unknown", ""} {
		t.Run(value, func(t *testing.T) {
			p := fixture(t, "darwin")
			a := &adapter{t: t, p: p, registered: true, enabled: true}
			got, err := inspectNative(p, macStatusRunner(a, value))
			if value == "unknown" {
				if !errors.Is(err, ErrUnavailable) || got.Healthy {
					t.Fatalf("unknown job status was trusted: %+v %v", got, err)
				}
				return
			}
			enabled := value == "enabled" || value == "false" || value == ""
			if err != nil || !got.Registered || !got.owned || !got.matches || got.Enabled != enabled || got.Healthy != enabled {
				t.Fatalf("native status %q: %+v %v", value, got, err)
			}
			if !enabled && got.Problem != "schedule-disabled" {
				t.Fatalf("disabled job has wrong problem: %+v", got)
			}
		})
	}
}

func TestMacStatusInstallation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix adapter fixture")
	}
	for _, value := range []string{"enabled", "false", "disabled", "true", "unknown"} {
		t.Run(value, func(t *testing.T) {
			p := fixture(t, "darwin")
			a := &adapter{t: t, p: p}
			runner := macStatusRunner(a, value)
			changed, err := Install(p, runner)
			if value != "enabled" && value != "false" {
				if changed || !errors.Is(err, ErrActivation) {
					t.Fatalf("unconfirmed activation accepted: %v %v", changed, err)
				}
				r, err := readReceipt(p)
				if err != nil || r.Outcome != "failed" || r.Problem != "schedule-activation-failed" {
					t.Fatalf("activation failure receipt: %+v %v", r, err)
				}
				return
			}
			if err != nil || !changed {
				t.Fatalf("first installation: %v %v", changed, err)
			}
			status, err := Inspect(p, runner)
			if err != nil || !status.Healthy || status.Installation != "installed" {
				t.Fatalf("installed status: %+v %v", status, err)
			}
			before, err := os.Stat(p.Artifacts[0].Path)
			if err != nil {
				t.Fatal(err)
			}
			a.calls = nil
			changed, err = Install(p, runner)
			if err != nil || changed || !reflect.DeepEqual(a.calls, p.Queries) {
				t.Fatalf("repeat installation: %v %v calls=%v", changed, err, a.calls)
			}
			after, err := os.Stat(p.Artifacts[0].Path)
			if err != nil || !os.SameFile(before, after) {
				t.Fatal("repeat installation replaced the artifact", err)
			}
		})
	}
}
