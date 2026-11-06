package corpus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestExplicitExclusionsSurviveSavedConfig(t *testing.T) {
	root := t.TempDir()
	excluded := []string{"atlas/REPORT.md", "atlas/tests/case.md"}
	kept := []string{"atlas/README.md", "other/REPORT.md"}
	for _, name := range append(append([]string{}, excluded...), kept...) {
		put(t, root, name, name)
	}
	config := Default(root, []string{root}, nil)
	if len(config.ThirdParty) != 0 {
		t.Fatal("fresh config contains project-specific third-party defaults")
	}
	config.Exclude = append(config.Exclude, "atlas/REPORT.md", "atlas/tests")
	state := t.TempDir()
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "config.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	config, err = Load(state)
	if err != nil {
		t.Fatal(err)
	}
	found, skipped, err := Scan(config)
	if err != nil || len(skipped) != 0 {
		t.Fatal(skipped, err)
	}
	for _, name := range excluded {
		path := filepath.Join(root, filepath.FromSlash(name))
		if _, ok := found[path]; ok || !Excluded(config, path) {
			t.Errorf("explicit exclusion was read: %s", name)
		}
	}
	for _, name := range kept {
		path := filepath.Join(root, filepath.FromSlash(name))
		if _, ok := found[path]; !ok || Excluded(config, path) {
			t.Errorf("ordinary file was excluded: %s", name)
		}
	}
}

func TestExplicitExclusionInDirectSource(t *testing.T) {
	root := t.TempDir()
	blocked := put(t, root, "atlas/REPORT.md", "blocked")
	allowed := put(t, root, "atlas/README.md", "allowed")
	config := Default(root, []string{filepath.Join(root, "atlas")}, nil)
	config.Exclude = append(config.Exclude, "atlas/REPORT.md")
	found, _, err := Scan(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := found[blocked]; ok {
		t.Fatal("configured path was read from a direct source")
	}
	if _, ok := found[allowed]; !ok {
		t.Fatal("allowed file was missed")
	}
}
