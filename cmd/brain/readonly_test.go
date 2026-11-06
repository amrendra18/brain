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
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestReadOnlyFallback(t *testing.T) {
	for _, name := range []string{"checkpointed", "live-WAL", "orphan-WAL"} {
		live := name == "live-WAL"
		t.Run(name, func(t *testing.T) {
			j := newJourney(t)
			j.put(t, "alpha/README.md", "# Keeper\n\nOld lighthouse.")
			j.ready(t)
			if live {
				observer, err := sql.Open("sqlite", filepath.Join(j.state, "index.sqlite"))
				if err != nil {
					t.Fatal(err)
				}
				defer observer.Close()
				var count int
				if err = observer.QueryRow("SELECT count(*) FROM chunks").Scan(&count); err != nil {
					t.Fatal(err)
				}
				j.put(t, "alpha/README.md", "# Keeper\n\nA new heron appears in the committed write-ahead log.")
				if code, reply := j.run(t, "index"); code != 0 {
					t.Fatal(code, reply)
				}
				wal, err := os.Stat(filepath.Join(j.state, "index.sqlite-wal"))
				if err != nil || wal.Size() == 0 {
					t.Fatal("live WAL was not created", err)
				}
			}
			word := "lighthouse"
			if live {
				word = "heron"
			}
			if code, reply := j.run(t, "search", word, "--no-refresh"); code != 0 || reply["total"] != float64(1) {
				t.Fatal(code, reply)
			}
			if err := testutil.Permissions(t, j.state, 0500); err != nil {
				t.Fatal(err)
			}
			code, reply := j.run(t, "search", word, "--no-refresh")
			noteCode, noteReply := j.run(t, "note", "This denied note must not be saved", "--project", "alpha")
			testutil.Permissions(t, j.state, 0700)
			if code != 0 || reply["total"] != float64(1) || reply["read_only"] != true || reply["index_age_s"] == nil {
				t.Fatal("read-only mode lost committed data", code, reply)
			}
			if noteCode != 1 || noteReply["error"].(map[string]any)["code"] != "state-unwritable" || !strings.Contains(noteReply["error"].(map[string]any)["next"].([]any)[0].(string), "doctor") {
				t.Fatal("read-only note refusal lacks write guidance", noteCode, noteReply)
			}
			if name == "orphan-WAL" {
				helper := exec.Command(os.Args[0])
				helper.Env = append(os.Environ(), "BRAIN_TEST_ORPHAN_WAL="+j.state)
				if data, err := helper.CombinedOutput(); err != nil {
					t.Fatal(err, string(data))
				}
				if err := os.Remove(filepath.Join(j.state, "index.sqlite-shm")); err != nil {
					t.Fatal(err)
				}
				wal, err := os.Stat(filepath.Join(j.state, "index.sqlite-wal"))
				if err != nil || wal.Size() == 0 {
					t.Fatal("no committed orphan WAL", err)
				}
				before := treeBytes(t, j.state)
				for _, format := range []string{"text", "copilot", "vscode"} {
					brief := exec.Command(testBinary, "--brief", "--format", format)
					brief.Dir = filepath.Join(j.source, "alpha")
					brief.Env = j.environment
					data, err := brief.Output()
					if err != nil || !strings.Contains(string(data), "1 file, 1 chunks") || !strings.Contains(string(data), "lighthouse") || !strings.Contains(string(data), "last scan 1") {
						t.Fatal(err, string(data))
					}
					if !reflect.DeepEqual(before, treeBytes(t, j.state)) {
						t.Fatal("orphan WAL brief created or changed files")
					}
				}
			}
			if runtime.GOOS != "darwin" {
				return
			}
			profile := "(version 1) (allow default) (deny file-write* (subpath " + strconv.Quote(j.state) + "))"
			beforeBrief := treeBytes(t, j.state)
			for _, format := range []string{"text", "copilot", "vscode"} {
				brief := exec.Command("/usr/bin/sandbox-exec", "-p", profile, testBinary, "--brief", "--format", format, "--state-dir", j.state)
				brief.Dir = filepath.Join(j.source, "alpha")
				brief.Env = j.environment
				var stderr bytes.Buffer
				brief.Stderr = &stderr
				data, err := brief.Output()
				if err != nil || stderr.Len() != 0 {
					t.Fatal("OS write-denied brief", name, format, err, string(data), stderr.String())
				}
				text := string(data)
				if format != "text" {
					var wrapper map[string]any
					if json.Unmarshal(data, &wrapper) != nil || len(wrapper) != 1 {
						t.Fatal(string(data))
					}
					if format == "copilot" {
						text = wrapper["additionalContext"].(string)
					} else {
						text = wrapper["hookSpecificOutput"].(map[string]any)["additionalContext"].(string)
					}
				}
				if !strings.Contains(text, "1 file, 1 chunks") || !strings.Contains(text, "last scan") || !strings.Contains(text, word) || !strings.Contains(text, "stored read-only snapshot") || !strings.Contains(text, "Run index explicitly to refresh sources.") {
					t.Fatal("brief lost committed snapshot", text)
				}
				if form := briefCommandForm(t, text, testBinary, j.state); form == "both" && !strings.Contains(text, `"doctor"`) || form != "both" && form != "search" {
					t.Fatal("brief lost doctor guidance", text)
				}
				if name == "orphan-WAL" {
					found := regexp.MustCompile(`last scan (\d+) s ago`).FindStringSubmatch(text)
					if len(found) != 2 {
						t.Fatal(text)
					}
					age, _ := strconv.Atoi(found[1])
					if age < 1000000000 {
						t.Fatal("brief ignored committed WAL age", text)
					}
				}
				if !reflect.DeepEqual(beforeBrief, treeBytes(t, j.state)) {
					t.Fatal("OS write-denied brief changed state", name, format)
				}
			}
			cmd := exec.Command("/usr/bin/sandbox-exec", "-p", profile, testBinary, "search", word, "--no-refresh", "--state-dir", j.state, "--format", "json")
			cmd.Dir = j.base
			cmd.Env = j.environment
			var output, stderr bytes.Buffer
			cmd.Stdout = &output
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil || stderr.Len() != 0 {
				t.Fatalf("required OS write-denied %s proof: %q: stdout %s, stderr %s (%v)", name, cmd.Args, output.String(), stderr.String(), err)
			}
			var sandboxed map[string]any
			if err := json.Unmarshal(output.Bytes(), &sandboxed); err != nil {
				t.Fatal(err)
			}
			if sandboxed["total"] != float64(1) || sandboxed["read_only"] != true || sandboxed["index_age_s"] == nil {
				t.Fatal("OS write-denied read lost committed data", sandboxed)
			}
			id := result(t, sandboxed, 0)["id"].(string)
			for _, words := range [][]string{nil, {"show", id}, {"projects"}, {"recent"}, {"map", "alpha"}, {"doctor"}} {
				args := append([]string{"-p", profile, testBinary}, words...)
				args = append(args, "--state-dir", j.state, "--format", "json")
				cmd = exec.Command("/usr/bin/sandbox-exec", args...)
				cmd.Dir = j.base
				cmd.Env = j.environment
				output.Reset()
				stderr.Reset()
				cmd.Stdout = &output
				cmd.Stderr = &stderr
				err := cmd.Run()
				wantCode := 0
				if len(words) > 0 && words[0] == "doctor" {
					wantCode = 1
				}
				if cmd.ProcessState == nil {
					t.Fatal(words, "command did not start", err)
				}
				if gotCode := cmd.ProcessState.ExitCode(); gotCode != wantCode || stderr.Len() != 0 {
					t.Fatal(words, "exit", gotCode, "want", wantCode, output.String(), stderr.String(), err)
				}
				sandboxed = map[string]any{}
				if err := json.Unmarshal(output.Bytes(), &sandboxed); err != nil {
					t.Fatal(err)
				}
				if len(words) > 0 && words[0] == "doctor" {
					if sandboxed["count"] != float64(1) || sandboxed["problems"].([]any)[0].(map[string]any)["code"] != "state-unwritable" {
						t.Fatal(sandboxed)
					}
					continue
				}
				if sandboxed["read_only"] != true || !strings.Contains(sandboxed["next"].([]any)[0].(string), "doctor") {
					t.Fatal(sandboxed)
				}
				if len(words) == 0 {
					if sandboxed["index"].(map[string]any)["files"] != float64(1) {
						t.Fatal(sandboxed)
					}
				} else if words[0] == "show" {
					if !strings.Contains(sandboxed["text"].(string), word) {
						t.Fatal(sandboxed)
					}
				} else if words[0] == "map" {
					if sandboxed["project"] != "alpha" || !strings.Contains(sandboxed["purpose"].(string), word) || sandboxed["index_age_s"] == nil {
						t.Fatal(sandboxed)
					}
				} else if sandboxed["count"] != float64(1) {
					t.Fatal(sandboxed)
				}
			}
		})
	}
}
