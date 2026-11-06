package verifyrun

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSkillDriftReplyValidation(t *testing.T) {
	path := filepath.Join("owned", "home", ".claude", "skills", "brain", "SKILL.md")
	for _, tc := range []struct {
		name, item     string
		current, write bool
		valid          bool
	}{
		{"missing", `"skill_current":false,"written":false`, false, false, true},
		{"matching", `"skill_current":true,"written":false`, true, false, true},
		{"copied", `"skill_current":true,"written":true`, true, true, true},
		{"missing-field", `"written":false`, false, false, false},
		{"false-after-copy", `"skill_current":false,"written":true`, true, true, false},
		{"wrong-path", `"skill_current":true,"written":false`, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			location := path
			if tc.name == "wrong-path" {
				location = filepath.Join("owned", "home", "unrelated", "SKILL.md")
			}
			data := []byte(fmt.Sprintf(`{"count":1,"total":1,"empty":false,"setups":[{"skill_file":%q,%s}]}`, location, tc.item))
			err := checkSkillSetup(data, path, tc.current, tc.write)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t: %v", tc.valid, err)
			}
		})
	}
}

func TestSkillDriftCopyBoundary(t *testing.T) {
	skill := filepath.Join(".claude", "skills", "brain", "SKILL.md")
	before := map[string]string{".": "directory", "existing.json": "unchanged"}
	after := map[string]string{".": "directory", "existing.json": "unchanged", ".claude": "directory", filepath.Join(".claude", "skills"): "directory", filepath.Join(".claude", "skills", "brain"): "directory", skill: "skill-digest"}
	if err := onlySkillAdded(before, after, skill); err != nil {
		t.Fatal(err)
	}
	after["existing.json"] = "changed"
	if err := onlySkillAdded(before, after, skill); err == nil || !strings.Contains(err.Error(), "existing") {
		t.Fatal("existing settings change was accepted", err)
	}
	after["existing.json"] = "unchanged"
	after[filepath.Join(".claude", "hooks.json")] = "unrelated"
	if err := onlySkillAdded(before, after, skill); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatal("unexpected hook write was accepted", err)
	}
	delete(after, filepath.Join(".claude", "hooks.json"))
	delete(after, skill)
	if err := onlySkillAdded(before, after, skill); err == nil || !strings.Contains(err.Error(), "did not create") {
		t.Fatal("missing skill copy was accepted", err)
	}
}

func TestSkillDriftDetectsPreviewWrite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake binary is a deliberate failure input")
	}
	script := filepath.Join(t.TempDir(), "false-preview")
	content := `#!/bin/sh
if [ "$1" = help ]; then
  printf '# fixture skill\n'
  exit 0
fi
: > "$HOME/unexpected-preview-write"
printf '{"count":1,"total":1,"empty":false,"setups":[{"skill_file":"%s/.claude/skills/brain/SKILL.md","skill_current":false,"written":false}]}' "$HOME"
`
	if err := os.WriteFile(script, []byte(content), 0700); err != nil {
		t.Fatal(err)
	}
	evidence := t.TempDir()
	for _, name := range []string{"logs", "traces"} {
		if err := os.Mkdir(filepath.Join(evidence, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	d := driver{binary: script, evidence: evidence, summary: &Summary{Scratch: t.TempDir(), SideEffects: map[string]string{}}}
	if err := d.skillDrift(0); err == nil || !strings.Contains(err.Error(), "preview changed isolated home") {
		t.Fatal("deliberately broken preview was accepted", err)
	}
	if len(d.summary.Commands) != 2 {
		t.Fatal("failure input did not reach setup preview", len(d.summary.Commands))
	}
}

func TestSkillDriftFixtureJourney(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fixture executable; the real binary journey is portable")
	}
	script := filepath.Join(t.TempDir(), "fixture-brain")
	content := `#!/bin/sh
if [ "$1" = help ]; then
  printf '# fixture skill\n'
  exit 0
fi
skill="$HOME/.claude/skills/brain/SKILL.md"
current=false
written=false
if [ "$3" = --write ]; then
  mkdir -p "$HOME/.claude/skills/brain"
  printf '# fixture skill\n' > "$skill"
  current=true
  written=true
elif [ -f "$skill" ] && [ "$(cat "$skill")" = '# fixture skill' ]; then
  current=true
fi
printf '{"count":1,"total":1,"empty":false,"setups":[{"skill_file":"%s","skill_current":%s,"written":%s}]}' "$skill" "$current" "$written"
`
	if err := os.WriteFile(script, []byte(content), 0700); err != nil {
		t.Fatal(err)
	}
	evidence := t.TempDir()
	for _, name := range []string{"logs", "traces"} {
		if err := os.Mkdir(filepath.Join(evidence, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	d := driver{binary: script, evidence: evidence, summary: &Summary{Scratch: t.TempDir(), SideEffects: map[string]string{}}}
	if err := d.skillDrift(0); err != nil {
		t.Fatal(err)
	}
	if len(d.summary.Commands) != 5 || d.summary.SideEffects["skill_drift_1"] == "" {
		t.Fatal("fixture journey lacks command or side-effect evidence", d.summary)
	}
	if _, err := os.Stat(filepath.Join(evidence, "traces", "skill-drift-1-files.json")); err != nil {
		t.Fatal("file trace missing", err)
	}
}

func TestSkillDriftRejectsEmptyGeneratedSkill(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX empty-help fixture")
	}
	script := filepath.Join(t.TempDir(), "empty-help")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	evidence := t.TempDir()
	if err := os.Mkdir(filepath.Join(evidence, "logs"), 0700); err != nil {
		t.Fatal(err)
	}
	d := driver{binary: script, evidence: evidence, summary: &Summary{Scratch: t.TempDir(), SideEffects: map[string]string{}}}
	if err := d.skillDrift(0); err == nil || !strings.Contains(err.Error(), "generated skill is empty") {
		t.Fatal("empty generated skill was accepted", err)
	}
}
