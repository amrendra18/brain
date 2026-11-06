package skill

import (
	"reflect"
	"strings"
	"testing"
)

func TestBriefLimits(t *testing.T) {
	if text := Brief(Facts{Name: "Alpha  Beta", Purpose: "Two\nlines"}); !strings.Contains(text, "Alpha  Beta") || strings.Count(text, "\n") != 3 {
		t.Fatal("project name or line count changed", text)
	}
	for _, facts := range []Facts{{Files: 1, Chunks: 1, Age: 2, Name: "alpha", ProjectFiles: 1, Purpose: "Keeps the lease.", Next: "brain search lease"}, {Problem: "init is needed.", Next: "brain init"}, {ReadOnly: true, Next: "brain doctor"}, {Name: strings.Repeat("n", 400), Purpose: strings.Repeat("p", 400), Next: strings.Repeat("x", 400)}} {
		text := Brief(facts)
		if strings.Count(text, "\n") != 3 || len([]rune(text)) >= 1000 || !strings.HasPrefix(text, "Brain") {
			t.Fatal(text)
		}
	}
}

func TestBriefKeepsSearchAlone(t *testing.T) {
	search := `"/fixture/brain" "search" "your words" "--state-dir" "` + strings.Repeat("s", 400) + `"`
	doctor := `"/fixture/brain" "doctor" "--state-dir" "` + strings.Repeat("s", 400) + `"`
	for _, row := range []struct {
		facts  Facts
		wanted string
	}{
		{Facts{IndexComplete: true, Next: search, Doctor: doctor}, "Search: " + search + ". The doctor command cannot fit this brief. Run the binary's help command for write guidance."},
		{Facts{IndexComplete: true, ReadOnly: true, Next: search, Doctor: doctor}, "Search stored data: " + search + ". The doctor command cannot fit this brief. Run the binary's help command for write guidance."},
		{Facts{IndexComplete: true, Gaps: 2, Next: search, Doctor: doctor}, "Search stored data: " + search + ". The doctor command cannot fit this brief. Run the binary's help command for repair guidance."},
		{Facts{IndexComplete: true, Next: "brain search", Doctor: "brain\ndoctor"}, "Search: brain search. The doctor command cannot fit this brief. Run the binary's help command for write guidance."},
		{Facts{IndexComplete: true, Next: "brain search", Doctor: "brain doctor"}, "Search: brain search. Doctor for write guidance: brain doctor"},
		{Facts{IndexComplete: true, Next: search + strings.Repeat("s", 600), Doctor: "brain doctor"}, "The command cannot fit this brief. Run the binary's help command for recovery guidance."},
		{Facts{IndexComplete: true, Next: "brain\nsearch", Doctor: "brain doctor"}, "The command cannot fit this brief. Run the binary's help command for recovery guidance."},
		{Facts{Problem: "init is needed.", Next: search + search + search}, "The command cannot fit this brief. Run the binary's help command for recovery guidance."},
	} {
		text := Brief(row.facts)
		lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
		if len(lines) != 3 || lines[2] != row.wanted || len([]rune(text)) >= 1000 {
			t.Fatalf("wrong command line %q", text)
		}
	}
}

func TestSkillFrontmatter(t *testing.T) {
	text := Text([]Command{{"search", "Find cited chunks.", `search "your words" --format json`}})
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if lines[0] != "---" || lines[3] != "---" || lines[4] != "" {
		t.Fatal("invalid front matter", text)
	}
	fields := map[string]string{}
	for _, line := range lines[1:3] {
		name, value, ok := strings.Cut(line, ": ")
		if !ok || value == "" || strings.Contains(value, ": ") || strings.Contains(value, " #") {
			t.Fatal("front matter must hold plain strings", line)
		}
		fields[name] = value
	}
	if fields["name"] != "brain" || len(fields["description"]) > 160 || len(fields) != 2 || len(lines) > 60 || len(strings.Fields(text)) > 400 {
		t.Fatal("skill limits", fields, len(lines), len(strings.Fields(text)))
	}
	if !strings.Contains(text, "command path") || !strings.Contains(text, "index_age_s") || !strings.Contains(text, "brain search") {
		t.Fatal("missing caller instructions", text)
	}
}

