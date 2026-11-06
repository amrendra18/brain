package cli

import (
	"brain/internal/corpus"
	"brain/internal/index"
	"brain/internal/platform"
	"brain/internal/schedule"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

func (call *call) schedule() int {
	if len(call.words) != 0 {
		if len(call.words) != 1 || call.words[0] != "status" {
			return call.usage("unexpected-argument", "Use schedule or schedule status.")
		}
		if _, present := call.flags["--time"]; present || call.flags["--write"] != "" {
			return call.usage("invalid-option", "schedule status does not accept --write or --time.")
		}
		return call.scheduleStatus(nil)
	}
	value, provided := call.flags["--time"]
	if !provided {
		value = "01:00"
	}
	parts := strings.Split(value, ":")
	if len(value) != 5 || len(parts) != 2 || len(parts[0]) != 2 || len(parts[1]) != 2 {
		return call.usage("invalid-value", "Use --time HH:MM from 00:00 to 23:59 in local time.")
	}
	hour, first := strconv.Atoi(parts[0])
	minute, second := strconv.Atoi(parts[1])
	if first != nil || second != nil || parts[0][0] < '0' || parts[0][0] > '9' || parts[1][0] < '0' || parts[1][0] > '9' || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return call.usage("invalid-value", "Use --time HH:MM from 00:00 to 23:59 in local time.")
	}
	fail := func() int {
		return call.failure("schedule-unavailable", "The schedule could not be prepared or activated. Review its preview and local scheduler access.", 1, []string{"schedule", "--time", value}, []string{"doctor"})
	}
	if _, err := corpus.LoadFile(call.config, call.state); err != nil {
		return call.configError(err)
	}
	config, err := platform.ConfigPath(call.config)
	if err != nil {
		return fail()
	}
	executable, err := os.Executable()
	if err != nil {
		return fail()
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return fail()
	}
	state, err := filepath.EvalSymlinks(call.state)
	if err != nil {
		return fail()
	}
	if err := index.CheckSchedulePrivacy(state, call.policy); err != nil {
		return call.privacyFailure(err)
	}
	saved, exists, err := schedule.Current(state)
	if err != nil {
		return fail()
	}
	if !provided && exists {
		hour, minute = saved.Spec.Hour, saved.Spec.Minute
		value = fmt.Sprintf("%02d:%02d", hour, minute)
	}
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return fail()
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		return fail()
	}
	plan, err := schedule.Preview(runtime.GOOS, schedule.Spec{Executable: executable, Config: config, State: state, Home: home, Hour: hour, Minute: minute})
	if err != nil {
		return fail()
	}
	if err := schedule.CheckPrivacy(plan, call.policy); err != nil {
		return call.privacyFailure(index.ErrPrivacyPath)
	}
	if err := index.CheckSchedulePrivacy(state, call.policy); err != nil {
		return call.privacyFailure(err)
	}
	reply := struct {
		Schedule schedule.Plan    `json:"schedule"`
		Written  bool             `json:"written"`
		Changed  bool             `json:"changed"`
		Status   *schedule.Status `json:"status,omitempty"`
	}{Schedule: plan}
	code := 0
	if call.flags["--write"] == "true" {
		info, err := os.Stat(state)
		if err != nil || !info.IsDir() || info.Mode().Perm()&0222 == 0 {
			return call.unwritable()
		}
		changed, err := schedule.Install(plan, nil)
		if errors.Is(err, schedule.ErrActivation) {
			return call.scheduleActivation(value)
		}
		if err != nil {
			if errors.Is(err, schedule.ErrFiles) && platform.CheckPrivateState(state) == nil && !platform.Writable(state) {
				return call.unwritable()
			}
			return fail()
		}
		status, err := schedule.Inspect(plan, nil)
		if err != nil || !status.Healthy {
			return fail()
		}
		reply.Written, reply.Changed, reply.Status = changed, changed, &status
		if !changed {
			code = 3
		}
	}
	if call.flags["--format"] == "json" {
		if WriteJSON(call.output, reply) != 0 {
			return 1
		}
	} else {
		text := fmt.Sprintf("Daily index at %s local time. Written: %t.\nArguments: %s\n%s\n", value, reply.Written, platform.Command(call.shell, plan.Arguments), plan.CatchUp)
		for _, artifact := range plan.Artifacts {
			text += "\n" + artifact.Path + "\n" + artifact.Data + "\n"
		}
		if _, err = writeText(call.output, text); err != nil {
			return 1
		}
	}
	return code
}

func (call *call) scheduleActivation(value string) int {
	return call.failure("schedule-activation-failed", "The scheduler did not activate the daily job. Review its preview and scheduler access, then retry installation.", 1, []string{"schedule", "--time", value}, []string{"doctor"})
}

func (call *call) scheduleStatus(runner schedule.Runner) int {
	fail := func() int {
		return call.failure("schedule-unavailable", "The saved schedule or its native status could not be read. Check local scheduler access and the saved receipt.", 1, []string{"doctor"})
	}
	state := call.state
	if canonical, err := filepath.EvalSymlinks(state); err == nil {
		state = canonical
	} else if !errors.Is(err, os.ErrNotExist) {
		return fail()
	}
	if err := index.CheckSchedulePrivacy(state, call.policy); err != nil {
		return call.privacyFailure(err)
	}
	plan, exists, err := schedule.Current(state)
	if err != nil {
		return fail()
	}
	reply := struct {
		Schedule     *schedule.Plan   `json:"schedule"`
		Time         string           `json:"time,omitempty"`
		Status       *schedule.Status `json:"status"`
		LastIndexRun *index.Run       `json:"last_index_run"`
	}{}
	if exists {
		if err := schedule.CheckPrivacy(plan, call.policy); err != nil {
			return call.privacyFailure(index.ErrPrivacyPath)
		}
		reply.Schedule = &plan
		reply.Time = fmt.Sprintf("%02d:%02d", plan.Spec.Hour, plan.Spec.Minute)
		status, err := schedule.Inspect(plan, runner)
		if err != nil {
			return fail()
		}
		reply.Status = &status
	}
	last, present, err := index.LastRun(state)
	if err != nil {
		return fail()
	}
	if present {
		reply.LastIndexRun = &last
	}
	if call.flags["--format"] == "json" {
		return WriteJSON(call.output, reply)
	}
	text := "No saved daily schedule.\n"
	if exists {
		text = fmt.Sprintf("Saved daily time: %s local. Installation: %s. Registered: %t. Enabled: %t. Healthy: %t. Running: %t.\n", reply.Time, reply.Status.Installation, reply.Status.Registered, reply.Status.Enabled, reply.Status.Healthy, reply.Status.Running)
		if reply.Status.ResultKnown {
			text += fmt.Sprintf("Last native result: exit %d.\n", reply.Status.LastExit)
		} else {
			text += "Last native result: no completed run is known.\n"
		}
		if reply.Status.Problem != "" {
			text += "Schedule problem: " + reply.Status.Problem + ".\n"
		}
	}
	if present {
		text += fmt.Sprintf("Last index run (manual or scheduled): %s, started %s.\n", last.Outcome, last.StartedAt.Format("2006-01-02T15:04:05Z07:00"))
	}
	if _, err := writeText(call.output, text); err != nil {
		return 1
	}
	return 0
}
