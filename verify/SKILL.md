---
name: verify-brain
description: Drive Brain's real command-line binary with private test data and retain evidence. Use after changes to mapped features.
---

# Verify Brain

## Doctor

Run `scripts/verify --help` for features and exit meanings. The command checks the pinned compiler, target executable, and owned evidence folders before launching Brain.
The verifier builds the default target from the current source tree. An absent explicit `--binary FILE` causes exit 3 before launch.

## Launch

Run `scripts/verify fast --feature cited-search` from the checkout. Each command uses the built `.local/bin/brain`.
The driver creates a temporary source, home and state folder. It initializes a usable index through public commands.
Use `--binary FILE` to choose another built target. The driver never starts a service.

## Drive

The `cited-search` journey runs `init`, `index`, `search "amber otters" --no-refresh`, and `show` using the returned chunk identifier.
It checks the expected project, source path, text, index age, config and database files.
Run `scripts/verify full` to repeat every mapped feature against fresh state.
The full lane repeats all nine mapped features twice against fresh temporary folders.
The tool contract runs eight built-in probes against the executable. A clean clone needs no separate checker.
It also copies the executable into an owned temporary home, checks PATH lookup, and runs its version command.
Use `scripts/verify fast --feature tool-contract` to select that feature alone.
Run the verifier on Windows and Linux to check behavior on those systems.

## Measure

Each command record includes elapsed milliseconds, arguments, stdout, stderr and exit code.
File snapshots under `traces/` record source and isolated home contents before and after each journey.
The tool contract records each command and probe. A failed probe fails the feature.
These times describe the small test input. They establish no corpus performance bar.

## Evidence

The last output line names `.local/verify/<UTC time>-<unique suffix>/`.
Read `summary.md`, `summary.json`, and `index.html`. The `logs/` folder holds complete command records.
The `screens/` folder stays empty because the surface is a CLI. There are no screenshot or heap claims.
Positive journeys use the real binary with synthetic documents. There are no substitute implementations.
Exit 0 means every selected feature was proved. Exit 1 means a feature failed, 2 means invalid usage, and 3 means the environment prevented launch.

## Cleanup

The command removes only its temporary source, home and state folder. It retains evidence after success and failure.
Check `scratch_removed` in `summary.json`. No installed hooks, skills, protected state or repositories are inspected.

## Helpers

`scripts/verify fast --expected-owner wrong-owner` deliberately fails the project assertion and exits 1.
`scripts/verify fast --binary /absent/brain` proves the missing-target refusal and exits 3.
Use `verify/features/README.md` to extend the feature map with the implementation.