func TestSetupPerHarnessPerOS(t *testing.T) {
	for _, system := range []string{"darwin", "linux", "windows"} {
		for _, harness := range []string{"claude", "codex", "copilot", "vscode"} {
			t.Run(system+"/"+harness, func(t *testing.T) {
				home, binary, state := "/fixture/home", "/fixture/Brain tool/brain", "/fixture/state"
				if system == "windows" {
					home, binary, state = `C:\fixture\home`, `C:\fixture\Brain tool\brain.exe`, `C:\fixture\state`
				}
				setup, ok := Setup(harness, system, home, binary, state)
				if !ok || setup.Harness != harness || setup.System != system || setup.SkillFile == "" || setup.HookFile == "" || setup.Allowance == "" || setup.Trust == "" || setup.HookEntry == nil {
					t.Fatal("incomplete setup", setup)
				}
				skillSuffix := map[string]string{"claude": ".claude/skills/brain/SKILL.md", "codex": ".agents/skills/brain/SKILL.md", "copilot": ".copilot/skills/brain/SKILL.md", "vscode": ".agents/skills/brain/SKILL.md"}[harness]
				hookSuffix := map[string]string{"claude": ".claude/settings.json", "codex": ".codex/hooks.json", "copilot": ".copilot/hooks/brain-copilot.json", "vscode": ".copilot/hooks/brain-vscode.json"}[harness]
				separator := "/"
				if system == "windows" {
					separator = `\`
					skillSuffix = strings.ReplaceAll(skillSuffix, "/", separator)
					hookSuffix = strings.ReplaceAll(hookSuffix, "/", separator)
				}
				if setup.SkillFile != home+separator+skillSuffix || setup.HookFile != home+separator+hookSuffix {
					t.Fatal("wrong documented folders", setup)
				}
				event := "SessionStart"
				if harness == "copilot" {
					event = "sessionStart"
				}
				entries := setup.HookEntry["hooks"].(map[string]any)[event].([]any)
				if len(entries) != 1 {
					t.Fatal("wrong hook events", setup.HookEntry)
				}
				hook := entries[0].(map[string]any)
				if harness == "claude" || harness == "codex" {
					hook = hook["hooks"].([]any)[0].(map[string]any)
				}
				if hook["type"] != "command" {
					t.Fatal("wrong hook type", hook)
				}
				if harness == "claude" && system == "windows" {
					if hook["command"] != binary || !reflect.DeepEqual(hook["args"], []string{"--brief", "--format", "text", "--state-dir", state}) {
						t.Fatal("wrong native exec arguments", hook)
					}
				}
				if harness == "copilot" && (setup.HookEntry["version"] != 1 || !strings.Contains(hook["bash"].(string), `"copilot"`) || !strings.Contains(hook["powershell"].(string), "'copilot'")) {
					t.Fatal("wrong Copilot hook", hook)
				}
				if harness == "vscode" {
					field := map[string]string{"darwin": "osx", "linux": "linux", "windows": "windows"}[system]
					if !strings.Contains(hook[field].(string), "vscode") {
						t.Fatal("wrong VS Code OS command", hook)
					}
				}
				text := setup.Text()
				for _, wanted := range []string{home, binary, state, "merge", "SKILL.md"} {
					if !strings.Contains(text, wanted) && !strings.Contains(text, strings.ReplaceAll(wanted, `\`, `\\`)) {
						t.Fatal("missing guidance", wanted, text)
					}
				}
				if harness == "claude" && system == "windows" && !strings.Contains(text, `"args"`) {
					t.Fatal(text)
				}
				if harness == "codex" && (!strings.Contains(text, "managed") || !strings.Contains(text, "desktop")) {
					t.Fatal(text)
				}
				if harness == "codex" && system == "windows" && !strings.Contains(text, "commandWindows") {
					t.Fatal(text)
				}
				if harness == "copilot" && (!strings.Contains(text, "additionalContext") || !strings.Contains(text, "cloud")) {
					t.Fatal(text)
				}
				if harness == "copilot" && system == "windows" && !strings.Contains(text, "powershell") {
					t.Fatal(text)
				}
				if harness == "vscode" && !strings.Contains(text, "hookSpecificOutput") {
					t.Fatal(text)
				}
			})
		}
	}
	for _, pair := range [][2]string{{"other", "darwin"}, {"claude", "other"}} {
		if _, ok := Setup(pair[0], pair[1], "/fixture", "/fixture/brain", "/fixture/state"); ok {
			t.Fatal("unknown matrix row accepted")
		}
	}
}
