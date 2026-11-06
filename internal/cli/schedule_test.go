package cli

import (
	"brain/internal/schedule"
	"bytes"
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

func TestSchedulePreviewNoWrites(t *testing.T) {
	_, folder, run := freshnessFixture(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PATH", t.TempDir())
	before, err := os.ReadDir(folder)
	if err != nil {
		t.Fatal(err)
	}
	code, out := run("schedule")
	var reply struct {
		Schedule         schedule.Plan
		Written, Changed bool
		Status           *schedule.Status
	}
	if code != 0 || json.Unmarshal([]byte(out), &reply) != nil || reply.Written || reply.Changed || reply.Status != nil || reply.Schedule.Spec.Hour != 1 {
		t.Fatal(code, out)
	}
	canonical, _ := filepath.EvalSymlinks(folder)
	if reply.Schedule.Spec.State != canonical || reply.Schedule.Spec.Config != filepath.Join(canonical, "config.json") || reply.Schedule.Arguments[1] != "index" {
		t.Fatal(reply)
	}
	after, err := os.ReadDir(folder)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal(before, after, err)
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatal(entries, err)
	}
	for _, value := range []string{"", "bad", "1:00", "00:1", "24:00", "23:60", "-1:00", "+1:00"} {
		if code, out := run("schedule", "--time", value); code != 2 {
			t.Fatal(value, code, out)
		}
	}
	if code, out := run("schedule", "extra"); code != 2 {
		t.Fatal(code, out)
	}
	if code, out := run("schedule", "--time", "00:00"); code != 0 || !strings.Contains(out, `"hour":0`) {
		t.Fatal(code, out)
	}
}

func TestScheduleWriteIdempotent(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS CLI adapter fixture; other native adapters have package tests")
	}
	awk, err := exec.LookPath("awk")
	if err != nil {
		t.Fatal(err)
	}
	_, state, run := freshnessFixture(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	control := filepath.Join(bin, "registered")
	log := filepath.Join(bin, "calls")
	t.Setenv("SCHEDULE_TEST_REGISTERED", control)
	t.Setenv("SCHEDULE_TEST_LOG", log)
	t.Setenv("SCHEDULE_TEST_AWK", awk)
	renderer := filepath.Join(bin, "loaded.awk")
	t.Setenv("SCHEDULE_TEST_RENDERER", renderer)
	if err := os.WriteFile(renderer, []byte(macLoadedArgumentsAWK), 0600); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
printf '%s\n' "$1" >> "$SCHEDULE_TEST_LOG"
case "$1" in
 print)
  if [ ! -f "$SCHEDULE_TEST_REGISTERED" ]; then exit 113; fi
  IFS= read -r artifact < "$SCHEDULE_TEST_REGISTERED"
  [ -n "$artifact" ] || exit 113
  printf 'path = %s\nstate = waiting\nruns = 1\nlast exit code = 3\n' "$artifact"
  "$SCHEDULE_TEST_AWK" -f "$SCHEDULE_TEST_RENDERER" "$SCHEDULE_TEST_REGISTERED.plist";;
 print-disabled) printf 'disabled services = {\n}\n';;
 bootstrap)
  if [ "$SCHEDULE_TEST_FAIL" = 1 ]; then exit 1; fi
  "$SCHEDULE_TEST_AWK" '{print}' "$3" > "$SCHEDULE_TEST_REGISTERED.plist" || exit 1
  printf '%s\n' "$3" > "$SCHEDULE_TEST_REGISTERED";;
 bootout) : > "$SCHEDULE_TEST_REGISTERED";;
 enable) exit 0;;
 *) exit 99;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "launchctl"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if code, out := run("schedule", "--write", "--time", "04:17"); code != 0 || !strings.Contains(out, `"written":true`) || !strings.Contains(out, `"healthy":true`) {
		t.Fatal(code, out)
	}
	before := scheduleFiles(t, state, home)
	if code, out := run("schedule", "--write"); code != 3 || !strings.Contains(out, `"written":false`) || !strings.Contains(out, `"changed":false`) {
		t.Fatal(code, out)
	}
	if after := scheduleFiles(t, state, home); !reflect.DeepEqual(before, after) {
		t.Fatal("unchanged schedule modified state or home", before, after)
	}
	if err := os.Chmod(state, 0500); err != nil {
		t.Fatal(err)
	}
	code, out := run("schedule", "--write", "--time", "04:17")
	if err := os.Chmod(state, 0700); err != nil {
		t.Fatal(err)
	}
	if code != 1 || !strings.Contains(out, "state-unwritable") {
		t.Fatal(code, out)
	}
	if code, out := run("doctor"); code != 1 || !strings.Contains(out, "index-missing") {
		t.Fatal(code, out)
	}
	if code, out := run("index"); code != 0 {
		t.Fatal(code, out)
	}
	if code, out := run("doctor"); code != 3 {
		t.Fatal(code, out)
	}
	t.Setenv("SCHEDULE_TEST_FAIL", "1")
	if code, out := run("schedule", "--write", "--time", "05:17"); code != 1 || !strings.Contains(out, "schedule-activation-failed") {
		t.Fatal(code, out)
	}
	if code, out := run("doctor"); code != 1 || !strings.Contains(out, "schedule-failed") {
		t.Fatal(code, out)
	}
	t.Setenv("SCHEDULE_TEST_FAIL", "0")
	if code, out := run("schedule", "--write", "--time", "05:17"); code != 0 {
		t.Fatal(code, out)
	}
	if code, out := run("doctor"); code != 3 {
		t.Fatal(code, out)
	}
	bytes, err := os.ReadFile(log)
	if err != nil || strings.Count(string(bytes), "bootstrap") != 3 {
		t.Fatal(string(bytes), err)
	}
	canonical, err := filepath.EvalSymlinks(state)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(canonical, 0700) })
	destination := canonical + "-moved"
	t.Cleanup(func() { _ = os.RemoveAll(destination) })
	for _, args := range [][]string{
		{"settings", "--write", "--state-dir", canonical},
		{"settings", "relocate", destination, "--write", "--state-dir", canonical},
		{"schedule", "status", "--state-dir", destination},
	} {
		var output strings.Builder
		if code := Run(append(args, "--format", "json"), &output); code != 0 {
			t.Fatal(args, code, output.String())
		}
		if args[0] == "schedule" && !strings.Contains(output.String(), `"time":"05:17"`) {
			t.Fatal(output.String())
		}
	}
}

