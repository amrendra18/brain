package verifyrun

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestConfigSourceValidators(t *testing.T) {
	good := []byte(`{"sources":[{}],"count":1,"total":1,"empty":false,"changed":true}`)
	if err := checkConfigList(good, "sources", true); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`{`, `{}`, strings.Replace(string(good), `"count":1`, `"count":2`, 1), strings.Replace(string(good), `"empty":false`, `"empty":true`, 1), strings.Replace(string(good), `"changed":true`, `"changed":false`, 1)} {
		if checkConfigList([]byte(bad), "sources", true) == nil {
			t.Fatalf("accepted broken list %s", bad)
		}
	}
	citation := []byte(`{"count":1,"index_age_s":0,"results":[{"id":"kept","path":"owned/README.md"}]}`)
	if _, err := checkCitation(citation, "owned/README.md", "kept"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{[]byte(`{`), []byte(`{}`), bytes.ReplaceAll(citation, []byte(`"kept"`), []byte(`"wrong"`)), bytes.ReplaceAll(citation, []byte(`"owned/README.md"`), []byte(`"other"`))} {
		if _, err := checkCitation(bad, "owned/README.md", "kept"); err == nil {
			t.Fatal("accepted wrong citation")
		}
	}
	if err := checkEmptySearch([]byte(`{"count":0,"index_age_s":0,"results":[]}`)); err != nil {
		t.Fatal(err)
	}
	if checkEmptySearch(citation) == nil || checkEmptySearch([]byte(`{`)) == nil {
		t.Fatal("accepted nonempty or invalid search")
	}
	if unchangedFiles(map[string]string{"note": "before"}, map[string]string{"note": "after"}) == nil {
		t.Fatal("accepted source write")
	}
	config, state := filepath.Join(t.TempDir(), "config.json"), filepath.Join(t.TempDir(), "state")
	recovery := map[string]any{"next": []string{"index"}, "next_argv": [][]string{{binary, "index", "--config", config, "--state-dir", state}}}
	data, _ := json.Marshal(recovery)
	if err := checkScopedNext(data, config, state, true); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []any{map[string]any{}, map[string]any{"next": []string{"index"}}, map[string]any{"next": []string{"index"}, "next_argv": [][]string{{binary, "index"}}}, map[string]any{"next": []string{"index"}, "next_argv": [][]string{{"brain", "index", "--config", config, "--state-dir", state}}}} {
		data, _ := json.Marshal(bad)
		if checkScopedNext(data, config, state, true) == nil {
			t.Fatal("accepted unscoped recovery", string(data))
		}
	}
	if checkScopedNext([]byte(`{`), config, state, true) == nil {
		t.Fatal("accepted invalid recovery")
	}
	unknown := []byte(`{"future_setting":{"keep":"top"},"options":{"future_option":"keep"},"sources":[{"project":"retained","future_source":"keep"}]}`)
	if err := checkUnknownConfig(unknown); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{[]byte(`{`), []byte(`{}`), bytes.ReplaceAll(unknown, []byte(`"future_source":"keep"`), []byte(`"other":"keep"`))} {
		if checkUnknownConfig(bad) == nil {
			t.Fatal("accepted lost unknown config")
		}
	}
}

func TestConfigSourcesFixtureJourney(t *testing.T) {
	configSourcesFixture(t, "")
}

func TestConfigSourcesRejectsBrokenCommands(t *testing.T) {
	for _, fault := range []string{"sources list", "index --config", "search violet herons", "sources add", "search silver turtles", "sources remove", "sources move", "exclude list", "exclude add", "exclude remove", "third-party list", "third-party add", "third-party remove"} {
		t.Run(fault, func(t *testing.T) { configSourcesFixture(t, fault) })
	}
}

