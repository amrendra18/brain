package schedule

import (
	"encoding/xml"
	"reflect"
	"strings"
	"time"
)

type windowsDefinition struct {
	Description string `xml:"RegistrationInfo>Description"`
	Actions     struct {
		Items []windowsAction `xml:",any"`
	} `xml:"Actions"`
	Triggers struct {
		Items []windowsTrigger `xml:",any"`
	} `xml:"Triggers"`
	Principals struct {
		Items []windowsPrincipal `xml:",any"`
	} `xml:"Principals"`
	Settings struct {
		Instances     string `xml:"MultipleInstancesPolicy"`
		NoBattery     bool   `xml:"DisallowStartIfOnBatteries"`
		StopOnBattery bool   `xml:"StopIfGoingOnBatteries"`
		CatchUp       bool   `xml:"StartWhenAvailable"`
		Enabled       bool   `xml:"Enabled"`
		Limit         string `xml:"ExecutionTimeLimit"`
	} `xml:"Settings"`
}

type windowsAction struct {
	XMLName   xml.Name
	Command   string     `xml:"Command"`
	Arguments string     `xml:"Arguments"`
	Directory string     `xml:"WorkingDirectory"`
	Other     []xml.Name `xml:",any"`
}

type windowsTrigger struct {
	XMLName    xml.Name
	Start      string `xml:"StartBoundary"`
	End        string `xml:"EndBoundary"`
	Enabled    bool   `xml:"Enabled"`
	Days       int    `xml:"ScheduleByDay>DaysInterval"`
	Delay      string `xml:"RandomDelay"`
	Repetition struct {
		Interval string `xml:"Interval"`
		Duration string `xml:"Duration"`
		Stop     bool   `xml:"StopAtDurationEnd"`
	} `xml:"Repetition"`
	Other []xml.Name `xml:",any"`
}

type windowsPrincipal struct {
	XMLName xml.Name
	Logon   string `xml:"LogonType"`
	Level   string `xml:"RunLevel"`
}

func (task windowsDefinition) matches(expected windowsDefinition) bool {
	if len(task.Actions.Items) != 1 || len(task.Triggers.Items) != 1 || len(task.Principals.Items) != 1 {
		return false
	}
	action, wantAction := task.Actions.Items[0], expected.Actions.Items[0]
	action.Command = strings.ToLower(action.Command)
	wantAction.Command = strings.ToLower(wantAction.Command)
	action.Arguments = strings.Join(strings.Fields(action.Arguments), " ")
	wantAction.Arguments = strings.Join(strings.Fields(wantAction.Arguments), " ")
	trigger, wantTrigger := task.Triggers.Items[0], expected.Triggers.Items[0]
	trigger.Start, trigger.End = boundary(trigger.Start), boundary(trigger.End)
	if trigger.Delay == "PT0S" {
		trigger.Delay = ""
	}
	return reflect.DeepEqual(action, wantAction) && reflect.DeepEqual(trigger, wantTrigger) && reflect.DeepEqual(task.Principals, expected.Principals) && reflect.DeepEqual(task.Settings, expected.Settings)
}

func boundary(value string) string {
	parsed, err := time.Parse("2006-01-02T15:04:05", value)
	if err != nil || parsed.Nanosecond() != 0 {
		return value
	}
	return parsed.Format("2006-01-02T15:04:05")
}