func TestScheduleTextAndUnavailable(t *testing.T) {
	_, folder, run := freshnessFixture(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PATH", t.TempDir())
	var output bytes.Buffer
	if code := Run([]string{"schedule"}, &output); code != 0 || !strings.Contains(output.String(), "Daily index at 01:00") {
		t.Fatal(code, output.String())
	}
	if code := Run([]string{"schedule"}, closedWriter{}); code != 1 {
		t.Fatal(code)
	}
	if code := Run([]string{"schedule", "--format", "json"}, closedWriter{}); code != 1 {
		t.Fatal(code)
	}
	if err := os.WriteFile(filepath.Join(folder, "config.json"), []byte("bad JSON"), 0600); err != nil {
		t.Fatal(err)
	}
	if code, out := run("schedule"); code != 1 || !strings.Contains(out, "config-invalid") {
		t.Fatal(code, out)
	}
}

func TestDoctorFirstScheduleFailure(t *testing.T) {
	_, _, run := freshnessFixture(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PATH", t.TempDir())
	if code, out := run("schedule", "--write"); code != 1 || !strings.Contains(out, "schedule-unavailable") {
		t.Fatal(code, out)
	}
	if code, out := run("doctor"); code != 1 || !strings.Contains(out, "schedule-failed") || !strings.Contains(out, "index-missing") {
		t.Fatal(code, out)
	}
}

func scheduleFiles(t *testing.T, roots ...string) map[string]string {
	t.Helper()
	files := map[string]string{}
	for _, root := range roots {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			var data []byte
			if !info.IsDir() {
				data, err = os.ReadFile(path)
				if err != nil {
					return err
				}
			}
			files[path] = fmt.Sprintf("%s %d %q", info.Mode(), info.ModTime().UnixNano(), data)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return files
}

const macLoadedArgumentsAWK = `
function replace(s, old, value,    result, at) {
 while ((at=index(s,old)) > 0) { result=result substr(s,1,at-1) value; s=substr(s,at+length(old)) }
 return result s
}
function xml(s) {
 s=replace(s,"&lt;","<"); s=replace(s,"&gt;",">")
 s=replace(s,"&quot;","\""); s=replace(s,"&#34;","\"")
 s=replace(s,"&apos;",sprintf("%c",39)); s=replace(s,"&#39;",sprintf("%c",39))
 return replace(s,"&amp;","&")
}
/<key>ProgramArguments<\/key>/ {
 s=$0; sub(/^.*<key>ProgramArguments<\/key><array>/,"",s); sub(/<\/array>.*$/,"",s)
 while(match(s,/<string>[^<]*<\/string>/)) { args[++n]=xml(substr(s,RSTART+8,RLENGTH-17)); s=substr(s,RSTART+RLENGTH) }
}
/<key>StartCalendarInterval<\/key>/ {
 h=$0; sub(/^.*<key>Hour<\/key><integer>/,"",h); sub(/<\/integer>.*$/,"",h)
 m=$0; sub(/^.*<key>Minute<\/key><integer>/,"",m); sub(/<\/integer>.*$/,"",m)
}
END { if(n==0) exit 8; printf "program = %s\narguments = {\n",args[1]; for(i=1;i<=n;i++) printf "\t%s\n",args[i]; print "}"; printf "event triggers = {\ncalendar => {\nstream = com.apple.launchd.calendarinterval\ndescriptor = {\n\"Minute\" => %s\n\"Hour\" => %s\n}\n}\n}\n",m,h }
`
