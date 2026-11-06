package schedule

import (
	"brain/internal/platform"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

type Spec struct {
	Executable string `json:"executable"`
	Config     string `json:"config"`
	State      string `json:"state"`
	Home       string `json:"home"`
	Hour       int    `json:"hour"`
	Minute     int    `json:"minute"`
}

type Artifact struct {
	Path string `json:"path"`
	Data string `json:"data"`
}

type Invocation struct {
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
	When       string   `json:"when,omitempty"`
}

type Plan struct {
	ID          string       `json:"id"`
	System      string       `json:"system"`
	Spec        Spec         `json:"spec"`
	Artifacts   []Artifact   `json:"artifacts"`
	Arguments   []string     `json:"arguments"`
	Actions     []Invocation `json:"actions"`
	Queries     []Invocation `json:"queries"`
	CatchUp     string       `json:"catch_up"`
	StateWrites []string     `json:"state_writes"`
}

var ErrInvalid = errors.New("the schedule needs absolute paths, valid text, and a local time from 00:00 to 23:59")
var ErrUnsupported = errors.New("daily scheduling is supported on macOS, Linux and Windows")

// Preview only renders a plan. The caller supplies canonical absolute paths.
func Preview(system string, spec Spec) (Plan, error) {
	return previewNamed(system, spec)
}

func previewNamed(system string, spec Spec) (Plan, error) {
	if system != "darwin" && system != "linux" && system != "windows" {
		return Plan{}, ErrUnsupported
	}
	if spec.Hour < 0 || spec.Hour > 23 || spec.Minute < 0 || spec.Minute > 59 {
		return Plan{}, ErrInvalid
	}
	for _, value := range []string{spec.Executable, spec.Config, spec.State, spec.Home} {
		if !validPath(system, value) {
			return Plan{}, ErrInvalid
		}
	}
	identity := cleanPath(system, spec.State)
	if system == "windows" {
		identity = strings.ToLower(identity)
	}
	sum := sha256.Sum256([]byte(identity))
	p := Plan{ID: fmt.Sprintf("brain-index-%x", sum[:16]), System: system, Spec: spec}
	p.Arguments = []string{spec.Executable, "index", "--config", spec.Config, "--state-dir", spec.State, "--format", "json"}
	p.StateWrites = []string{join(system, spec.State, receiptName), join(system, spec.State, "locks/schedule.lock")}
	switch system {
	case "darwin":
		p.mac()
	case "linux":
		p.linux()
	case "windows":
		p.windows()
	}
	return p, nil
}

func validPath(system, value string) bool {
	if !utf8.ValidString(value) || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return false
	}
	if system == "windows" {
		return len(value) > 3 && value[1] == ':' && (value[2] == '\\' || value[2] == '/') && !strings.Contains(value[3:], ":")
	}
	return strings.HasPrefix(value, "/")
}

