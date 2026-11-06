package main

import (
	"brain/internal/index"
	"brain/scripts/toolchain"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestIndexFinalReceiptFailure(t *testing.T) {
	j := newJourney(t)
	j.put(t, "README.md", "# Kept\n\nA retained garnet record.")
	j.ready(t)
	folder := t.TempDir()
	original := filepath.Join(repoRoot, "internal", "index", "freshness.go")
	data, err := os.ReadFile(original)
	if err != nil {
		t.Fatal(err)
	}
	needle := "func SaveRun(state string, run Run) error {"
	if strings.Count(string(data), needle) != 1 {
		t.Fatal("receipt boundary changed")
	}
	changed := strings.Replace(string(data), needle, needle+"\nif run.Outcome == \"complete\" { return os.ErrPermission }", 1)
	replacement := filepath.Join(folder, "freshness.go")
	if err = os.WriteFile(replacement, []byte(changed), 0600); err != nil {
		t.Fatal(err)
	}
	overlay, err := json.Marshal(struct{ Replace map[string]string }{map[string]string{original: replacement}})
	if err != nil {
		t.Fatal(err)
	}
	overlayPath := filepath.Join(folder, "overlay.json")
	if err = os.WriteFile(overlayPath, overlay, 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(folder, filepath.Base(testBinary))
	build := exec.Command(toolchain.Binary(), "build", "-trimpath", "-buildvcs=false", "-overlay", overlayPath, "-o", binary, "./cmd/brain")
	build.Dir = repoRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	command := exec.Command(binary, "index", "--format", "json")
	command.Dir, command.Env = j.base, j.environment
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err = command.Run()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 || stderr.Len() != 0 {
		t.Fatal(err, stdout.String(), stderr.String())
	}
	var reply struct {
		Error struct {
			Code     string
			Message  string
			NextArgv [][]string `json:"next_argv"`
		}
		New *int
	}
	if err = json.Unmarshal(stdout.Bytes(), &reply); err != nil || reply.New != nil || reply.Error.Code != "index-unavailable" || reply.Error.Message == "" || len(reply.Error.NextArgv) == 0 {
		t.Fatal(err, stdout.String())
	}
	receipt, present, err := index.LastRun(j.state)
	if err != nil || !present || receipt.Outcome != "failed" {
		t.Fatal(receipt, present, err)
	}
	if code, found := j.run(t, "search", "garnet"); code != 0 || found["total"] != float64(1) {
		t.Fatal(code, found)
	}
}
