package verifyrun

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func init() {
	if os.Getenv("BRAIN_VERIFY_STUB_FIXTURE") == "exclusions" {
		os.Exit(exclusionFixtureProcess())
	}
}

func TestExclusionValidators(t *testing.T) {
	for _, tc := range []struct {
		name, data string
		ok         bool
	}{
		{"missing", `{"error":{"code":"chunk-not-found","message":"No current chunk has that id."},"next_argv":[["search","allowed"]]}`, true},
		{"invalid", `{`, false},
		{"empty", `{}`, false},
		{"wrong-code", `{"error":{"code":"index-unavailable"}}`, false},
		{"heading", `{"error":{"code":"chunk-not-found","message":"SealedHeading"}}`, false},
		{"escaped", `{"error":{"code":"chunk-not-found"},"next_argv":[["search","Sealed\u0048eading"]]}`, false},
		{"content", `{"error":{"code":"chunk-not-found"},"detail":{"Hidden cedar":"ignored"}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkExcludedChunk([]byte(tc.data), "SealedHeading", "Hidden cedar"); (err == nil) != tc.ok {
				t.Fatal(tc.name, err)
			}
		})
	}
}

func TestExclusionSnapshot(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "README.md")
	if err := os.WriteFile(path, []byte("allowed"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "alias.md")
	if err := os.Symlink("absent.md", link); err != nil {
		t.Skip("host cannot create the owned symlink fixture", err)
	}
	before, err := exclusionSnapshot(root)
	if err != nil || !strings.HasPrefix(before["alias.md"], "symlink:absent.md mode:") || before["README.md"] == "" {
		t.Fatal(before, err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("README.md", link); err != nil {
		t.Fatal(err)
	}
	after, err := exclusionSnapshot(root)
	if err != nil || unchangedFiles(before, after) == nil {
		t.Fatal("changed symlink was not detected", err)
	}
	if _, err := exclusionSnapshot(filepath.Join(root, "missing")); err == nil {
		t.Fatal("accepted missing source root")
	}
}

func TestExclusionsFixtureJourney(t *testing.T) {
	d := stubFixtureDriver(t, "exclusions", "")
	if err := d.exclusions(0); err != nil {
		t.Fatal(err)
	}
	if len(d.summary.Commands) != 13 || d.summary.SideEffects["exclusions_1"] == "" {
		t.Fatalf("journey evidence incomplete: %d commands, %v", len(d.summary.Commands), d.summary.SideEffects)
	}
}

func TestExclusionsRejectsBrokenCommands(t *testing.T) {
	for _, fault := range []string{"index", "search amber otters", "search violet herons", "search silver turtles", "search guarded badgers", "search hostile foxes", "search ignorecontrolmarker", "search hidden cedar", "show", "missing-control", "wrong-citation", "leak-ignored", "leak-link", "leak-control", "second-index", "second-search", "stale-text", "stale-heading", "unscoped-next", "missing-heading", "source-write", "home-write", "target-write", "link-write"} {
		t.Run(fault, func(t *testing.T) {
			d := stubFixtureDriver(t, "exclusions", fault)
			if err := d.exclusions(0); err == nil {
				t.Fatal("accepted deliberately broken test input", fault)
			}
		})
	}
}

func TestExclusionsSetupErrors(t *testing.T) {
	for _, fault := range []string{"scratch-file", "sources-file", "config-directory", "home-file", "missing-traces"} {
		t.Run(fault, func(t *testing.T) {
			d := stubFixtureDriver(t, "exclusions", "")
			base := filepath.Join(d.summary.Scratch, "exclusions-0")
			if err := os.MkdirAll(base, 0700); err != nil {
				t.Fatal(err)
			}
			var err error
			switch fault {
			case "scratch-file":
				err = os.Remove(base)
				if err == nil {
					err = os.WriteFile(base, nil, 0600)
				}
			case "sources-file":
				err = os.WriteFile(filepath.Join(base, "sources"), nil, 0600)
			case "config-directory":
				err = os.Mkdir(filepath.Join(base, "selected.json"), 0700)
			case "home-file":
				err = os.WriteFile(filepath.Join(base, "home"), nil, 0600)
			case "missing-traces":
				err = os.Remove(filepath.Join(d.evidence, "traces"))
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := d.exclusions(0); err == nil {
				t.Fatal("accepted broken fixture setup", fault)
			}
		})
	}
}

func exclusionFixtureProcess() int {
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
		Sources []struct{ Path string }
	}
	data, _ := os.ReadFile(config)
	json.Unmarshal(data, &cfg)
	source := cfg.Sources[0].Path
	data, _ = os.ReadFile(filepath.Join(source, ".brainignore"))
	changed := strings.Contains(string(data), "later.md")
	reply := func(value any) { json.NewEncoder(os.Stdout).Encode(value) }
	switch args[0] {
	case "index":
		if changed && fault == "second-index" {
			fmt.Print(`{"broken_test_input":true}`)
			return 1
		}
		switch fault {
		case "source-write":
			os.WriteFile(filepath.Join(source, "unexpected.md"), []byte("unexpected"), 0600)
		case "home-write":
			os.WriteFile(filepath.Join(os.Getenv("HOME"), "unexpected.md"), []byte("unexpected"), 0600)
		case "target-write":
			os.WriteFile(filepath.Join(filepath.Dir(source), "hostile.md"), []byte("unexpected"), 0600)
		case "link-write":
			os.Remove(filepath.Join(source, "alias.md"))
			os.Symlink("README.md", filepath.Join(source, "alias.md"))
		}
		reply(map[string]any{"indexed": true})
	case "search":
		paths := map[string]string{"amber otters": "README.md", "violet herons": "nested/kept.md", "silver turtles": "other/anchored.md", "hidden cedar": "later.md"}
		path := paths[args[1]]
		if changed && args[1] == "hidden cedar" {
			if fault == "second-search" {
				fmt.Print(`{"broken_test_input":true}`)
				return 1
			}
			if fault != "stale-text" {
				path = ""
			}
		}
		if fault == "missing-control" && args[1] == "amber otters" {
			path = ""
		}
		for broken, query := range map[string]string{"leak-ignored": "guarded badgers", "leak-link": "hostile foxes", "leak-control": "ignorecontrolmarker"} {
			if fault == broken && args[1] == query {
				path = "forbidden.md"
			}
		}
		results := []map[string]any{}
		if path != "" {
			if fault == "wrong-citation" {
				path = "wrong.md"
			}
			results = append(results, map[string]any{"id": "fixture-id", "path": filepath.Join(source, filepath.FromSlash(path))})
		}
		reply(map[string]any{"count": len(results), "index_age_s": 0, "results": results})
	case "show":
		if !changed {
			heading := "SealedHeading"
			if fault == "missing-heading" {
				heading = ""
			}
			reply(map[string]any{"heading": heading, "text": "Hidden cedar records need later exclusion."})
			return 0
		}
		message := "No current chunk has that id."
		if fault == "stale-heading" {
			message += " SealedHeading"
		}
		argv := []string{os.Getenv("BRAIN_VERIFY_LAUNCHER"), "search", "allowed", "--config", config, "--state-dir", state}
		if fault == "unscoped-next" {
			argv = argv[:3]
		}
		reply(map[string]any{"error": map[string]any{"code": "chunk-not-found", "message": message, "next": []string{"search allowed"}, "next_argv": [][]string{argv}}})
		return 1
	}
	return 0
}
