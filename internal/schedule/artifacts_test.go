package schedule

import (
	"brain/internal/platform"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func windowsFixture(t *testing.T) Plan {
	t.Helper()
	p, err := Preview("windows", Spec{Executable: `C:\owned\brain.exe`, Config: `C:\owned\config.json`, State: `C:\owned\state`, Home: `C:\owned`, Hour: 3})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestScheduleWindowsRunningAndCompleted(t *testing.T) {
	p := windowsFixture(t)
	for _, test := range []struct {
		code                    int
		running, healthy, known bool
	}{{267009, true, true, false}, {267009, false, true, false}, {267008, false, true, false}, {267045, false, true, false}, {267011, false, true, false}, {0, false, true, true}, {3, false, true, true}, {78, false, false, true}} {
		out, _ := json.Marshal(map[string]any{"exists": true, "enabled": true, "running": test.running, "result_known": true, "last_exit": test.code, "xml": p.Artifacts[0].Data})
		got, err := inspectNative(p, func(context.Context, Invocation) (ProcessResult, error) {
			return ProcessResult{Output: string(out)}, nil
		})
		if err != nil || got.Healthy != test.healthy || got.ResultKnown != test.known {
			t.Fatalf("native result %d: %+v %v", test.code, got, err)
		}
	}
}

func TestScheduleWindowsLiveDefinition(t *testing.T) {
	p := windowsFixture(t)
	for _, change := range []struct {
		name, old, new string
		healthy        bool
	}{
		{"expected", "", "", true},
		{"normalized-time", "T03:00:00</StartBoundary>", "T03:00:00.0000000</StartBoundary>", true},
		{"native-metadata", "<RegistrationInfo>", "<RegistrationInfo><Author>Native user</Author>", true},
		{"command", `WindowsPowerShell\v1.0\powershell.exe`, `cmd.exe`, false},
		{"arguments", "-NoLogo -NoProfile", "-NoLogo -NoProfile -ExecutionPolicy Bypass", false},
		{"time", "T03:00:00</StartBoundary>", "T04:00:00</StartBoundary>", false},
		{"repeat", "<DaysInterval>1</DaysInterval>", "<DaysInterval>2</DaysInterval>", false},
		{"extra-trigger", "</Triggers>", "<BootTrigger><Enabled>true</Enabled></BootTrigger></Triggers>", false},
		{"extra-action", "</Actions>", "<Exec><Command>cmd.exe</Command></Exec></Actions>", false},
		{"working-directory", "</Exec>", "<WorkingDirectory>C:\\elsewhere</WorkingDirectory></Exec>", false},
		{"catch-up", "<StartWhenAvailable>true</StartWhenAvailable>", "<StartWhenAvailable>false</StartWhenAvailable>", false},
		{"privilege", "<RunLevel>LeastPrivilege</RunLevel>", "<RunLevel>HighestAvailable</RunLevel>", false},
	} {
		t.Run(change.name, func(t *testing.T) {
			live := p.Artifacts[0].Data
			if change.old != "" {
				live = strings.Replace(live, change.old, change.new, 1)
				if live == p.Artifacts[0].Data {
					t.Fatal("test input did not change")
				}
			}
			out, _ := json.Marshal(map[string]any{"exists": true, "enabled": true, "result_known": false, "last_exit": 0, "xml": live})
			got, err := inspectNative(p, func(context.Context, Invocation) (ProcessResult, error) {
				return ProcessResult{Output: string(out)}, nil
			})
			if err != nil || !got.owned || got.Healthy != change.healthy {
				t.Fatalf("live definition: %+v %v", got, err)
			}
			if !change.healthy && got.Problem != "schedule-definition-differs" {
				t.Fatal("drift was not distinguished from ownership")
			}
		})
	}
}

func TestScheduleForeignArtifactDuringQuery(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix adapter fixture")
	}
	for _, kind := range []string{"new", "replaced", "modified", "replaced-identical"} {
		t.Run(kind, func(t *testing.T) {
			p := fixture(t, "darwin")
			a := &adapter{t: t, p: p}
			foreign := []byte("foreign contents")
			if kind != "new" {
				if _, err := Install(p, a.run); err != nil {
					t.Fatal(err)
				}
				if kind == "replaced-identical" {
					foreign = []byte(p.Artifacts[0].Data)
				}
				p.Spec.Minute++
				p, _ = Preview("darwin", p.Spec)
				a.p = p
			}
			injected := false
			a.calls = nil
			run := func(ctx context.Context, call Invocation) (ProcessResult, error) {
				if !injected {
					if err := os.MkdirAll(filepath.Dir(p.Artifacts[0].Path), 0700); err != nil {
						t.Fatal(err)
					}
					if strings.HasPrefix(kind, "replaced") {
						if err := os.Rename(p.Artifacts[0].Path, p.Artifacts[0].Path+".previous"); err != nil {
							t.Fatal(err)
						}
					}
					if err := os.WriteFile(p.Artifacts[0].Path, foreign, 0600); err != nil {
						t.Fatal(err)
					}
					injected = true
				}
				return a.run(ctx, call)
			}
			changed, err := Install(p, run)
			if changed || !errors.Is(err, ErrCollision) {
				t.Fatalf("foreign interleaving: %v %v", changed, err)
			}
			data, err := os.ReadFile(p.Artifacts[0].Path)
			if err != nil || string(data) != string(foreign) {
				t.Fatal("foreign contents changed")
			}
			for _, call := range a.calls {
				if call.Args[0] != "print" && call.Args[0] != "print-disabled" {
					t.Fatal("collision changed a native job")
				}
			}
		})
	}
}

func TestScheduleArtifactCreationDoesNotClobber(t *testing.T) {
	root := t.TempDir()
	if err := platform.WritePrivateFileIfUnchanged(root, "job", []byte("owned"), nil, ""); err != nil {
		t.Fatal(err)
	}
	if err := platform.WritePrivateFileIfUnchanged(root, "job", []byte("replacement"), nil, ""); !errors.Is(err, platform.ErrFileChanged) {
		t.Fatal("exclusive creation replaced a file", err)
	}
	data, _ := os.ReadFile(filepath.Join(root, "job"))
	if string(data) != "owned" {
		t.Fatal("exclusive creation changed existing bytes")
	}
}

func TestSchedulePartialOwnedUpdateVisible(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix adapter fixture")
	}
	p := fixture(t, "darwin")
	a := &adapter{t: t, p: p}
	if _, err := Install(p, a.run); err != nil {
		t.Fatal(err)
	}
	r, err := readReceipt(p)
	if err != nil {
		t.Fatal(err)
	}
	r.Outcome = "prepared"
	if err := saveReceipt(p, r); err != nil {
		t.Fatal(err)
	}
	receiptBytes, err := os.ReadFile(filepath.Join(p.Spec.State, receiptName))
	if err != nil {
		t.Fatal(err)
	}
	partial := []byte("interrupted owned artifact update")
	if err := os.WriteFile(p.Artifacts[0].Path, partial, 0600); err != nil {
		t.Fatal(err)
	}
	status, err := Inspect(p, a.run)
	if !errors.Is(err, ErrCollision) || status.Healthy || status.Installation != "prepared" || status.Problem != "schedule-install-incomplete" {
		t.Fatalf("partial update: %+v %v", status, err)
	}
	if changed, err := Install(p, a.run); changed || !errors.Is(err, ErrCollision) {
		t.Fatalf("unsafe partial retry: %v %v", changed, err)
	}
	actual, err := os.ReadFile(p.Artifacts[0].Path)
	if err != nil || string(actual) != string(partial) {
		t.Fatal("retry changed unverified partial artifact")
	}
	actual, err = os.ReadFile(filepath.Join(p.Spec.State, receiptName))
	if err != nil || string(actual) != string(receiptBytes) {
		t.Fatal("retry changed interrupted receipt")
	}
}
