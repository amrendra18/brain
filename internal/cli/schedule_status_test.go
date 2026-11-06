package cli

import (
	"brain/internal/index"
	"brain/internal/redact"
	"brain/internal/schedule"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestScheduleDefaultTime(t *testing.T) {
	_, _, run := freshnessFixture(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	code, out := run("schedule")
	var reply struct{ Schedule schedule.Plan }
	if code != 0 || json.Unmarshal([]byte(out), &reply) != nil || reply.Schedule.Spec.Hour != 1 || reply.Schedule.Spec.Minute != 0 {
		t.Fatal(code, out)
	}
}

func saveScheduleFixture(t *testing.T, plan schedule.Plan) {
	t.Helper()
	data, err := json.Marshal(map[string]any{"id": plan.ID, "system": plan.System, "spec": plan.Spec, "owned": nil, "outcome": "installed"})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(plan.Spec.State, "schedule.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, artifact := range plan.Artifacts {
		if err = os.MkdirAll(filepath.Dir(artifact.Path), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(artifact.Path, []byte(artifact.Data), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSchedulePreservesTime(t *testing.T) {
	_, state, run := freshnessFixture(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	code, out := run("schedule", "--time", "04:17")
	var preview struct{ Schedule schedule.Plan }
	if code != 0 || json.Unmarshal([]byte(out), &preview) != nil {
		t.Fatal(code, out)
	}
	saveScheduleFixture(t, preview.Schedule)
	for _, test := range []struct {
		args         []string
		hour, minute int
	}{{[]string{"schedule"}, 4, 17}, {[]string{"schedule", "--time", "00:00"}, 0, 0}, {[]string{"schedule", "--time", "23:59"}, 23, 59}} {
		before := scheduleFiles(t, state, home)
		code, out = run(test.args...)
		if code != 0 || json.Unmarshal([]byte(out), &preview) != nil || preview.Schedule.Spec.Hour != test.hour || preview.Schedule.Spec.Minute != test.minute {
			t.Fatal(code, out)
		}
		if !reflect.DeepEqual(before, scheduleFiles(t, state, home)) {
			t.Fatal("preview changed files")
		}
	}
	for _, bad := range []string{`{}`, `{"outcome":"installed"}`, `invalid`} {
		if err := os.WriteFile(filepath.Join(state, "schedule.json"), []byte(bad), 0600); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{{"schedule"}, {"schedule", "--time", "02:30"}} {
			if code, out = run(args...); code != 1 {
				t.Fatal("invalid receipt reset", code, out)
			}
		}
	}
}

func scheduleStatusFixture(t *testing.T) (*call, schedule.Plan, *bytes.Buffer) {
	t.Helper()
	home := t.TempDir()
	home, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(home, "state")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	// The injected native reader lets the same status tests run on every host.
	p, err := schedule.Preview("darwin", schedule.Spec{Executable: filepath.Join(home, "brain"), Config: filepath.Join(state, "config.json"), State: state, Home: home, Hour: 4, Minute: 17})
	if err != nil {
		t.Fatal(err)
	}
	saveScheduleFixture(t, p)
	var out bytes.Buffer
	c := &call{command: "schedule", words: []string{"status"}, state: state, config: p.Spec.Config, policy: redact.Default(), shell: defaultShell(), flags: map[string]string{"--format": "json"}, output: &out}
	return c, p, &out
}

func TestScheduleStatus(t *testing.T) {
	for _, test := range []struct {
		name                       string
		registered, enabled, known bool
		last                       int
	}{{"absent-job", false, false, false, 0}, {"never-run", true, true, false, 0}, {"completed", true, true, true, 0}, {"unchanged", true, true, true, 3}, {"failed", true, true, true, 7}, {"disabled", true, false, false, 0}} {
		t.Run(test.name, func(t *testing.T) {
			c, p, out := scheduleStatusFixture(t)
			run := index.Run{StartedAt: time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC), FinishedAt: time.Date(2026, 10, 28, 10, 1, 0, 0, time.UTC), Outcome: "complete"}
			if err := index.SaveRun(c.state, run); err != nil {
				t.Fatal(err)
			}
			runner := func(_ context.Context, call schedule.Invocation) (schedule.ProcessResult, error) {
				if call.Executable != "launchctl" {
					t.Fatal("unexpected native program", call)
				}
				switch call.Args[0] {
				case "print-disabled":
					return schedule.ProcessResult{Output: fmt.Sprintf("disabled services = {\n\"%s\" => %t\n}\n", p.ID, !test.enabled)}, nil
				case "print":
					if !test.registered {
						return schedule.ProcessResult{ExitCode: 113}, nil
					}
					text := "path = " + p.Artifacts[0].Path + "\nstate = waiting\nprogram = " + p.Spec.Executable + "\narguments = {\n\t" + strings.Join(p.Arguments, "\n\t") + "\n}\n"
					text += fmt.Sprintf("event triggers = {\ncalendar => {\nstream = com.apple.launchd.calendarinterval\ndescriptor = {\n\"Minute\" => %d\n\"Hour\" => %d\n}\n}\n}\n", p.Spec.Minute, p.Spec.Hour)
					if test.known {
						text += fmt.Sprintf("runs = 1\nlast exit code = %d\n", test.last)
					} else {
						text += "runs = 0\nlast exit code = (never exited)\n"
					}
					return schedule.ProcessResult{Output: text}, nil
				default:
					t.Fatal("status changed scheduler", call)
					return schedule.ProcessResult{}, nil
				}
			}
			before := scheduleFiles(t, p.Spec.Home)
			if code := c.scheduleStatus(runner); code != 0 {
				t.Fatal(code, out.String())
			}
			var reply struct {
				Schedule     schedule.Plan
				Time         string
				Status       schedule.Status
				LastIndexRun *index.Run `json:"last_index_run"`
			}
			if json.Unmarshal(out.Bytes(), &reply) != nil || reply.Time != "04:17" || reply.Schedule.Spec.Hour != 4 || reply.Status.Registered != test.registered || reply.Status.ResultKnown != test.known || reply.Status.LastExit != int64(test.last) || reply.LastIndexRun == nil || reply.LastIndexRun.Outcome != "complete" {
				t.Fatal(out.String())
			}
			healthy := test.registered && test.enabled && (!test.known || test.last == 0 || test.last == 3)
			if reply.Status.Healthy != healthy {
				t.Fatal(out.String())
			}
			if !reflect.DeepEqual(before, scheduleFiles(t, p.Spec.Home)) {
				t.Fatal("status changed files")
			}
			c.flags["--format"] = "text"
			out.Reset()
			if code := c.scheduleStatus(runner); code != 0 || !strings.Contains(out.String(), "manual or scheduled") {
				t.Fatal(code, out.String())
			}
			if !test.known && !strings.Contains(out.String(), "no completed run is known") {
				t.Fatal(out.String())
			}
			c.output = closedWriter{}
			if code := c.scheduleStatus(runner); code != 1 {
				t.Fatal("text write failure", code)
			}
			c.flags["--format"] = "json"
			if code := c.scheduleStatus(runner); code != 1 {
				t.Fatal("json write failure", code)
			}
		})
	}
}

func TestScheduleStatusAbsentAndRefusals(t *testing.T) {
	syntheticKey := "AKIA" + strings.Repeat("0", 16)
	if !redact.Default().Match(syntheticKey) {
		t.Fatal("synthetic key must exercise default protection")
	}
	for _, kind := range []string{"absent", "missing-state", "corrupt", "unreadable", "protected", "corrupt-index-run", "unavailable"} {
		t.Run(kind, func(t *testing.T) {
			c, p, out := scheduleStatusFixture(t)
			calls := 0
			runner := func(context.Context, schedule.Invocation) (schedule.ProcessResult, error) {
				calls++
				return schedule.ProcessResult{ExitCode: 113}, nil
			}
			receipt := filepath.Join(c.state, "schedule.json")
			switch kind {
			case "absent", "missing-state":
				if err := os.Remove(receipt); err != nil {
					t.Fatal(err)
				}
				if kind == "missing-state" {
					c.state = filepath.Join(p.Spec.Home, "missing")
				}
			case "corrupt":
				if err := os.WriteFile(receipt, []byte("bad"), 0600); err != nil {
					t.Fatal(err)
				}
			case "unreadable":
				if runtime.GOOS == "windows" {
					t.Skip("Unix permission fixture")
				}
				if err := os.Chmod(receipt, 0000); err != nil {
					t.Fatal(err)
				}
			case "protected":
				if err := os.WriteFile(receipt, []byte(fmt.Sprintf(`{"secret":%q}`, syntheticKey)), 0600); err != nil {
					t.Fatal(err)
				}
			case "corrupt-index-run":
				if err := os.WriteFile(filepath.Join(c.state, "index-run.json"), []byte("bad"), 0600); err != nil {
					t.Fatal(err)
				}
			case "unavailable":
				runner = func(context.Context, schedule.Invocation) (schedule.ProcessResult, error) {
					return schedule.ProcessResult{ExitCode: 1}, nil
				}
			}
			code := c.scheduleStatus(runner)
			if kind == "absent" || kind == "missing-state" {
				if code != 0 || calls != 0 || !strings.Contains(out.String(), `"schedule":null`) {
					t.Fatal(code, calls, out.String())
				}
				c.flags["--format"] = "text"
				out.Reset()
				if c.scheduleStatus(runner) != 0 || !strings.Contains(out.String(), "No saved daily schedule") {
					t.Fatal(out.String())
				}
			} else if code != 1 || strings.Contains(out.String(), syntheticKey) {
				t.Fatal(code, out.String())
			}
		})
	}
	_, _, run := freshnessFixture(t)
	for _, args := range [][]string{{"schedule", "status", "--write"}, {"schedule", "status", "--time", "01:00"}} {
		if code, out := run(args...); code != 2 {
			t.Fatal(code, out)
		}
	}
}
