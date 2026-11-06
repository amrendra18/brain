package mapper

import (
	"brain/internal/corpus"
	"brain/internal/index"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestMapFencedExamplesAreNotFacts(t *testing.T) {
	for _, example := range []string{
		"```md\n~~~\n## Status\nExample fact.\n```",
		"````md\n```\n## Status\nExample fact.\n````",
		"~~~md\n~~~ trailing text\n## Status\nExample fact.\n~~~",
	} {
		base := t.TempDir()
		source, folder := filepath.Join(base, "alpha"), filepath.Join(base, "state")
		for _, path := range []string{source, folder} {
			if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
		}
		for name, text := range map[string]string{
			"README.md": "# Alpha\n\n" + example + "\n\nReal purpose.",
			"PLAN.md":   "# Plan\n\n" + example + "\n\n## Status\nReal progress.\n## Work\n- [ ] Build it.",
		} {
			if err := os.WriteFile(filepath.Join(source, name), []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
		}
		db, err := index.Open(folder, true, false, false)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		config := corpus.Default(source, []string{source}, nil)
		if _, err = index.Refresh(db, folder, config, false, 0); err != nil {
			t.Fatal(err)
		}
		page, _, err := Build(db, config, folder, "alpha")
		if err != nil || page.Purpose != "Real purpose." || !reflect.DeepEqual(page.Milestones.Lines, []string{"Real progress."}) || page.Unchecked["PLAN.md"] != 1 {
			t.Fatalf("%q: %+v, %v", example, page, err)
		}
	}
}
