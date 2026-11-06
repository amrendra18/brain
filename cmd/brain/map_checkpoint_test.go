package main

import (
	"brain/scripts/toolchain"
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMapCheckpointInterleaving(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("deterministic OS write-denial interleaving requires macOS sandbox-exec")
	}
	j := newJourney(t)
	j.put(t, "alpha/README.md", "# Alpha\n\nThe old keeper purpose.")
	j.put(t, "alpha/PLAN.md", "# Plan\n\n## Status\n"+strings.Repeat("Old milestone keeper "+strings.Repeat("x", 70)+"\n", 4000))
	j.ready(t)
	code, expected := j.run(t, "map", "alpha", "--no-refresh")
	if code != 0 {
		t.Fatal(code, expected)
	}
	folder := t.TempDir()
	replace := map[string]string{}
	for _, item := range []struct{ name, needle, change string }{
		{"mapper/mapper.go", "\tnames := []string{}", `	fmt.Fprintln(os.Stderr, "MAP_FACTS_READY")
	deadline := time.Now().Add(10*time.Second)
	for { if _, problem := os.Stat(os.Getenv("BRAIN_TEST_RELEASE_BRIEF")); problem == nil { break }; if time.Now().After(deadline) { panic("map barrier timed out") }; time.Sleep(time.Millisecond) }
	names := []string{}`},
		{"cli/state.go", "\tlock, err := platform.Acquire(call.state, \"index\", true)", "\tfmt.Fprintln(os.Stderr, \"INDEX_LOCK_READY\")\n\tlock, err := platform.Acquire(call.state, \"index\", true)"},
	} {
		original := filepath.Join(repoRoot, "internal", filepath.FromSlash(item.name))
		data, err := os.ReadFile(original)
		if err != nil {
			t.Fatal(err)
		}
		source := string(data)
		if strings.Count(source, item.needle) != 1 {
			t.Fatal("the read/write boundary changed", item.name)
		}
		source = strings.Replace(source, item.needle, item.change, 1)
		if !strings.Contains(source, "\n\t\"fmt\"") {
			source = strings.Replace(source, "import (", "import (\n\"fmt\"", 1)
		}
		if !strings.Contains(source, "\n\t\"os\"") {
			source = strings.Replace(source, "import (", "import (\n\"os\"", 1)
		}
		if item.name == "mapper/mapper.go" {
			source = strings.Replace(source, "import (", "import (\n\"time\"", 1)
		}
		changed := filepath.Join(folder, strings.ReplaceAll(item.name, "/", "_"))
		if err = os.WriteFile(changed, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
		replace[original] = changed
	}
	encoded, err := json.Marshal(struct{ Replace map[string]string }{replace})
	if err != nil {
		t.Fatal(err)
	}
	overlay := filepath.Join(folder, "overlay.json")
	if err = os.WriteFile(overlay, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(folder, "brain-interleave")
	build := exec.Command(toolchain.Binary(), "build", "-trimpath", "-buildvcs=false", "-overlay", overlay, "-o", binary, "./cmd/brain")
	build.Dir = repoRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	release := filepath.Join(folder, "release")
	start := func(args ...string) (*exec.Cmd, *bytes.Buffer, <-chan string) {
		t.Helper()
		command := exec.Command(binary, args...)
		if len(args) > 0 && args[0] == "map" {
			profile := fmt.Sprintf("(version 1) (allow default) (deny file-write* (subpath %q))", j.state)
			command = exec.Command("/usr/bin/sandbox-exec", append([]string{"-p", profile, binary}, args...)...)
		}
		command.Dir, command.Env = filepath.Join(j.source, "alpha"), append(j.environment, "BRAIN_TEST_RELEASE_BRIEF="+release)
		output := &bytes.Buffer{}
		command.Stdout = output
		stderr, err := command.StderrPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err = command.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { command.Process.Kill() })
		lines := make(chan string, 8)
		go func() {
			defer close(lines)
			scanner := bufio.NewScanner(stderr)
			for scanner.Scan() {
				lines <- scanner.Text()
			}
		}()
		return command, output, lines
	}
	marker := func(lines <-chan string, want string) {
		t.Helper()
		select {
		case got := <-lines:
			if got != want {
				t.Fatal("unexpected interleaving marker", got, want)
			}
		case <-time.After(15 * time.Second):
			t.Fatal("interleaving marker timed out", want)
		}
	}
	reader, reply, lines := start("map", "alpha", "--no-refresh", "--format", "json")
	marker(lines, "MAP_FACTS_READY")
	j.put(t, "alpha/README.md", "# Alpha\n\nThe new heron purpose.")
	j.put(t, "alpha/PLAN.md", "# Plan\n\n## Status\n"+strings.Repeat("New milestone heron "+strings.Repeat("y", 70)+"\n", 4000))
	j.put(t, "alpha/new.md", "# New\n\nA second beacon.")
	writer, written, lines := start("index", "--format", "json")
	marker(lines, "INDEX_LOCK_READY")
	if err = os.WriteFile(release, []byte("read may finish"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = reader.Wait(); err != nil {
		t.Fatal(err, reply.String())
	}
	var page map[string]any
	if err = json.Unmarshal(reply.Bytes(), &page); err != nil {
		t.Fatal(err, reply.String())
	}
	if page["read_only"] != true || page["purpose"] != "The old keeper purpose." || !strings.HasPrefix(page["milestones"].(map[string]any)["lines"].([]any)[0].(string), "Old milestone keeper") {
		t.Fatal("the map mixed checkpoints", page)
	}
	for _, key := range []string{"read_only", "index_age_s", "next", "next_argv"} {
		delete(page, key)
		delete(expected, key)
	}
	if !reflect.DeepEqual(page, expected) {
		t.Fatal("the map lost part of its old snapshot", page, expected)
	}
	if err = writer.Wait(); err != nil {
		t.Fatal(err, written.String())
	}
	code, after := j.run(t)
	if code != 0 || after["index"].(map[string]any)["files"] != float64(3) {
		t.Fatal(code, after)
	}
}
