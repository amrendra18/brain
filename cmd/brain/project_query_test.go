package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAmbiguousProjectSelection(t *testing.T) {
	j := newJourney(t)
	j.put(t, "one/Alpha/README.md", "# North\n\nNorthern lighthouse.")
	j.put(t, "two/alpha/README.md", "# South\n\nSouthern lighthouse.")
	control := j.put(t, "three/Gamma/README.md", "# Control\n\nControl lighthouse.")
	if code, reply := j.run(t, "init",
		"--source", filepath.Join(j.source, "one", "Alpha"),
		"--source", filepath.Join(j.source, "two", "alpha"),
		"--source", filepath.Join(j.source, "three", "Gamma")); code != 0 {
		t.Fatal(code, reply)
	}
	if code, reply := j.run(t, "index"); code != 0 {
		t.Fatal(code, reply)
	}
	if code, reply := j.run(t, "search", "lighthouse", "--no-refresh"); code != 0 || reply["total"] != float64(3) {
		t.Fatal("unfiltered control", code, reply)
	}
	for _, project := range []string{"Alpha", "alpha", "ALPHA"} {
		for _, args := range [][]string{
			{"search", "lighthouse", "--project", project, "--no-refresh"},
			{"map", project, "--no-refresh"},
			{"note", "This must not be assigned to another project.", "--project", project},
		} {
			code, reply := j.run(t, args...)
			failure, ok := reply["error"].(map[string]any)
			if code != 1 || !ok || failure["code"] != "project-ambiguous" {
				t.Fatal(args, code, reply)
			}
			if len(failure["next_argv"].([]any)) == 0 {
				t.Fatal("ambiguous project has no recovery", reply)
			}
		}
	}
	entries, err := os.ReadDir(filepath.Join(j.state, "notes"))
	if err != nil && !os.IsNotExist(err) || len(entries) != 0 {
		t.Fatal("ambiguous note changed the notes folder", entries, err)
	}
	code, reply := j.run(t, "search", "lighthouse", "--project", "gAmMa", "--no-refresh")
	if code != 0 || reply["count"] != float64(1) || result(t, reply, 0)["path"] != control {
		t.Fatal("unique mixed-case search", code, reply)
	}
	if code, reply := j.run(t, "map", "gAmMa", "--no-refresh"); code != 0 || reply["project"] != "Gamma" {
		t.Fatal("unique mixed-case map", code, reply)
	}
	code, reply = j.run(t, "note", "A unique mixed-case note.", "--project", "gAmMa")
	if code != 0 || reply["path"] != filepath.Join(j.state, "notes", "Gamma.md") {
		t.Fatal("unique mixed-case note", code, reply)
	}
}

func TestExplicitQueryModeReply(t *testing.T) {
	j := newJourney(t)
	j.put(t, "alpha/lease.md", "lease agreement")
	j.put(t, "alpha/fruit.md", "strawberry sunlight")
	j.ready(t)
	code, reply := j.run(t, "search", "lease OR strawberry", "--no-refresh")
	if code != 0 || reply["total"] != float64(2) || reply["query"].(map[string]any)["mode"] != "or" {
		t.Fatal(code, reply)
	}
}
