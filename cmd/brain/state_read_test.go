package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestMalformedNoteStateCompletes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("filesystem named-pipe fixture requires Unix")
	}
	j := newJourney(t)
	j.put(t, "alpha/README.md", "# Alpha\n\nAmber herons keep records.")
	j.ready(t)
	if code, reply := j.run(t, "note", "retained decision", "--project", "alpha"); code != 0 {
		t.Fatal(code, reply)
	}
	path := filepath.Join(j.state, "notes", "alpha.md")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("mkfifo", "-m", "600", path).CombinedOutput(); err != nil {
		t.Fatalf("create owned pipe: %s, %v", output, err)
	}
	before, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"map", "alpha", "--no-refresh", "--format", "json"},
		{"note", "must not overwrite", "--project", "alpha", "--format", "json"},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		command := exec.CommandContext(ctx, testBinary, args...)
		command.Dir, command.Env = j.base, j.environment
		var stdout, stderr bytes.Buffer
		command.Stdout, command.Stderr = &stdout, &stderr
		err := command.Run()
		cancel()
		if ctx.Err() == context.DeadlineExceeded || stderr.Len() != 0 {
			t.Fatalf("malformed note command blocked or wrote stderr: %v, %v, %s", args, ctx.Err(), &stderr)
		}
		code := 0
		if err != nil {
			failure, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatal(err)
			}
			code = failure.ExitCode()
		}
		var reply map[string]any
		if err := json.Unmarshal(stdout.Bytes(), &reply); err != nil {
			t.Fatal(stdout.String(), err)
		}
		if args[0] == "map" {
			if code != 0 || reply["notes"].(map[string]any)["empty"] != true {
				t.Fatal("map did not safely omit malformed notes", code, reply)
			}
		} else if code != 1 || reply["error"].(map[string]any)["code"] != "notes-unwritable" {
			t.Fatal("note did not refuse malformed state", code, reply)
		}
		after, err := os.Lstat(path)
		if err != nil || after.Mode()&os.ModeNamedPipe == 0 || !os.SameFile(before, after) {
			t.Fatal("command replaced the malformed note", err)
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if code, reply := j.run(t, "map", "alpha", "--no-refresh"); code != 0 || reply["notes"].(map[string]any)["empty"] != false {
		t.Fatal("regular map did not recover", code, reply)
	}
	if code, reply := j.run(t, "note", "restored decision", "--project", "alpha"); code != 0 {
		t.Fatal("regular note did not recover", code, reply)
	}
	data, err := os.ReadFile(path)
	if err != nil || !bytes.HasPrefix(data, original) || !bytes.Contains(data, []byte("restored decision")) || bytes.Contains(data, []byte("must not overwrite")) {
		t.Fatal("regular notes were lost or malformed write was saved", err)
	}
}
