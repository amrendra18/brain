package main

import (
	corpusrules "brain/internal/corpus"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type yardstick struct {
	Floor   int      `json:"floor"`
	Exclude []string `json:"exclude"`
	Entries []struct {
		Set, Question, Query string
		Owners               []string
		Note                 string
	} `json:"entries"`
}

func readYardstick(t *testing.T) yardstick {
	t.Helper()
	var yard yardstick
	data, err := os.ReadFile(filepath.Join(repoRoot, "tests", "yardstick.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &yard); err != nil {
		t.Fatal(err)
	}
	return yard
}

func checkedCorpusRoot(root string) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	protected := func(path string) bool {
		for _, part := range strings.Split(filepath.ToSlash(path), "/") {
			if protectedCorpusProject(part) {
				return true
			}
		}
		return false
	}
	if protected(root) {
		return "", fmt.Errorf("protected corpus root is forbidden")
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	if protected(root) {
		return "", fmt.Errorf("protected corpus root is forbidden")
	}
	return root, nil
}

func protectedCorpusProject(name string) bool {
	return strings.EqualFold(name, "brain")
}

func realCorpus(t *testing.T) string {
	t.Helper()
	selected := os.Getenv("BRAIN_TEST_CORPUS")
	if selected == "" {
		root, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		fixture := filepath.Join(repoRoot, "tests", "synthetic")
		for _, project := range []string{"atlas", "harbor", "orchard"} {
			entries, err := os.ReadDir(filepath.Join(fixture, project))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(root, project), 0700); err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if !entry.Type().IsRegular() {
					t.Fatal("synthetic fixture is not a regular file", entry.Name())
				}
				data, err := os.ReadFile(filepath.Join(fixture, project, entry.Name()))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, project, entry.Name()), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
		}
		return root
	}
	root, err := checkedCorpusRoot(selected)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func corpusOwners(root string, owners []string) error {
	root, err := checkedCorpusRoot(root)
	if err != nil {
		return err
	}
	for _, owner := range owners {
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(owner)))
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("required corpus owner is missing or not a regular file: %s", owner)
		}
	}
	return nil
}

