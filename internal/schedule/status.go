package schedule

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"reflect"
	"runtime"
	"strconv"
	"strings"
)

type Status struct {
	Registered   bool   `json:"registered"`
	Enabled      bool   `json:"enabled"`
	Healthy      bool   `json:"healthy"`
	Running      bool   `json:"running"`
	ResultKnown  bool   `json:"result_known"`
	LastExit     int64  `json:"last_exit"`
	Problem      string `json:"problem,omitempty"`
	Installation string `json:"installation"`
}

type nativeUnits uint8

const (
	timerUnit nativeUnits = 1 << iota
	serviceUnit
)

type observation struct {
	Status
	owned   bool
	matches bool
	units   nativeUnits
}

func validate(p Plan) error {
	want, err := Preview(p.System, p.Spec)
	if err != nil || !reflect.DeepEqual(want, p) {
		return ErrInvalid
	}
	return nil
}

// Inspect reads native status without changing files or starting the job.
func Inspect(p Plan, runner Runner) (Status, error) {
	if err := validate(p); err != nil {
		return Status{}, err
	}
	if p.System != runtime.GOOS && runner == nil {
		return Status{}, ErrUnsupported
	}
	receipt, err := readReceipt(p)
	if err != nil {
		return Status{Problem: "schedule-files-unsafe"}, err
	}
	observed, err := inspectNative(p, runner)
	observed.Installation = string(receipt.Outcome)
	if observed.Installation == "" {
		observed.Installation = "absent"
	}
	if err != nil {
		observed.Problem = "schedule-unavailable"
		return observed.Status, err
	}
	if observed.Registered && (!observed.owned || receipt.Outcome == "") {
		observed.Healthy = false
		observed.Problem = "schedule-collision"
		return observed.Status, ErrCollision
	}
	artifacts, filesErr := checkArtifacts(p, receipt)
	if filesErr != nil {
		observed.Healthy = false
		observed.Problem = failureCode(filesErr)
		if receipt.Outcome == removing {
			observed.Problem = "schedule-removal-incomplete"
		} else if receipt.Outcome == "prepared" || receipt.Outcome == "failed" {
			observed.Problem = "schedule-install-incomplete"
		}
		return observed.Status, filesErr
	}
	if !artifacts.Identical {
		observed.Healthy = false
		observed.Problem = "schedule-files-differ"
	}
	if receipt.Outcome == removing {
		observed.Healthy = false
		observed.Problem = "schedule-removal-incomplete"
	} else if receipt.Outcome == "prepared" || receipt.Outcome == "failed" {
		observed.Healthy = false
		observed.Problem = "schedule-install-incomplete"
	}
	return observed.Status, nil
}

func inspectNative(p Plan, runner Runner) (observation, error) {
	var o observation
	var err error
	switch p.System {
	case "darwin":
		o, err = inspectMac(p, runner)
	case "linux":
		o, err = inspectLinux(p, runner)
	case "windows":
		o, err = inspectWindows(p, runner)
	}
	if err != nil {
		return o, err
	}
	o.Healthy = o.Registered && o.Enabled && o.owned && o.matches
	if !o.Registered {
		o.Problem = "schedule-missing"
	} else if !o.Enabled {
		o.Problem = "schedule-disabled"
	} else if o.owned && !o.matches {
		o.Problem = "schedule-definition-differs"
	}
	if o.ResultKnown && o.LastExit != 0 && o.LastExit != 3 {
		o.Healthy = false
		o.Problem = "schedule-run-failed"
	}
	return o, nil
}

