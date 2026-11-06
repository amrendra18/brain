package verifyrun

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
)

type scheduleReply struct {
	Schedule struct {
		ID     string `json:"id"`
		System string `json:"system"`
		Spec   struct {
			Executable, Config, State, Home string
			Hour, Minute                    int
		} `json:"spec"`
		Arguments []string                      `json:"arguments"`
		Artifacts []struct{ Path, Data string } `json:"artifacts"`
		CatchUp   string                        `json:"catch_up"`
	} `json:"schedule"`
	Written *bool `json:"written"`
	Changed *bool `json:"changed"`
	Status  *struct {
		Registered, Enabled, Healthy bool
		Installation                 string
	} `json:"status"`
}

func checkSchedule(data []byte, j *featureJourney, minute int, write, changed bool) (scheduleReply, error) {
	var r scheduleReply
	if err := json.Unmarshal(data, &r); err != nil {
		return r, err
	}
	spec := r.Schedule.Spec
	expected := []string{j.driver.binary, "index", "--config", j.config, "--state-dir", j.state, "--format", "json"}
	if r.Schedule.ID == "" || r.Schedule.System != runtime.GOOS || spec.Executable != j.driver.binary || spec.Config != j.config || spec.State != j.state || spec.Home != j.home || spec.Hour != 3 || spec.Minute != minute || !reflect.DeepEqual(r.Schedule.Arguments, expected) || r.Schedule.CatchUp == "" {
		return r, errors.New("schedule lost selected executable, config, state, home or local time")
	}
	if r.Written == nil || *r.Written != changed || r.Changed == nil || *r.Changed != changed {
		return r, errors.New("schedule write/change result differs from expected effect")
	}
	if write {
		if r.Status == nil || !r.Status.Registered || !r.Status.Enabled || !r.Status.Healthy || r.Status.Installation != "installed" {
			return r, errors.New("schedule activation was not confirmed by the stand-in")
		}
	} else if r.Status != nil {
		return r, errors.New("schedule preview queried activation status")
	}
	if len(r.Schedule.Artifacts) == 0 {
		return r, errors.New("schedule preview omitted installation files")
	}
	for _, artifact := range r.Schedule.Artifacts {
		relative, err := filepath.Rel(j.home, artifact.Path)
		if err != nil || !filepath.IsLocal(relative) || artifact.Data == "" {
			return r, errors.New("schedule file escaped owned home or has no content")
		}
	}
	return r, nil
}

func scheduleFiles(before, after map[string]string, home string, r scheduleReply) error {
	allowed := map[string]bool{}
	for _, artifact := range r.Schedule.Artifacts {
		name, err := filepath.Rel(home, artifact.Path)
		if err != nil {
			return err
		}
		allowed[name] = true
		for parent := filepath.Dir(name); parent != "."; parent = filepath.Dir(parent) {
			allowed[parent] = true
		}
	}
	for name, value := range before {
		if after[name] != value && !allowed[name] {
			return fmt.Errorf("schedule changed unrelated home entry %s", name)
		}
	}
	for name := range after {
		if _, ok := before[name]; !ok && !allowed[name] {
			return fmt.Errorf("schedule wrote unrelated home entry %s", name)
		}
	}
	for _, artifact := range r.Schedule.Artifacts {
		data, err := os.ReadFile(artifact.Path)
		if err != nil || string(data) != artifact.Data {
			return errors.Join(err, errors.New("installed schedule differs from preview"))
		}
	}
	return nil
}

func scheduleSnapshot(root string) (map[string]string, error) {
	values, err := snapshot(root)
	if err != nil {
		return nil, err
	}
	for name, value := range values {
		info, err := os.Stat(filepath.Join(root, name))
		if err != nil {
			return nil, err
		}
		values[name] = fmt.Sprintf("%s %o %d", value, info.Mode(), info.ModTime().UnixNano())
	}
	return values, nil
}

