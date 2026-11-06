# Config and sources

1. Run `scripts/verify fast --feature config-sources` to drive the real binary with an owned config, state and source folders.
2. Adding a source leaves search unchanged until `index`. Repeating add or remove returns exit 3 and preserves exact config bytes.
3. Removing a preceding source preserves the retained citation ID. Moving the owned fixture folder and updating its source preserves that same ID.
4. Unknown top-level, options and retained-source config fields survive edits. Exclusion and third-party rules support list, add, remove and repeated operations.
5. Removing a built-in exclusion returns invalid input without changing config. Emitted recovery arguments retain the selected config and state.
6. Command logs and source/home snapshots remain under `.local/verify/`. The expected source snapshot changes only when the verifier deliberately moves its fixture folder.
7. This journey passes `--no-refresh` to exercise that accepted search option. The freshness journey proves ordinary reads use the stored index.
8. Validator tests deliberately supply wrong citation IDs, unscoped recovery arguments and changed source snapshots. A fixture executable tests the journey itself. Only real-binary evidence proves product behavior.
