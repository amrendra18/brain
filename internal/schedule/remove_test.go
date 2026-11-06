package schedule

import (
	"brain/internal/platform"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func removalRunner(a *adapter) Runner {
	return func(ctx context.Context, call Invocation) (ProcessResult, error) {
		if a.p.System == "linux" && call.Executable == "systemctl" && len(call.Args) > 1 {
			switch call.Args[1] {
			case "disable", "stop":
				a.calls = append(a.calls, call)
				a.enabled = false
				return ProcessResult{}, nil
			case "daemon-reload":
				if _, err := os.Stat(a.p.Artifacts[0].Path); errors.Is(err, os.ErrNotExist) {
					a.calls = append(a.calls, call)
					a.registered = false
					return ProcessResult{}, nil
				}
			}
		}
		return a.run(ctx, call)
	}
}

func TestScheduleRelocationOwnership(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix filesystem fixtures")
	}
	for _, system := range []string{"darwin", "linux"} {
		t.Run(system, func(t *testing.T) {
			p := fixture(t, system)
			a := &adapter{t: t, p: p}
			runner := removalRunner(a)
			if changed, err := RemoveOwned(p, runner); changed || err != nil {
				t.Fatal(changed, err)
			}
			if _, err := Install(p, runner); err != nil {
				t.Fatal(err)
			}
			foreign := filepath.Join(p.Spec.Home, "unrelated-job")
			if err := os.WriteFile(foreign, []byte("unrelated definition"), 0600); err != nil {
				t.Fatal(err)
			}
			if changed, err := RemoveOwned(p, runner); !changed || err != nil {
				t.Fatal(changed, err)
			}
			for _, artifact := range p.Artifacts {
				if _, err := os.Lstat(artifact.Path); !errors.Is(err, os.ErrNotExist) {
					t.Fatal(artifact.Path, err)
				}
			}
			if _, exists, err := Current(p.Spec.State); err != nil || exists {
				t.Fatal(exists, err)
			}
			if data, err := os.ReadFile(foreign); err != nil || string(data) != "unrelated definition" {
				t.Fatal(string(data), err)
			}
			if changed, err := RemoveOwned(p, runner); changed || err != nil {
				t.Fatal(changed, err)
			}
		})
	}
}

func TestScheduleRemovalRefusesChangedOwnership(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix filesystem fixtures")
	}
	for _, kind := range []string{"artifact", "native-command", "native-path", "spec", "receipt", "unreadable", "symlink", "hardlink", "missing-receipt", "unavailable", "during-query", "during-unregister"} {
		t.Run(kind, func(t *testing.T) {
			p := fixture(t, "darwin")
			a := &adapter{t: t, p: p}
			if _, err := Install(p, a.run); err != nil {
				t.Fatal(err)
			}
			artifact := p.Artifacts[0].Path
			runner := a.run
			switch kind {
			case "artifact":
				if err := os.WriteFile(artifact, []byte("foreign bytes"), 0600); err != nil {
					t.Fatal(err)
				}
			case "native-command":
				a.p.Spec.Executable += "-foreign"
				a.p.Arguments = append([]string{}, p.Arguments...)
				a.p.Arguments[0] += "-foreign"
			case "native-path":
				a.p.Artifacts = append([]Artifact{}, p.Artifacts...)
				a.p.Artifacts[0].Path += ".foreign"
			case "spec":
				p.Spec.Minute++
				p, _ = Preview(p.System, p.Spec)
			case "receipt":
				if err := os.WriteFile(filepath.Join(p.Spec.State, receiptName), []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			case "unreadable":
				if err := os.Chmod(filepath.Join(p.Spec.State, receiptName), 0000); err != nil {
					t.Fatal(err)
				}
			case "symlink", "hardlink":
				target := artifact + ".original"
				if err := os.Rename(artifact, target); err != nil {
					t.Fatal(err)
				}
				var err error
				if kind == "symlink" {
					err = os.Symlink(target, artifact)
				} else {
					err = os.Link(target, artifact)
				}
				if err != nil {
					t.Fatal(err)
				}
			case "missing-receipt":
				if err := os.Remove(filepath.Join(p.Spec.State, receiptName)); err != nil {
					t.Fatal(err)
				}
			case "unavailable":
				a.fail = "unavailable"
			case "during-query", "during-unregister":
				injected := false
				runner = func(ctx context.Context, call Invocation) (ProcessResult, error) {
					if !injected && (kind == "during-query" || call.Args[0] == "bootout") {
						if err := os.WriteFile(artifact, []byte("foreign bytes"), 0600); err != nil {
							t.Fatal(err)
						}
						injected = true
					}
					return a.run(ctx, call)
				}
			}
			a.calls = nil
			if changed, err := RemoveOwned(p, runner); changed || err == nil {
				t.Fatal("unsafe removal accepted", changed, err)
			}
			if _, err := os.Lstat(artifact); err != nil {
				t.Fatal("unverified artifact removed", err)
			}
			if kind != "during-unregister" {
				for _, call := range a.calls {
					if call.Args[0] != "print" && call.Args[0] != "print-disabled" {
						t.Fatal("changed native job", call)
					}
				}
			}
		})
	}
}

