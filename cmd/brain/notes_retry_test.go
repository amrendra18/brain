package main

import (
	"brain/internal/notes"
	"brain/tests/testutil"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestOversizedNoteHelpNeverSaves(t *testing.T) {
	for _, shell := range []bool{false, true} {
		j := newJourney(t)
		j.put(t, "alpha/README.md", "# Alpha\n\nA harbour.")
		j.ready(t)
		text := strings.Repeat("界", notes.LimitBytes/3+1)
		code, reply := j.run(t, "note", text, "--project", "alpha")
		if code != 2 {
			t.Fatal(code, reply)
		}
		failure := reply["error"].(map[string]any)
		if failure["code"] != "note-too-long" || len(failure["next_argv"].([]any)) != 1 {
			t.Fatal(failure)
		}
		for i, raw := range failure["next_argv"].([]any) {
			words := []string{}
			for _, arg := range raw.([]any) {
				words = append(words, arg.(string))
			}
			if shell {
				words = testutil.Shell(runtime.GOOS, os.Getenv("SystemRoot"), failure["next"].([]any)[i].(string))
			}
			command := exec.Command(words[0], words[1:]...)
			command.Dir, command.Env = j.base, append(j.environment, "PATH=")
			var output, stderr bytes.Buffer
			command.Stdout, command.Stderr = &output, &stderr
			if err := command.Run(); err != nil || stderr.Len() != 0 || !strings.Contains(output.String(), "65,536") {
				t.Fatal(shell, err, output.String(), stderr.String())
			}
		}
		if _, err := os.Stat(filepath.Join(j.state, "notes", "alpha.md")); !os.IsNotExist(err) {
			t.Fatal("refusal or help saved a note", err)
		}
	}
}
