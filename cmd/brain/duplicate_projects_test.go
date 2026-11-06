package main

import (
	"path/filepath"
	"testing"
)

func TestDuplicateProjectsStayVisible(t *testing.T) {
	for _, separate := range []bool{false, true} {
		t.Run(map[bool]string{false: "one source", true: "separate roots"}[separate], func(t *testing.T) {
			j := newJourney(t)
			paths := map[string]string{}
			for _, project := range []string{"alpha", "beta"} {
				paths[project] = j.put(t, project+"/README.md", "# Shared\n\nThe lighthouse keeper returns.")
			}
			args := []string{"init", "--source", j.source}
			if separate {
				args = []string{"init", "--source", filepath.Join(j.source, "alpha"), "--source", filepath.Join(j.source, "beta")}
			}
			if code, reply := j.run(t, args...); code != 0 {
				t.Fatal(code, reply)
			}
			if code, reply := j.run(t, "index"); code != 0 {
				t.Fatal(code, reply)
			}
			ids := map[string]bool{}
			for project, path := range paths {
				code, reply := j.run(t, "search", "lighthouse", "--project", project, "--no-refresh")
				if code != 0 || reply["count"] != float64(1) {
					t.Fatal(project, code, reply)
				}
				item := result(t, reply, 0)
				if item["path"] != path || item["project"] != project {
					t.Fatal(project, item)
				}
				id := item["id"].(string)
				if ids[id] {
					t.Fatal("separate documents share an identifier", id)
				}
				ids[id] = true
			}
		})
	}
}
