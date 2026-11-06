# Cited search

## Sub-features

1. Initialize an empty state folder with a selected document source.
2. Index the document through the real executable.
3. Search two keywords and receive the correct project, path, snippet, identifier and index age.
4. Show the identifier and receive the complete cited text.
5. Preserve source bytes and leave the isolated home unchanged.

## How to get to it (user POV)

The verifier builds the default target from the current source tree. Run `brain init --source PATH`, then `brain index` using the same state folder.
Run `brain search "amber otters"` and `brain show ID` to follow the returned citation.
The harness adds explicit temporary state and JSON output arguments to each command.

## Driving it with scripts/verify

Run `scripts/verify fast --feature cited-search`.
The driver creates a synthetic Harbor README and asserts every step against its known contents.
It requires exactly one result, the correct source path and project, and matching text from `show`.
It records file hashes and command output before removing private test state.

## Gotchas

1. The driver builds its default target. An absent explicit `--binary FILE` causes exit 3 before launch.
2. `--expected-owner wrong-owner` is a deliberate broken expectation. It must exit 1 and retain evidence.
3. `full` repeats the host journey with fresh state. It does not prove native Windows or Linux behavior.
4. This synthetic input proves mechanics. The real-corpus evaluation measures retrieval usefulness separately.
