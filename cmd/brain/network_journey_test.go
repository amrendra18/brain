package main

import (
	"brain/scripts/toolchain"
	"brain/tests/testutil"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNoNetwork(t *testing.T) {
	var prefix []string
	switch runtime.GOOS {
	case "darwin":
		prefix = []string{"/usr/bin/sandbox-exec", "-p", "(version 1) (allow default) (deny network*)"}
	case "linux":
		unshare, err := exec.LookPath("unshare")
		if err != nil {
			t.Skip("unshare is unavailable; the source and dependency guards still run")
		}
		prefix = []string{unshare, "-r", "-n"}
	default:
		t.Skip("this system has no supported OS network-denial runner; its runtime proof remains unproved")
	}
	probe := filepath.Join(t.TempDir(), "network-probe")
	build := exec.Command(toolchain.Binary(), "build", "-trimpath", "-o", probe, "./tests/testdata/netprobe")
	build.Dir = repoRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatal("build network probe", string(output), err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("create local control listener", err)
	}
	defer listener.Close()
	address := listener.Addr().String()
	if output, err := exec.Command(probe, address, "allowed").CombinedOutput(); err != nil {
		t.Fatal(string(output), err)
	}
	denied := exec.Command(prefix[0], append(prefix[1:], probe, address, "blocked")...)
	if output, err := denied.CombinedOutput(); err != nil || !strings.Contains(string(output), "the OS blocked the connection") {
		t.Fatalf("required OS network proof: %q: %s (%v)", denied.Args, string(output), err)
	}
	j := newJourney(t)
	j.put(t, "alpha/README.md", "# Keeper\n\nThe lighthouse keeper tends the lamp.")
	runSandbox := func(arguments ...string) map[string]any {
		args := append(append(append([]string{}, prefix[1:]...), testBinary), arguments...)
		format := "json"
		if len(arguments) > 0 && arguments[0] == "--brief" {
			format = "copilot"
		}
		args = append(args, "--format", format)
		cmd := exec.Command(prefix[0], args...)
		cmd.Dir = j.base
		cmd.Env = j.environment
		var output, stderr bytes.Buffer
		cmd.Stdout = &output
		cmd.Stderr = &stderr
		err := cmd.Run()
		if len(arguments) > 0 && arguments[0] == "doctor" {
			if failure, ok := err.(*exec.ExitError); ok && failure.ExitCode() == 3 {
				err = nil
			}
		}
		if err != nil || stderr.Len() != 0 {
			t.Fatalf("network-denied %v: stdout %s, stderr %s (%v)", arguments, output.String(), stderr.String(), err)
		}
		var reply map[string]any
		if err := json.Unmarshal(output.Bytes(), &reply); err != nil {
			t.Fatal("sandboxed command returned invalid JSON", err)
		}
		return reply
	}
	initialized := runSandbox("init", "--source", j.source)
	if initialized["sources"] != float64(1) || initialized["state_dir"] != j.state {
		t.Fatal(initialized)
	}
	indexed := runSandbox("index")
	if indexed["new"] != float64(1) || indexed["chunks"] != float64(1) || indexed["count"] != float64(0) {
		t.Fatal(indexed)
	}
	found := runSandbox("search", "lighthouse keeper", "--no-refresh")
	if found["total"] != float64(1) || found["count"] != float64(1) || found["index_age_s"] == nil {
		t.Fatal(found)
	}
	item := result(t, found, 0)
	if item["path"] != filepath.Join(j.source, "alpha", "README.md") || !strings.Contains(item["snippet"].(string), "lighthouse keeper") {
		t.Fatal(item)
	}
	shown := runSandbox("show", item["id"].(string), "--no-refresh")
	if shown["id"] != item["id"] || shown["path"] != item["path"] || shown["index_age_s"] == nil || shown["text"] != "# Keeper\n\nThe lighthouse keeper tends the lamp." {
		t.Fatal(shown)
	}
	view := runSandbox()
	if view["index"].(map[string]any)["files"] != float64(1) || view["index"].(map[string]any)["index_age_s"] == nil {
		t.Fatal(view)
	}
	projects := runSandbox("projects")
	if projects["count"] != float64(1) || projects["index_age_s"] == nil || projects["projects"].([]any)[0].(map[string]any)["purpose"] != "The lighthouse keeper tends the lamp." {
		t.Fatal(projects)
	}
	recent := runSandbox("recent")
	if recent["count"] != float64(1) || recent["index_age_s"] == nil || recent["documents"].([]any)[0].(map[string]any)["path"] != item["path"] {
		t.Fatal(recent)
	}
	doctor := runSandbox("doctor")
	if doctor["count"] != float64(0) || doctor["empty"] != true {
		t.Fatal(doctor)
	}
	note := runSandbox("note", "A network-free decision about harbour ropes", "--project", "alpha")
	if note["indexed"] != true || note["path"] != filepath.Join(j.state, "notes", "alpha.md") {
		t.Fatal(note)
	}
	foundNote := runSandbox("search", "harbour ropes", "--no-refresh")
	if foundNote["total"] != float64(1) || foundNote["index_age_s"] == nil || result(t, foundNote, 0)["id"] != note["entry_id"] {
		t.Fatal(foundNote)
	}
	j.put(t, "alpha/talk.rtf", `{\rtf1 Converted transcript about ferry schedules.\par}`)
	converted := runSandbox("index")
	if converted["new"] != float64(1) || converted["count"] != float64(0) {
		t.Fatal(converted)
	}
	foundTranscript := runSandbox("search", "ferry schedules", "--no-refresh")
	if foundTranscript["total"] != float64(1) || foundTranscript["index_age_s"] == nil || result(t, foundTranscript, 0)["kind"] != "transcript" {
		t.Fatal(foundTranscript)
	}
	brief := runSandbox("--brief")
	if len(brief) != 1 || !strings.Contains(brief["additionalContext"].(string), "3 files, 3 chunks") {
		t.Fatal(brief)
	}
	testutil.Repository(t, filepath.Join(j.source, "alpha"), "The local keeper commit")
	mapped := runSandbox("map", "alpha", "--no-refresh")
	if mapped["project"] != "alpha" || mapped["index_age_s"] == nil || mapped["notes"].(map[string]any)["count"] != float64(1) {
		t.Fatal(mapped)
	}
	if runtime.GOOS == "darwin" && mapped["commit"].(map[string]any)["subject"] != "The local keeper commit" {
		t.Fatal(mapped)
	}
	setup := runSandbox("setup", "all")
	if setup["count"] != float64(4) {
		t.Fatal(setup)
	}
	fmt.Fprintln(os.Stderr, "OS network proof: the control connection opened; the OS blocked the sandboxed connection; init/index/search/show/state/projects/recent/note/map and RTF conversion returned exit 0; healthy doctor returned exit 3")
}
