package main

import (
	"brain/internal/corpus"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInitLiteralSourceMetacharacters(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows folder names cannot contain question marks or stars")
	}
	for _, name := range []string{"project?", "project*"} {
		t.Run(name, func(t *testing.T) {
			j := newJourney(t)
			chosen := filepath.Join(j.base, name)
			sibling := filepath.Join(j.base, "projectA")
			for _, folder := range []string{chosen, sibling} {
				if err := os.Mkdir(folder, 0700); err != nil {
					t.Fatal(err)
				}
			}
			chosenDoc := filepath.Join(chosen, "README.md")
			siblingDoc := filepath.Join(sibling, "README.md")
			if err := os.WriteFile(chosenDoc, []byte("chosenmarker source document"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(siblingDoc, []byte("siblingmarker source document"), 0600); err != nil {
				t.Fatal(err)
			}
			before := sourceHashes(t, chosenDoc, siblingDoc)
			if code, reply := j.run(t, "init", "--source", chosen); code != 0 || reply["sources"] != float64(1) {
				t.Fatal(code, reply)
			}
			configBytes, err := os.ReadFile(filepath.Join(j.state, "config.json"))
			if err != nil {
				t.Fatal(err)
			}
			var config struct {
				Sources []struct {
					Path    string `json:"path"`
					Literal bool   `json:"literal"`
				} `json:"sources"`
			}
			if err := json.Unmarshal(configBytes, &config); err != nil || len(config.Sources) != 1 || config.Sources[0].Path != chosen || !config.Sources[0].Literal {
				t.Fatal("literal source intent was not saved", string(configBytes), err)
			}
			if code, reply := j.run(t, "index"); code != 0 || reply["new"] != float64(1) {
				t.Fatal(code, reply)
			}
			if code, reply := j.run(t, "search", "chosenmarker"); code != 0 || reply["count"] != float64(1) || !strings.Contains(result(t, reply, 0)["path"].(string), name) {
				t.Fatal(code, reply)
			}
			if code, reply := j.run(t, "search", "siblingmarker"); code != 0 || reply["count"] != float64(0) {
				t.Fatal(code, reply)
			}
			if code, reply := j.run(t, "projects"); code != 0 || reply["count"] != float64(1) || strings.Contains(stringJSON(t, reply["projects"]), sibling) {
				t.Fatal(code, reply)
			}
			if code, reply := j.run(t, "doctor"); code != 3 || reply["count"] != float64(0) {
				t.Fatal(code, reply)
			}
			if err := os.Rename(chosen, filepath.Join(j.base, "moved")); err != nil {
				t.Fatal(err)
			}
			if code, reply := j.run(t, "index"); code != 1 || reply["error"].(map[string]any)["code"] != "source-unreadable" {
				t.Fatal(code, reply)
			}
			if code, reply := j.run(t, "search", "siblingmarker", "--no-refresh"); code != 0 || reply["count"] != float64(0) {
				t.Fatal(code, reply)
			}
			if got := sourceHashes(t, filepath.Join(j.base, "moved", "README.md"), siblingDoc); got[0] != before[0] || got[1] != before[1] {
				t.Fatal("source document bytes changed")
			}
		})
	}
}

func TestSourcePatternIdentityCommands(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("this fixture uses Unix wildcard folder names")
	}
	for _, test := range []struct {
		pattern string
		count   float64
	}{
		{"pro*", 2},
		{"pro?lpha", 1},
		{"[ap]*", 3},
	} {
		t.Run(test.pattern, func(t *testing.T) {
			j := newJourney(t)
			for _, name := range []string{"alpha", "proalpha", "probeta", "zero"} {
				folder := filepath.Join(j.base, name)
				if err := os.Mkdir(folder, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(folder, "README.md"), []byte("patternmarker "+name), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(folder, ".env-secret.md"), []byte("patternmarker excluded"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if code, reply := j.run(t, "init", "--source", j.source); code != 0 {
				t.Fatal(code, reply)
			}
			config := corpus.Default(j.base, []string{filepath.Join(j.base, test.pattern)}, nil)
			data, err := json.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(j.state, "config.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			if code, reply := j.run(t, "index"); code != 0 || reply["new"] != test.count {
				t.Fatal(code, reply)
			}
			code, reply := j.run(t, "search", "patternmarker")
			if code != 0 || reply["count"] != test.count {
				t.Fatal(code, reply)
			}
			ids := map[string]bool{}
			for _, raw := range reply["results"].([]any) {
				item := raw.(map[string]any)
				id := item["id"].(string)
				if ids[id] || strings.Contains(item["path"].(string), ".env-secret") {
					t.Fatal("duplicate chunk id or excluded document", item)
				}
				ids[id] = true
			}
			if code, reply := j.run(t, "projects"); code != 0 || reply["count"] != test.count {
				t.Fatal(code, reply)
			}
			if code, reply := j.run(t, "doctor"); code != 3 || reply["count"] != float64(0) {
				t.Fatal(code, reply)
			}
		})
	}
}

func sourceHashes(t *testing.T, paths ...string) [][32]byte {
	t.Helper()
	result := make([][32]byte, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		result = append(result, sha256.Sum256(data))
	}
	return result
}

func stringJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