func TestScheduleRemovalInterrupted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix filesystem fixtures")
	}
	for _, system := range []string{"darwin", "linux"} {
		for _, point := range []string{"first-action", "after-unregister", "after-files"} {
			t.Run(system+"/"+point, func(t *testing.T) {
				p := fixture(t, system)
				a := &adapter{t: t, p: p}
				runner := removalRunner(a)
				if _, err := Install(p, runner); err != nil {
					t.Fatal(err)
				}
				injected := false
				actions := 0
				failing := func(ctx context.Context, call Invocation) (ProcessResult, error) {
					query := false
					for _, q := range p.Queries {
						query = query || reflect.DeepEqual(q, call)
					}
					if !query {
						actions++
					}
					shouldFail := point == "first-action" && !query || point == "after-unregister" && actions > 0 && query || point == "after-files" && system == "linux" && len(call.Args) > 1 && call.Args[1] == "daemon-reload"
					if shouldFail && !injected {
						injected = true
						return ProcessResult{}, errors.New("injected interruption")
					}
					return runner(ctx, call)
				}
				if point == "after-files" && system == "darwin" {
					return
				}
				changed, err := RemoveOwned(p, failing)
				if changed || err == nil || !injected {
					t.Fatal(changed, err, injected)
				}
				current, exists, err := Current(p.Spec.State)
				if err != nil || !exists || current.ID != p.ID {
					t.Fatal("retry receipt missing", exists, err)
				}
				if changed, err := Install(p, runner); changed || !errors.Is(err, ErrBusy) {
					t.Fatal("installation raced removal", changed, err)
				}
				if changed, err := RemoveOwned(p, runner); !changed || err != nil {
					t.Fatal("retry", changed, err)
				}
			})
		}
	}
}

func TestScheduleRemovalAbsentAndInvalid(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix filesystem fixtures")
	}
	p := fixture(t, "darwin")
	a := &adapter{t: t, p: p}
	if err := os.Remove(p.Spec.State); err != nil {
		t.Fatal(err)
	}
	if changed, err := RemoveOwned(p, a.run); changed || err != nil {
		t.Fatal(changed, err)
	}
	if _, err := os.Lstat(p.Spec.State); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("absent state created", err)
	}
	a.registered = true
	if changed, err := RemoveOwned(p, a.run); changed || !errors.Is(err, ErrCollision) {
		t.Fatal(changed, err)
	}
	a.fail = "unavailable"
	if _, err := RemoveOwned(p, a.run); err == nil {
		t.Fatal("unknown absence trusted")
	}
	p.ID += "changed"
	if _, err := RemoveOwned(p, a.run); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	p = fixture(t, "darwin")
	a = &adapter{t: t, p: p}
	if _, err := Install(p, a.run); err != nil {
		t.Fatal(err)
	}
	lock, err := platform.Acquire(p.Spec.State, "schedule", false)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, err := RemoveOwned(p, a.run); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
}

func TestScheduleRemovalWindowsActions(t *testing.T) {
	p := windowsFixture(t)
	for _, running := range []bool{false, true} {
		calls := removalActions(p, observation{Status: Status{Registered: true, Running: running}})
		want := 1
		if running {
			want = 2
		}
		if len(calls) != want || calls[len(calls)-1].Args[0] != "/Delete" {
			t.Fatal(calls)
		}
		for _, call := range calls {
			if !strings.Contains(strings.Join(call.Args, " "), p.ID) {
				t.Fatal("unscoped removal", call)
			}
		}
	}
}

func TestScheduleRemovalReceiptChangedDuringQuery(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix filesystem fixture")
	}
	p := fixture(t, "darwin")
	a := &adapter{t: t, p: p}
	if _, err := Install(p, a.run); err != nil {
		t.Fatal(err)
	}
	a.calls = nil
	modified := false
	runner := func(ctx context.Context, call Invocation) (ProcessResult, error) {
		if !modified {
			modified = true
			if err := os.WriteFile(filepath.Join(p.Spec.State, receiptName), []byte("{}"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		return a.run(ctx, call)
	}
	if changed, err := RemoveOwned(p, runner); changed || !errors.Is(err, ErrCollision) {
		t.Fatal(changed, err)
	}
	for _, call := range a.calls {
		if call.Args[0] != "print" && call.Args[0] != "print-disabled" {
			t.Fatal("changed native job", call)
		}
	}
	data, err := os.ReadFile(filepath.Join(p.Spec.State, receiptName))
	if err != nil || string(data) != "{}" {
		t.Fatal("unverified receipt overwritten", string(data), err)
	}
}

func TestScheduleRemovalPartialLinuxUnits(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix filesystem fixture")
	}
	for _, foreign := range []bool{false, true} {
		p := fixture(t, "linux")
		a := &adapter{t: t, p: p}
		if _, err := Install(p, a.run); err != nil {
			t.Fatal(err)
		}
		if foreign {
			a.p.Arguments = append([]string{}, p.Arguments...)
			a.p.Arguments[0] += "foreign"
		}
		runner := func(ctx context.Context, call Invocation) (ProcessResult, error) {
			if reflect.DeepEqual(call, p.Queries[0]) {
				return ProcessResult{Output: "LoadState=not-found\n"}, nil
			}
			return a.run(ctx, call)
		}
		observation, err := inspectNative(p, runner)
		if err != nil || observation.units != serviceUnit || observation.matches == foreign {
			t.Fatal(observation, err)
		}
		actions := removalActions(p, observation)
		if len(actions) != 1 || !reflect.DeepEqual(actions[0], invocation("systemctl", "--user", "stop", p.ID+".service")) {
			t.Fatal(actions)
		}
		if foreign {
			if _, err := RemoveOwned(p, runner); !errors.Is(err, ErrCollision) {
				t.Fatal("foreign service accepted", err)
			}
		}
	}
}
