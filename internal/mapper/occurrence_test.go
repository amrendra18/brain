package mapper

import (
	"brain/internal/corpus"
	"brain/internal/index"
	"os"
	"path/filepath"
	"testing"
)

func TestMapOccurrenceText(t *testing.T) {
	root := t.TempDir()
	source, folder := filepath.Join(root, "corpus"), filepath.Join(root, "state")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(folder, 0700); err != nil {
		t.Fatal(err)
	}
	plan := "## Status\n\nReady.\n\n## Work\n\n- [ ] first\n- [ ] second\n\n## Work\n\n- [ ] first - [ ] second\n"
	documents := map[string]string{
		"PLAN.md":   plan,
		"README.md": "# Purpose\n\nAn amber archive.\n",
	}
	for name, text := range documents {
		if err := os.WriteFile(filepath.Join(source, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	db, err := index.Open(folder, true, false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	config := corpus.Default(source, []string{source}, nil)
	if _, err := index.Refresh(db, folder, config, false, 0); err != nil {
		t.Fatal(err)
	}
	page, _, err := Build(db, config, folder, "CORPUS")
	if err != nil || page.Unchecked["PLAN.md"] != 3 || page.Purpose != "An amber archive." {
		t.Fatal(page, err)
	}
	for name, text := range documents {
		data, err := os.ReadFile(filepath.Join(source, name))
		if err != nil || string(data) != text {
			t.Fatal("source changed", name, err)
		}
	}
}