func cleanPath(system, value string) string {
	if system == "windows" {
		return strings.ReplaceAll(path.Clean(strings.ReplaceAll(value, `\`, "/")), "/", `\`)
	}
	return path.Clean(value)
}

func join(system, root, suffix string) string { return cleanPath(system, root+"/"+suffix) }
func xmlText(value string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(value))
	return b.String()
}
func invocation(executable string, args ...string) Invocation {
	return Invocation{Executable: executable, Args: args}
}

func (p *Plan) mac() {
	var args strings.Builder
	for _, arg := range p.Arguments {
		fmt.Fprintf(&args, "<string>%s</string>", xmlText(arg))
	}
	file := join(p.System, p.Spec.Home, "Library/LaunchAgents/"+p.ID+".plist")
	data := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>%s</string>
<key>ProgramArguments</key><array>%s</array>
<key>StartCalendarInterval</key><dict><key>Hour</key><integer>%d</integer><key>Minute</key><integer>%d</integer></dict>
<key>ProcessType</key><string>Background</string>
<key>Umask</key><integer>63</integer>
</dict></plist>
`, p.ID, args.String(), p.Spec.Hour, p.Spec.Minute)
	p.Artifacts = []Artifact{{file, data}}
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	remove := invocation("launchctl", "bootout", domain+"/"+p.ID)
	remove.When = "registered"
	p.Actions = []Invocation{remove, invocation("launchctl", "enable", domain+"/"+p.ID), invocation("launchctl", "bootstrap", domain, file)}
	p.Queries = []Invocation{invocation("launchctl", "print", domain+"/"+p.ID), invocation("launchctl", "print-disabled", domain)}
	p.CatchUp = "Runs at the selected local time while logged in. A missed sleep-time run is coalesced on wake. Powered-off or logged-out runs are not recovered. Clock changes follow launchd."
}

func systemdArg(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%").Replace(value) + `"`
}

func (p *Plan) linux() {
	args := make([]string, len(p.Arguments))
	for i, arg := range p.Arguments {
		args[i] = systemdArg(arg)
	}
	args[0] = systemdArg(":" + p.Arguments[0])
	service := p.ID + ".service"
	timer := p.ID + ".timer"
	folder := join(p.System, p.Spec.Home, ".config/systemd/user")
	p.Artifacts = []Artifact{
		{folder + "/" + service, "[Unit]\nDescription=Brain daily index\n[Service]\nType=oneshot\nExecStart=" + strings.Join(args, " ") + "\nSuccessExitStatus=3\nUMask=0077\nStandardOutput=null\nStandardError=null\n"},
		{folder + "/" + timer, fmt.Sprintf("[Unit]\nDescription=Brain daily index\n[Timer]\nOnCalendar=*-*-* %02d:%02d:00\nPersistent=true\nUnit=%s\n[Install]\nWantedBy=timers.target\n", p.Spec.Hour, p.Spec.Minute, service)},
	}
	p.Actions = []Invocation{invocation("systemctl", "--user", "daemon-reload"), invocation("systemctl", "--user", "enable", "--now", timer), invocation("systemctl", "--user", "restart", timer)}
	for i, unit := range []string{timer, service} {
		properties := "LoadState,ActiveState,UnitFileState,FragmentPath,DropInPaths,Result,ExecMainCode,ExecMainStatus,ExecMainStartTimestampMonotonic"
		if i == 0 {
			properties += ",Unit,Persistent,TimersCalendar,TimersMonotonic"
		}
		p.Queries = append(p.Queries, invocation("systemctl", "--user", "show", unit, "--no-pager", "--all", "--property="+properties))
	}
	unitPath := "/org/freedesktop/systemd1/unit/" + strings.ReplaceAll(p.ID, "-", "_2d") + "_2eservice"
	p.Queries = append(p.Queries, invocation("busctl", "--user", "--json=short", "--no-pager", "--auto-start=no", "--allow-interactive-authorization=no", "get-property", "org.freedesktop.systemd1", unitPath, "org.freedesktop.systemd1.Service", "ExecStartEx"))
	p.CatchUp = "Persistent calendar timers catch up missed runs when the user manager starts, possibly during installation. No wake is requested. Logged-out runs require an independently configured user manager. Clock changes follow systemd."
}

func powershell(script string) Invocation {
	units := utf16.Encode([]rune(script))
	data := make([]byte, len(units)*2)
	for i, unit := range units {
		binary.LittleEndian.PutUint16(data[i*2:], unit)
	}
	return invocation("powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(data))
}

func (p *Plan) windows() {
	// Task Scheduler expands environment variables in Exec arguments. Encoding the native argv avoids expansion of literal percent paths.
	command := platform.Command("cmd", p.Arguments)
	parts := strings.Fields(command)
	encoded := parts[len(parts)-1]
	file := join(p.System, p.Spec.State, "schedule-task.xml")
	data := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
<RegistrationInfo><Description>%s</Description></RegistrationInfo>
<Triggers><CalendarTrigger><StartBoundary>2000-01-01T%02d:%02d:00</StartBoundary><EndBoundary>9999-12-31T23:59:59</EndBoundary><Enabled>true</Enabled><ScheduleByDay><DaysInterval>1</DaysInterval></ScheduleByDay></CalendarTrigger></Triggers>
<Principals><Principal id="Author"><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal></Principals>
<Settings><MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy><DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries><StopIfGoingOnBatteries>false</StopIfGoingOnBatteries><StartWhenAvailable>true</StartWhenAvailable><Enabled>true</Enabled><ExecutionTimeLimit>PT0S</ExecutionTimeLimit></Settings>
<Actions Context="Author"><Exec><Command>%%SystemRoot%%\System32\WindowsPowerShell\v1.0\powershell.exe</Command><Arguments>-NoLogo -NoProfile -NonInteractive -EncodedCommand %s</Arguments></Exec></Actions>
</Task>
`, p.ID, p.Spec.Hour, p.Spec.Minute, encoded)
	p.Artifacts = []Artifact{{file, data}}
	p.Actions = []Invocation{invocation("schtasks.exe", "/Create", "/TN", p.ID, "/XML", file, "/IT", "/F")}
	script := `$ErrorActionPreference='Stop'; $s=New-Object -ComObject 'Schedule.Service'; $s.Connect(); try { $t=$s.GetFolder('\').GetTask('` + p.ID + `') } catch { if ($_.Exception.GetBaseException().HResult -eq -2147024894) { '{"exists":false}'; exit 0 }; exit 1 }; [ordered]@{ exists=$true; enabled=$t.Enabled; running=($t.State -eq 4); result_known=($t.LastRunTime.Year -gt 2000); last_exit=[long]$t.LastTaskResult; xml=$t.Xml } | ConvertTo-Json -Compress`
	p.Queries = []Invocation{powershell(script)}
	p.CatchUp = "StartWhenAvailable queues missed daily starts, normally after a ten-minute delay. The task requires the user to be logged in. Battery use is allowed; no wake is requested. Clock changes follow Task Scheduler."
}