func (d *driver) schedule(attempt int) (problem error) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return errors.New("schedule command stand-ins need a macOS or Linux host; Windows native execution remains unverified")
	}
	j, err := d.newFeatureJourney("schedule", attempt)
	if err != nil {
		return err
	}
	source, err := j.document("harbor", "# Harbor\n\nOwned schedule source fixture.\n")
	if err != nil {
		return err
	}
	if err := j.writeConfig([]map[string]any{{"path": source, "project": "harbor"}}); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(j.home, "unrelated.txt"), []byte("keep"), 0600); err != nil {
		return err
	}
	preserveSources, err := j.preserve(fmt.Sprintf("schedule-%d-sources", attempt), j.sources)
	if err != nil {
		return err
	}
	defer func() { problem = errors.Join(problem, preserveSources()) }()
	configBefore, err := os.ReadFile(j.config)
	if err != nil {
		return err
	}
	defer func() {
		after, err := os.ReadFile(j.config)
		problem = errors.Join(problem, err)
		if string(after) != string(configBefore) {
			problem = errors.Join(problem, errors.New("schedule changed selected config"))
		}
	}()
	bin, fail, log, err := scheduleStandIn(j.base)
	if err != nil {
		return err
	}
	d.summary.StandIns = append(d.summary.StandIns, "Native scheduler executable replaced with an owned "+runtime.GOOS+" stand-in. No native activation or scheduled run is proved.")
	defer func() {
		calls, readErr := os.ReadFile(log)
		if os.IsNotExist(readErr) {
			calls = []byte("Stand-in was not invoked.\n")
			readErr = nil
		}
		problem = errors.Join(problem, readErr)
		if readErr == nil {
			problem = errors.Join(problem, os.WriteFile(filepath.Join(d.evidence, "logs", fmt.Sprintf("schedule-%d-stand-in.txt", attempt)), calls, 0600))
		}
		home, homeErr := snapshot(j.home)
		state, stateErr := snapshot(j.state)
		problem = errors.Join(problem, homeErr, stateErr, writeJSON(filepath.Join(d.evidence, "traces", fmt.Sprintf("schedule-%d-final-files.json", attempt)), map[string]any{"home": home, "state": state}))
	}()
	env := []string{"PATH=" + bin}
	before, err := scheduleSnapshot(j.base)
	if err != nil {
		return err
	}
	data, err := j.callEnv(0, env, "schedule", "--time", "03:17")
	if err != nil {
		return err
	}
	preview, err := checkSchedule(data, j, 17, false, false)
	if err != nil {
		return err
	}
	after, err := scheduleSnapshot(j.base)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(before, after) {
		return errors.New("schedule preview wrote files or called the scheduler")
	}
	homeBefore, err := snapshot(j.home)
	if err != nil {
		return err
	}
	data, err = j.callEnv(0, env, "schedule", "--time", "03:17", "--write")
	if err != nil {
		return err
	}
	installed, err := checkSchedule(data, j, 17, true, true)
	if err != nil {
		return err
	}
	if installed.Schedule.ID != preview.Schedule.ID {
		return errors.New("installation changed schedule identity")
	}
	homeAfter, err := snapshot(j.home)
	if err != nil {
		return err
	}
	if err := scheduleFiles(homeBefore, homeAfter, j.home, installed); err != nil {
		return err
	}
	noOpHome, err := scheduleSnapshot(j.home)
	if err != nil {
		return err
	}
	noOpState, err := scheduleSnapshot(j.state)
	if err != nil {
		return err
	}
	data, err = j.callEnv(3, env, "schedule", "--time", "03:17", "--write")
	if err != nil {
		return err
	}
	if _, err := checkSchedule(data, j, 17, true, false); err != nil {
		return err
	}
	repeatedHome, err := scheduleSnapshot(j.home)
	if err != nil {
		return err
	}
	repeatedState, err := scheduleSnapshot(j.state)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(noOpHome, repeatedHome) || !reflect.DeepEqual(noOpState, repeatedState) {
		return errors.New("identical schedule touched home or state files")
	}
	if err := os.WriteFile(fail, []byte("deliberate activation failure"), 0600); err != nil {
		return err
	}
	data, err = j.callEnv(1, env, "schedule", "--time", "03:18", "--write")
	if err != nil {
		return err
	}
	if err := checkScopedNext(data, j.config, j.state, true); err != nil {
		return err
	}
	var refusal struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &refusal); err != nil {
		return err
	}
	if refusal.Error.Code != "schedule-activation-failed" {
		return errors.New("activation failure was not identified")
	}
	receipt, err := os.ReadFile(filepath.Join(j.state, "schedule.json"))
	if err != nil {
		return err
	}
	var failed struct {
		Outcome string `json:"outcome"`
	}
	if err := json.Unmarshal(receipt, &failed); err != nil {
		return err
	}
	if failed.Outcome != "failed" {
		return errors.New("activation failure was not retained in schedule receipt")
	}
	if err := os.Remove(fail); err != nil {
		return err
	}
	data, err = j.callEnv(0, env, "schedule", "--time", "03:18", "--write")
	if err != nil {
		return err
	}
	recovered, err := checkSchedule(data, j, 18, true, true)
	if err != nil {
		return err
	}
	if recovered.Schedule.ID != preview.Schedule.ID {
		return errors.New("retry created a second schedule identity")
	}
	finalHome, err := snapshot(j.home)
	if err != nil {
		return err
	}
	if err := scheduleFiles(homeBefore, finalHome, j.home, recovered); err != nil {
		return err
	}
	calls, err := os.ReadFile(log)
	if err != nil {
		return err
	}
	if len(calls) == 0 {
		return errors.New("scheduler stand-in was never called")
	}
	if err := writeJSON(filepath.Join(d.evidence, "traces", fmt.Sprintf("schedule-%d-files.json", attempt)), map[string]any{"preview_before": before, "preview_after": after, "home_before": homeBefore, "home_after": finalHome, "no_op_home_before": noOpHome, "no_op_home_after": repeatedHome, "no_op_state_before": noOpState, "no_op_state_after": repeatedState, "failed_receipt": json.RawMessage(receipt)}); err != nil {
		return err
	}
	d.summary.SideEffects[fmt.Sprintf("schedule_%d", attempt+1)] = "Preview changed no files. Stand-in installation wrote only selected schedule files and state. Identical installation preserved bytes, modes and modification times. Deliberate activation failure was retained; retry recovered. Native activation remains unproved."
	return nil
}

