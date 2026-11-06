package platform

import (
	"fmt"
	"os"
)

// WriteGuidance reports environment evidence; it never reads harness settings.
func WriteGuidance(folder string) string {
	if os.Getenv("CODEX_SANDBOX") != "" {
		return fmt.Sprintf("Best effort: CODEX_SANDBOX suggests Codex. Merge this legacy allowance into its config.toml:\n[sandbox_workspace_write]\nwritable_roots = [%q]\nA managed permission profile may require its owner to allow this folder. Local settings may not override managed permissions.\nRestart the session after you change it.", folder)
	}
	if os.Getenv("CLAUDE_CODE") != "" || os.Getenv("CLAUDECODE") != "" {
		return fmt.Sprintf("Best effort: environment markers suggest Claude Code. If its sandbox is enabled, add %q to sandbox.filesystem.allowWrite. Native Windows has no Claude Code sandbox.", folder)
	}
	return fmt.Sprintf("Best effort: no known harness was detected. Allow writes to %q or choose another local state folder.", folder)
}
