package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestFreshnessReadsNeverOpenSources(t *testing.T) {
	binary := exclusionObservedBinary(t, false)
	j := sourceObservationFixture(t, binary)
	code, found, opened := sourceObservedCommand(t, binary, j, "search", "lighthouse")
	if code != 0 || found["count"] != float64(1) {
		t.Fatal(code, found)
	}
	if err := freshnessNoSourceOpenProof(opened, j.source); err != nil {
		t.Fatal(err)
	}
	id := result(t, found, 0)["id"].(string)
	j.put(t, "alpha/README.md", "# Changed\n\nA newly added kingfisher.")
	for _, check := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"search", "lighthouse"}, 0, "lighthouse"},
		{[]string{"show", id}, 0, "lighthouse"},
		{[]string{"projects"}, 0, "alpha"},
		{[]string{"recent"}, 0, "README.md"},
		{[]string{"map", "alpha"}, 0, "lighthouse"},
		{[]string{"status"}, 0, `"index_complete":true`},
		{nil, 0, `"index_complete":true`},
		{[]string{"brief"}, 0, `"ready":true`},
		{[]string{"--brief"}, 0, `"ready":true`},
		{[]string{"doctor"}, 3, `"count":0`},
		{[]string{"note", "A violet memory.", "--project", "alpha"}, 0, `"indexed":true`},
		{[]string{"note", "An amber memory."}, 0, `"indexed":true`},
		{[]string{"search", "violet"}, 0, "violet"},
		{[]string{"search", "kingfisher"}, 0, `"count":0`},
	} {
		name := strings.Join(check.args, "-")
		if name == "" {
			name = "bare-status-alias"
		}
		t.Run(name, func(t *testing.T) {
			code, reply, opened := sourceObservedCommand(t, binary, j, check.args...)
			data, err := json.Marshal(reply)
			if err != nil || code != check.code || !strings.Contains(string(data), check.want) {
				t.Fatal(code, string(data), err)
			}
			if err := freshnessNoSourceOpenProof(opened, j.source); err != nil {
				t.Fatal(err)
			}
			t.Log("zero source opens; expected stored reply retained")
		})
	}
	t.Run("note-before-full-index", func(t *testing.T) {
		j := sourceObservationInitialized(t)
		code, reply, opened := sourceObservedCommand(t, binary, j, "note", "A tangerine memory.")
		if code != 0 || reply["indexed"] != true {
			t.Fatal(code, reply)
		}
		if err := freshnessNoSourceOpenProof(opened, j.source); err != nil {
			t.Fatal(err)
		}
		code, reply, opened = sourceObservedCommand(t, binary, j, "search", "tangerine")
		if code != 0 || reply["count"] != float64(1) || reply["index_complete"] != false {
			t.Fatal(code, reply)
		}
		if err := freshnessNoSourceOpenProof(opened, j.source); err != nil {
			t.Fatal(err)
		}
	})
}

func TestFreshnessObserverRejectsDiscardedScans(t *testing.T) {
	binary := exclusionObservedBinary(t, false, map[string]map[string]string{
		filepath.Join(repoRoot, "internal", "cli", "state.go"): {
			"func (call *call) ready(config corpus.Config) (*index.Stored, metadata, error) {": "\n_, _, _ = corpus.Scan(config)\n",
		},
		filepath.Join(repoRoot, "internal", "cli", "note.go"): {
			"func (call *call) note(config corpus.Config) int {": "\n_, _, _ = corpus.Scan(config)\n",
		},
	})
	j := sourceObservationFixture(t, binary)
	for _, args := range [][]string{{"search", "lighthouse"}, {"note", "A violet memory.", "--project", "alpha"}} {
		t.Run(args[0], func(t *testing.T) {
			code, reply, opened := sourceObservedCommand(t, binary, j, args...)
			if code != 0 || args[0] == "search" && reply["count"] != float64(1) || args[0] == "note" && reply["indexed"] != true {
				t.Fatal("deliberately broken input lost its valid reply", code, reply)
			}
			if err := freshnessNoSourceOpenProof(opened, j.source); err == nil {
				t.Fatal("source-open proof accepted deliberately broken discarded scan")
			} else {
				t.Log("deliberately broken input rejected:", err)
			}
		})
	}
}

func sourceObservationFixture(t *testing.T, binary string) *journey {
	t.Helper()
	j := sourceObservationInitialized(t)
	opened := exclusionObservedIndex(t, binary, j)
	allowed, _ := exclusionOpenCounts(opened, j.source, nil)
	if allowed == 0 {
		t.Fatal("positive control: explicit index did not observe the readable source document")
	}
	t.Logf("positive control: explicit index observed %d source document opens", allowed)
	return j
}

func sourceObservationInitialized(t *testing.T) *journey {
	t.Helper()
	j := newJourney(t)
	j.put(t, "alpha/README.md", "# Beacon\n\nA lighthouse.")
	if code, reply := j.run(t, "init", "--source", j.source); code != 0 {
		t.Fatal(code, reply)
	}
	return j
}

func freshnessNoSourceOpenProof(opened []string, source string) error {
	var sourceOpens []string
	for _, path := range opened {
		if path == source || strings.HasPrefix(path, source+string(filepath.Separator)) {
			sourceOpens = append(sourceOpens, path)
		}
	}
	if len(sourceOpens) != 0 {
		return fmt.Errorf("observed %d source opens; want zero: %v", len(sourceOpens), sourceOpens)
	}
	return nil
}
