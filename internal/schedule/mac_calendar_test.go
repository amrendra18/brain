package schedule

import (
	"context"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestMacCalendarMatches(t *testing.T) {
	p, err := Preview("darwin", Spec{Executable: "/owned/brain", Config: "/owned/config", State: "/owned/state", Home: "/owned", Hour: 3})
	if err != nil {
		t.Fatal(err)
	}
	calendar := macLoadedCalendar(p)
	for _, tt := range []struct {
		name string
		text string
		want bool
	}{
		{"native-daily", "job = {\n" + calendar + "\n\tevent channels = {\n\t\t\"com.apple.launchd.calendarinterval\" = {\n\t\t\tactive = 0\n\t\t}\n\t}\n}\n", true},
		{"no-optional-metadata", strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(calendar, "\t\t\tkeepalive = 0\n", ""), "\t\t\tservice = "+p.ID+"\n", ""), "\t\t\tmonitor = com.apple.UserEventAgent-Aqua\n", ""), true},
		{"missing", "", false},
		{"missing-hour", strings.ReplaceAll(calendar, "\t\t\t\t\"Hour\" => 3\n", ""), false},
		{"missing-minute", strings.ReplaceAll(calendar, "\t\t\t\t\"Minute\" => 0\n", ""), false},
		{"different-hour", strings.ReplaceAll(calendar, `"Hour" => 3`, `"Hour" => 22`), false},
		{"different-minute", strings.ReplaceAll(calendar, `"Minute" => 0`, `"Minute" => 48`), false},
		{"weekday", strings.ReplaceAll(calendar, `"Hour" => 3`, "\"Hour\" => 3\n\t\t\t\t\"Weekday\" => 1"), false},
		{"month", strings.ReplaceAll(calendar, `"Hour" => 3`, "\"Hour\" => 3\n\t\t\t\t\"Month\" => 2"), false},
		{"day", strings.ReplaceAll(calendar, `"Hour" => 3`, "\"Hour\" => 3\n\t\t\t\t\"Day\" => 3"), false},
		{"duplicate-hour", strings.ReplaceAll(calendar, `"Hour" => 3`, "\"Hour\" => 3\n\t\t\t\t\"Hour\" => 3"), false},
		{"invalid-number", strings.ReplaceAll(calendar, `"Hour" => 3`, `"Hour" => invalid`), false},
		{"invalid-field", strings.ReplaceAll(calendar, `"Hour" => 3`, `"Hour" = 3`), false},
		{"duplicate-block", calendar + calendar, false},
		{"nested-block", "event triggers = {\n" + calendar + "}\n", false},
		{"duplicate-trigger", strings.ReplaceAll(calendar, "\t}\n", "\t\tother => {\n\t\t}\n\t}\n"), false},
		{"duplicate-stream", strings.ReplaceAll(calendar, "stream = com.apple.launchd.calendarinterval", "stream = com.apple.launchd.calendarinterval\n\t\t\tstream = com.apple.launchd.calendarinterval"), false},
		{"duplicate-descriptor", strings.ReplaceAll(calendar, "\t\t}\n", "\t\t\tdescriptor = {\n\t\t\t}\n\t\t}\n"), false},
		{"other-stream", strings.ReplaceAll(calendar, "stream = com.apple.launchd.calendarinterval", "stream = other"), false},
		{"missing-stream", strings.ReplaceAll(calendar, "\t\t\tstream = com.apple.launchd.calendarinterval\n", ""), false},
		{"keepalive", strings.ReplaceAll(calendar, "keepalive = 0", "keepalive = 1"), false},
		{"unknown-property", strings.ReplaceAll(calendar, "keepalive = 0", "interval = 3600"), false},
		{"malformed-property", strings.ReplaceAll(calendar, "keepalive = 0", "keepalive => 0"), false},
		{"missing-trigger", "event triggers = {\n}\n", false},
		{"malformed-trigger", "event triggers = {\nunknown = 3\n}\n", false},
		{"unclosed", strings.TrimSuffix(calendar, "\t}\n"), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := macCalendarMatches(p, tt.text); got != tt.want {
				t.Fatalf("calendar matches = %v, want %v\n%s", got, tt.want, tt.text)
			}
		})
	}
	p.Spec.Hour = 0
	if macCalendarMatches(p, "event triggers = {\njob => {\nstream = com.apple.launchd.calendarinterval\ndescriptor = {\n\"Minute\" => 0\n}\n}\n}\n") {
		t.Fatal("missing midnight hour was treated as zero")
	}
}

func TestLoadedMacCalendarDrift(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix adapter fixture")
	}
	for _, kind := range []string{"matching", "different", "missing", "duplicate", "weekday"} {
		t.Run(kind, func(t *testing.T) {
			p := fixture(t, "darwin")
			a := &adapter{t: t, p: p}
			if _, err := Install(p, a.run); err != nil {
				t.Fatal(err)
			}
			runner := func(ctx context.Context, call Invocation) (ProcessResult, error) {
				result, err := a.run(ctx, call)
				if reflect.DeepEqual(call, p.Queries[0]) && result.ExitCode == 0 {
					calendar := macLoadedCalendar(p)
					replacement := calendar
					switch kind {
					case "different":
						loaded := p
						loaded.Spec.Hour, loaded.Spec.Minute = 22, 48
						replacement = macLoadedCalendar(loaded)
					case "missing":
						replacement = ""
					case "duplicate":
						replacement += calendar
					case "weekday":
						replacement = strings.ReplaceAll(calendar, `"Hour" => 3`, "\"Hour\" => 3\n\t\t\t\t\"Weekday\" => 1")
					}
					result.Output = strings.Replace(result.Output, calendar, replacement, 1)
				}
				return result, err
			}
			status, err := Inspect(p, runner)
			if err != nil || status.Healthy != (kind == "matching") {
				t.Fatalf("loaded calendar %s: %+v %v", kind, status, err)
			}
			if kind != "matching" && status.Problem != "schedule-definition-differs" {
				t.Fatalf("loaded calendar %s has wrong problem: %+v", kind, status)
			}
			changed, err := Install(p, runner)
			if kind == "matching" {
				if changed || err != nil {
					t.Fatalf("matching calendar changed: %v %v", changed, err)
				}
			} else if !changed && err == nil {
				t.Fatal("different loaded calendar returned an identical no-op")
			}
		})
	}
}
