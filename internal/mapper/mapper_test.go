package mapper

import (
	"brain/internal/corpus"
	"brain/internal/index"
	"brain/internal/notes"
	"brain/internal/platform"
	"brain/tests/testutil"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMapRules(t *testing.T) {
	for _, test := range []struct {
		text  string
		lines []string
	}{
		{"# Plan\n\n## Status\n\nOne.\n\n   Two, stripped.   \n### Deeper\nNot this.", []string{"One.", "Two, stripped."}},
		{"```\n## Status in a fence\n```\n\n## Milestone STATUS\nThis one.", []string{"This one."}},
		{"## Status\n\n```\n# not a heading\n```\nAfter the fence.\n## Next", []string{"```", "# not a heading", "```", "After the fence."}},
		{"```md\n~~~\n## Status\nExample.\n```\n## Status\nReal.", []string{"Real."}},
		{"````md\n```\n## Status\nExample.\n````\n## Status\nReal.", []string{"Real."}},
		{"~~~md\n~~~ text\n## Status\nExample.\n~~~\n## Status\nReal.", []string{"Real."}},
		{"## Status\n````\n```\n## Example\n````\nReal.\n## Next", []string{"````", "```", "## Example", "````", "Real."}},
		{"    ## Status\nIgnored.\n## Work\n- [ ] Box.", []string{}},
		{"##Status\nIgnored.\n## Status\n## Next", []string{}},
	} {
		if found := StatusLines(test.text); !reflect.DeepEqual(found, test.lines) {
			t.Fatal(found, test)
		}
	}
	if found := StatusLines("## Status\u2028A1 underway.\u0085## Next"); !reflect.DeepEqual(found, []string{"A1 underway."}) {
		t.Fatal(found)
	}
	if Unchecked("- [ ] One.\u2028- [ ] Two.") != 2 {
		t.Fatal("Unicode line breaks lost unchecked boxes")
	}
	if Unchecked("- [ ] a\n  - [ ] b\n* [ ] c\n- [x] d\nText - [ ] e") != 2 {
		t.Fatal("unchecked rule changed")
	}
	for _, text := range []string{
		"```md\n- [ ] Example\n```\n- [ ] Real",
		"````md\n```\n- [ ] Example\n````\n- [ ] Real",
		"~~~\n```\n- [ ] Example\n~~~\n- [ ] Real",
	} {
		if count := Unchecked(text); count != 1 {
			t.Fatalf("fenced example counted as work: %d", count)
		}
	}
	if found := StatusLines("## Status\n" + strings.Repeat("é", 125)); len([]rune(found[0])) != 120 {
		t.Fatal(found)
	}
	state := t.TempDir()
	if len(Entries(state, "../elsewhere")) != 0 || len(Entries(state, "alpha")) != 0 {
		t.Fatal("unowned or missing notes were read")
	}
	for _, item := range []struct{ stamp, text string }{{"2026-01-05T09:00:00Z", "The first note,\non two lines."}, {"2026-01-05T09:30:00Z", "The second note."}} {
		if _, err := notes.Append(state, "alpha", notes.Entry(item.text, item.stamp)); err != nil {
			t.Fatal(err)
		}
	}
	found := Entries(state, "alpha")
	if !reflect.DeepEqual(found, []Entry{{"2026-01-05T09:30:00Z", "The second note."}, {"2026-01-05T09:00:00Z", "The first note, on two lines."}}) {
		t.Fatal(found)
	}
}

func TestMapBuild(t *testing.T) {
	root := t.TempDir()
	source, state := filepath.Join(root, "corpus"), filepath.Join(root, "state")
	os.MkdirAll(filepath.Join(source, "alpha"), 0700)
	os.Mkdir(state, 0700)
	for name, text := range map[string]string{"README.md": "# Alpha\n\nA harbour keeper.", "PLAN.md": "## Status\nA1 underway.\n## Work\n- [ ] Build it.", "GOAL.md": "- [ ] One goal."} {
		os.WriteFile(filepath.Join(source, "alpha", name), []byte(text), 0600)
	}
	db, err := index.Open(state, true, false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	config := corpus.Default(source, []string{source}, nil)
	if _, err = index.Refresh(db, state, config, false, 0); err != nil {
		t.Fatal(err)
	}
	if _, err = notes.Append(state, "alpha", notes.Entry("A saved note.", "2026-01-05T09:00:00Z")); err != nil {
		t.Fatal(err)
	}
	page, names, err := Build(db, config, state, "ALPHA")
	if err != nil || page.Project != "alpha" || page.Purpose != "A harbour keeper." || page.Unchecked["PLAN.md"] != 1 || page.Unchecked["GOAL.md"] != 1 || page.Notes.Total != 1 || page.Milestones.Total != 1 || len(names) != 1 || page.ScannedAt == 0 {
		t.Fatal(page, names, err)
	}
	page, _, err = Build(db, config, state, "missing")
	if err != nil || page.Project != "" {
		t.Fatal(page, err)
	}
	db.Close()
	if _, _, err = Build(db, config, state, "alpha"); err == nil {
		t.Fatal("closed database accepted")
	}
}

func TestGitFacts(t *testing.T) {
	git := testutil.Repository(t, t.TempDir(), strings.Repeat("Subject ", 40))
	if got := LatestCommit(filepath.Dir(git)); platform.GitPath() != "" && (got == nil || got.Date != "2025-10-12T20:13:20Z" || got.Subject != strings.TrimSpace(strings.Repeat("Subject ", 25))) {
		t.Fatal(got)
	}
	empty := testutil.Repository(t, t.TempDir(), "")
	if LatestCommit(filepath.Dir(empty)) != nil {
		t.Fatal("empty repository reported a commit")
	}
	t.Setenv("PATH", "")
	if LatestCommit(filepath.Dir(git)) != nil {
		t.Fatal("missing Git accepted")
	}
	if LatestCommit(t.TempDir()) != nil {
		t.Fatal("missing repository accepted")
	}
	var bounded boundedOutput
	data := []byte(strings.Repeat("x", 10000))
	n, err := bounded.Write(data)
	bounded.Write(data)
	if n != len(data) || err != nil || len(bounded) != 4096 {
		t.Fatal(n, err, len(bounded))
	}
}

func TestMapSnapshot(t *testing.T) {
	root := t.TempDir()
	source, folder := filepath.Join(root, "corpus"), filepath.Join(root, "state")
	os.MkdirAll(filepath.Join(source, "alpha"), 0700)
	os.Mkdir(folder, 0700)
	for name, text := range map[string]string{"README.md": "# Alpha\n\nVersion 0", "PLAN.md": "## Status\nVersion 0"} {
		if err := os.WriteFile(filepath.Join(source, "alpha", name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	config := corpus.Default(source, []string{source}, nil)
	reader, err := index.Open(folder, true, false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err = index.Refresh(reader, folder, config, false, 0); err != nil {
		t.Fatal(err)
	}
	writer, err := index.Open(folder, false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	stop := make(chan struct{})
	ended := make(chan error, 1)
	go func() {
		for n := 1; ; n++ {
			select {
			case <-stop:
				ended <- nil
				return
			default:
			}
			tx, err := writer.Begin()
			if err != nil {
				ended <- err
				return
			}
			_, err = tx.Exec("UPDATE chunks SET text=CASE kind WHEN 'readme' THEN '# Alpha'||char(10)||char(10)||? ELSE '## Status'||char(10)||? END", fmt.Sprintf("Version %d", n), fmt.Sprintf("Version %d", n))
			if err != nil {
				tx.Rollback()
				ended <- err
				return
			}
			if err = tx.Commit(); err != nil {
				ended <- err
				return
			}
		}
	}()
	var failure string
	for n := 0; n < 200; n++ {
		page, _, err := Build(reader, config, folder, "alpha")
		if err != nil {
			failure = err.Error()
			break
		}
		if page.Milestones.Count != 1 || page.Purpose != page.Milestones.Lines[0] {
			failure = fmt.Sprintf("mixed map snapshot: %+v", page)
			break
		}
	}
	close(stop)
	if err := <-ended; err != nil {
		t.Fatal(err)
	}
	if failure != "" {
		t.Fatal(failure)
	}
}

func TestMapNotesLink(t *testing.T) {
	folder := t.TempDir()
	if err := os.Mkdir(filepath.Join(folder, "notes"), 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(folder, "auth.json")
	if err := os.WriteFile(target, []byte("Owned excluded fixture. Never read it."), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(folder, "notes", "alpha.md")); err != nil {
		t.Skip("owned file links unavailable")
	}
	if found := Entries(folder, "alpha"); len(found) != 0 {
		t.Fatal("notes link read an excluded path", found)
	}
}
