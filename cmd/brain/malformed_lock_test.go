//go:build !windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestBriefMalformedLockRefusesPromptly(t *testing.T) {
	j := newJourney(t)
	j.put(t, "README.md", "# Beacon\n\nA lighthouse.")
	j.ready(t)
	database := filepath.Join(j.state, "index.sqlite")
	before, err := os.ReadFile(database)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(j.state, "locks", "index.lock")
	original := name + ".saved"
	if err = os.Rename(name, original); err != nil {
		t.Fatal(err)
	}
	if err = syscall.Mkfifo(name, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, testBinary, "--brief", "--format=json")
	command.Dir, command.Env = j.base, j.environment
	started := time.Now()
	output, runErr := command.CombinedOutput()
	elapsed := time.Since(started)
	if ctx.Err() != nil || elapsed >= 2*time.Second {
		t.Fatalf("pipe refusal exceeded two seconds: %v; %s", elapsed, output)
	}
	var reply map[string]any
	if runErr != nil || json.Unmarshal(output, &reply) != nil || reply["ready"] != false || reply["problem"] == "" {
		t.Fatalf("malformed lock was not refused: %v; %s", runErr, output)
	}
	if err = os.Remove(name); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(original, name); err != nil {
		t.Fatal(err)
	}
	if code, reply := j.run(t, "--brief"); code != 0 || reply["ready"] != true {
		t.Fatal("restored regular lock refused", code, reply)
	}
	after, err := os.ReadFile(database)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("database changed during malformed-lock refusal or restoration", err)
	}
	t.Logf("pipe refused in %s; restored brief returned exit 0; database bytes unchanged", elapsed)
}
