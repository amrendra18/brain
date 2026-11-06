package schedule

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
)

func TestLoadedMacJobDrift(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix adapter fixture")
	}
	for _, kind := range []string{"executable", "config", "missing", "duplicate"} {
		t.Run(kind, func(t *testing.T) {
			p := fixture(t, "darwin")
			a := &adapter{t: t, p: p}
			if _, err := Install(p, a.run); err != nil {
				t.Fatal(err)
			}
			wrong := func(ctx context.Context, call Invocation) (ProcessResult, error) {
				r, err := a.run(ctx, call)
				if call.Args[0] != "print" || r.ExitCode != 0 {
					return r, err
				}
				start := strings.Index(r.Output, "program = ")
				if start >= 0 {
					r.Output = r.Output[:start]
				}
				loaded := p
				loaded.Arguments = append([]string(nil), p.Arguments...)
				switch kind {
				case "executable":
					loaded.Spec.Executable = "/wrong/brain"
					loaded.Arguments[0] = loaded.Spec.Executable
				case "config":
					loaded.Arguments[3] = "/wrong/config.json"
				case "missing":
					return r, err
				}
				r.Output += macLoadedCommand(loaded)
				if kind == "duplicate" {
					r.Output += macLoadedCommand(p)
				}
				return r, err
			}
			status, err := Inspect(p, wrong)
			if err == nil && status.Healthy {
				t.Fatalf("wrong loaded job reported healthy: %+v", status)
			}
			if changed, err := Install(p, wrong); !changed && err == nil {
				t.Fatal("wrong loaded job returned no change")
			}
		})
	}
}

func TestLoadedLinuxJobDrift(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix adapter fixture")
	}
	for _, kind := range []string{"executable", "config", "time", "persistent", "unit", "extra-calendar", "extra-command", "flags", "missing-busctl"} {
		t.Run(kind, func(t *testing.T) {
			p := fixture(t, "linux")
			a := &adapter{t: t, p: p}
			if _, err := Install(p, a.run); err != nil {
				t.Fatal(err)
			}
			wrong := func(ctx context.Context, call Invocation) (ProcessResult, error) {
				r, err := a.run(ctx, call)
				if call.Executable == "busctl" {
					if kind == "missing-busctl" {
						return ProcessResult{}, errors.New("missing adapter")
					}
					executable := p.Spec.Executable
					argv := append([]string(nil), p.Arguments...)
					flags := []string{"no-env-expand"}
					if kind == "executable" {
						executable = "/wrong/brain"
						argv[0] = executable
					}
					if kind == "config" {
						argv[3] = "/wrong/config.json"
					}
					if kind == "flags" {
						flags = nil
					}
					commands := [][]any{{executable, argv, flags, 0, 0, 0, 0, 0, 0, 0}}
					if kind == "extra-command" {
						commands = append(commands, commands[0])
					}
					data, _ := json.Marshal(map[string]any{"type": "a(sasasttttuii)", "data": commands})
					r.Output = string(data)
				} else if call.Executable == "systemctl" && len(call.Args) > 2 && call.Args[1] == "show" {
					if strings.HasSuffix(call.Args[2], ".service") && (kind == "executable" || kind == "config") {
						r.Output += "ExecStartEx={ path=/wrong/brain ; argv[]=/wrong/brain index --config /wrong/config.json ; flags=no-env-expand }\n"
					}
					if strings.HasSuffix(call.Args[2], ".timer") {
						switch kind {
						case "time":
							r.Output = strings.ReplaceAll(r.Output, "03:00:00", "04:00:00")
							r.Output += "TimersCalendar={ OnCalendar=*-*-* 04:00:00 ; next_elapse=n/a }\n"
						case "persistent":
							r.Output = strings.ReplaceAll(r.Output, "Persistent=yes", "Persistent=no")
						case "unit":
							r.Output = strings.ReplaceAll(r.Output, "Unit="+p.ID+".service", "Unit=wrong.service")
						case "extra-calendar":
							r.Output += "TimersCalendar={ OnCalendar=*-*-* 04:00:00 ; next_elapse=n/a }\n"
						}
					}
				}
				return r, err
			}
			status, err := Inspect(p, wrong)
			if err == nil && status.Healthy {
				t.Fatalf("wrong loaded job reported healthy: %+v", status)
			}
			if changed, err := Install(p, wrong); !changed && err == nil {
				t.Fatal("wrong loaded job returned no change")
			}
		})
	}
}

func TestLoadedCommandArgumentsRemainSeparate(t *testing.T) {
	root := `/owned space/é'"$cash%value;`
	spec := Spec{Executable: root + "/brain", Config: root + "/selected configuration.json ", State: root + "/state", Home: root, Hour: 3}
	for _, system := range []string{"darwin", "linux"} {
		p, err := Preview(system, spec)
		if err != nil {
			t.Fatal(err)
		}
		if system == "darwin" {
			if matches, err := macCommandMatches(p, macLoadedCommand(p)); err != nil || !matches {
				t.Fatal("literal launchd arguments changed", err)
			}
			continue
		}
		argv := append([]string(nil), p.Arguments...)
		for _, split := range []bool{false, true} {
			if split {
				argv = append(append(append([]string{}, argv[:3]...), strings.Split(argv[3], " ")...), argv[4:]...)
			}
			data, _ := json.Marshal(map[string]any{"type": "a(sasasttttuii)", "data": [][]any{{p.Spec.Executable, argv, []string{"no-env-expand"}, 0, 0, 0, 0, 0, 0, 0}}})
			matches, err := linuxCommandMatches(p, func(context.Context, Invocation) (ProcessResult, error) {
				return ProcessResult{Output: string(data)}, nil
			})
			if err != nil || matches == split {
				t.Fatalf("split=%v loaded argv matched=%v err=%v", split, matches, err)
			}
		}
	}
}

func TestLoadedDefinitionMissingAndMalformed(t *testing.T) {
	p, _ := Preview("linux", Spec{Executable: "/owned/brain", Config: "/owned/config", State: "/owned/state", Home: "/owned", Hour: 3})
	for _, data := range []string{"", `{}`, `{"type":"a(sasasttttuii)","data":[[]]}`, `{"type":"a(sasasttttuii)","data":[[12,[],[],0,0,0,0,0,0,0]]}`} {
		if _, err := linuxCommandMatches(p, func(context.Context, Invocation) (ProcessResult, error) { return ProcessResult{Output: data}, nil }); !errors.Is(err, ErrUnavailable) {
			t.Fatal("unknown loaded command trusted", err)
		}
	}
	for _, text := range []string{"", "program = /owned/brain\narguments = {\n", "arguments = {\n/owned/brain\n}\n"} {
		if _, err := macCommandMatches(p, text); !errors.Is(err, ErrUnavailable) {
			t.Fatal("incomplete launchd command trusted", err)
		}
	}
	wrongTimer := fmt.Sprintf("Unit=%s.service\nPersistent=yes\nTimersMonotonic={ OnBootUSec=1min ; next_elapse=1min }\nTimersCalendar={ OnCalendar=*-*-* 03:00:00 ; next_elapse=n/a }\n", p.ID)
	if linuxTimerMatches(p, wrongTimer, properties(wrongTimer, "=")) {
		t.Fatal("additional monotonic trigger trusted")
	}
}
