package schedule

import (
	"brain/internal/platform"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"
)

func fixture(t *testing.T, system string) Plan {
	t.Helper()
	home := t.TempDir()
	state := filepath.Join(home, "state")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	p, err := Preview(system, Spec{Executable: filepath.Join(home, "brain"), Config: filepath.Join(home, "selected.json"), State: state, Home: home, Hour: 3})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

type adapter struct {
	t                          *testing.T
	p                          Plan
	registered, enabled, known bool
	last                       int
	fail                       string
	calls                      []Invocation
}

func (a *adapter) run(ctx context.Context, call Invocation) (ProcessResult, error) {
	a.t.Helper()
	if _, ok := ctx.Deadline(); !ok {
		a.t.Fatal("scheduler call has no deadline")
	}
	a.calls = append(a.calls, call)
	if a.fail == "unavailable" {
		return ProcessResult{}, errors.New(a.p.Spec.Config)
	}
	if a.fail != "" && strings.Contains(strings.Join(call.Args, " "), a.fail) {
		return ProcessResult{ExitCode: 1, Output: a.p.Spec.State}, nil
	}
	if call.Executable == "busctl" {
		data, _ := json.Marshal(map[string]any{"type": "a(sasasttttuii)", "data": [][]any{{a.p.Spec.Executable, a.p.Arguments, []string{"no-env-expand"}, 0, 0, 0, 0, 0, 0, 0}}})
		return ProcessResult{Output: string(data)}, nil
	}
	for i, query := range a.p.Queries {
		if !reflect.DeepEqual(query, call) {
			continue
		}
		if a.p.System == "darwin" {
			if i == 1 {
				return ProcessResult{Output: fmt.Sprintf("disabled services = {\n\"%s\" => %t\n}", a.p.ID, !a.enabled)}, nil
			}
			if !a.registered {
				return ProcessResult{ExitCode: 113}, nil
			}
			out := "path = " + a.p.Artifacts[0].Path + "\nstate = not running\n" + macLoadedCommand(a.p) + macLoadedCalendar(a.p)
			if a.known {
				out += fmt.Sprintf("last exit code = %d\n", a.last)
			}
			return ProcessResult{Output: out}, nil
		}
		if !a.registered {
			return ProcessResult{Output: "LoadState=not-found\n"}, nil
		}
		active, enabled := "inactive", "disabled"
		if a.enabled {
			active, enabled = "active", "enabled"
		}
		out := fmt.Sprintf("LoadState=loaded\nActiveState=%s\nUnitFileState=%s\nFragmentPath=%s\nDropInPaths=\nResult=success\nExecMainStartTimestampMonotonic=0\n", active, enabled, a.p.Artifacts[1-i].Path)
		if i == 0 {
			out += fmt.Sprintf("Unit=%s.service\nPersistent=yes\nTimersCalendar={ OnCalendar=*-*-* %02d:%02d:00 ; next_elapse=n/a }\nTimersMonotonic=\n", a.p.ID, a.p.Spec.Hour, a.p.Spec.Minute)
		}
		if a.known && i == 1 {
			out += fmt.Sprintf("ExecMainStartTimestampMonotonic=123\nExecMainStatus=%d\n", a.last)
		}
		return ProcessResult{Output: out}, nil
	}
	if call.Executable == "launchctl" && call.Args[0] == "bootout" {
		a.registered = false
		return ProcessResult{}, nil
	}
	a.registered, a.enabled = true, true
	return ProcessResult{}, nil
}

func macLoadedCommand(p Plan) string {
	return "program = " + p.Spec.Executable + "\narguments = {\n\t" + strings.Join(p.Arguments, "\n\t") + "\n}\n"
}

func macLoadedCalendar(p Plan) string {
	return fmt.Sprintf("\tevent triggers = {\n\t\t%s.268435474 => {\n\t\t\tkeepalive = 0\n\t\t\tservice = %s\n\t\t\tstream = com.apple.launchd.calendarinterval\n\t\t\tmonitor = com.apple.UserEventAgent-Aqua\n\t\t\tdescriptor = {\n\t\t\t\t\"Minute\" => %d\n\t\t\t\t\"Hour\" => %d\n\t\t\t}\n\t\t}\n\t}\n", p.ID, p.ID, p.Spec.Minute, p.Spec.Hour)
}

func TestSchedulePreviewNoWrites(t *testing.T) {
	root := t.TempDir()
	fakeBin := t.TempDir()
	t.Setenv("PATH", fakeBin)
	t.Setenv("SCHEDULE_PREVIEW_MARKER", filepath.Join(root, "process-started"))
	for _, name := range []string{"launchctl", "systemctl", "busctl", "schtasks.exe", "powershell.exe"} {
		if err := os.WriteFile(filepath.Join(fakeBin, name), []byte("#!/bin/sh\nprintf called > \"$SCHEDULE_PREVIEW_MARKER\"\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if runtime.GOOS != "windows" {
		result, err := run(nil, invocation(filepath.Join(fakeBin, "launchctl")))
		if err != nil || result.ExitCode != 0 {
			t.Fatal("preview process control did not run", err)
		}
		marker := filepath.Join(root, "process-started")
		data, err := os.ReadFile(marker)
		if err != nil || string(data) != "called" {
			t.Fatal("preview process control did not write its marker")
		}
		if err := os.Remove(marker); err != nil {
			t.Fatal(err)
		}
	}
	missing := filepath.Join(root, "missing")
	for _, system := range []string{"darwin", "linux", "windows"} {
		t.Run(system, func(t *testing.T) {
			spec := Spec{Executable: missing + "/brain", Config: missing + "/config.json", State: missing + "/state", Home: missing, Hour: 3}
			if system == "windows" {
				spec = Spec{Executable: `C:\missing\brain.exe`, Config: `C:\missing\config.json`, State: `C:\missing\state`, Home: `C:\missing`, Hour: 3}
			}
			a, err := Preview(system, spec)
			if err != nil {
				t.Fatal(err)
			}
			b, err := Preview(system, spec)
			if err != nil || !reflect.DeepEqual(a, b) {
				t.Fatal("preview is not deterministic")
			}
			if len(a.Artifacts) == 0 || len(a.Actions) == 0 || len(a.Queries) == 0 || a.CatchUp == "" {
				t.Fatal("preview omits effects or limitations")
			}
			spec.Config += ".other"
			other, _ := Preview(system, spec)
			if other.ID != a.ID {
				t.Fatal("config created another state job")
			}
			spec.State += "other"
			other, _ = Preview(system, spec)
			if other.ID == a.ID {
				t.Fatal("different states share a job")
			}
		})
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("preview wrote files")
	}
}

func TestScheduleCurrentAndInvalidPlans(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix adapter fixture")
	}
	p := fixture(t, "darwin")
	missing := filepath.Join(p.Spec.Home, "missing")
	if _, exists, err := Current(missing); err != nil || exists {
		t.Fatalf("missing state: %v %v", exists, err)
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("Current created state")
	}
	if _, exists, err := Current(p.Spec.State); err != nil || exists {
		t.Fatalf("missing receipt: %v %v", exists, err)
	}
	a := &adapter{t: t, p: p}
	if _, err := Install(p, a.run); err != nil {
		t.Fatal(err)
	}
	current, exists, err := Current(p.Spec.State)
	if err != nil || !exists || !reflect.DeepEqual(current, p) {
		t.Fatalf("saved plan: %+v %v %v", current, exists, err)
	}
	for _, edit := range []func(*Spec){func(s *Spec) { s.Hour = 24 }, func(s *Spec) { s.Minute = -1 }, func(s *Spec) { s.Config = "relative" }, func(s *Spec) { s.State = "/bad\npath" }, func(s *Spec) { s.Executable = "/bad\x00path" }} {
		spec := p.Spec
		edit(&spec)
		if _, err := Preview("darwin", spec); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid scope accepted")
		}
	}
	if _, err := Preview("other", p.Spec); !errors.Is(err, ErrUnsupported) {
		t.Fatal("unsupported scheduler accepted")
	}
	if _, err := Inspect(Plan{}, a.run); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid inspection plan accepted")
	}
	foreignSystem := "linux"
	if runtime.GOOS == "linux" {
		foreignSystem = "darwin"
	}
	foreign, _ := Preview(foreignSystem, p.Spec)
	if _, err := Install(foreign, nil); !errors.Is(err, ErrUnsupported) {
		t.Fatal("foreign native scheduler started")
	}
	for _, data := range []string{"bad json", `{"outcome":"installed"}`} {
		if err := os.WriteFile(filepath.Join(p.Spec.State, receiptName), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := Current(p.Spec.State); err == nil {
			t.Fatal("invalid receipt accepted")
		}
	}
}

func TestScheduleWriteIdempotent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix native adapters use fixture Unix paths")
	}
	for _, system := range []string{"darwin", "linux"} {
		t.Run(system, func(t *testing.T) {
			p := fixture(t, system)
			if err := os.Chmod(p.Spec.Home, 0755); err != nil {
				t.Fatal(err)
			}
			unrelated := filepath.Join(p.Spec.Home, "unrelated")
			if err := os.WriteFile(unrelated, []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			a := &adapter{t: t, p: p}
			changed, err := Install(p, a.run)
			if err != nil || !changed {
				t.Fatalf("first adapter install: %v, %v", changed, err)
			}
			before, _ := os.Stat(p.Artifacts[0].Path)
			a.calls = nil
			changed, err = Install(p, a.run)
			if err != nil || changed || len(a.calls) != len(p.Queries) {
				t.Fatalf("identical adapter install: %v, %v, calls=%v", changed, err, a.calls)
			}
			after, _ := os.Stat(p.Artifacts[0].Path)
			if !os.SameFile(before, after) {
				t.Fatal("no-op replaced artifact")
			}
			home, _ := os.Stat(p.Spec.Home)
			if home.Mode().Perm() != 0755 {
				t.Fatal("installation changed home permissions")
			}
			keep, _ := os.ReadFile(unrelated)
			if string(keep) != "keep" {
				t.Fatal("installation changed unrelated file")
			}
			for _, artifact := range p.Artifacts {
				data, _ := os.ReadFile(artifact.Path)
				info, _ := os.Stat(artifact.Path)
				if string(data) != artifact.Data || info.Mode().Perm() != 0600 {
					t.Fatal("artifact data or privacy differs")
				}
			}
			expected := map[string]bool{unrelated: true, filepath.Join(p.Spec.State, receiptName): true, filepath.Join(p.Spec.State, "locks", "schedule.lock"): true}
			for _, artifact := range p.Artifacts {
				expected[artifact.Path] = true
			}
			if err := filepath.WalkDir(p.Spec.Home, func(name string, entry os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if !entry.IsDir() && !expected[name] {
					t.Errorf("unexpected installation file %s", filepath.Base(name))
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			oldID := p.ID
			p.Spec.Config += ".next"
			p.Spec.Executable += "-next"
			p, _ = Preview(system, p.Spec)
			a.p = p
			if changed, err = Install(p, a.run); err != nil || !changed || oldID != p.ID {
				t.Fatalf("replacement adapter install: %v %v", changed, err)
			}
			status, err := Inspect(p, a.run)
			if err != nil || !status.Healthy || status.Installation != "installed" || status.ResultKnown {
				t.Fatalf("status: %+v %v", status, err)
			}
		})
	}
}

func TestScheduleFailureVisible(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix adapter fixture")
	}
	for _, system := range []string{"darwin", "linux"} {
		t.Run(system, func(t *testing.T) {
			p := fixture(t, system)
			a := &adapter{t: t, p: p, fail: "enable"}
			if changed, err := Install(p, a.run); changed || !errors.Is(err, ErrActivation) {
				t.Fatalf("failure: %v %v", changed, err)
			}
			r, err := readReceipt(p)
			if err != nil || r.Outcome != "failed" || r.Problem != "schedule-activation-failed" {
				t.Fatalf("receipt: %+v %v", r, err)
			}
			a.fail = ""
			status, err := Inspect(p, a.run)
			if err != nil || status.Healthy || status.Problem != "schedule-install-incomplete" {
				t.Fatalf("failed status: %+v %v", status, err)
			}
			if changed, err := Install(p, a.run); !changed || err != nil {
				t.Fatalf("activation retry: %v %v", changed, err)
			}
			for _, exit := range []int{3, 78} {
				a.known, a.last = true, exit
				status, err = Inspect(p, a.run)
				if err != nil || !status.ResultKnown || status.LastExit != int64(exit) || status.Healthy != (exit == 3) {
					t.Fatalf("native exit %d: %+v %v", exit, status, err)
				}
			}
			r.Outcome = "prepared"
			if err := saveReceipt(p, r); err != nil {
				t.Fatal(err)
			}
			status, err = Inspect(p, a.run)
			if err != nil || status.Problem != "schedule-install-incomplete" {
				t.Fatalf("interrupted install: %+v %v", status, err)
			}
			a.known = false
			if changed, err := Install(p, a.run); !changed || err != nil {
				t.Fatalf("interrupted retry: %v %v", changed, err)
			}
			a.fail = "unavailable"
			status, err = Inspect(p, a.run)
			if !errors.Is(err, ErrUnavailable) || status.Problem != "schedule-unavailable" {
				t.Fatalf("missing scheduler: %+v %v", status, err)
			}
			if strings.Contains(err.Error(), p.Spec.State) || strings.Contains(err.Error(), p.Spec.Config) {
				t.Fatal("native error exposed paths")
			}
			if _, err = Install(p, a.run); !errors.Is(err, ErrUnavailable) {
				t.Fatal(err)
			}
		})
	}
}

func TestScheduleCollisionsAndScope(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix adapter fixture")
	}
	for _, kind := range []string{"foreign-file", "foreign-job", "symlink", "hardlink", "public-file", "busy", "unsafe-state", "tampered-plan"} {
		t.Run(kind, func(t *testing.T) {
			p := fixture(t, "darwin")
			a := &adapter{t: t, p: p}
			want := ErrFiles
			switch kind {
			case "foreign-job":
				a.registered, a.enabled = true, true
				want = ErrCollision
			case "busy":
				lock, err := platform.Acquire(p.Spec.State, "schedule", false)
				if err != nil {
					t.Fatal(err)
				}
				defer lock.Close()
				want = ErrBusy
			case "unsafe-state":
				if err := os.Chmod(p.Spec.State, 0755); err != nil {
					t.Fatal(err)
				}
			case "tampered-plan":
				p.Actions[0].Executable = "unowned"
				want = ErrInvalid
			default:
				if err := os.MkdirAll(filepath.Dir(p.Artifacts[0].Path), 0700); err != nil {
					t.Fatal(err)
				}
				foreign := filepath.Join(p.Spec.Home, "foreign")
				if err := os.WriteFile(foreign, []byte("private foreign contents"), 0600); err != nil {
					t.Fatal(err)
				}
				switch kind {
				case "symlink":
					if err := os.Symlink(foreign, p.Artifacts[0].Path); err != nil {
						t.Fatal(err)
					}
				case "hardlink":
					if err := os.Link(foreign, p.Artifacts[0].Path); err != nil {
						t.Fatal(err)
					}
				default:
					mode := os.FileMode(0600)
					if kind == "public-file" {
						mode = 0644
					} else {
						want = ErrCollision
					}
					if err := os.WriteFile(p.Artifacts[0].Path, []byte("foreign"), mode); err != nil {
						t.Fatal(err)
					}
				}
			}
			if changed, err := Install(p, a.run); changed || !errors.Is(err, want) {
				t.Fatalf("got %v %v, want %v", changed, err, want)
			}
			for _, call := range a.calls {
				if !reflect.DeepEqual(call, p.Queries[0]) && !reflect.DeepEqual(call, p.Queries[1]) {
					t.Fatalf("collision changed job: %+v", call)
				}
			}
		})
	}
}

func TestScheduleArguments(t *testing.T) {
	for _, system := range []string{"darwin", "linux", "windows"} {
		t.Run(system, func(t *testing.T) {
			root := `/owned space/é'"%NAME% $cash; &()`
			if system == "windows" {
				root = `C:\owned space\é'%NAME% $cash; &()`
			}
			spec := Spec{Executable: root + "/brain", Config: root + `/config'".json`, State: root + "/state\\", Home: root, Hour: 23, Minute: 59}
			p, err := Preview(system, spec)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{spec.Executable, "index", "--config", spec.Config, "--state-dir", spec.State, "--format", "json"}
			if !reflect.DeepEqual(p.Arguments, want) {
				t.Fatal("scope arguments changed")
			}
			switch system {
			case "darwin":
				var plist struct {
					Args []string `xml:"dict>array>string"`
				}
				if err := xml.Unmarshal([]byte(p.Artifacts[0].Data), &plist); err != nil || !reflect.DeepEqual(plist.Args, want) {
					t.Fatalf("launchd argv: %v %v", plist.Args, err)
				}
			case "linux":
				line := properties(p.Artifacts[0].Data, "=")["ExecStart"]
				got := decodeSystemd(t, line)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("systemd argv: %#v", got)
				}
				if !strings.Contains(p.Artifacts[1].Data, "OnCalendar=*-*-* 23:59:00\nPersistent=true") || !strings.Contains(p.Artifacts[0].Data, "SuccessExitStatus=3") {
					t.Fatal("systemd catch-up or no-op exit missing")
				}
			case "windows":
				var task struct {
					Command   string `xml:"Actions>Exec>Command"`
					Arguments string `xml:"Actions>Exec>Arguments"`
					End       string `xml:"Triggers>CalendarTrigger>EndBoundary"`
					Catch     bool   `xml:"Settings>StartWhenAvailable"`
				}
				if err := xml.Unmarshal([]byte(p.Artifacts[0].Data), &task); err != nil {
					t.Fatal(err)
				}
				if !task.Catch || task.End == "" {
					t.Fatal("missed-start trigger is ineligible")
				}
				words := strings.Fields(task.Arguments)
				script := decodePowerShell(t, words[len(words)-1])
				if strings.Contains(task.Arguments, "%") || !strings.Contains(script, "$p.UseShellExecute = $false") || strings.Contains(script, "ArgumentList") {
					t.Fatal("wrapper loses percent paths or requires newer PowerShell")
				}
				file := psValue(t, script, "$p.FileName = ")
				args := psValue(t, script, "$p.Arguments = ")
				got := append([]string{file}, decodeWindows(args)...)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("Windows argv: %#v, want %#v", got, want)
				}
			}
		})
	}
}

