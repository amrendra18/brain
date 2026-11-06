package verifyrun

import (
	"brain/internal/schedule"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func init() {
	if os.Getenv("BRAIN_VERIFY_STUB_FIXTURE") == "schedule" {
		os.Exit(scheduleFixtureProcess())
	}
}

func TestScheduleFixtureJourney(t *testing.T) {
	d := stubFixtureDriver(t, "schedule", "")
	if err := d.schedule(0); err != nil {
		t.Fatal(err)
	}
	if len(d.summary.StandIns) != 1 || d.summary.SideEffects["schedule_1"] == "" {
		t.Fatal("schedule evidence missing")
	}
	if err := d.saveText(); err != nil {
		t.Fatal(err)
	}
	text, err := os.ReadFile(filepath.Join(d.evidence, "summary.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(text), "stand-in") || strings.Contains(string(text), "Stand-ins: none") {
		t.Fatal("stand-in was hidden", string(text))
	}
}

func TestScheduleRejectsBrokenInputs(t *testing.T) {
	for _, fault := range []string{"preview-write", "preview-scope", "preview-json", "install", "install-scope", "noop", "noop-write", "failure", "failure-recovery", "failure-code", "failure-receipt", "retry", "retry-identity", "source-write", "unrelated-write"} {
		t.Run(fault, func(t *testing.T) {
			d := stubFixtureDriver(t, "schedule", fault)
			if err := d.schedule(0); err == nil {
				t.Fatal("accepted deliberately broken test input", fault)
			}
		})
	}
}

func TestScheduleValidators(t *testing.T) {
	d := stubFixtureDriver(t, "schedule", "")
	j, err := d.newFeatureJourney("validator", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checkSchedule([]byte(`{`), j, 17, false, false); err == nil {
		t.Fatal("accepted invalid JSON")
	}
	if _, err := checkSchedule([]byte(`{}`), j, 17, false, false); err == nil {
		t.Fatal("accepted missing plan")
	}
	before := map[string]string{".": "directory", "unrelated": "old"}
	if scheduleFiles(before, map[string]string{".": "directory", "unrelated": "new"}, j.home, scheduleReply{}) == nil {
		t.Fatal("accepted unrelated overwrite")
	}
	if scheduleFiles(before, map[string]string{".": "directory", "unrelated": "old", "new": "new"}, j.home, scheduleReply{}) == nil {
		t.Fatal("accepted unrelated new file")
	}
	if _, err := scheduleSnapshot(filepath.Join(j.base, "absent")); err == nil {
		t.Fatal("accepted missing snapshot root")
	}
	if _, _, _, err := scheduleStandIn(filepath.Join(j.base, "absent")); err == nil {
		t.Fatal("accepted missing scheduler parent")
	}
}

func scheduleFixtureProcess() int {
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	args = args[1:]
	config, state, minute := "", "", 17
	write := false
	for i, arg := range args {
		if arg == "--config" {
			config = args[i+1]
		}
		if arg == "--state-dir" {
			state = args[i+1]
		}
		if arg == "--time" && args[i+1] == "03:18" {
			minute = 18
		}
		if arg == "--write" {
			write = true
		}
	}
	home := os.Getenv("HOME")
	base := filepath.Dir(home)
	fault := os.Getenv("BRAIN_VERIFY_FAULT")
	executable := os.Getenv("BRAIN_VERIFY_LAUNCHER")
	if executable == "" {
		panic("fixture launcher missing")
	}
	plan, err := schedule.Preview(runtime.GOOS, schedule.Spec{Executable: executable, Config: config, State: state, Home: home, Hour: 3, Minute: minute})
	if err != nil {
		panic(err)
	}
	r := scheduleReply{}
	encoded, err := json.Marshal(plan)
	if err != nil {
		panic(err)
	}
	if err := json.Unmarshal(encoded, &r.Schedule); err != nil {
		panic(err)
	}
	changed := false
	r.Written = &changed
	r.Changed = &changed
	reply := func(value any) { json.NewEncoder(os.Stdout).Encode(value) }
	if !write {
		if fault == "preview-write" {
			os.WriteFile(filepath.Join(home, "unexpected"), []byte("changed"), 0600)
		}
		if fault == "preview-scope" {
			r.Schedule.Spec.Config = "wrong"
		}
		if fault == "preview-json" {
			fmt.Print("{")
			return 0
		}
		reply(r)
		return 0
	}
	var previous struct {
		Outcome string `json:"outcome"`
		Minute  int    `json:"minute"`
	}
	receipt := filepath.Join(state, "schedule.json")
	data, _ := os.ReadFile(receipt)
	json.Unmarshal(data, &previous)
	stage := "install"
	if previous.Outcome == "installed" && minute == 17 {
		stage = "noop"
	}
	if minute == 18 && previous.Outcome != "failed" {
		stage = "failure"
	}
	if minute == 18 && previous.Outcome == "failed" {
		stage = "retry"
	}
	if fault == stage {
		fmt.Print(`{"broken_test_input":true}`)
		return 2
	}
	if stage == "noop" {
		if fault == "noop-write" {
			os.WriteFile(filepath.Join(state, "unexpected"), []byte("changed"), 0600)
		}
	} else {
		for _, artifact := range plan.Artifacts {
			os.MkdirAll(filepath.Dir(artifact.Path), 0700)
			os.WriteFile(artifact.Path, []byte(artifact.Data), 0600)
		}
		var activationError error
		for _, action := range plan.Actions {
			if activationError = exec.Command(action.Executable, action.Args...).Run(); activationError != nil {
				break
			}
		}
		if activationError != nil {
			outcome := "failed"
			if fault == "failure-receipt" {
				outcome = "installed"
			}
			writeJSON(receipt, map[string]any{"outcome": outcome, "minute": minute})
			argv := []string{executable, "schedule", "--write", "--config", config, "--state-dir", state}
			if fault == "failure-recovery" {
				argv = []string{executable, "schedule"}
			}
			code := "schedule-activation-failed"
			if fault == "failure-code" {
				code = "unknown"
			}
			reply(map[string]any{"error": map[string]any{"code": code, "next": []string{"retry"}, "next_argv": [][]string{argv}}})
			return 1
		}
		changed = true
		writeJSON(receipt, map[string]any{"outcome": "installed", "minute": minute})
	}
	r.Status = &struct {
		Registered, Enabled, Healthy bool
		Installation                 string
	}{true, true, true, "installed"}
	if fault == "install-scope" {
		r.Schedule.Arguments = []string{executable, "index"}
	}
	if fault == "retry-identity" && stage == "retry" {
		r.Schedule.ID = "another-id"
	}
	if fault == "source-write" {
		os.WriteFile(filepath.Join(base, "sources", "harbor", "README.md"), []byte("unexpected"), 0600)
	}
	if fault == "unrelated-write" {
		os.WriteFile(filepath.Join(home, "unrelated.txt"), []byte("unexpected"), 0600)
	}
	reply(r)
	if stage == "noop" {
		return 3
	}
	return 0
}

func TestSchedulerStandInLifecycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX scheduler stand-ins")
	}
	base := t.TempDir()
	bin, fail, log, err := scheduleStandIn(base)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	invoke := func(name string, want int, args ...string) string {
		t.Helper()
		cmd := exec.Command(filepath.Join(bin, name), args...)
		cmd.Env = []string{"HOME=" + home, "PATH=" + bin}
		data, err := cmd.Output()
		got := 0
		if err != nil {
			if exit, ok := err.(*exec.ExitError); ok {
				got = exit.ExitCode()
			} else {
				t.Fatal(err)
			}
		}
		if got != want {
			t.Fatalf("stand-in %s %v exit %d, want %d", name, args, got, want)
		}
		return string(data)
	}
	spec := schedule.Spec{Executable: filepath.Join(home, `Brain "quoted" & <tag> 旅\tool`), Config: filepath.Join(home, `selected "quoted" & 旅.json `), State: filepath.Join(home, "state"), Home: home, Hour: 3, Minute: 17}
	makePlan := func(system string) schedule.Plan {
		t.Helper()
		plan, err := schedule.Preview(system, spec)
		if err != nil {
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
		return plan
	}
	mac := makePlan("darwin")
	invoke("launchctl", 113, "print", "gui/test/id")
	if err := os.WriteFile(fail, []byte("test input"), 0600); err != nil {
		t.Fatal(err)
	}
	invoke("launchctl", 7, "enable", "gui/test/id")
	invoke("launchctl", 7, "bootstrap", "gui/test", mac.Artifacts[0].Path)
	if err := os.Remove(fail); err != nil {
		t.Fatal(err)
	}
	invoke("launchctl", 0, "enable", "gui/test/id")
	invoke("launchctl", 0, "bootstrap", "gui/test", mac.Artifacts[0].Path)
	loadedMac := invoke("launchctl", 0, "print", "gui/test/id")
	if !strings.Contains(loadedMac, "program = "+spec.Executable+"\n") || !strings.Contains(loadedMac, "\t"+spec.Config+"\n") {
		t.Fatal("loaded arguments were not decoded", loadedMac)
	}
	spec.Config = filepath.Join(home, "changed-config.json")
	makePlan("darwin")
	if out := invoke("launchctl", 0, "print", "gui/test/id"); out != loadedMac {
		t.Fatal("artifact edit changed loaded macOS definition", out)
	}
	invoke("launchctl", 0, "bootstrap", "gui/test", mac.Artifacts[0].Path)
	if out := invoke("launchctl", 0, "print", "gui/test/id"); !strings.Contains(out, "\t"+spec.Config+"\n") {
		t.Fatal("bootstrap did not reload arguments", out)
	}
	if out := invoke("launchctl", 0, "print-disabled", "gui/test"); !strings.Contains(out, "disabled services = {") {
		t.Fatal(out)
	}
	invoke("launchctl", 0, "bootout", "gui/test/id")
	invoke("launchctl", 113, "print", "gui/test/id")
	if err := os.Remove(filepath.Join(bin, "registered")); err != nil {
		t.Fatal(err)
	}
	linux := makePlan("linux")
	timer := linux.ID + ".timer"
	if out := invoke("systemctl", 0, "--user", "show", timer); !strings.Contains(out, "LoadState=not-found") {
		t.Fatal(out)
	}
	invoke("systemctl", 0, "--user", "daemon-reload")
	invoke("systemctl", 0, "--user", "enable", "--now", timer)
	invoke("systemctl", 0, "--user", "restart", timer)
	loadedTimer := invoke("systemctl", 0, "--user", "show", timer)
	for _, field := range []string{"FragmentPath=" + linux.Artifacts[1].Path, "Unit=" + linux.ID + ".service", "Persistent=yes", "TimersCalendar={ OnCalendar=*-*-* 03:17:00 ; next_elapse=n/a }", "TimersMonotonic="} {
		if !strings.Contains(loadedTimer, field) {
			t.Fatal("missing timer field", field, loadedTimer)
		}
	}
	bus := linux.Queries[len(linux.Queries)-1]
	loadedArgs := invoke("busctl", 0, bus.Args...)
	var reply struct {
		Type string
		Data [][]json.RawMessage
	}
	if err := json.Unmarshal([]byte(loadedArgs), &reply); err != nil || reply.Type != "a(sasasttttuii)" || len(reply.Data) != 1 {
		t.Fatal("invalid loaded arguments", loadedArgs, err)
	}
	var argv []string
	if err := json.Unmarshal(reply.Data[0][1], &argv); err != nil || !reflect.DeepEqual(argv, linux.Arguments) {
		t.Fatal("loaded argument vector differs", argv, linux.Arguments, err)
	}
	if string(reply.Data[0][2]) != `["no-env-expand"]` {
		t.Fatal("missing literal argument flag", loadedArgs)
	}
	spec.Config = filepath.Join(home, "another-config.json")
	spec.Minute = 18
	makePlan("linux")
	if out := invoke("busctl", 0, bus.Args...); out != loadedArgs {
		t.Fatal("artifact edit changed loaded service", out)
	}
	if out := invoke("systemctl", 0, "--user", "show", timer); out != loadedTimer {
		t.Fatal("artifact edit changed loaded timer", out)
	}
	invoke("systemctl", 0, "--user", "daemon-reload")
	if out := invoke("busctl", 0, bus.Args...); !strings.Contains(out, "another-config.json") {
		t.Fatal("daemon-reload did not reload service", out)
	}
	if out := invoke("systemctl", 0, "--user", "show", timer); !strings.Contains(out, "03:18:00") {
		t.Fatal("daemon-reload did not reload timer", out)
	}

	if err := os.WriteFile(fail, []byte("test input"), 0600); err != nil {
		t.Fatal(err)
	}
	invoke("systemctl", 7, "--user", "restart", "id.timer")
	invoke("launchctl", 8, "unexpected")
	invoke("systemctl", 8, "--user", "unexpected")
	if data, err := os.ReadFile(log); err != nil || len(data) == 0 {
		t.Fatal("stand-in calls missing", err)
	}
}

func TestScheduleStandInsMatchLoadedContract(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX scheduler stand-ins")
	}
	for _, system := range []string{"darwin", "linux"} {
		t.Run(system, func(t *testing.T) {
			base := t.TempDir()
			bin, _, _, err := scheduleStandIn(base)
			if err != nil {
				t.Fatal(err)
			}
			home := t.TempDir()
			plan, err := schedule.Preview(system, schedule.Spec{Executable: filepath.Join(base, `brain "quoted" & 旅`), Config: filepath.Join(base, `selected %$ config.json`), State: filepath.Join(base, "state"), Home: home, Hour: 3, Minute: 17})
			if err != nil {
				t.Fatal(err)
			}
			runner := func(ctx context.Context, call schedule.Invocation) (schedule.ProcessResult, error) {
				cmd := exec.CommandContext(ctx, filepath.Join(bin, call.Executable), call.Args...)
				cmd.Env = []string{"HOME=" + home, "PATH=" + bin}
				data, err := cmd.Output()
				result := schedule.ProcessResult{Output: string(data)}
				if exit, ok := err.(*exec.ExitError); ok {
					result.ExitCode = exit.ExitCode()
					err = nil
				}
				return result, err
			}
			if changed, err := schedule.Install(plan, runner); err != nil || !changed {
				t.Fatal("stand-in installation failed", changed, err)
			}
			if status, err := schedule.Inspect(plan, runner); err != nil || !status.Healthy {
				t.Fatal("loaded definition was not verified", status, err)
			}
			if changed, err := schedule.Install(plan, runner); err != nil || changed {
				t.Fatal("identical loaded definition was not a no-op", changed, err)
			}
		})
	}
}
