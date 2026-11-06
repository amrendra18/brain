package schedule

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

func macCommandMatches(p Plan, text string) (bool, error) {
	var program string
	var args []string
	programs, blocks := 0, 0
	inside := false
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimLeft(raw, " \t")
		if inside {
			if strings.TrimSpace(line) == "}" {
				inside = false
				continue
			}
			args = append(args, line)
			continue
		}
		if value, ok := strings.CutPrefix(line, "program = "); ok {
			program = value
			programs++
		}
		if strings.TrimSpace(line) == "arguments = {" {
			inside = true
			blocks++
		}
	}
	if inside || programs != 1 || blocks != 1 || len(args) == 0 {
		return false, ErrUnavailable
	}
	return program == p.Spec.Executable && reflect.DeepEqual(args, p.Arguments), nil
}

func linuxTimerMatches(p Plan, text string, values map[string]string) bool {
	calendar := []string{}
	for _, line := range strings.Split(text, "\n") {
		if value, ok := strings.CutPrefix(line, "TimersCalendar="); ok {
			calendar = append(calendar, value)
		}
	}
	prefix := fmt.Sprintf("{ OnCalendar=*-*-* %02d:%02d:00 ; next_elapse=", p.Spec.Hour, p.Spec.Minute)
	return values["Unit"] == p.ID+".service" && values["Persistent"] == "yes" && values["TimersMonotonic"] == "" && len(calendar) == 1 && strings.HasPrefix(calendar[0], prefix) && strings.HasSuffix(calendar[0], " }")
}

func linuxCommandMatches(p Plan, runner Runner) (bool, error) {
	result, err := run(runner, p.Queries[2])
	if err != nil || result.ExitCode != 0 {
		return false, ErrUnavailable
	}
	var reply struct {
		Type string              `json:"type"`
		Data [][]json.RawMessage `json:"data"`
	}
	if json.Unmarshal([]byte(result.Output), &reply) != nil || reply.Type != "a(sasasttttuii)" {
		return false, ErrUnavailable
	}
	if len(reply.Data) != 1 {
		return false, nil
	}
	command := reply.Data[0]
	if len(command) != 10 {
		return false, ErrUnavailable
	}
	var executable string
	var argv, flags []string
	if json.Unmarshal(command[0], &executable) != nil || json.Unmarshal(command[1], &argv) != nil || json.Unmarshal(command[2], &flags) != nil {
		return false, ErrUnavailable
	}
	return executable == p.Spec.Executable && reflect.DeepEqual(argv, p.Arguments) && reflect.DeepEqual(flags, []string{"no-env-expand"}), nil
}