func properties(text string, separator string) map[string]string {
	result := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), separator)
		if ok {
			result[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return result
}

func inspectMac(p Plan, runner Runner) (observation, error) {
	result, err := run(runner, p.Queries[0])
	if err != nil {
		return observation{}, err
	}
	if result.ExitCode == 113 {
		return observation{}, nil
	}
	if result.ExitCode != 0 {
		return observation{}, ErrUnavailable
	}
	values := properties(result.Output, " = ")
	if values["path"] == "" || values["state"] == "" {
		return observation{}, ErrUnavailable
	}
	o := observation{Status: Status{Registered: true, Enabled: true, Running: values["state"] == "running"}, owned: values["path"] == p.Artifacts[0].Path, matches: true}
	o.matches, err = macCommandMatches(p, result.Output)
	if err != nil {
		return o, err
	}
	o.matches = o.matches && macCalendarMatches(p, result.Output)
	disabled, err := run(runner, p.Queries[1])
	if err != nil || disabled.ExitCode != 0 || !strings.Contains(disabled.Output, "disabled services = {") {
		return observation{}, ErrUnavailable
	}
	for key, value := range properties(disabled.Output, " => ") {
		if strings.Trim(key, `"`) == p.ID {
			switch value {
			case "false", "enabled":
				o.Enabled = true
			case "true", "disabled":
				o.Enabled = false
			default:
				return observation{}, ErrUnavailable
			}
		}
	}
	if value, ok := values["last exit code"]; ok && value != "(never exited)" {
		o.LastExit, err = strconv.ParseInt(value, 10, 64)
		if err != nil {
			return observation{}, ErrUnavailable
		}
		o.ResultKnown = values["runs"] != "0"
	}
	if values["last spawn error"] != "" || values["last terminating signal"] != "" {
		o.ResultKnown = true
		o.LastExit = -1
	}
	return o, nil
}

func inspectLinux(p Plan, runner Runner) (observation, error) {
	o := observation{owned: true, matches: true}
	loaded := 0
	for i, call := range p.Queries[:2] {
		result, err := run(runner, call)
		if err != nil {
			return o, ErrUnavailable
		}
		values := properties(result.Output, "=")
		if values["LoadState"] == "not-found" {
			continue
		}
		if result.ExitCode != 0 || values["LoadState"] != "loaded" || values["ActiveState"] == "" || values["FragmentPath"] == "" {
			return o, ErrUnavailable
		}
		artifact := p.Artifacts[1-i]
		loaded++
		o.Registered = true
		o.owned = o.owned && values["FragmentPath"] == artifact.Path && values["DropInPaths"] == ""
		if i == 0 {
			o.units |= timerUnit
			o.Enabled = values["UnitFileState"] == "enabled" && values["ActiveState"] == "active"
			o.matches = linuxTimerMatches(p, result.Output, values)
			continue
		}
		o.units |= serviceUnit
		o.Running = values["ActiveState"] == "activating" || values["ActiveState"] == "active"
		if start := values["ExecMainStartTimestampMonotonic"]; start != "" && start != "0" {
			o.LastExit, err = strconv.ParseInt(values["ExecMainStatus"], 10, 64)
			if err != nil {
				return o, ErrUnavailable
			}
			o.ResultKnown = true
		}
		if result := values["Result"]; result != "" && result != "success" {
			o.ResultKnown = true
			if o.LastExit == 0 || o.LastExit == 3 {
				o.LastExit = -1
			}
		}
	}
	if loaded != 2 {
		o.Enabled = false
	}
	if o.units&serviceUnit != 0 && o.owned {
		matches, err := linuxCommandMatches(p, runner)
		if err != nil {
			return o, err
		}
		o.matches = o.matches && matches
	}
	return o, nil
}

func inspectWindows(p Plan, runner Runner) (observation, error) {
	result, err := run(runner, p.Queries[0])
	if err != nil || result.ExitCode != 0 {
		return observation{}, ErrUnavailable
	}
	var reply struct {
		Exists      *bool  `json:"exists"`
		Enabled     bool   `json:"enabled"`
		Running     bool   `json:"running"`
		ResultKnown bool   `json:"result_known"`
		LastExit    int64  `json:"last_exit"`
		XML         string `json:"xml"`
	}
	if json.Unmarshal([]byte(strings.TrimPrefix(result.Output, "\ufeff")), &reply) != nil || reply.Exists == nil {
		return observation{}, ErrUnavailable
	}
	if !*reply.Exists {
		return observation{}, nil
	}
	var task windowsDefinition
	if xml.Unmarshal([]byte(reply.XML), &task) != nil {
		return observation{}, ErrUnavailable
	}
	var expected windowsDefinition
	if xml.Unmarshal([]byte(p.Artifacts[0].Data), &expected) != nil {
		return observation{}, ErrInvalid
	}
	known := !reply.Running && (reply.ResultKnown || reply.LastExit != 0)
	switch reply.LastExit {
	case 0x41300, 0x41301, 0x41303, 0x41325:
		known = false
	}
	return observation{Status: Status{Registered: true, Enabled: reply.Enabled, Running: reply.Running, ResultKnown: known, LastExit: reply.LastExit}, owned: task.Description == p.ID, matches: task.matches(expected)}, nil
}

func failureCode(err error) string {
	switch {
	case errors.Is(err, ErrUnavailable):
		return "schedule-unavailable"
	case errors.Is(err, ErrCollision):
		return "schedule-collision"
	case errors.Is(err, ErrActivation):
		return "schedule-activation-failed"
	default:
		return "schedule-files-unsafe"
	}
}