func scheduleStandIn(base string) (bin, fail, log string, err error) {
	bin = filepath.Join(base, "scheduler")
	if err = os.Mkdir(bin, 0700); err != nil {
		return
	}
	fail = filepath.Join(bin, "fail")
	log = filepath.Join(bin, "calls.txt")
	awk, err := exec.LookPath("awk")
	if err != nil {
		return "", "", "", err
	}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	renderer := filepath.Join(bin, "loaded.awk")
	if err = os.WriteFile(renderer, []byte(loadedScheduleAWK), 0600); err != nil {
		return
	}
	prefix := "#!/bin/sh\nrecord=" + quote(filepath.Join(bin, "registered")) + "\nfail=" + quote(fail) + "\nlog=" + quote(log) + "\nawk=" + quote(awk) + "\nrenderer=" + quote(renderer) + "\nprintf '%s\\n' \"$*\" >> \"$log\"\n"
	mac := `case "$1" in
print)
 [ -f "$record" ] || exit 113
 IFS= read -r artifact < "$record"
 [ -n "$artifact" ] || exit 113
 printf 'path = %s\nstate = waiting\nlast exit code = 3\n' "$artifact"
 "$awk" -v mode=mac -f "$renderer" "$record.plist" ;;
print-disabled) printf 'disabled services = {\n}\n' ;;
bootout) : > "$record" ;;
enable) [ ! -f "$fail" ] || exit 7 ;;
bootstrap)
 [ ! -f "$fail" ] || exit 7
 "$awk" '{print}' "$3" > "$record.plist" || exit 8
 printf '%s\n' "$3" > "$record" ;;
*) exit 8 ;;
esac
`
	linux := `case "$2" in
show)
 case "$3" in *.timer) kind=timer;; *.service) kind=service;; *) exit 8;; esac
 if [ ! -f "$record.$kind-path" ]; then printf 'LoadState=not-found\n'; exit 0; fi
 IFS= read -r artifact < "$record.$kind-path"
 if [ "${artifact##*/}" != "$3" ]; then printf 'LoadState=not-found\n'; exit 0; fi
 active=inactive; enabled=disabled
 if [ -s "$record" ]; then active=active; enabled=enabled; fi
 printf 'LoadState=loaded\nActiveState=%s\nUnitFileState=%s\nFragmentPath=%s\nDropInPaths=\nResult=success\nExecMainStartTimestampMonotonic=0\n' "$active" "$enabled" "$artifact"
 if [ "$kind" = timer ]; then "$awk" -v mode=timer -f "$renderer" "$record.timer"; fi ;;
daemon-reload)
 [ ! -f "$fail" ] || exit 7
 for service in "$HOME/.config/systemd/user/"*.service; do
  [ -f "$service" ] || continue
  timer="${service%.service}.timer"
  "$awk" '{print}' "$service" > "$record.service" || exit 8
  "$awk" '{print}' "$timer" > "$record.timer" || exit 8
  printf '%s\n' "$service" > "$record.service-path"
  printf '%s\n' "$timer" > "$record.timer-path"
 done ;;
enable|restart)
 [ ! -f "$fail" ] || exit 7
 printf 'enabled\n' > "$record" ;;
*) exit 8 ;;
esac
`
	bus := ` [ -f "$record.service" ] || exit 8
 "$awk" -v mode=service -f "$renderer" "$record.service"
`
	for name, script := range map[string]string{"launchctl": mac, "systemctl": linux, "busctl": bus} {
		if err = os.WriteFile(filepath.Join(bin, name), []byte(prefix+script), 0700); err != nil {
			return
		}
	}
	return
}

