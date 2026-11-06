# Agent rules

Brain indexes local notes and project documents for offline search. Search results cite their source files and report the index age. The shipped binary makes no model or network calls.

## Product boundaries

1. Keep source documents read-only. Write only to selected state, configuration, schedule, and skill destinations when the command explicitly requests it.
2. Never open excluded paths, including `.local/`, `.git/`, `.env*`, `*.pem`, `*.key`, `auth.json`, `node_modules`, `dist`, `.venv`, `.stryker-tmp`, `.claude/worktrees`, `*.jsonl`, and Windows reserved names. `TestExclusionsNeverRead` checks this.
3. Search and other reading commands use the stored index and include `index_age_s`. Only an explicit indexing command refreshes sources. `TestFreshnessReadsNeverOpenSources` and `TestManualIndexDiscoversProject` check this.
4. Keep ranking, chunking, and conversion above the measured retrieval floor. `TestYardstick` checks ranking.
5. Keep the runtime offline. `TestNoNetworkImports` and `TestNoNetwork` check this.
6. Every refusal gives a plain message without a private path, a shell-form `next` command, and a shell-free `next_argv` when safe. `TestNextRunsWithoutBrainOnPATH` checks recovery.
7. Protect saved notes and replies with the redaction rules in `internal/redact/`.
8. A killed or concurrent process must preserve the old index and saved notes. `TestKilledIndexLeavesOldIndex`, `TestConcurrentSearchDuringIndex`, and `TestNoteLockAcrossProcesses` check this.
9. Preserve equivalent behavior across macOS, Windows, and Linux. Use relative source paths and stable chunk IDs. `TestRelativePathsSameIds` checks this.
10. A safe but unwritable state folder allows read-only answers. Mutating commands refuse with `state-unwritable`. `TestReadOnlyFallback` checks this.
11. Every public command has stateless help and one structured output object. `TestToolContract`, `TestHelpFormats`, and `TestContractClosedStdin` check this.
12. Keep the generated skill in sync with command help by running `scripts/generate-skill --check`.
13. Keep the runtime offline and Go modules pinned in `go.mod` and `go.sum`. `TestPinnedModuleCache` checks the module files and cached modules.

## Development

1. Use owned temporary state for tests. Never run development checks against a user's default state folder.
2. Run `./scripts/check` and `./scripts/verify fast` for focused work.
3. Run `./scripts/check full` and `./scripts/verify full` before a release.
4. Keep generated output and compiler caches under ignored `.local/`.
5. See [CONTRIBUTING.md](CONTRIBUTING.md) for the pinned toolchain and check commands.
