package corpus

import (
	"brain/internal/platform"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func put(t *testing.T, root, name, text string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestExcludedCandidates(t *testing.T) {
	t.Run("portable reserved names", testReservedPaths)
	root := t.TempDir()
	put(t, root, "alpha/README.md", "kept")
	for _, name := range []string{"alpha/.local/hidden.md", "alpha/.env-folder/hidden.md", "alpha/private.pem/hidden.md", "alpha/private.key/hidden.md", "alpha/auth.json/hidden.md", "alpha/node_modules/hidden.md", "alpha/dist/hidden.md", "alpha/.venv/hidden.md", "alpha/.stryker-tmp/hidden.md", "alpha/.claude/worktrees/hidden.md", "alpha/.git/hidden.md", "alpha/talk.jsonl/hidden.md", "alpha/CON.md", "alpha/LPT1.md"} {
		put(t, root, name, "excluded")
	}
	config := Default(root, []string{root}, nil)
	found, skipped, err := Scan(config)
	if err != nil || len(found) != 1 || len(skipped) != 0 {
		t.Fatal(len(found), skipped, err)
	}
	for _, name := range []string{"alpha/.local", "alpha/.env-folder", "alpha/private.pem", "alpha/.claude/worktrees", "alpha/.local/*"} {
		config.Sources = []Source{{Path: filepath.Join(root, filepath.FromSlash(name))}}
		found, _, err = Scan(config)
		if err != nil || len(found) != 0 {
			t.Fatal(name, len(found), err)
		}
	}
	config.Sources = []Source{{Path: filepath.Join(root, "alpha/.local"), Kind: "note"}}
	data, _ := json.Marshal(config)
	os.WriteFile(filepath.Join(root, "config.json"), data, 0600)
	put(t, root, "notes/owned.md", "legitimate owned note")
	loaded, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	found, _, err = Scan(loaded)
	if err != nil || len(found) != 1 || found[filepath.Join(root, "notes/owned.md")].Kind != "note" {
		t.Fatal(found, err)
	}
}
func TestKindsAndSources(t *testing.T) {
	root := t.TempDir()
	for name, want := range map[string]string{"AGENTS.md": "rules", "claude.md": "rules", "SKILL.md": "skill", "README-old.md": "readme", "MASTER_PLAN.md": "plan", "PRD.md": "prd", "EXPLANATION.md": "doc", "talk.rtf": "transcript", "memory/MEMORY.md": "memory", "PLAN2.md": "doc", "PRD2.md": "doc", "README.docx": "doc", "plan.docx": "doc", "memory/keeper.rtf": "transcript", "memory/keeper.docx": "doc"} {
		if Kind(filepath.Join(root, name)) != want {
			t.Fatal(name, Kind(name))
		}
	}
	put(t, root, "README.md", "root")
	put(t, root, "alpha/docs/a.md", "alpha")
	put(t, root, "archive/old.md", "old")
	put(t, root, "external/tool-main/README.md", "third party")
	if err := os.Symlink(filepath.Join(root, "README.md"), filepath.Join(root, "alias.md")); err != nil {
		t.Fatal(err)
	}
	config := Default(root, []string{root}, []string{"external/*-main"})
	config.ThirdParty = []string{"external/*-main"}
	found, _, err := Scan(config)
	if err != nil || len(found) != 4 {
		t.Fatal(len(found), err)
	}
	if found[filepath.Join(root, "external/tool-main/README.md")].Kind != "third-party" || !found[filepath.Join(root, "archive/old.md")].Superseded {
		t.Fatal(found)
	}
	config.Sources = []Source{{Path: filepath.Join(root, "*/docs"), Project: "memory"}, {Path: filepath.Join(root, "missing-notes"), Kind: "note"}}
	found, _, err = Scan(config)
	if err != nil || len(found) != 1 {
		t.Fatal(found, err)
	}
	config.Sources = []Source{{Path: filepath.Join(root, "missing")}}
	if _, _, err = Scan(config); err == nil {
		t.Fatal("missing source accepted")
	}
	config.Sources = []Source{{Path: filepath.Join(root, "notes"), Kind: "note"}}
	put(t, root, "notes/alpha.md", "note")
	found, _, err = Scan(config)
	if err != nil || found[filepath.Join(root, "notes/alpha.md")].Project != "alpha" {
		t.Fatal(found, err)
	}
	if !platform.ReservedName("COM9.md") || !platform.ReservedName("AUX") || platform.ReservedName("company.md") {
		t.Fatal("reserved name rule")
	}
}
func TestConfig(t *testing.T) {
	root := t.TempDir()
	config := Default(root, nil, nil)
	if len(config.Sources) != 1 {
		t.Fatal(config)
	}
	configPath := filepath.Join(root, "config.json")
	for _, data := range [][]byte{[]byte("not json"), []byte("{}"), []byte(`{"sources":[{"path":"relative"}],"options":{"max_file_bytes":1,"inline_refresh_limit":1,"purge_after_days":1}}`)} {
		os.WriteFile(configPath, data, 0600)
		if _, err := Load(root); err == nil {
			t.Fatal("bad config accepted")
		}
	}
	data, _ := json.Marshal(config)
	os.WriteFile(configPath, data, 0600)
	found, err := Load(root)
	if err != nil || len(found.Sources) != 2 {
		t.Fatal(found, err)
	}
	if _, err := Load(filepath.Join(root, "absent")); err == nil {
		t.Fatal("missing config accepted")
	}
}

func TestWildcardAbsentAndNonfolderMatches(t *testing.T) {
	root := t.TempDir()
	put(t, root, "memories/file/memory", "ordinary file")
	put(t, root, "memories/plain", "ordinary file")
	for _, pattern := range []string{"absent/projects/*/memory", "memories/*/memory", "memories/*"} {
		config := Default(root, []string{filepath.Join(root, filepath.FromSlash(pattern))}, nil)
		found, skipped, err := Scan(config)
		if err != nil || len(found) != 0 || len(skipped) != 0 {
			t.Fatal(pattern, found, skipped, err)
		}
	}
}

func TestLiteralBracketsAndWildcardRoots(t *testing.T) {
	base := t.TempDir()
	literal := filepath.Join(base, "year[2026]", "notes[2026]")
	sibling := filepath.Join(base, "year[2026]", "notes2")
	chosen := put(t, literal, "README.md", "chosen marker")
	unselected := put(t, sibling, "README.md", "unselected marker")
	config := Default(base, []string{literal}, nil)
	found, skipped, err := Scan(config)
	if err != nil || len(skipped) != 0 || len(found) != 1 || found[chosen].Path != chosen || found[unselected].Path != "" {
		t.Fatal(found, skipped, err)
	}
	if SourcePattern(config.Sources[0]) || !MatchesRoot(config.Sources[0], literal) || MatchesRoot(config.Sources[0], sibling) {
		t.Fatal("bracketed source changed identity")
	}
	missing := filepath.Join(base, "year[2026]", "absent[2026]")
	config.Sources = []Source{{Path: missing}}
	if _, _, err := Scan(config); err != ErrUnreadable {
		t.Fatal("missing literal source selected another root", err)
	}
	pattern := filepath.Join(base, "year[2026]", "notes*")
	config.Sources = []Source{{Path: pattern}}
	found, skipped, err = Scan(config)
	if err != nil || len(skipped) != 0 || len(found) != 2 || found[chosen].Path != chosen || found[unselected].Path != unselected {
		t.Fatal(found, skipped, err)
	}
	if !MatchesRoot(config.Sources[0], literal) || !MatchesRoot(config.Sources[0], sibling) || MatchesRoot(config.Sources[0], filepath.Join(base, "year2", "notes2")) {
		t.Fatal("wildcard matched the wrong ancestor")
	}
	if !matchSourcePart("[ap]*", "proalpha") || matchSourcePart("[ap]*", "zero") {
		t.Fatal("leading character-class wildcard changed")
	}
	config.Sources = []Source{{Path: filepath.Join(base, "year[2026]", "notes[2026]*")}}
	found, skipped, err = Scan(config)
	if err != nil || len(skipped) != 0 || len(found) != 1 || found[chosen].Path != chosen || found[unselected].Path != "" || !MatchesRoot(config.Sources[0], literal) || MatchesRoot(config.Sources[0], sibling) {
		t.Fatal("brackets inside a wildcard component changed identity", found, skipped, err)
	}
	noteRoot := filepath.Join(base, "state[note]", "notes")
	note := put(t, noteRoot, "saved.md", "saved marker")
	config.Sources = []Source{{Path: noteRoot, Kind: "note"}}
	found, skipped, err = Scan(config)
	if err != nil || len(skipped) != 0 || len(found) != 1 || found[note].Kind != "note" {
		t.Fatal(found, skipped, err)
	}
}

func TestSourcePatternIdentity(t *testing.T) {
	base := t.TempDir()
	for _, name := range []string{"alpha", "proalpha", "probeta", "zero"} {
		put(t, filepath.Join(base, name), "README.md", name+" marker")
		put(t, filepath.Join(base, name), ".env-secret.md", "excluded secret")
	}
	for _, test := range []struct {
		pattern string
		count   int
	}{
		{"pro*", 2},
		{"pro?lpha", 1},
		{"[ap]*", 3},
	} {
		config := Default(base, []string{filepath.Join(base, test.pattern)}, nil)
		if !SourcePattern(config.Sources[0]) {
			t.Fatal(test.pattern, "lost pattern identity")
		}
		found, skipped, err := Scan(config)
		if err != nil || len(skipped) != 0 || len(found) != test.count {
			t.Fatal(test.pattern, len(found), skipped, err)
		}
		for _, entry := range found {
			if entry.Relative != "README.md" || entry.Namespace == "source:0:" || !MatchesRoot(config.Sources[0], entry.Source) {
				t.Fatal(test.pattern, entry)
			}
		}
	}
	legacy := Source{Path: filepath.Join(base, "pro*")}
	if !SourcePattern(legacy) || !MatchesRoot(legacy, filepath.Join(base, "proalpha")) {
		t.Fatal("older config lost its intentional pattern")
	}
	literal := Source{Path: filepath.Join(base, "pro*"), Literal: true}
	if SourcePattern(literal) || MatchesRoot(literal, filepath.Join(base, "proalpha")) || !MatchesRoot(literal, literal.Path) {
		t.Fatal("literal intent did not keep exact identity")
	}
}

func TestGenericNeverRead(t *testing.T) {
	root := t.TempDir()
	allowed := put(t, root, "project/README.md", "allowed content")
	protected := put(t, root, ".ssh/private.md", "protected content")
	for _, sources := range [][]string{{root}, {filepath.Join(root, ".ssh")}, {filepath.Join(root, ".ssh", "*")}} {
		config := Default(root, sources, nil)
		result, err := ScanWithPolicy(config)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := result.Entries[protected]; ok || !NeverReadPath(protected) {
			t.Fatal("protected source was accepted", sources)
		}
		if sources[0] == root {
			if _, ok := result.Entries[allowed]; !ok {
				t.Fatal("allowed sibling missing")
			}
		} else if len(result.Entries) != 0 {
			t.Fatal("direct protected root was read", result.Entries)
		}
	}
}

func TestDefaultReadsOnlyRoot(t *testing.T) {
	root := t.TempDir()
	config := Default(root, nil, nil)
	if len(config.Sources) != 1 || config.Sources[0].Path != root || !config.Sources[0].Literal || config.Sources[0].Project != "" {
		t.Fatalf("default sources %+v, want only the literal folder %s", config.Sources, root)
	}
}
