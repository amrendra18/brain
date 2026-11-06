package state_test

import (
	"brain/internal/corpus"
	"brain/internal/index"
	"brain/internal/mapper"
	"brain/internal/query"
	"brain/internal/search"
	"brain/internal/state"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLabeledWildcardProjectRoot(t *testing.T) {
	base := t.TempDir()
	source, folder := filepath.Join(base, "corpus"), filepath.Join(base, "state")
	write := func(path, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, root := range []string{"alpha", "beta"} {
		write(filepath.Join(source, root, "docs", "README.md"), "# Guide\n\n"+root+" keeps harbour records.")
		write(filepath.Join(source, root, "docs", "PLAN.md"), "## Status\n"+root+" underway.\n## Work\n- [ ] Build it.")
		write(filepath.Join(source, root, "docs", "00", "nested", "GUIDE.md"), "Nested navigation.")
	}
	config := corpus.Default(source, []string{source}, nil)
	config.Sources = append(config.Sources, corpus.Source{Path: filepath.Join(source, "*", "docs"), Project: "group"})
	if err := os.Mkdir(folder, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := index.Open(folder, true, false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = index.Refresh(db, folder, config, false, 0); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(source, "alpha", "docs")
	check := func() {
		t.Helper()
		projects, err := state.Projects(db, config)
		if err != nil || len(projects) != 1 || projects[0].Path != root || projects[0].Purpose != "alpha keeps harbour records." || projects[0].Files != 6 {
			t.Fatalf("projects: %+v, %v", projects, err)
		}
		page, _, err := mapper.Build(db, config, folder, "group")
		if err != nil || page.Path != root || page.Purpose != projects[0].Purpose || !reflect.DeepEqual(page.Milestones.Lines, []string{"alpha underway."}) || page.Unchecked["PLAN.md"] != 1 {
			t.Fatalf("map: %+v, %v", page, err)
		}
		_, _, _, project, err := state.Brief(db, config, filepath.Join(root, "00", "nested"))
		if err != nil || project.Name != "group" {
			t.Fatalf("brief: %+v, %v", project, err)
		}
		found, err := search.Find(db, query.Make("harbour", []string{"group"}), "group", "readme", 10, true)
		if err != nil || found.Count != 2 {
			t.Fatalf("search: %+v, %v", found, err)
		}
		for _, result := range found.Results {
			if result.Project != "group" || result.ID == "" || (result.Path != filepath.Join(root, "README.md") && result.Path != filepath.Join(source, "beta", "docs", "README.md")) {
				t.Fatalf("citation changed: %+v", result)
			}
		}
	}
	check()
	if err = os.Rename(source, source+"-unavailable"); err != nil {
		t.Fatal(err)
	}
	check()
}
