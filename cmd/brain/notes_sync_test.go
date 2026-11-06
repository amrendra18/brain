package main

import (
	"brain/scripts/toolchain"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNoteDirectorySyncFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no directory sync; the post-rename Unix failure is tested on Unix")
	}
	j := newJourney(t)
	j.put(t, "alpha/README.md", "# Alpha\n\nA harbour.")
	j.ready(t)
	folder := t.TempDir()
	original := filepath.Join(repoRoot, "internal", "platform", "unix.go")
	data, err := os.ReadFile(original)
	if err != nil {
		t.Fatal(err)
	}
	// Only the last directory flush fails. File Sync and rename stay real.
	old := "return file.Sync()"
	if strings.Count(string(data), old) != 1 {
		t.Fatal("directory flush boundary changed; review this fault fixture")
	}
	changed := strings.Replace(string(data), old, "return syscall.EIO", 1)
	faultSource := filepath.Join(folder, "unix.go")
	if err = os.WriteFile(faultSource, []byte(changed), 0600); err != nil {
		t.Fatal(err)
	}
	overlay := filepath.Join(folder, "overlay.json")
	encoded, err := json.Marshal(struct{ Replace map[string]string }{map[string]string{original: faultSource}})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(overlay, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	faultBinary := filepath.Join(folder, "brain-fault")
	build := exec.Command(toolchain.Binary(), "build", "-trimpath", "-buildvcs=false", "-overlay", overlay, "-o", faultBinary, "./cmd/brain")
	build.Dir = repoRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	command := exec.Command(faultBinary, "note", "Saved before the directory flush failed", "--project", "alpha", "--format", "json")
	command.Dir, command.Env = j.base, j.environment
	var output, stderr bytes.Buffer
	command.Stdout, command.Stderr = &output, &stderr
	err = command.Run()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 1 || stderr.Len() != 0 {
		t.Fatal(err, output.String(), stderr.String())
	}
	var reply map[string]any
	if err = json.Unmarshal(output.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	failure := reply["error"].(map[string]any)
	if failure["code"] != "notes-unwritable" || strings.Contains(failure["message"].(string), "Nothing was saved") || !strings.Contains(failure["message"].(string), "Check the notes before retrying") {
		t.Fatal(reply)
	}
	saved, err := os.ReadFile(filepath.Join(j.state, "notes", "alpha.md"))
	if err != nil || strings.Count(string(saved), "Saved before the directory flush failed") != 1 {
		t.Fatal(string(saved), err)
	}
	args := []string{}
	for _, arg := range failure["next_argv"].([]any)[0].([]any) {
		args = append(args, arg.(string))
	}
	if args[1] != "doctor" {
		t.Fatal("save refusal repeats the note", args)
	}
	follow := exec.Command(args[0], args[1:]...)
	follow.Dir, follow.Env = j.base, append(j.environment, "PATH=")
	if output, err := follow.Output(); err == nil {
		t.Fatal("healthy doctor must exit 3", string(output))
	} else if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 3 || len(exit.Stderr) != 0 {
		t.Fatal(err, string(output))
	}
	code, found := j.run(t, "search", "directory flush failed")
	if code != 0 || found["total"] != float64(0) {
		t.Fatal("failed note save unexpectedly refreshed the index", code, found)
	}
	if code, reply := j.run(t, "index"); code != 0 {
		t.Fatal(code, reply)
	}
	code, found = j.run(t, "search", "directory flush failed")
	if code != 0 || found["total"] != float64(1) {
		t.Fatal(code, found)
	}
}
