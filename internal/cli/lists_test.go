package cli

import (
	"brain/tests/testutil"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestStateAndListReplies(t *testing.T) {
	base := t.TempDir()
	source, folder := filepath.Join(base, "corpus"), filepath.Join(base, "state")
	os.MkdirAll(filepath.Join(source, "alpha"), 0700)
	os.WriteFile(filepath.Join(source, "alpha", "README.md"), []byte("# Alpha\n\nReturn a lease."), 0600)
	os.WriteFile(filepath.Join(source, "alpha", "PLAN.md"), []byte("# Plan\n\nThe beacon plan."), 0600)
	t.Setenv("BRAIN_HOME", folder)
	t.Setenv("HOME", base)
	t.Setenv("USERPROFILE", base)
	var output bytes.Buffer
	run := func(arguments ...string) int { output.Reset(); return Run(arguments, &output) }
	if run() != 1 || !strings.Contains(output.String(), "state-uninitialised") {
		t.Fatal(output.String())
	}
	if run("init", "--source", source) != 0 {
		t.Fatal(output.String())
	}
	if run("--format", "json") != 0 {
		t.Fatal(output.String())
	}
	var view viewReply
	if json.Unmarshal(output.Bytes(), &view) != nil || view.Index.Present || view.Index.Pending != 0 || view.Index.PendingKnown {
		t.Fatal(view, output.String())
	}
	if run() != 0 || !strings.Contains(output.String(), "Index: not built yet.") {
		t.Fatal(output.String())
	}
	for _, name := range []string{"projects", "recent"} {
		if run(name) != 1 || !strings.Contains(output.String(), "index-missing") {
			t.Fatal(output.String())
		}
	}
	if run("index") != 0 {
		t.Fatal(output.String())
	}
	if run("projects", "--format", "json") != 0 {
		t.Fatal(output.String())
	}
	var projects projectsReply
	json.Unmarshal(output.Bytes(), &projects)
	if projects.Count != 1 || projects.Total != 1 || projects.Empty || projects.Projects[0].Purpose != "Return a lease." || projects.IndexAgeS != 0 {
		t.Fatal(projects)
	}
	if run("recent", "--limit", "1", "--format", "json") != 0 {
		t.Fatal(output.String())
	}
	var recent recentReply
	json.Unmarshal(output.Bytes(), &recent)
	if recent.Count != 1 || recent.Total != 2 || recent.Days != 7 || recent.Empty {
		t.Fatal(recent)
	}
	for _, args := range [][]string{nil, {"projects"}, {"recent"}, {"doctor"}, {"doctor", "--format", "json"}, {"help", "projects"}, {"help", "recent"}, {"help", "doctor"}, {"help", "--version"}, {"help", "help"}, {"help", ""}} {
		code := run(args...)
		if code != 0 && code != 3 {
			t.Fatal(args, output.String())
		}
		if Run(args, closedWriter{}) != 1 {
			t.Fatal("closed output accepted", args)
		}
	}
	if run("doctor", "--format", "json") != 3 {
		t.Fatal(output.String())
	}
	var doctor doctorReply
	json.Unmarshal(output.Bytes(), &doctor)
	if !doctor.Empty || doctor.Count != 0 || doctor.Problems == nil || len(doctor.Checks) != 19 {
		t.Fatal(doctor)
	}
	for _, args := range [][]string{{"recent", "--days", "0"}, {"recent", "--limit", "101"}, {"projects", "extra"}, {"--format", "json", "extra"}, {"doctor", "--full"}} {
		if run(args...) != 2 {
			t.Fatal(args, output.String())
		}
	}
	testutil.Permissions(t, folder, 0500)
	defer testutil.Permissions(t, folder, 0700)
	if run("doctor", "--format", "json") != 1 {
		t.Fatal(output.String())
	}
	json.Unmarshal(output.Bytes(), &doctor)
	if doctor.Problems[0].Code != "state-unwritable" {
		t.Fatal(doctor)
	}
	for _, name := range []string{"projects", "recent"} {
		if run(name, "--format", "json") != 0 || !strings.Contains(output.String(), `"read_only":true`) || !strings.Contains(output.String(), "doctor") {
			t.Fatal(output.String())
		}
	}
	testutil.Permissions(t, folder, 0700)
	os.Rename(source, source+"-moved")
	if run() != 0 || !strings.Contains(output.String(), "Pending changes: unknown") {
		t.Fatal(output.String())
	}
	if run("doctor") != 3 {
		t.Fatal(output.String())
	}
	if run("projects") != 0 {
		t.Fatal(output.String())
	}
	os.Rename(source+"-moved", source)
	os.WriteFile(filepath.Join(folder, "index.sqlite"), []byte("damaged database"), 0600)
	if run("--format", "json") != 1 || !strings.Contains(output.String(), "index-missing") {
		t.Fatal(output.String())
	}
	if run("doctor", "--format", "json") != 1 {
		t.Fatal(output.String())
	}
	json.Unmarshal(output.Bytes(), &doctor)
	if doctor.Problems[0].Code != "index-damaged" {
		t.Fatal(doctor)
	}
	if run("doctor") != 1 {
		t.Fatal(output.String())
	}
	if run("init", "--state-dir", testutil.RemoteState(runtime.GOOS)) != 1 || !strings.Contains(output.String(), "state-remote") {
		t.Fatal(output.String())
	}
	if run("doctor", "--state-dir", testutil.RemoteState(runtime.GOOS), "--format", "json") != 1 {
		t.Fatal(output.String())
	}
	if run("doctor", "--state-dir", filepath.Join(base, "missing"), "--format", "json") != 1 {
		t.Fatal(output.String())
	}
	for _, args := range [][]string{{"help", "doctor", "--state-dir", folder}, {"help", "doctor", "--state-dir=" + folder}} {
		if run(args...) != 0 || !strings.Contains(output.String(), "Guidance for this state folder:") {
			t.Fatal(output.String())
		}
		if Run(args, closedWriter{}) != 1 {
			t.Fatal("closed scoped help output accepted")
		}
	}
	for _, args := range [][]string{{"help", "doctor", "extra"}, {"help", "doctor", "--nope"}, {"help", "doctor", "--state-dir"}, {"help", "doctor", "--state-dir", "relative"}} {
		if run(args...) != 2 {
			t.Fatal(args, output.String())
		}
	}
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	c := &call{}
	if got := c.localRecovery(); len(got) != 2 || got[0] != "help" {
		t.Fatal(got)
	}
}
