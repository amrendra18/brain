package verifyrun

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func init() {
	if os.Getenv("BRAIN_VERIFY_STUB_FIXTURE") == "freshness" {
		os.Exit(freshnessFixtureProcess())
	}
}

func stubFixtureDriver(t *testing.T, mode, fault string) *driver {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fixture launcher; host-independent validators remain active")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "fixture")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	content := "#!/bin/sh\nexport GORACE=" + quote(strings.TrimSpace(os.Getenv("GORACE")+" atexit_sleep_ms=0")) + "\nexport BRAIN_VERIFY_LAUNCHER=" + quote(script) + "\nexport BRAIN_VERIFY_STUB_FIXTURE=" + quote(mode) + "\nexport BRAIN_VERIFY_FAULT=" + quote(fault) + "\nexport GOCOVERDIR=" + quote(t.TempDir()) + "\nexec " + quote(self) + " -test.run=^$ -- \"$@\"\n"
	if err := os.WriteFile(script, []byte(content), 0700); err != nil {
		t.Fatal(err)
	}
	evidence := t.TempDir()
	for _, dir := range []string{"logs", "traces"} {
		if err := os.Mkdir(filepath.Join(evidence, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	return &driver{binary: script, evidence: evidence, summary: &Summary{Scratch: t.TempDir(), SideEffects: map[string]string{}}}
}

func TestFreshnessValidators(t *testing.T) {
	for _, tc := range []struct {
		data string
		ok   bool
	}{
		{`{"index_age_s":8,"index_complete":true}`, true},
		{`{"index_age_s":0,"index_complete":true}`, false},
		{`{"index_age_s":8,"index_complete":false}`, false},
		{`{"index_age_s":8}`, false}, {`{`, false},
	} {
		if err := checkFreshness([]byte(tc.data), 7); (err == nil) != tc.ok {
			t.Fatal(tc, err)
		}
	}
	for _, tc := range []struct {
		data string
		ok   bool
	}{
		{`{"index":{"index_age_s":8,"index_complete":true,"pending_known":false}}`, true},
		{`{"index":{"index_age_s":8,"index_complete":true,"pending_known":true}}`, false},
		{`{"index":{"index_age_s":8,"pending_known":false}}`, false}, {`{`, false},
	} {
		if err := checkStoredStatus([]byte(tc.data)); (err == nil) != tc.ok {
			t.Fatal(tc, err)
		}
	}
	state := t.TempDir()
	for _, tc := range []struct {
		path, id    string
		indexed, ok bool
	}{
		{filepath.Join(state, "notes", "harbor.md"), "id", true, true},
		{filepath.Join(state, "outside.md"), "id", true, false},
		{filepath.Join(state, "notes", "harbor.md"), "", true, false},
		{filepath.Join(state, "notes", "harbor.md"), "id", false, false},
	} {
		data, _ := json.Marshal(map[string]any{"path": tc.path, "entry_id": tc.id, "indexed": tc.indexed})
		if _, _, err := checkSavedNote(data, state); (err == nil) != tc.ok {
			t.Fatal(tc, err)
		}
	}
	if _, _, err := checkSavedNote([]byte(`{`), state); err == nil {
		t.Fatal("accepted broken note reply")
	}
}

func TestFreshnessFixtureJourney(t *testing.T) {
	d := stubFixtureDriver(t, "freshness", "")
	if err := d.freshness(0); err != nil {
		t.Fatal(err)
	}
	if d.summary.SideEffects["freshness_1"] == "" {
		t.Fatal("freshness evidence missing")
	}
}

func TestFreshnessRejectsBrokenCommands(t *testing.T) {
	for _, fault := range []string{"index", "search amber otters", "search violet herons", "show", "status", "doctor", "--brief", "note", "search coral foxes", "search copper falcons", "search silver turtles", "refresh-on-note", "source-write"} {
		t.Run(fault, func(t *testing.T) {
			d := stubFixtureDriver(t, "freshness", fault)
			if err := d.freshness(0); err == nil {
				t.Fatal("accepted broken test input", fault)
			}
		})
	}
}

func freshnessFixtureProcess() int {
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	args = args[1:]
	fault := os.Getenv("BRAIN_VERIFY_FAULT")
	if fault != "" && strings.HasPrefix(strings.Join(args, " "), fault) {
		fmt.Print(`{"broken_test_input":true}`)
		return 1
	}
	config, state := "", ""
	for i, arg := range args {
		if arg == "--config" {
			config = args[i+1]
		}
		if arg == "--state-dir" {
			state = args[i+1]
		}
	}
	var cfg struct {
		Sources []struct {
			Path string `json:"path"`
		} `json:"sources"`
	}
	data, _ := os.ReadFile(config)
	json.Unmarshal(data, &cfg)
	var stored struct {
		Time      int64
		Documents map[string]string
	}
	indexFile := filepath.Join(state, "fixture-index.json")
	data, _ = os.ReadFile(indexFile)
	json.Unmarshal(data, &stored)
	if stored.Documents == nil {
		stored.Documents = map[string]string{}
	}
	scan := func() {
		for _, source := range cfg.Sources {
			entries, _ := os.ReadDir(source.Path)
			for _, entry := range entries {
				name := filepath.Join(source.Path, entry.Name())
				body, _ := os.ReadFile(name)
				stored.Documents[name] = string(body)
			}
		}
		stored.Time = time.Now().Unix()
		writeJSON(indexFile, stored)
	}
	reply := func(value any) { json.NewEncoder(os.Stdout).Encode(value) }
	age := time.Now().Unix() - stored.Time
	switch args[0] {
	case "index":
		scan()
		reply(map[string]any{"indexed": true})
	case "search":
		results := []map[string]any{}
		for path, body := range stored.Documents {
			if strings.Contains(strings.ToLower(body), args[1]) {
				id := args[1]
				if args[1] == "coral foxes" {
					id = "note-id"
				}
				results = append(results, map[string]any{"id": id, "path": path})
			}
		}
		reply(map[string]any{"count": len(results), "index_age_s": age, "index_complete": true, "results": results})
	case "note":
		path := filepath.Join(state, "notes", "harbor.md")
		os.MkdirAll(filepath.Dir(path), 0700)
		os.WriteFile(path, []byte(args[1]), 0600)
		stored.Documents[path] = args[1]
		writeJSON(indexFile, stored)
		if fault == "refresh-on-note" {
			scan()
		}
		if fault == "source-write" {
			os.WriteFile(filepath.Join(cfg.Sources[0].Path, "UNEXPECTED.md"), []byte("unexpected"), 0600)
		}
		reply(map[string]any{"path": path, "entry_id": "note-id", "indexed": true})
	case "status":
		reply(map[string]any{"index": map[string]any{"index_age_s": age, "index_complete": true, "pending_known": false}})
	case "--brief":
		reply(map[string]any{"text": "line one\nline two\nline three\n", "index_age_s": age})
	case "doctor":
		reply(map[string]any{"checks": []any{}, "problems": []any{}})
		return 3
	default:
		reply(map[string]any{"index_age_s": age, "index_complete": true})
	}
	return 0
}