func decodePowerShell(t *testing.T, encoded string) string {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	units := make([]uint16, len(data)/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(data[i*2:])
	}
	return string(utf16.Decode(units))
}

func psValue(t *testing.T, script, prefix string) string {
	t.Helper()
	_, rest, ok := strings.Cut(script, prefix+"'")
	if !ok {
		t.Fatal("missing PowerShell value")
	}
	var result strings.Builder
	for i := 0; i < len(rest); i++ {
		if rest[i] == '\'' {
			if i+1 < len(rest) && rest[i+1] == '\'' {
				result.WriteByte('\'')
				i++
				continue
			}
			return result.String()
		}
		result.WriteByte(rest[i])
	}
	t.Fatal("unterminated PowerShell value")
	return ""
}

func decodeWindows(line string) []string {
	var args []string
	var word strings.Builder
	quoted := false
	for i := 0; i < len(line); {
		if line[i] == ' ' && !quoted {
			args = append(args, word.String())
			word.Reset()
			i++
			continue
		}
		slashes := 0
		for i < len(line) && line[i] == '\\' {
			slashes++
			i++
		}
		if i < len(line) && line[i] == '"' {
			word.WriteString(strings.Repeat(`\`, slashes/2))
			if slashes%2 == 1 {
				word.WriteByte('"')
			} else {
				quoted = !quoted
			}
			i++
			continue
		}
		word.WriteString(strings.Repeat(`\`, slashes))
		if i < len(line) {
			word.WriteByte(line[i])
			i++
		}
	}
	return append(args, word.String())
}

func decodeSystemd(t *testing.T, line string) []string {
	t.Helper()
	var args []string
	for len(line) > 0 {
		end := 1
		for end < len(line) {
			if line[end] == '\\' {
				end += 2
				continue
			}
			if line[end] == '"' {
				break
			}
			end++
		}
		value, err := strconv.Unquote(line[:end+1])
		if err != nil {
			t.Fatal(err)
		}
		if len(args) == 0 {
			if !strings.HasPrefix(value, ":") {
				t.Fatal("systemd environment substitution was not disabled")
			}
			value = strings.TrimPrefix(value, ":")
		}
		args = append(args, strings.ReplaceAll(value, "%%", "%"))
		line = strings.TrimSpace(line[end+1:])
	}
	return args
}

func TestNativeStatusAdapters(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix adapter fixture")
	}
	p := fixture(t, "linux")
	for _, output := range []string{"", "LoadState=masked\n", "LoadState=loaded\nActiveState=failed\nFragmentPath=/foreign\nResult=exit-code\nExecMainStartTimestampMonotonic=2\nExecMainStatus=203\n"} {
		o, err := inspectNative(p, func(context.Context, Invocation) (ProcessResult, error) { return ProcessResult{Output: output}, nil })
		if output == "" || strings.Contains(output, "masked") {
			if !errors.Is(err, ErrUnavailable) {
				t.Fatal("unknown native format was trusted")
			}
			continue
		}
		if err != nil || !o.ResultKnown || o.LastExit != 203 || o.Healthy || o.owned {
			t.Fatalf("pre-launch failure: %+v %v", o, err)
		}
	}
	wp, err := Preview("windows", Spec{Executable: `C:\owned\brain.exe`, Config: `C:\owned\config.json`, State: `C:\owned\state`, Home: `C:\owned`, Hour: 3})
	if err != nil {
		t.Fatal(err)
	}
	for _, last := range []int{0, 3, 2147942402} {
		out, _ := json.Marshal(map[string]any{"exists": true, "enabled": true, "result_known": true, "last_exit": last, "xml": wp.Artifacts[0].Data})
		o, err := inspectNative(wp, func(context.Context, Invocation) (ProcessResult, error) {
			return ProcessResult{Output: string(out)}, nil
		})
		if err != nil || o.LastExit != int64(last) || o.Healthy != (last == 0 || last == 3) {
			t.Fatalf("Windows result: %+v %v", o, err)
		}
	}
	for _, output := range []string{`{"exists":false}`, `{}`, `{"exists":true,"xml":"bad"}`, `not json`} {
		o, err := inspectNative(wp, func(context.Context, Invocation) (ProcessResult, error) { return ProcessResult{Output: output}, nil })
		if output == `{"exists":false}` {
			if err != nil || o.Registered {
				t.Fatal("missing task misreported")
			}
		} else if err == nil {
			t.Fatal("invalid task status accepted")
		}
	}
}

func TestScheduleNeverRunAndLaunchFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix adapter fixture")
	}
	p := fixture(t, "darwin")
	for _, detail := range []string{"runs = 0\nlast exit code = (never exited)\n", "runs = 0\nlast exit code = 0\n", "runs = 0\nlast spawn error = 2\n"} {
		o, err := inspectNative(p, func(_ context.Context, call Invocation) (ProcessResult, error) {
			if call.Args[0] == "print-disabled" {
				return ProcessResult{Output: "disabled services = {\n}\n"}, nil
			}
			return ProcessResult{Output: "path = " + p.Artifacts[0].Path + "\nstate = not running\n" + macLoadedCommand(p) + macLoadedCalendar(p) + detail}, nil
		})
		failure := strings.Contains(detail, "spawn error")
		if err != nil || o.ResultKnown != failure || o.Healthy == failure {
			t.Fatalf("launchd observation: %+v %v", o, err)
		}
	}
	wp, _ := Preview("windows", Spec{Executable: `C:\owned\brain.exe`, Config: `C:\owned\config.json`, State: `C:\owned\state`, Home: `C:\owned`, Hour: 3})
	for _, last := range []int{267011, 2147942402} {
		out, _ := json.Marshal(map[string]any{"exists": true, "enabled": true, "result_known": false, "last_exit": last, "xml": wp.Artifacts[0].Data})
		o, err := inspectNative(wp, func(context.Context, Invocation) (ProcessResult, error) {
			return ProcessResult{Output: string(out)}, nil
		})
		if err != nil || o.ResultKnown != (last == 2147942402) || o.Healthy != (last == 267011) {
			t.Fatalf("Windows never-started task: %+v %v", o, err)
		}
	}
}

func TestScheduleChangedScopeAfterFailedInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix adapter fixture")
	}
	p := fixture(t, "linux")
	a := &adapter{t: t, p: p}
	if _, err := Install(p, a.run); err != nil {
		t.Fatal(err)
	}
	a.fail = "unavailable"
	for i := 0; i < 3; i++ {
		p.Spec.Config += ".next"
		p.Spec.Minute++
		p, _ = Preview("linux", p.Spec)
		a.p = p
		if _, err := Install(p, a.run); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("failed scope replacement %d: %v", i, err)
		}
	}
	a.fail = ""
	if changed, err := Install(p, a.run); !changed || err != nil {
		t.Fatalf("scope repair: %v %v", changed, err)
	}
}
