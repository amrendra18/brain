package main

import (
	"brain/scripts/toolchain"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"brain/internal/platform"
)

func TestNextPerShellNative(t *testing.T) {
	for _, shell := range []string{"sh", "powershell", "cmd", "powershell7"} {
		t.Run(shell, func(t *testing.T) {
			lineShell := shell
			if shell == "powershell7" {
				lineShell = "powershell"
			}
			probe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			words := []string{probe, "two spaces", "café 東京", `literal "double"`, "keeper’s ‘boat’", `end\`, "%NAME%", "!NAME!", "'apostrophe'"}
			line := platform.Command(lineShell, words)
			var command *exec.Cmd
			if shell == "sh" {
				if runtime.GOOS == "windows" {
					t.Skip("POSIX shell execution belongs to native Unix runners")
				}
				command = exec.Command("/bin/sh", "-c", line)
			} else {
				if runtime.GOOS != "windows" {
					t.Skip("native Windows shell execution is unproved on this host")
				}
				command = windowsShell(t, shell, line)
			}
			command.Env = append(os.Environ(), "PATH=", "BRAIN_TEST_ARGV=1", "NAME=must-not-expand")
			data, err := command.Output()
			if err != nil {
				t.Fatal(err, string(data))
			}
			var got []string
			if json.Unmarshal(data, &got) != nil || !reflect.DeepEqual(got, words[1:]) {
				t.Fatal(string(data), words)
			}
		})
	}
}

func TestRosettaJourney(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("Rosetta journey requires an Apple silicon Mac")
	}
	if data, err := exec.Command("/usr/bin/arch", "-x86_64", "/usr/bin/true").CombinedOutput(); err != nil {
		t.Skipf("Rosetta is absent: %v %s", err, data)
	}
	binary := filepath.Join(t.TempDir(), "brain-intel")
	command := exec.Command(toolchain.Binary(), "build", "-trimpath", "-o", binary, "./cmd/brain")
	command.Dir = repoRoot
	command.Env = append(os.Environ(), "GOOS=darwin", "GOARCH=amd64", "CGO_ENABLED=0")
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatal(err, string(data))
	}
	previous := testBinary
	testBinary = binary
	defer func() { testBinary = previous }()
	j := newJourney(t)
	j.put(t, "alpha/README.md", "# Harbour\n\nRosetta lighthouse keeper.")
	j.ready(t)
	code, reply := j.run(t, "search", "Rosetta lighthouse")
	if code != 0 || reply["total"] != float64(1) {
		t.Fatal(code, reply)
	}
	id := reply["results"].([]any)[0].(map[string]any)["id"].(string)
	if code, reply := j.run(t, "show", id); code != 0 || !bytes.Contains(mustJSON(t, reply), []byte("Rosetta lighthouse")) {
		t.Fatal(code, reply)
	}
	if code, reply := j.run(t, "note", "Rosetta saved decision", "--project", "alpha"); code != 0 {
		t.Fatal(code, reply)
	}
	if code, reply := j.run(t, "map", "alpha"); code != 0 || reply["index_age_s"] == nil {
		t.Fatal(code, reply)
	}
	if code, reply := j.run(t, "doctor"); code != 3 {
		t.Fatal(code, reply)
	}
	t.Log("Intel init, index, search, show, note, map and healthy doctor executed through Rosetta")
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
