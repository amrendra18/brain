package main

import (
	"brain/tests/testutil"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestMissingSourceTrailingSlash(t *testing.T) {
	j := newJourney(t)
	j.put(t, "README.md", "# Keeper\n\nA lasting beacon.")
	j.ready(t)
	path := filepath.Join(j.state, "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err = json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	config["sources"].([]any)[0].(map[string]any)["path"] = j.source + string(filepath.Separator)
	data, err = json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(j.source, j.source+"-moved"); err != nil {
		t.Fatal(err)
	}
	code, reply := j.run(t)
	if code != 0 || reply["index"].(map[string]any)["pending"] != float64(0) {
		t.Fatal(code, reply)
	}
	code, reply = j.run(t, "doctor")
	if code != 1 || reply["count"] != float64(1) || reply["problems"].([]any)[0].(map[string]any)["code"] != "index-stale" {
		t.Fatal(code, reply)
	}
	for _, entry := range reply["checks"].([]any) {
		check := entry.(map[string]any)
		if check["name"] == "pending" {
			t.Fatal(check)
		}
	}
}

func ownedTree(t *testing.T, root string) map[string][32]byte {
	t.Helper()
	result := map[string][32]byte{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		result[relative] = sha256.Sum256(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestDoctorGuidanceCarriesState(t *testing.T) {
	for _, marker := range []string{"CODEX_SANDBOX=seatbelt", "CLAUDE_CODE=1"} {
		t.Run(marker, func(t *testing.T) {
			j := newJourney(t)
			j.put(t, "README.md", "# Keeper\n\nA beacon.")
			j.ready(t)
			j.environment = append(j.environment, marker, "PATH=")
			if err := testutil.Permissions(t, j.state, 0500); err != nil {
				t.Fatal(err)
			}
			defer testutil.Permissions(t, j.state, 0700)
			before := ownedTree(t, j.base)
			code, reply := j.run(t, "doctor")
			if code != 1 {
				t.Fatal(code, reply)
			}
			problem := reply["problems"].([]any)[0].(map[string]any)
			if problem["code"] != "state-unwritable" {
				t.Fatal(problem)
			}
			argv := []string{}
			for _, word := range problem["next_argv"].([]any)[0].([]any) {
				argv = append(argv, word.(string))
			}
			if !reflect.DeepEqual(argv, []string{testBinary, "help", "doctor", "--state-dir", j.state, "--config", filepath.Join(j.state, "config.json")}) {
				t.Fatal(argv)
			}
			shell := testutil.Shell(runtime.GOOS, os.Getenv("SystemRoot"), problem["next"].([]any)[0].(string))
			for _, command := range []*exec.Cmd{exec.Command(argv[0], argv[1:]...), exec.Command(shell[0], shell[1:]...)} {
				command.Dir = j.base
				command.Env = j.environment
				var stderr bytes.Buffer
				command.Stderr = &stderr
				data, err := command.Output()
				if err != nil || stderr.Len() != 0 {
					t.Fatal(err, string(data), stderr.String())
				}
				if !strings.Contains(string(data), "Best effort") || !strings.Contains(string(data), strconv.Quote(j.state)) {
					t.Fatal(string(data))
				}
				if strings.HasPrefix(marker, "CODEX") && !strings.Contains(string(data), "writable_roots = ["+strconv.Quote(j.state)+"]") {
					t.Fatal(string(data))
				}
				if strings.HasPrefix(marker, "CLAUDE") && !strings.Contains(string(data), "sandbox.filesystem.allowWrite") {
					t.Fatal(string(data))
				}
			}
			if after := ownedTree(t, j.base); !reflect.DeepEqual(before, after) {
				t.Fatal("doctor/help changed files or wrote settings")
			}
		})
	}
}

func TestRemoteHomeRecovery(t *testing.T) {
	j := newJourney(t)
	home := "/Network/brain-review-home"
	if runtime.GOOS == "linux" {
		home = "/net/brain-review-home"
	}
	if runtime.GOOS == "windows" {
		home = `\\brain-test-server\home`
	}
	j.environment = []string{"PATH=", "HOME=" + home, "USERPROFILE=" + home, "LOCALAPPDATA=", "XDG_DATA_HOME=", "BRAIN_HOME=" + j.state, "SystemRoot=" + os.Getenv("SystemRoot")}
	before := ownedTree(t, j.base)
	code, reply := j.run(t, "init", "--state-dir", testutil.RemoteState(runtime.GOOS))
	if code != 1 {
		t.Fatal(code, reply)
	}
	problem := reply["error"].(map[string]any)
	if problem["code"] != "state-remote" {
		t.Fatal(problem)
	}
	argv := []string{}
	for _, word := range problem["next_argv"].([]any)[0].([]any) {
		argv = append(argv, word.(string))
	}
	if !reflect.DeepEqual(argv, []string{testBinary, "help", "init"}) {
		t.Fatal("recovery chose remote or temporary storage", argv)
	}
	shell := testutil.Shell(runtime.GOOS, os.Getenv("SystemRoot"), problem["next"].([]any)[0].(string))
	for _, command := range []*exec.Cmd{exec.Command(argv[0], argv[1:]...), exec.Command(shell[0], shell[1:]...)} {
		command.Dir = j.base
		command.Env = j.environment
		var stderr bytes.Buffer
		command.Stderr = &stderr
		data, err := command.Output()
		if err != nil || stderr.Len() != 0 || !strings.Contains(string(data), "Choose source folders") || !strings.Contains(string(data), "absolute local state folder") || !strings.Contains(string(data), "--state-dir DIR") {
			t.Fatal(err, string(data), stderr.String())
		}
	}
	if !reflect.DeepEqual(before, ownedTree(t, j.base)) {
		t.Fatal("remote recovery wrote files")
	}
}

func TestRemoteStateRecoveryInitializesLocalConfig(t *testing.T) {
	for _, selection := range []string{"implicit", "flag", "environment"} {
		t.Run(selection, func(t *testing.T) {
			j := newJourney(t)
			config := filepath.Join(j.base, "selected.json")
			args := []string{"init", "--state-dir", testutil.RemoteState(runtime.GOOS)}
			if selection == "flag" {
				args = append(args, "--config", config)
			}
			if selection == "environment" {
				j.environment = append(j.environment, "BRAIN_CONFIG="+config)
			}
			code, reply := j.run(t, args...)
			if code != 1 || reply["error"].(map[string]any)["code"] != "state-remote" {
				t.Fatal(code, reply)
			}
			failure := reply["error"].(map[string]any)
			argv := []string{}
			for _, word := range failure["next_argv"].([]any)[0].([]any) {
				argv = append(argv, word.(string))
			}
			if len(argv) < 2 || argv[1] != "init" {
				t.Fatal("no local initialization", argv)
			}
			selectedState, selectedConfig := "", ""
			for i, word := range argv {
				if i+1 == len(argv) {
					continue
				}
				if word == "--state-dir" {
					selectedState = argv[i+1]
				}
				if word == "--config" {
					selectedConfig = argv[i+1]
				}
			}
			relative, err := filepath.Rel(j.base, selectedState)
			if err != nil || !filepath.IsLocal(relative) {
				t.Fatal("recovery escaped owned home", argv, err)
			}
			want := config
			if selection == "implicit" {
				want = filepath.Join(selectedState, "config.json")
			}
			if selectedConfig != want {
				t.Fatal("wrong recovery config", argv, want)
			}
			follow := exec.Command(argv[0], argv[1:]...)
			follow.Dir, follow.Env = j.source, j.environment
			data, err := follow.Output()
			var initialized map[string]any
			if err != nil || json.Unmarshal(data, &initialized) != nil || initialized["config"] != want {
				t.Fatal("recovery failed", err, string(data))
			}
			if _, err := os.Stat(want); err != nil {
				t.Fatal("no config after recovery", err)
			}
		})
	}
}