func realJourney(t *testing.T, root string, exclude []string) *journey {
	t.Helper()
	root, err := checkedCorpusRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	if selected := os.Getenv("BRAIN_TEST_CORPUS"); selected != "" {
		selected, err = checkedCorpusRoot(selected)
		if err != nil {
			t.Fatal(err)
		}
		if selected == root {
			path := os.Getenv("BRAIN_TEST_EXCLUDES_FILE")
			if path == "" {
				t.Skip("explicit corpus needs a private exclusion file")
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var private []string
			if err := json.Unmarshal(data, &private); err != nil || len(private) == 0 {
				t.Fatal("private exclusion file needs a nonempty JSON list", err)
			}
			exclude = append(exclude, private...)
		}
	}
	j := newJourney(t)
	j.source = root
	if code, reply := j.run(t, "init", "--source", root); code != 0 {
		t.Fatal(code, reply)
	}
	path := filepath.Join(j.state, "config.json")
	config, err := corpusrules.Load(j.state)
	if err != nil {
		t.Fatal(err)
	}
	// Select projects before descent so the protected sibling is never traversed.
	// A blanket name rule would also hide unrelated archived research owners.
	projects, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	config.Sources = nil
	for _, project := range projects {
		if protectedCorpusProject(project.Name()) || !project.IsDir() || filepath.Clean(filepath.Join(root, project.Name())) == filepath.Clean(repoRoot) {
			continue
		}
		config.Sources = append(config.Sources, corpusrules.Source{Path: filepath.Join(root, project.Name()), Project: project.Name()})
	}
	if len(config.Sources) == 0 {
		t.Fatal("the selected real corpus contains no permitted project folders")
	}
	config.Exclude = append(config.Exclude, exclude...)
	t.Logf("real corpus uses %d project sources, omits the protected sibling, checkout and home memories; configured third-party rules: %d", len(config.Sources), len(config.ThirdParty))
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return j
}
func TestFullIndexTime(t *testing.T) {
	corpus := realCorpus(t)
	j := realJourney(t, corpus, nil)
	start := time.Now()
	code, reply := j.run(t, "index")
	duration := time.Since(start)
	fmt.Fprintf(os.Stderr, "full index of real corpus: %.6f s end to end (timing report only); %v files, %v chunks, %v copies folded, %v bytes, %v skipped\n", duration.Seconds(), reply["new"], reply["chunks"], reply["duplicates_folded"], reply["db_bytes"], reply["count"])
	if code != 0 || reply["new"].(float64) <= 0 || reply["chunks"].(float64) <= 0 {
		t.Fatal(code, reply, duration)
	}
}

func TestSyntheticOwner(t *testing.T) {
	corpus := realCorpus(t)
	const relativeOwner = "atlas/valve.md"
	if err := corpusOwners(corpus, []string{relativeOwner}); err != nil {
		t.Fatal(err)
	}
	j := realJourney(t, corpus, nil)
	if code, reply := j.run(t, "index"); code != 0 {
		t.Fatal(code, reply)
	}
	code, reply := j.run(t, "search", "atlas amber valve", "--limit", "3")
	if code != 0 {
		t.Fatal(code, reply)
	}
	owner := filepath.Join(corpus, filepath.FromSlash(relativeOwner))
	for _, raw := range reply["results"].([]any) {
		item := raw.(map[string]any)
		if item["path"] == owner {
			return
		}
		for _, other := range item["also_in"].([]any) {
			if other == owner {
				return
			}
		}
	}
	t.Fatal("synthetic valve owner was absent from the top three", reply)
}
func TestYardstick(t *testing.T) {
	corpus, yard := realCorpus(t), readYardstick(t)
	for _, entry := range yard.Entries {
		if err := corpusOwners(corpus, entry.Owners); err != nil {
			t.Fatal(err)
		}
	}
	fmt.Fprintf(os.Stderr, "yardstick additional configured exclusions: %s\n", strings.Join(yard.Exclude, ", "))
	score := func(j *journey) (int, int, time.Duration) {
		code, reply := j.run(t, "index")
		if code != 0 {
			t.Fatal(code, reply)
		}
		hits, total := 0, 0
		var slowest time.Duration
		for _, entry := range yard.Entries {
			if entry.Set != "original" {
				continue
			}
			total++
			start := time.Now()
			code, reply := j.run(t, "search", entry.Query, "--limit", "3")
			duration := time.Since(start)
			if duration > slowest {
				slowest = duration
			}
			if code != 0 {
				t.Fatal(code, reply)
			}
			owners := map[string]bool{}
			for _, owner := range entry.Owners {
				owners[filepath.Join(corpus, filepath.FromSlash(owner))] = true
			}
			hit := false
			paths := []string{}
			for _, raw := range reply["results"].([]any) {
				item := raw.(map[string]any)
				path := item["path"].(string)
				relative, _ := filepath.Rel(corpus, path)
				paths = append(paths, relative)
				if owners[path] {
					hit = true
				}
				for _, path := range item["also_in"].([]any) {
					if owners[path.(string)] {
						hit = true
					}
				}
			}
			if hit {
				hits++
			} else {
				fmt.Fprintf(os.Stderr, "yardstick miss: %s (%v): %s\n", entry.Query, reply["query"], strings.Join(paths, ", "))
			}
		}
		return hits, total, slowest
	}
	excluded, total, slowest := score(realJourney(t, corpus, yard.Exclude))
	fmt.Fprintf(os.Stderr, "yardstick, set original, configured report exclusion: %d of %d hits (floor %d)\nslowest refreshed query: %.6f s (bar 2 s)\n", excluded, total, yard.Floor, slowest.Seconds())
	included, includedTotal, _ := score(realJourney(t, corpus, nil))
	fmt.Fprintf(os.Stderr, "yardstick, original without configured report exclusion: %d of %d hits (report still excluded by built-in rule)\n", included, includedTotal)
	if excluded < yard.Floor || total != 20 || slowest >= 2*time.Second {
		t.Fatal(excluded, total, yard.Floor, slowest)
	}
}
