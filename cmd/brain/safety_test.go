package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestConfigRecoveryKeepsExclusions(t *testing.T) {
	j := newJourney(t)
	j.put(t, "public.md", "allowedopalmarker")
	j.put(t, "private.md", "privateopalmarker")
	if code, reply := j.run(t, "init", "--source", j.source); code != 0 {
		t.Fatal(code, reply)
	}
	path := filepath.Join(j.state, "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	config["exclude"] = append(config["exclude"].([]any), "private.md")
	valid, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, valid, 0600); err != nil {
		t.Fatal(err)
	}
	if code, reply := j.run(t, "index"); code != 0 {
		t.Fatal(code, reply)
	}
	for query, want := range map[string]float64{"allowedopalmarker": 1, "privateopalmarker": 0} {
		if code, reply := j.run(t, "search", query); code != 0 || reply["count"] != want {
			t.Fatal(query, code, reply)
		}
	}
	broken := append(append([]byte{}, valid...), ',')
	if err := os.WriteFile(path, broken, 0600); err != nil {
		t.Fatal(err)
	}
	code, reply := j.run(t, "search", "privateopalmarker")
	if code != 1 || reply["error"].(map[string]any)["code"] != "config-invalid" {
		t.Fatal(code, reply)
	}
	for _, given := range reply["error"].(map[string]any)["next_argv"].([]any) {
		args := []string{}
		for _, value := range given.([]any) {
			args = append(args, value.(string))
		}
		command := exec.Command(args[0], args[1:]...)
		command.Dir, command.Env = j.base, j.environment
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatal(err, string(output))
		}
	}
	if code, reply := j.run(t, "init", "--source", j.source); code != 1 {
		t.Fatal("invalid config was replaced", code, reply)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, broken) {
		t.Fatal("recovery changed config", err, string(got))
	}
	if err := os.WriteFile(path, valid, 0600); err != nil {
		t.Fatal(err)
	}
	if code, reply := j.run(t, "index", "--full"); code != 0 && code != 3 {
		t.Fatal(code, reply)
	}
	if code, reply := j.run(t, "search", "privateopalmarker"); code != 0 || reply["count"] != float64(0) {
		t.Fatal("repaired exclusions changed", code, reply)
	}
}

func TestUnsafeStateRefusesBeforeWrite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode check")
	}
	j := newJourney(t)
	j.put(t, "private.md", "privateopalmarker")
	if err := os.Mkdir(j.state, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(j.state, 0755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "--source", j.source}, {"index"}, {"search", "privateopalmarker"}, {"doctor"}} {
		code, reply := j.run(t, args...)
		want := 1
		if code != want {
			t.Fatal(args, code, reply)
		}
		var failure map[string]any
		if args[0] == "doctor" {
			failure = reply["problems"].([]any)[0].(map[string]any)
		} else {
			failure = reply["error"].(map[string]any)
		}
		if failure["code"] != "state-unsafe" {
			t.Fatal(args, failure)
		}
	}
	files, err := os.ReadDir(j.state)
	if err != nil || len(files) != 0 {
		t.Fatal("unsafe state was written", files, err)
	}
	info, err := os.Stat(j.state)
	if err != nil || info.Mode().Perm() != 0755 {
		t.Fatal("shared permissions changed", info, err)
	}
	if err := os.Chmod(j.state, 0700); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "--source", j.source}, {"index"}, {"search", "privateopalmarker"}} {
		if code, reply := j.run(t, args...); code != 0 {
			t.Fatal(args, code, reply)
		}
	}
	info, err = os.Stat(filepath.Join(j.state, "index.sqlite"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("database is not private", info, err)
	}
}

func TestMapFencedExamplesAreNotFacts(t *testing.T) {
	j := newJourney(t)
	j.put(t, "alpha/README.md", "# Alpha\n\n```markdown\n~~~\nExample sentence inside code.\n```\n\nThe real project purpose.\n")
	j.put(t, "alpha/PLAN.md", "# Plan\n\n```markdown\n~~~\n## Status\nExample milestone.\n```\n\n## Status\nReal milestone.\n")
	for _, args := range [][]string{{"init", "--source", j.source}, {"index"}} {
		if code, reply := j.run(t, args...); code != 0 {
			t.Fatal(code, reply)
		}
	}
	code, reply := j.run(t, "map", "alpha", "--no-refresh")
	if code != 0 || reply["purpose"] != "The real project purpose." {
		t.Fatal(code, reply)
	}
	lines := reply["milestones"].(map[string]any)["lines"].([]any)
	if !reflect.DeepEqual(lines, []any{"Real milestone."}) {
		t.Fatal(lines)
	}
	if code, reply := j.run(t, "search", "Example sentence", "--no-refresh"); code != 0 || reply["count"] != float64(1) ||
		!strings.Contains(reply["results"].([]any)[0].(map[string]any)["snippet"].(string), "Example sentence") {
		t.Fatal("fenced source text lost from search", code, reply)
	}
}
