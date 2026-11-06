# Tool contract

## Sub-features

1. The built executable reports version, revision, and modified state as one structured reply.
2. Top-level `--help` is plain text. Structured help lists commands, and every listed command has complete structured and plain help.
3. Unknown verbs return a structured refusal with exit 2 and a recovery command.
4. Unknown flags return a structured refusal with exit 2 and a recovery command.
5. Public help and the generated agent skill use Brain branding and contain no home-folder paths or remote addresses.
6. The generated agent skill exactly matches the tracked Brain skill.
7. The README gives installation, version, requirements, network, and removal guidance.
8. Stateless commands leave the isolated home, state, and source folders unchanged.

## How to run it

Run `scripts/verify fast --feature tool-contract` from a checkout. The verifier builds Brain from the current source tree. It then runs all eight probes against that executable. Run `scripts/verify full` to repeat every mapped feature twice with fresh temporary folders.

The verifier also copies the executable into an owned temporary home, finds it on the temporary command path, and runs its version command. It changes no installed skill, hook, schedule, or state folder.

## Evidence and limits

Each executable call has closed standard input, a 30-second limit, and a 1 MiB combined output cap before buffering. On macOS and Linux, the verifier refuses children left in its owned process group. Command records include arguments, exit code, output, and elapsed time. Each call gets a before and after file snapshot, so a later call cannot hide an earlier write. `traces/contract-N-probes.json` records each probe. `traces/contract-N-files.json` holds the final folder snapshots. Evidence remains under `.local/verify/`; the temporary folders are removed.

A failed, missing, or malformed reply fails the feature. Deliberately broken test inputs prove those refusals. These checks prove behavior on the system running them. Native Windows and Linux runs remain to be tested on those systems. Windows child-process detection remains unproved.