const loadedScheduleAWK = `
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
function json(s,    out,i,c) {
 out="\""
 for (i=1;i<=length(s);i++) { c=substr(s,i,1); if(c=="\\" || c=="\"") out=out "\\"; out=out c }
 return out "\""
}
mode=="mac" && /<key>ProgramArguments<\/key>/ {
 s=$0; sub(/^.*<key>ProgramArguments<\/key><array>/,"",s); sub(/<\/array>.*$/,"",s)
 while(match(s,/<string>[^<]*<\/string>/)) { args[++n]=xml(substr(s,RSTART+8,RLENGTH-17)); s=substr(s,RSTART+RLENGTH) }
}
mode=="service" && /^ExecStart=/ {
 s=substr($0,11); quoted=0; value=""
 for(i=1;i<=length(s);i++) {
  c=substr(s,i,1)
  if(c=="\\") { value=value substr(s,++i,1); continue }
  if(c=="\"") { if(quoted) { args[++n]=replace(value,"%%","%"); value="" }; quoted=!quoted; continue }
  if(quoted) value=value c
 }
 if(substr(args[1],1,1)==":") { args[1]=substr(args[1],2); noenv=1 }
}
mode=="timer" && /^Unit=/ { print $0 }
mode=="timer" && /^Persistent=/ { print "Persistent=" (substr($0,12)=="true" ? "yes" : "no") }
mode=="timer" && /^OnCalendar=/ { print "TimersCalendar={ OnCalendar=" substr($0,12) " ; next_elapse=n/a }" }
/<key>StartCalendarInterval<\/key>/ {
 h=$0; sub(/^.*<key>Hour<\/key><integer>/,"",h); sub(/<\/integer>.*$/,"",h)
 m=$0; sub(/^.*<key>Minute<\/key><integer>/,"",m); sub(/<\/integer>.*$/,"",m)
}
END {
 if(mode=="mac") { if(n==0) exit 8; printf "program = %s\narguments = {\n",args[1]; for(i=1;i<=n;i++) printf "\t%s\n",args[i]; print "}"; printf "event triggers = {\ncalendar => {\nstream = com.apple.launchd.calendarinterval\ndescriptor = {\n\"Minute\" => %s\n\"Hour\" => %s\n}\n}\n}\n",m,h }
 if(mode=="timer") print "TimersMonotonic="
 if(mode=="service") {
  if(n==0) exit 8
  printf "{\"type\":\"a(sasasttttuii)\",\"data\":[[%s,[",json(args[1])
  for(i=1;i<=n;i++) printf "%s%s",(i>1 ? "," : ""),json(args[i])
  printf "],[%s],0,0,0,0,0,0,0]]}\n",(noenv ? "\"no-env-expand\"" : "")
 }
}
`
