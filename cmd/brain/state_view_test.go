package main

import (
	"brain/tests/testutil"
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestStateView(t *testing.T) {
	j := newJourney(t)
	j.put(t, "alpha/README.md", "# Alpha\n\nAlpha returns a lease.")
	j.put(t, "beta/PLAN.md", "# Beta\n\nThe beacon plan.")
	if code, reply := j.run(t); code != 1 || reply["error"].(map[string]any)["code"] != "state-uninitialised" {
		t.Fatal(code, reply)
	}
	if code, reply := j.run(t, "init", "--source", j.source); code != 0 {
		t.Fatal(code, reply)
	}
	code, reply := j.run(t)
	summary := reply["index"].(map[string]any)
	if code != 0 || summary["present"] != false || summary["pending"] != float64(0) || summary["pending_known"] != false || summary["index_age_s"] != nil {
		t.Fatal(code, reply)
	}
	if code, reply = j.run(t, "index"); code != 0 {
		t.Fatal(code, reply)
	}
	os.WriteFile(filepath.Join(j.source, "alpha/README.md"), []byte("# Alpha\n\nNew heron."), 0600)
	for run := 0; run < 2; run++ {
		code, reply = j.run(t)
		summary = reply["index"].(map[string]any)
		if code != 0 || summary["files"] != float64(2) || summary["pending"] != float64(0) || summary["pending_known"] != false || summary["present"] != true {
			t.Fatal(code, reply)
		}
	}
	os.Rename(j.source, j.source+"-moved")
	code, reply = j.run(t)
	summary = reply["index"].(map[string]any)
	if code != 0 || summary["pending"] != float64(0) || summary["pending_known"] != false {
		t.Fatal(code, reply)
	}
	os.Rename(j.source+"-moved", j.source)
	testutil.Permissions(t, j.state, 0500)
	defer testutil.Permissions(t, j.state, 0700)
	for _, command := range [][]string{nil, {"projects"}, {"recent"}, {"search", "lease"}} {
		code, reply = j.run(t, command...)
		if code != 0 || reply["read_only"] != true || !strings.Contains(reply["next"].([]any)[0].(string), "doctor") {
			t.Fatal(code, reply)
		}
	}
}
func TestListShapes(t *testing.T) {
	for _, populated := range []bool{false, true} {
		j := newJourney(t)
		if populated {
			j.put(t, "alpha/README.md", "# Alpha\n\nReturn a lease.")
			j.put(t, "alpha/PLAN.md", "# Plan\n\nAnother lease.")
			j.put(t, "large.md", strings.Repeat("x", 1100000))
		}
		if code, reply := j.run(t, "init", "--source", j.source); code != 0 {
			t.Fatal(code, reply)
		}
		for _, one := range []struct {
			args []string
			list string
		}{{[]string{"index"}, "skipped"}, {[]string{"search", "lease", "--limit", "1"}, "results"}, {[]string{"projects"}, "projects"}, {[]string{"recent", "--limit", "1"}, "documents"}, {[]string{"doctor"}, "problems"}} {
			code, reply := j.run(t, one.args...)
			want := 0
			if one.args[0] == "index" {
				want = 3
				if populated {
					want = 1
				}
			} else if one.args[0] == "doctor" {
				want = 3
				if populated {
					want = 1
				}
			}
			if code != want {
				t.Fatal(code, reply)
			}
			items := reply[one.list].([]any)
			if reply["count"] != float64(len(items)) || reply["total"].(float64) < float64(len(items)) || reply["empty"] != (len(items) == 0) {
				t.Fatal(reply)
			}
		}
		if populated {
			_, reply := j.run(t, "projects")
			project := reply["projects"].([]any)[0].(map[string]any)
			if project["purpose"] != "Return a lease." || project["files"] != float64(2) || project["path"] != filepath.Join(j.source, "alpha") {
				t.Fatal(project)
			}
			old := time.Now().Add(-10 * 24 * time.Hour)
			os.Chtimes(filepath.Join(j.source, "alpha/PLAN.md"), old, old)
			if code, reply := j.run(t, "index"); code != 1 {
				t.Fatal(code, reply)
			}
			_, reply = j.run(t, "recent", "--days", "7")
			if reply["total"] != float64(1) {
				t.Fatal(reply)
			}
			_, reply = j.run(t, "recent", "--days", "30", "--limit", "1")
			if reply["count"] != float64(1) || reply["total"] != float64(2) {
				t.Fatal(reply)
			}
		}
	}
}
func TestDoctorDetectsCorruptIndex(t *testing.T) {
	j := newJourney(t)
	j.put(t, "README.md", "# Lease\n\nReturn a lease.")
	j.ready(t)
	if code, reply := j.run(t, "doctor"); code != 3 || reply["count"] != float64(0) {
		t.Fatal(code, reply)
	}
	os.Truncate(filepath.Join(j.state, "index.sqlite"), 1000)
	code, reply := j.run(t, "doctor")
	if code != 1 || reply["count"] != float64(1) || reply["problems"].([]any)[0].(map[string]any)["code"] != "index-damaged" {
		t.Fatal(code, reply)
	}
	if code, reply = j.run(t, "index", "--full"); code != 0 {
		t.Fatal(code, reply)
	}
	if code, reply = j.run(t, "doctor"); code != 3 {
		t.Fatal(code, reply)
	}
	db, err := sql.Open("sqlite", filepath.Join(j.state, "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("DROP TRIGGER chunks_delete;DELETE FROM chunks"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if code, reply = j.run(t, "doctor"); code != 1 || reply["problems"].([]any)[0].(map[string]any)["code"] != "index-damaged" {
		t.Fatal(code, reply)
	}
}
func TestDoctorRefusesRemoteFolder(t *testing.T) {
	j := newJourney(t)
	j.put(t, "README.md", "# Keeper\n\nThe keeper returns a lease.")
	j.ready(t)
	config, _ := os.ReadFile(filepath.Join(j.state, "config.json"))
	database, _ := os.ReadFile(filepath.Join(j.state, "index.sqlite"))
	alias := "/" + j.state
	if runtime.GOOS == "windows" {
		alias = testutil.RemoteState(runtime.GOOS)
	}
	for _, words := range [][]string{nil, {"init"}, {"index"}, {"search", "keeper"}, {"show", "id"}, {"projects"}, {"recent"}, {"map", "alpha"}, {"note", "A decision"}} {
		code, reply := j.run(t, append(words, "--state-dir", alias)...)
		if code != 1 || reply["error"].(map[string]any)["code"] != "state-remote" {
			t.Fatal(words, code, reply)
		}
	}
	afterConfig, _ := os.ReadFile(filepath.Join(j.state, "config.json"))
	afterDatabase, _ := os.ReadFile(filepath.Join(j.state, "index.sqlite"))
	if !bytes.Equal(config, afterConfig) || !bytes.Equal(database, afterDatabase) {
		t.Fatal("remote alias changed state")
	}
	for _, command := range []string{"init", "doctor"} {
		code, reply := j.run(t, command, "--state-dir", testutil.RemoteState(runtime.GOOS))
		if command == "init" {
			if code != 1 || reply["error"].(map[string]any)["code"] != "state-remote" {
				t.Fatal(code, reply)
			}
		} else if code != 1 || reply["problems"].([]any)[0].(map[string]any)["code"] != "state-remote" {
			t.Fatal(code, reply)
		}
	}
}
func TestHelpHasExamples(t *testing.T) {
	j := newJourney(t)
	j.put(t, "alpha/README.md", "# Alpha\n\nAlpha returns a lease with the full commit identifier.\n")
	j.ready(t)
	command := exec.Command(testBinary, "help")
	command.Dir = j.base
	command.Env = j.environment
	data, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	examples := []string{}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "  Example: ") {
			examples = append(examples, strings.TrimPrefix(line, "  Example: "))
		}
	}
	if len(examples) != 19 {
		t.Fatal("missing help examples", len(examples))
	}
	for i, example := range examples {
		words := testutil.Shell(runtime.GOOS, os.Getenv("SystemRoot"), example)
		command = exec.Command(words[0], words[1:]...)
		command.Dir = j.base
		command.Env = append(j.environment, "PATH=")
		var stderr bytes.Buffer
		command.Stderr = &stderr
		output, err := command.Output()
		code := 0
		if err != nil {
			exit, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatal(example, string(output), err)
			}
			code = exit.ExitCode()
		}
		if code != []int{0, 0, 0, 0, 0, 0, 0, 3, 3, 0, 0, 0, 0, 0, 0, 3, 0, 0, 0}[i] {
			t.Fatal(example, code, string(output))
		}
		if stderr.Len() != 0 {
			t.Fatal(stderr.String())
		}
	}
	for _, topic := range []string{"settings", "schedule", "sources", "exclude", "third-party", "init", "index", "search", "show", "projects", "recent", "note", "map", "doctor", "--version", "help", "--brief", "setup"} {
		command = exec.Command(testBinary, "help", topic)
		command.Dir = j.base
		command.Env = append(j.environment, "PATH=")
		data, err = command.Output()
		if err != nil || !strings.Contains(string(data), "Example:") {
			t.Fatal(topic, err, string(data))
		}
	}
	var reply map[string]any
	_, dataString := call(t, "--version")
	json.Unmarshal([]byte(dataString), &reply)
	if !reflect.DeepEqual(reply["version"], "0.1.0-dev") {
		t.Fatal(reply)
	}
}