func configSourcesFixture(t *testing.T, fault string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fixture launcher; real binary journey is portable")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "fixture")
	content := "#!/bin/sh\nexport BRAIN_VERIFY_CONFIG_FIXTURE=1\nexport BRAIN_VERIFY_FAULT='" + fault + "'\nexport GOCOVERDIR='" + strings.ReplaceAll(t.TempDir(), "'", "'\\''") + "'\nexec '" + strings.ReplaceAll(self, "'", "'\\''") + "' -test.run=^$ -- \"$@\"\n"
	if err := os.WriteFile(script, []byte(content), 0700); err != nil {
		t.Fatal(err)
	}
	evidence := t.TempDir()
	for _, dir := range []string{"logs", "traces"} {
		if err := os.Mkdir(filepath.Join(evidence, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	d := driver{binary: script, evidence: evidence, summary: &Summary{Scratch: t.TempDir(), SideEffects: map[string]string{}}}
	if err := d.configSources(0); fault != "" {
		if err == nil {
			t.Fatal("accepted deliberately broken command", fault)
		}
		return
	} else if err != nil {
		t.Fatal(err)
	}
	if len(d.summary.Commands) < 25 || d.summary.SideEffects["config_sources_1"] == "" {
		t.Fatal("journey evidence missing")
	}
}

func configFixtureProcess() int {
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	args = args[1:]
	if fault := os.Getenv("BRAIN_VERIFY_FAULT"); fault != "" && strings.HasPrefix(strings.Join(args, " "), fault) {
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
	data, _ := os.ReadFile(config)
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		panic(err)
	}
	save := func() {
		if err := writeJSON(config, cfg); err != nil {
			panic(err)
		}
	}
	reply := func(value any) {
		if err := json.NewEncoder(os.Stdout).Encode(value); err != nil {
			panic(err)
		}
	}
	indexFile := filepath.Join(state, "fixture-index.json")
	switch args[0] {
	case "index":
		documents := map[string]string{}
		for _, raw := range cfg["sources"].([]any) {
			source := raw.(map[string]any)
			path := filepath.Join(source["path"].(string), "README.md")
			body, _ := os.ReadFile(path)
			documents[path] = string(body)
		}
		if err := writeJSON(indexFile, documents); err != nil {
			panic(err)
		}
		reply(map[string]any{"indexed": true})
		return 0
	case "search":
		var documents map[string]string
		data, _ := os.ReadFile(indexFile)
		json.Unmarshal(data, &documents)
		results := []map[string]any{}
		for path, body := range documents {
			if strings.Contains(strings.ToLower(body), args[1]) {
				results = append(results, map[string]any{"id": args[1], "path": path})
			}
		}
		reply(map[string]any{"count": len(results), "index_age_s": 0, "results": results})
		return 0
	}
	key := "sources"
	if args[0] == "exclude" {
		key = "exclude"
	}
	if args[0] == "third-party" {
		key = "third_party"
	}
	items := cfg[key].([]any)
	changed := false
	if args[1] == "remove" && args[2] == ".env*" {
		self, _ := os.Executable()
		reply(map[string]any{"error": map[string]any{"next": []string{"list"}, "next_argv": [][]string{{self, "exclude", "list", "--config", config, "--state-dir", state}}}})
		return 2
	}
	if args[1] != "list" {
		found := -1
		for i, item := range items {
			value := item
			if key == "sources" {
				value = item.(map[string]any)["path"]
			}
			if value == args[2] {
				found = i
			}
		}
		switch args[1] {
		case "add":
			if found < 0 {
				var value any = args[2]
				if key == "sources" {
					value = map[string]any{"path": args[2], "project": "added"}
				}
				items = append(items, value)
				changed = true
			}
		case "remove":
			if found >= 0 {
				items = append(items[:found], items[found+1:]...)
				changed = true
			}
		case "move":
			if found >= 0 {
				items[found].(map[string]any)["path"] = args[3]
				changed = true
			}
		default:
			panic(fmt.Sprint(args))
		}
		if changed {
			cfg[key] = items
			save()
		}
	}
	replyKey := "rules"
	if key == "sources" {
		replyKey = "sources"
	}
	reply(map[string]any{replyKey: items, "count": len(items), "total": len(items), "empty": len(items) == 0, "changed": changed, "next": []string{}, "next_argv": [][]string{}})
	if args[1] != "list" && !changed {
		return 3
	}
	return 0
}
