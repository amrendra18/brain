package main

import (
	"brain/internal/notes"
	"brain/internal/platform"
	"brain/tests/testutil"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

func mapJourney(t *testing.T) *journey {
	t.Helper()
	j := newJourney(t)
	j.put(t, "alpha/README.md", "# Alpha\n\nAlpha keeps the harbour log for every mooring.\n\n## Use\nRun it daily.")
	j.put(t, "alpha/PLAN.md", "# Plan\n\n## Milestone status\n\nA1 committed on 2026-10-01.\nA2 in progress.\n\n### Reader\n- [ ] Write it.\n  - [ ] Test it.\n## Other status\nNot this line.\n- [ ] Ship it.")
	j.put(t, "alpha/GOAL.md", "# Goal\n- [ ] Every mooring has a log.\n- [x] Name it.")
	j.put(t, "beta/README.md", "# Beta\n\nBeta has no plan.")
	j.ready(t)
	return j
}
func mapText(t *testing.T, j *journey, name string) string {
	t.Helper()
	command := exec.Command(testBinary, "map", name)
	command.Dir, command.Env = j.base, j.environment
	output, err := command.Output()
	if err != nil {
		t.Fatal(err, string(output))
	}
	return string(output)
}
func TestMapBounded(t *testing.T) {
	j := mapJourney(t)
	for _, item := range []struct{ when, text string }{{"2026-01-05T09:00:00Z", "The first note,\non two lines."}, {"2026-01-05T09:30:00Z", "The second note."}} {
		if _, err := notes.Append(j.state, "alpha", notes.Entry(item.text, item.when)); err != nil {
			t.Fatal(err)
		}
	}
	code, reply := j.run(t, "map", "ALPHA")
	expected := map[string]any{"project": "alpha", "path": filepath.Join(j.source, "alpha"), "purpose": "Alpha keeps the harbour log for every mooring.", "commit": nil, "milestones": map[string]any{"count": float64(2), "total": float64(2), "empty": false, "lines": []any{"A1 committed on 2026-10-01.", "A2 in progress."}}, "unchecked": map[string]any{"PLAN.md": float64(3), "GOAL.md": float64(1)}, "notes": map[string]any{"count": float64(2), "total": float64(2), "empty": false, "entries": []any{map[string]any{"when": "2026-01-05T09:30:00Z", "text": "The second note."}, map[string]any{"when": "2026-01-05T09:00:00Z", "text": "The first note, on two lines."}}}, "gaps": map[string]any{"items": []any{}, "count": float64(0), "total": float64(0), "empty": true}, "index_age_s": float64(0), "next": []any{}, "next_argv": []any{}}
	expected["index_complete"] = true
	age, validAge := reply["index_age_s"].(float64)
	if !validAge || age < 0 {
		t.Fatal("map index age is invalid", reply)
	}
	expected["index_age_s"] = age
	if code != 0 || !reflect.DeepEqual(reply, expected) {
		t.Fatal(code, reply, expected)
	}
	text := mapText(t, j, "alpha")
	ageLine := regexp.MustCompile(`\nIndex age [0-9]+ s\.\n$`)
	if ageLine.ReplaceAllString(text, "") != ageLine.ReplaceAllString(mapText(t, j, "alpha"), "") || strings.Count(text, "\n") >= 60 || !strings.Contains(text, "The first note, on two lines.") {
		t.Fatal(text)
	}
	status := ""
	for n := 0; n < 30; n++ {
		status += fmt.Sprintf("%02d %s\n", n, strings.Repeat("w", 300))
	}
	j.put(t, "alpha/README.md", "# Alpha\n\n"+strings.Repeat("Harbour ", 60))
	j.put(t, "alpha/PLAN.md", "## Status\n"+status)
	for n := 0; n < 5; n++ {
		if _, err := notes.Append(j.state, "alpha", notes.Entry(fmt.Sprintf("Note %d %s", n, strings.Repeat("x", 400)), fmt.Sprintf("2026-01-05T0%d:00:00Z", n))); err != nil {
			t.Fatal(err)
		}
	}
	if code, reply := j.run(t, "index"); code != 0 {
		t.Fatal(code, reply)
	}
	code, reply = j.run(t, "map", "alpha")
	milestones, entries := reply["milestones"].(map[string]any), reply["notes"].(map[string]any)
	if code != 0 || len([]rune(reply["purpose"].(string))) != 199 || milestones["count"] != float64(20) || milestones["total"] != float64(30) || entries["count"] != float64(3) || entries["total"] != float64(7) {
		t.Fatal(code, reply)
	}
	for _, entry := range entries["entries"].([]any) {
		if len([]rune(entry.(map[string]any)["text"].(string))) != 200 {
			t.Fatal(entry)
		}
	}
	text = mapText(t, j, "alpha")
	if strings.Count(text, "\n") != 37 || !strings.Contains(text, "Lines not shown: 10.") || !strings.Contains(text, "Older notes not shown: 4.") {
		t.Fatal(text)
	}
	for _, line := range strings.Split(text, "\n") {
		if len([]rune(line)) > 240 {
			t.Fatal("map line exceeded its bound", line)
		}
	}
}
func TestMapNoGit(t *testing.T) {
	j := mapJourney(t)
	testutil.Repository(t, j.source, "The parent commit")
	if code, reply := j.run(t, "map", "alpha"); code != 0 || reply["commit"] != nil {
		t.Fatal(code, reply)
	}
	testutil.Repository(t, filepath.Join(j.source, "alpha"), "")
	if code, reply := j.run(t, "map", "alpha"); code != 0 || reply["commit"] != nil {
		t.Fatal(code, reply)
	}
	testutil.Repository(t, filepath.Join(j.source, "beta"), "The beta commit")
	j.environment = append(j.environment, "PATH=")
	if code, reply := j.run(t, "map", "beta"); code != 0 || reply["commit"] != nil || reply["purpose"] != "Beta has no plan." {
		t.Fatal(code, reply)
	}
	code, reply := j.run(t, "doctor")
	if code != 3 || reply["count"] != float64(0) {
		t.Fatal(code, reply)
	}
	for _, check := range reply["checks"].([]any) {
		one := check.(map[string]any)
		if one["name"] == "git" && (one["ok"] != true || !strings.Contains(one["detail"].(string), "unavailable")) {
			t.Fatal(one)
		}
	}
}
func TestMapProjectUnknown(t *testing.T) {
	j := mapJourney(t)
	for _, args := range [][]string{{"map"}, {"map", "alpha", "extra"}, {"map", "alpha", "--full"}} {
		code, reply := j.run(t, args...)
		if code != 2 {
			t.Fatal(code, reply)
		}
	}
	code, reply := j.run(t, "map", "unknown /private/fixture")
	failure := reply["error"].(map[string]any)
	if code != 1 || failure["code"] != "project-unknown" || strings.Contains(failure["message"].(string), "/private/") {
		t.Fatal(code, reply)
	}
	argv := []string{}
	for _, word := range failure["next_argv"].([]any)[0].([]any) {
		argv = append(argv, word.(string))
	}
	follow := exec.Command(argv[0], argv[1:]...)
	follow.Dir, follow.Env = j.base, append(j.environment, "PATH=")
	if output, err := follow.Output(); err != nil {
		t.Fatal(err, string(output))
	}
	shell := testutil.Shell(runtime.GOOS, os.Getenv("SystemRoot"), failure["next"].([]any)[0].(string))
	follow = exec.Command(shell[0], shell[1:]...)
	follow.Dir, follow.Env = j.base, append(j.environment, "PATH=")
	if output, err := follow.Output(); err != nil {
		t.Fatal(err, string(output))
	}
}
func TestMapRefresh(t *testing.T) {
	j := mapJourney(t)
	j.put(t, "alpha/GOAL.md", "# Goal\n- [ ] One.\n- [ ] Two.")
	code, reply := j.run(t, "map", "alpha", "--no-refresh")
	if code != 0 || reply["unchecked"].(map[string]any)["GOAL.md"] != float64(1) {
		t.Fatal(code, reply)
	}
	if code, reply := j.run(t, "index"); code != 0 {
		t.Fatal(code, reply)
	}
	code, reply = j.run(t, "map", "alpha")
	if code != 0 || reply["unchecked"].(map[string]any)["GOAL.md"] != float64(2) || reply["index_age_s"] != float64(0) {
		t.Fatal(code, reply)
	}
	data, err := os.ReadFile(filepath.Join(j.state, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err = json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	config["options"].(map[string]any)["inline_refresh_limit"] = float64(1)
	data, err = json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(j.state, "config.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	j.put(t, "alpha/GOAL.md", "# Goal\nNo boxes.")
	code, reply = j.run(t, "map", "alpha")
	if code != 0 || reply["unchecked"].(map[string]any)["GOAL.md"] != float64(2) {
		t.Fatal(code, reply)
	}
	testutil.Permissions(t, j.state, 0500)
	defer testutil.Permissions(t, j.state, 0700)
	code, reply = j.run(t, "map", "alpha")
	if code != 0 || reply["read_only"] != true || !strings.Contains(reply["next"].([]any)[0].(string), "doctor") {
		t.Fatal(code, reply)
	}
	j.put(t, ".local/PLAN.md", "## Status\nExcluded alias must never be read.")
	os.Remove(filepath.Join(j.source, "alpha", "PLAN.md"))
	if err = os.Symlink(filepath.Join(j.source, ".local", "PLAN.md"), filepath.Join(j.source, "alpha", "PLAN.md")); err != nil {
		t.Skip("owned source alias unavailable")
	}
	code, reply = j.run(t, "map", "alpha", "--no-refresh")
	if code != 0 || reply["milestones"].(map[string]any)["count"] != float64(2) {
		t.Fatal(code, reply)
	}
}
func TestMapReadsGitReadOnly(t *testing.T) {
	if platform.GitPath() == "" {
		t.Skip("Git is unavailable; the no-Git fallback still runs")
	}
	j := mapJourney(t)
	project := filepath.Join(j.source, "alpha")
	git := testutil.Repository(t, project, "Add the harbour log plan", "-----BEGIN PGP SIGNATURE-----\nowned invalid signature\n-----END PGP SIGNATURE-----")
	other := testutil.Repository(t, filepath.Join(j.source, "beta"), "The beta commit")
	marker := filepath.Join(j.base, "external-command-ran")
	helper := filepath.Join(j.base, "external-helper")
	if runtime.GOOS == "windows" {
		helper += ".cmd"
		os.WriteFile(helper, []byte("@echo off\r\necho called > \""+marker+"\"\r\n"), 0700)
	} else {
		os.WriteFile(helper, []byte("#!/bin/sh\nprintf called > '"+strings.ReplaceAll(marker, "'", "'\\''")+"'\n"), 0700)
	}
	quote := func(value string) string {
		return strings.ReplaceAll(strings.ReplaceAll(value, "\\", "\\\\"), "\"", "\\\"")
	}
	config := "[core]\n repositoryformatversion = 0\n bare = false\n fsmonitor = \"" + quote(helper) + "\"\n pager = \"" + quote(helper) + "\"\n[log]\n showSignature = true\n[gpg]\n program = \"" + quote(helper) + "\"\n[protocol \"ext\"]\n allow = always\n"
	if err := os.WriteFile(filepath.Join(git, "config"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	control := exec.Command(platform.GitPath(), "--no-pager", "-C", project, "log", "-1", "--show-signature", "--format=%s")
	control.Env = j.environment
	if output, err := control.CombinedOutput(); err != nil {
		t.Fatal("configured signature control did not run", err, string(output))
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("configured signature control did not invoke its program", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	before := treeBytes(t, project)
	j.environment = append(j.environment, "GIT_DIR="+other, "GIT_OPTIONAL_LOCKS=1", "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=log.showSignature", "GIT_CONFIG_VALUE_0=true", "GIT_PAGER="+helper)
	code, reply := j.run(t, "map", "alpha", "--no-refresh")
	expected := map[string]any{"date": "2025-10-12T20:13:20Z", "subject": "Add the harbour log plan"}
	if code != 0 || !reflect.DeepEqual(reply["commit"], expected) || !reflect.DeepEqual(before, treeBytes(t, project)) {
		t.Fatal(code, reply)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("Git ran a configured external command", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	config = strings.Replace(config, "repositoryformatversion = 0", "repositoryformatversion = 1", 1)
	config += "[extensions]\n partialClone = origin\n[remote \"origin\"]\n promisor = true\n partialclonefilter = blob:none\n url = http://" + listener.Addr().String() + "/objects\n[protocol \"http\"]\n allow = always\n"
	if err = os.WriteFile(filepath.Join(git, "config"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(git, "refs", "heads", "main"), []byte(strings.Repeat("a", 40)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	before = treeBytes(t, project)
	code, reply = j.run(t, "map", "alpha", "--no-refresh")
	if code != 0 || reply["commit"] != nil || !reflect.DeepEqual(before, treeBytes(t, project)) {
		t.Fatal(code, reply)
	}
	tcp := listener.(*net.TCPListener)
	tcp.SetDeadline(time.Now().Add(30 * time.Millisecond))
	connection, err := tcp.Accept()
	if connection != nil {
		connection.Close()
		t.Fatal("Git connected to its promisor remote")
	}
	if failure, ok := err.(net.Error); !ok || !failure.Timeout() {
		t.Fatal(err)
	}
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("Git ran an external command", err)
	}
	if runtime.GOOS == "darwin" {
		// Real OS denial covers the complete repository, including Git's own files.
		sandbox := fmt.Sprintf("(version 1) (allow default) (deny network*) (deny file-write* (subpath %q))", project)
		command := exec.Command("/usr/bin/sandbox-exec", "-p", sandbox, testBinary, "map", "alpha", "--no-refresh", "--format", "json")
		command.Dir, command.Env = j.base, j.environment
		var stdout, stderr bytes.Buffer
		command.Stdout, command.Stderr = &stdout, &stderr
		if err = command.Run(); err != nil || stderr.Len() != 0 {
			t.Fatal(err, stdout.String(), stderr.String())
		}
		if err = json.Unmarshal(stdout.Bytes(), &reply); err != nil || reply["commit"] != nil || reply["purpose"] != "Alpha keeps the harbour log for every mooring." {
			t.Fatal(err, reply)
		}
		if !reflect.DeepEqual(before, treeBytes(t, project)) {
			t.Fatal("read-only Git map changed repository bytes")
		}
	}
}

func TestMapLineBreakPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows refuses line breaks in filesystem names")
	}
	j := newJourney(t)
	j.state = filepath.Join(j.base, "state"+strings.Repeat("\n", 70))
	j.environment = append(j.environment, "BRAIN_HOME="+j.state)
	project := "alpha" + strings.Repeat("\n", 70)
	j.put(t, project+"/README.md", "# Alpha\n\nA line-safe harbour.")
	j.ready(t)
	code, reply := j.run(t, "map", project)
	if code != 0 || reply["path"] != filepath.Join(j.source, project) || reply["project"] != project {
		t.Fatal(code, reply)
	}
	text := mapText(t, j, project)
	if strings.Count(text, "\n") >= 60 || !strings.Contains(text, `alpha\n`) {
		t.Fatal("path broke the map line bound", text)
	}
	testutil.Permissions(t, j.state, 0500)
	defer testutil.Permissions(t, j.state, 0700)
	code, reply = j.run(t, "map", project)
	if code != 0 || reply["read_only"] != true {
		t.Fatal(code, reply)
	}
	text = mapText(t, j, project)
	if strings.Count(text, "\n") >= 60 || !strings.Contains(text, "--format json for exact next and next_argv") {
		t.Fatal("recovery broke the map line bound", text)
	}
	failure := reply["next_argv"].([]any)[0].([]any)
	args := []string{}
	for _, arg := range failure {
		args = append(args, arg.(string))
	}
	for flag, value := range map[string]string{"--state-dir": j.state, "--config": filepath.Join(j.state, "config.json")} {
		found := 0
		for i, word := range args {
			if word == flag && i+1 < len(args) && args[i+1] == value {
				found++
			}
		}
		if found != 1 {
			t.Fatal("exact recovery lost scope", flag, args)
		}
	}
	command := exec.Command(args[0], args[1:]...)
	words := testutil.Shell(runtime.GOOS, os.Getenv("SystemRoot"), reply["next"].([]any)[0].(string))
	shellCommand := exec.Command(words[0], words[1:]...)
	for _, command := range []*exec.Cmd{command, shellCommand} {
		command.Env, command.Dir = append(j.environment, "PATH="), j.base
		output, err := command.Output()
		exit, ok := err.(*exec.ExitError)
		var diagnosis map[string]any
		if !ok || exit.ExitCode() != 1 || json.Unmarshal(output, &diagnosis) != nil || diagnosis["problems"].([]any)[0].(map[string]any)["code"] != "state-unwritable" {
			t.Fatal(err, string(output))
		}
	}
}
