package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Exercise the manual config recipe through the command users run.
func TestManualLiteralSourceMetacharacters(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows folder names cannot contain question marks or stars")
	}
	for _, tc := range []struct{ name, chosen, sibling string }{
		{"question", "client?notes", "clientAnotes"},
		{"star", "team*notes", "teamAnotes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			j := newJourney(t)
			for _, name := range []string{"pattern-one", "pattern-two", tc.chosen, tc.sibling} {
				folder := filepath.Join(j.base, name)
				if err := os.Mkdir(folder, 0700); err != nil {
					t.Fatal(err)
				}
				marker := "patternmarker " + name
				if name == tc.chosen {
					marker = "selectedmarker " + name
				} else if name == tc.sibling {
					marker = "siblingmarker " + name
				}
				if err := os.WriteFile(filepath.Join(folder, "README.md"), []byte(marker), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if code, reply := j.run(t, "init", "--source", j.source); code != 0 {
				t.Fatal(code, reply)
			}
			configPath := filepath.Join(j.state, "config.json")
			readConfig := func() map[string]any {
				t.Helper()
				data, err := os.ReadFile(configPath)
				if err != nil {
					t.Fatal(err)
				}
				var config map[string]any
				if err := json.Unmarshal(data, &config); err != nil {
					t.Fatal(err)
				}
				return config
			}
			writeConfig := func(config map[string]any) {
				t.Helper()
				data, err := json.MarshalIndent(config, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(configPath, append(data, '\n'), 0600); err != nil {
					t.Fatal(err)
				}
			}
			config := readConfig()
			config["sources"] = []any{map[string]any{"path": filepath.Join(j.base, "pattern-*"), "project": "pattern"}}
			writeConfig(config)
			if code, reply := j.run(t, "index"); code != 0 || reply["new"] != float64(2) {
				t.Fatal(code, reply)
			}
			patternIDs := func() map[string]string {
				t.Helper()
				code, reply := j.run(t, "search", "patternmarker", "--no-refresh")
				if code != 0 || reply["count"] != float64(2) {
					t.Fatal(code, reply)
				}
				ids := map[string]string{}
				for _, raw := range reply["results"].([]any) {
					item := raw.(map[string]any)
					ids[item["path"].(string)] = item["id"].(string)
				}
				return ids
			}
			before := patternIDs()
			chosen := filepath.Join(j.base, tc.chosen)
			config = readConfig()
			// This is the old manual recipe. Repair it by adding literal below.
			config["sources"] = append(config["sources"].([]any), map[string]any{"path": chosen, "project": "outside-project"})
			writeConfig(config)
			if code, reply := j.run(t, "index"); code != 0 || reply["new"] != float64(2) {
				t.Fatal("old manual source must reproduce the sibling selection", code, reply)
			}
			if code, reply := j.run(t, "search", "siblingmarker", "--no-refresh"); code != 0 || reply["count"] != float64(1) {
				t.Fatal("old manual source did not select the sibling", code, reply)
			}
			config = readConfig()
			config["sources"].([]any)[1].(map[string]any)["literal"] = true
			writeConfig(config)
			if code, reply := j.run(t, "index"); code != 0 {
				t.Fatal(code, reply)
			}
			after := patternIDs()
			for path, id := range before {
				if got := after[path]; got != id {
					t.Fatalf("pattern chunk ID changed for %s: %q to %q", path, id, got)
				}
			}
			if code, reply := j.run(t, "search", "selectedmarker", "--no-refresh"); code != 0 || reply["count"] != float64(1) || result(t, reply, 0)["path"] != filepath.Join(chosen, "README.md") {
				t.Fatal(code, reply)
			}
			if code, reply := j.run(t, "search", "siblingmarker", "--no-refresh"); code != 0 || reply["count"] != float64(0) {
				t.Fatal("repair retained an unchosen sibling", code, reply)
			}
			if err := os.Rename(chosen, chosen+"-moved"); err != nil {
				t.Fatal(err)
			}
			if code, reply := j.run(t, "index"); code != 1 || reply["error"].(map[string]any)["code"] != "source-unreadable" || !strings.Contains(strings.ToLower(reply["error"].(map[string]any)["message"].(string)), "source") || len(reply["error"].(map[string]any)["next"].([]any)) == 0 {
				t.Fatal("missing exact root needs guidance", code, reply)
			}
		})
	}
}
