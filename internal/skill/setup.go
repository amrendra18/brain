package skill

import (
	"brain/internal/platform"
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

type matrixRow struct{ harness, system, skill, hook, shell string }

var matrix = []matrixRow{
	{"claude", "darwin", ".claude/skills/brain/SKILL.md", ".claude/settings.json", "sh"},
	{"claude", "linux", ".claude/skills/brain/SKILL.md", ".claude/settings.json", "sh"},
	{"claude", "windows", ".claude/skills/brain/SKILL.md", ".claude/settings.json", "exec"},
	{"codex", "darwin", ".agents/skills/brain/SKILL.md", ".codex/hooks.json", "sh"},
	{"codex", "linux", ".agents/skills/brain/SKILL.md", ".codex/hooks.json", "sh"},
	{"codex", "windows", ".agents/skills/brain/SKILL.md", ".codex/hooks.json", "cmd"},
	{"copilot", "darwin", ".copilot/skills/brain/SKILL.md", ".copilot/hooks/brain-copilot.json", "sh"},
	{"copilot", "linux", ".copilot/skills/brain/SKILL.md", ".copilot/hooks/brain-copilot.json", "sh"},
	{"copilot", "windows", ".copilot/skills/brain/SKILL.md", ".copilot/hooks/brain-copilot.json", "powershell"},
	{"vscode", "darwin", ".agents/skills/brain/SKILL.md", ".copilot/hooks/brain-vscode.json", "sh"},
	{"vscode", "linux", ".agents/skills/brain/SKILL.md", ".copilot/hooks/brain-vscode.json", "sh"},
	{"vscode", "windows", ".agents/skills/brain/SKILL.md", ".copilot/hooks/brain-vscode.json", "powershell"},
}

type Installation struct {
	Harness      string         `json:"harness"`
	System       string         `json:"system"`
	SkillFile    string         `json:"skill_file"`
	HookFile     string         `json:"hook_file"`
	HookEntry    map[string]any `json:"hook_entry"`
	Allowance    string         `json:"allowance"`
	Trust        string         `json:"trust"`
	Limits       string         `json:"limits"`
	Written      bool           `json:"written"`
	SkillCurrent bool           `json:"skill_current"`
}

// CurrentSkill compares a safe installed copy with the generated skill.
// Missing, linked, unreadable, and oversized destinations are not current.
func CurrentSkill(home, skillFile string, generated []byte) bool {
	relative, err := filepath.Rel(home, skillFile)
	if err != nil || !filepath.IsLocal(relative) {
		return false
	}
	installed, err := platform.ReadStateAtMost(home, relative, int64(len(generated)))
	return err == nil && bytes.Equal(installed, generated)
}

func Setup(harness, system, home, executable, state string, config ...string) (Installation, bool) {
	for _, row := range matrix {
		if row.harness != harness || row.system != system {
			continue
		}
		join := func(relative string) string {
			value := path.Join(strings.ReplaceAll(home, `\`, "/"), relative)
			if system == "windows" {
				value = strings.ReplaceAll(value, "/", `\`)
			}
			return value
		}
		installation := Installation{Harness: harness, System: system, SkillFile: join(row.skill), HookFile: join(row.hook)}
		format := "text"
		if harness == "copilot" {
			format = "copilot"
		}
		if harness == "vscode" {
			format = "vscode"
		}
		argv := []string{executable, "--brief", "--format", format, "--state-dir", state}
		if len(config) > 0 && config[0] != "" {
			argv = append(argv, "--config", config[0])
		}
		command := platform.Command(row.shell, argv)
		hook := map[string]any{"type": "command", "command": command}
		event := "SessionStart"
		switch harness {
		case "claude":
			if system == "windows" {
				hook["command"], hook["args"] = executable, argv[1:]
			}
			installation.HookEntry = map[string]any{"hooks": map[string]any{event: []any{map[string]any{"hooks": []any{hook}}}}}
			installation.Trust = "Review and enable this SessionStart entry in Claude Code's /hooks. Restart the session after installing the skill."
			installation.Allowance = fmt.Sprintf("When the sandbox is enabled, merge %q into sandbox.filesystem.allowWrite. You change these settings yourself.", state)
			if system == "windows" {
				installation.Allowance = "Native Windows has no Claude Code sandbox. WSL uses the Linux build and its separate Linux home."
			}
		case "codex":
			if system == "windows" {
				hook["commandWindows"] = command
			}
			installation.HookEntry = map[string]any{"hooks": map[string]any{event: []any{map[string]any{"hooks": []any{hook}}}}}
			installation.Trust = "Open /hooks in Codex CLI and trust this entry. Trust it again after every hook edit. Restart for the skill."
			installation.Allowance = fmt.Sprintf("Best effort legacy guidance: merge this into %s:\n[sandbox_workspace_write]\nwritable_roots = [%q]\nA managed permission profile may require its owner to allow this state folder. Local settings may not override managed permissions.", join(".codex/config.toml"), state)
			installation.Limits = "Codex desktop hooks may not run. Invoke the skill and search directly when no brief appears. Unix login profiles should keep stdout quiet. Codex 0.162 and earlier also used " + join(".codex/skills/brain/SKILL.md") + "; this command copies only the documented .agents skill."
			if system == "windows" {
				installation.Limits += " The encoded command invokes " + executable + " through Windows PowerShell."
			}
		case "copilot":
			event = "sessionStart"
			hook = map[string]any{"type": "command", "bash": platform.Command("sh", argv), "powershell": platform.Command("powershell", argv), "timeoutSec": 10}
			installation.HookEntry = map[string]any{"version": 1, "hooks": map[string]any{event: []any{hook}}}
			installation.Trust = "Review the user hook entry and run /skills reload in Copilot CLI. Use version 1.0.89 or newer. Windows needs PowerShell 6 or newer."
			installation.Allowance = fmt.Sprintf("Allow the binary to read sources and write %q under the caller's local permission policy. No Copilot sandbox allowance is documented.", state)
			installation.Limits = "Copilot CLI consumes top-level additionalContext. Copilot cloud cannot access this computer's corpus."
		case "vscode":
			field := map[string]string{"darwin": "osx", "linux": "linux", "windows": "windows"}[system]
			hook[field] = command
			installation.HookEntry = map[string]any{"hooks": map[string]any{event: []any{hook}}}
			installation.Trust = "Review the hook and enable chat.useHooks in VS Code. Hooks are Preview and run on the first prompt. Reload the window for the skill."
			installation.Allowance = fmt.Sprintf("Allow local source reads and writes to %q under the extension host's permission policy. No hook sandbox allowance is documented.", state)
			installation.Limits = "VS Code consumes hookSpecificOutput.additionalContext. Its hook shell is unverified; the Windows entry assumes PowerShell. Check a fresh session and use the skill/search fallback if no brief appears. Copilot CLI and VS Code share the user hook folder. Choose the entry for the intended caller; do not enable both alternatives for the same session."
		}
		if harness == "copilot" {
			installation.Limits += " Copilot CLI and VS Code share the user hook folder. Choose one caller's entry for a session."
		}
		return installation, true
	}
	return Installation{}, false
}

func (installation Installation) Text() string {
	entry, _ := json.MarshalIndent(installation.HookEntry, "", "  ")
	return fmt.Sprintf("Harness: %s (%s)\nSkill file: %s\nHook file: %s\nReview existing settings. Add or merge this entry; preserve all other entries:\n%s\nAllowance: %s\nTrust: %s\nLimits: %s\nSkill copied: %t\nSkill current: %t\n", installation.Harness, installation.System, installation.SkillFile, installation.HookFile, entry, installation.Allowance, installation.Trust, installation.Limits, installation.Written, installation.SkillCurrent)
}
