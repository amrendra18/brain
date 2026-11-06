# Verified features

| # | Feature | Map | Journey |
| --- | --- | --- | --- |
| 1 | Initialize, index and cited search | [Cited search](cited-search.md) | `scripts/verify fast --feature cited-search` |
| 2 | External contract probes and temporary installation | [Tool contract](tool-contract.md) | `scripts/verify fast --feature tool-contract` |
| 3 | Installed skill drift in an owned home | [Skill drift](skill-drift.md) | `scripts/verify fast --feature skill-drift` |
| 4 | Separate config and stable source citations | [Config and sources](config-sources.md) | `scripts/verify fast --feature config-sources` |
| 5 | Stored reads and explicit indexing | [Freshness](freshness.md) | `scripts/verify fast --feature freshness` |
| 6 | Protected values and policy refresh | [Privacy](privacy.md) | `scripts/verify fast --feature privacy` |
| 7 | Excluded files and ignore rules | [Exclusions](exclusions.md) | `scripts/verify fast --feature exclusions` |
| 8 | Daily scheduling with owned stand-ins | [Schedule](schedule.md) | `scripts/verify fast --feature schedule` |
| 9 | Saved selection and data relocation | [Settings and relocation](settings-relocation.md) | `scripts/verify fast --feature settings-relocation` |

Without `--feature`, both fast and full run all nine mapped features. Full repeats each feature twice against fresh temporary folders.
The verifier builds its default target from the current source tree. An absent explicit `--binary FILE` causes exit 3 before launch.
