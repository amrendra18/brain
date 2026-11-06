# Skill drift

## What this proves

1. The real Brain binary generates its skill with `help --skill` and compares an installed copy during `setup claude` preview.
2. A missing copy reports `skill_current:false`. Preview leaves the owned home unchanged.
3. Explicit `setup claude --write` copies only `SKILL.md` into the documented user skill folder. It reports `written:true` and `skill_current:true`.
4. A later preview reports `skill_current:true`. Changing that owned copy makes the next preview report `false` without changing any file.
5. The copied bytes equal the same binary's generated skill. Command replies, exits, file hashes, and scratch cleanup stay in `.local/verify/`.

## How to run it

Run `scripts/verify fast --feature skill-drift` from the checkout. `scripts/verify full` repeats the journey in two fresh homes. The default verifier includes this feature with cited search and the external tool contract.

The verifier builds the current binary unless `--binary FILE` deliberately selects another one. A selected outside binary has unverified source identity in the evidence.

## Boundaries

1. Every home and copied skill in this proof is a temporary owned fixture. The proof installs no live host skill or hook.
2. The deliberately changed skill is a test input. Its false result proves that preview notices drift and does not overwrite it.
3. The named product tests cover unreadable and unsafe destinations. This journey covers missing, matching, stale, and explicit copy behavior through the real binary.
4. Native Windows and Linux execution needs those hosts; a cross-build alone does not prove it.
