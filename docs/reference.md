# Brain reference

This guide covers source rules, privacy, state, indexing, schedules, notes, maps, and agent integrations. Start with the [README](../README.md) for installation.

## Upgrades

Preview `./scripts/install --skills codex,claude`, then apply the same command with `--write`. Repeat `--bin-dir` if you chose a custom executable folder during installation. Upgrades preserve sources, exclusions, privacy rules, notes, state location, and the chosen daily time. Use source commands for later source changes and `brain settings relocate` to move the data folder.

## What install writes

1. The build writes executable and compiler caches under the checkout's `.local/` folder.
2. The installer copies the executable to `~/.local/bin/brain` by default. `--bin-dir DIR` selects another destination.
3. The installer preserves replaced executable and selected skill bytes under `~/.local/share/brain-install-backups/`. Each run keeps a private receipt and prior files.
4. Selected skills receive `SKILL.md`. Sibling files and other skills remain unchanged. Unsafe linked destinations refuse.
5. Saved settings record the data location. The data folder holds the index, notes, conversion cache, and run records.
6. First initialization writes configuration. Existing configuration stays intact. An explicitly selected external configuration stays external.
7. Daily installation writes only the selected job's definitions and receipt. `brain index` never installs a job.
8. Hooks, agent settings, and shell profiles remain under your control. Setup prints optional guidance only.

An interrupted installation reports completed steps and retained backups. Retry the same command after resolving the reported problem.
Do not replace only the binary with an older version after a newer version has changed its stored data.

## Network

Brain makes no network or model calls while indexing, searching, or printing setup guidance.
Building needs pinned modules in a local cache. The built program needs no network connection.
Source documents stay read-only. No background service starts with a Brain command.

## Remove

Remove the selected daily job before removing the executable. First run `brain schedule --format json` with your usual configuration and state flags. Copy its `id` and artifact paths. The job identifier belongs only to that state folder.

On macOS, replace `JOB_ID` and `PLIST_PATH` below with that preview's values:

```sh
set -e
job='JOB_ID'
plist='PLIST_PATH'
launchctl bootout "gui/$(id -u)/$job"
if launchctl print "gui/$(id -u)/$job" >/dev/null 2>&1; then exit 1; fi
rm -- "$plist"
```

On Linux, replace `JOB_ID` with the preview's identifier:

```sh
set -e
job='JOB_ID'
systemctl --user disable --now "$job.timer"
systemctl --user stop "$job.service"
rm -- "$HOME/.config/systemd/user/$job.timer" "$HOME/.config/systemd/user/$job.service"
systemctl --user daemon-reload
```

On Windows, use the task name in the schedule preview. Replace `TASK_NAME` below, then remove only the preview's artifact files:

```powershell
Unregister-ScheduledTask -TaskName 'TASK_NAME' -Confirm
```

Check that the selected job is absent before removing its definition. If the scheduler refuses removal, resolve that refusal first. Preserve other jobs and the selected state's saved notes. The schedule receipt remains historical evidence in state; do not remove state just to uninstall the job.

Remove the installed executable:

```sh
rm "$HOME/.local/bin/brain"
```

On Windows, remove `brain.exe` from the folder you selected.
Remove only Brain's skill folder and hook entries that you previously installed.
Review your selected state folder before removing it. It contains saved notes as well as the rebuildable index.
Source documents remain in their original folders.

The installer and the build leave three more folders. Remove each one when you no longer need it:

1. `~/.local/share/brain-install-backups/` holds the files that earlier installs replaced.
2. The settings folder holds `selection.json`. It is `~/Library/Preferences/Brain` on macOS, `%APPDATA%\Brain` on Windows, and `$XDG_CONFIG_HOME/brain` or `~/.config/brain` on Linux.
3. `~/.cache/brain/go-mod-cache` holds downloaded Go modules. Go makes these files read-only, so run `chmod -R u+w ~/.cache/brain` before you remove the folder.

## Manage sources and rules

Run these example commands after replacing the example paths:

```sh
brain sources list --format json
brain sources add /path/to/outside-project --project outside-project --format json
brain index --format json
brain sources move /path/to/outside-project /path/to/relocated-project --format json
brain sources remove /path/to/relocated-project --format json
brain exclude add 'private-notes/*' --format json
brain third-party add 'downloaded-examples/*' --format json
```

Source commands update the configuration. They never move or delete your documents.
Run `index` after changing sources or rules to update search results.
Source additions select exact folders, including names containing `?` or `*`.
The optional project name groups nested documents under one search filter.
A repeated operation that changes nothing exits 3 and preserves the configuration bytes.
Built-in safety exclusions cannot be removed.

Each source has a stable identity. Removing another source preserves retained citations.
Use `sources move OLD NEW` after moving a folder yourself. Relative document paths must stay the same.
Overlapping roots still use the later configured source. Changing which source owns a file can change its citations.
Existing wildcard sources remain supported. Moving one requires the same wildcard suffix.

## Exclude sensitive files

Brain skips `id_*`, `*.p12`, `*.pfx`, `.netrc`, `credentials*`, `*.kdbx`, `.ssh/`, and `.gnupg/` before opening their content.
Add project-specific patterns to the configuration's `exclude` list before indexing their files.
These defaults join the existing safety exclusions and cannot be overridden.

Place `.brainignore` in a source folder or its subfolders. Patterns follow gitignore syntax and match case exactly.
For example:

```gitignore
private/
*.draft.md
!public.draft.md
/review.md
**/scratch.md
```

The first rule excludes the entire `private` folder. A child rule cannot reopen that folder.
The next two rules exclude draft files while keeping `public.draft.md`.
The leading slash limits `review.md` to the folder containing this ignore file. Double stars match nested folders.
Nested ignore files add rules for their own folders. Negation cannot override built-in safety exclusions.
Brain treats ignore files as control inputs. Their contents never become search documents.
An unreadable, oversized, linked, or nonregular ignore file stops indexing with repair guidance.

Run `brain index` after changing ignore rules. Reading commands keep the stored index until that explicit refresh.
The refresh removes newly excluded text and old citation headings while retaining allowed citations.
Changed exclusion rules also clear old deleted-chunk heading suggestions, whose source ownership is unavailable.
This removal does not claim erasure of old storage blocks. Content redaction and old-state replacement use the privacy refresh described below.

## Protect secrets

Brain removes recognized access tokens and private-key blocks before storing indexed text.
Search, `show`, saved notes and error replies use sanitized text.
Ordinary client names and project names stay visible.
The [rule catalogue](../internal/redact/CATALOGUE.md) lists the supported forms and limits.
Generic passwords, unfamiliar token formats and encoded secrets are not automatically covered.

Brain keeps source documents unchanged. Search citations still identify those files.
An agent with file access can read the original separately, including its secrets.
Brain is not an access-control boundary for other programs.
Notes saved through `brain note` retain sanitized text only.

For extra exact values, add `private_values_file` to the selected config.
Its value is an absolute path to a private local JSON file outside Brain's state folder.
The file contains an array of strings, for example `["your-exact-secret"]`.
Each string matches exactly, including case. An empty array keeps only the built-in rules.
Brain refuses linked files, invalid input and values that conflict with redaction markers.
On macOS and Linux, the control file must belong to your account. Use owner-only permissions, such as mode 0600.
Brain also rejects permission entries that grant other accounts access on macOS.
Brain does not verify Windows file access permissions. Restrict access through Windows security settings before selecting the file.
The control file is never indexed. Brain does not change it.

Run `brain index` after changing the private-value list.
Older or interrupted privacy state refuses reading until explicit indexing finishes.
That refresh replaces indexed text, converted copies and saved notes while preserving allowed text.
Read-only state can answer only when its stored privacy policy is current.
The refresh removes protected values from remaining application-owned files.
It does not promise erasure of deleted storage blocks or copies held by other programs.

`brain doctor` reports affected files and replacement rule labels.
Several overlapping detections can share one replacement label.
A recovery command is a suggested next action after an error.
Brain checks those suggestions itself. It omits them only when no usable command can avoid revealing a protected value.

## Select a configuration

Every stateful command accepts `--config FILE`. Selection order is that flag, `BRAIN_CONFIG`, then the selected state's `config.json`.
An explicitly empty or invalid selection refuses. Brain never silently falls back to another configuration.
Relative configuration paths resolve from the current folder.
The parent folder must already exist when selecting a configuration outside private state.

For example, use one configuration with a separate index:

```sh
brain init --source /path/to/project --config /path/to/config.json --state-dir /path/to/private-state --format json
brain index --config /path/to/config.json --state-dir /path/to/private-state --format json
brain search "lease timeout" --config /path/to/config.json --state-dir /path/to/private-state --format json
```

Stateful recovery commands retain safe state and config selections. Stateless errors return general help without those selections.
Brain omits a recovery command when its required values would reveal protected text.
Config edits preserve unknown fields and use a lock beside the selected file.
Another program can add a project with `sources add`, then run `index` with the same state and config choices.
These commands need no extra adapter.

## Daily indexing

Preview the daily job after initializing your selected configuration and state:

```sh
brain schedule --format json
```

Preview prints the selected executable, configuration, state, installation files and native actions. It writes nothing.
New jobs default to 01:00 local. Omitting `--time` preserves an existing job's chosen time.
Saved settings keep bare commands and the daily job on the same data. Explicit scope flags select a separate command scope.
Install the job explicitly:

```sh
brain schedule --write --format json
brain schedule status --format json
```

To change the daily time, run `brain schedule --time 02:30 --write`.
Status separates the native scheduler result from the last index attempt, which may have been manual.
An unknown native result means no result was available; it does not prove a successful run.

Run installation from the installed executable. Each job pins that executable's absolute path.
Repeated identical installation exits 3. Changing the time or configuration updates the same state's job.
The job indexes new content within registered sources. Register a separate outside folder with `sources add` first.
Brain searches local documents; this job does not fetch online content.

On macOS, the user job runs while logged in and catches a missed sleep-time run on wake.
It does not recover runs missed while powered off or logged out.
Linux uses a persistent user timer and requires `systemctl` and `busctl`. Catch-up occurs when its user manager starts.
Windows uses a daily task with native catch-up settings.
Preview states each platform's limits. Check the native job after activation and after its first scheduled run.
On macOS, inspect the loaded command and arguments, then check that a scheduled run occurs.

Updating an existing job writes its owned definition before activation. Other readers can see the update in progress.
An interrupted partial definition is reported as incomplete. Review and repair that definition before retrying; Brain preserves unverified files.

## Recover a blocked schedule preview

A privacy refusal can block preview because the saved job record contains protected text. `brain help schedule` gives static guidance without reading that record.

1. You open `schedule.json` privately inside your selected state folder. Keep the original while checking ownership.
2. You use `id` as the job name and `system` as its operating system. Compare `spec.state`, `spec.config` and `spec.executable` with your installation.
3. You verify the native job identifier, selected `spec.state` and an executable you installed. Config and time may differ after an interrupted update.
4. You locate the definitions below. Either their identity, command, arguments and time match `spec`, or their SHA-256 matches `owned`. Those hashes describe prior bytes during updates. A current definition can match `spec` while differing from the older hash. Healthy receipts have `owned: null`; verify their definitions directly. Preserve anything you cannot verify.
5. You unregister only the verified native job using the commands below. If it is already absent, confirm that absence before continuing.
6. You remove only the verified definition files after the job is absent. On Linux, you then run `systemctl --user daemon-reload`.
7. After cleanup, you remove `schedule.json`. You run `index` with the same configuration and state flags.

The definition paths are:

1. macOS: `Library/LaunchAgents/JOB_ID.plist` under `spec.home`.
2. Linux: `.config/systemd/user/JOB_ID.service` and `JOB_ID.timer` under `spec.home`.
3. Windows: `schedule-task.xml` under `spec.state`.

These commands unregister jobs without deleting definition files. Replace `JOB_ID` locally with the verified record's identifier.

```sh
# macOS
launchctl bootout "gui/$(id -u)/JOB_ID"

# Linux
systemctl --user disable --now JOB_ID.timer
systemctl --user stop JOB_ID.service
```

```powershell
# Windows
Unregister-ScheduledTask -TaskName 'JOB_ID' -Confirm
```

If the record is damaged or ownership is unclear, restore a trusted record copy or review ownership manually. Keep unverified jobs and files. Relocate protected required paths before installing a replacement job. Brain preserves this record so recovery cannot silently lose native-job ownership.

## Search and refresh

Search replies cite file paths and include `index_age_s`.
`index_complete` says whether a complete source scan has finished. A note-only index can answer without claiming a complete scan.
Each result has a chunk id. Use that id with `brain show ID` to read the chunk and its neighbours.
Text search prints every result's ID. Text show prints commands for the previous and next chunk when available.
Each source file keeps its own chunks, even when another file contains the same text.
Repeated text within one file appears once. Brain never folds separate files together.
The `duplicates_folded` counter is zero after a complete refresh.
`also_in` is empty after a complete refresh.
Chunk IDs use a stable source identity, relative wildcard expansion, exact relative path, and normalized text.
Source positions become stable identities. Notes use a fixed source name, `notes`.
The source move command preserves IDs when relative paths stay the same.
Appending a disjoint source or discovering another wildcard match preserves existing IDs.
Overlapping roots use the later configured source, which may change IDs.
Changing root boundaries or overlapping ownership can change IDs.
Existing indexes keep their old answers until `brain index` updates their documents.
Changed text can receive new chunk IDs after indexing.
Use two or three specific keywords, for example `brain search "lease timeout"`.
Search tries all query words first, then any word when the first search finds nothing.
The reply labels those modes `and` and `or`.
Explicit mixed expressions and `NOT` use `expression`; quoted operator words remain search terms.
Project selection ignores letter case. Colliding names such as `Alpha` and `alpha` produce a refusal in search, map and note.
Give those configured sources distinct `project` labels, then run `brain index`.

Search, show, projects, recent, map, status, doctor and brief read stored data without scanning source folders.
`--no-refresh` is accepted and changes nothing, because ordinary reads already use the stored index.
`brain index` discovers new or changed documents beneath configured roots.
`index --full` reads every file again and purges files deleted more than seven days ago.
Status reports `pending_known=false` because unscanned changes cannot be counted.
A saved note is indexed separately. It does not refresh other documents or reset the complete-scan age.
Doctor reports overdue indexing, unsuccessful attempts, configuration changes and known schedule failures.

Markdown, RTF transcripts and the main text of DOCX documents are indexed.
The docx reader keeps headings, table rows and link text.
The RTF reader handles TextEdit groups, code page 1252 and Unicode escapes without an external program.
Pictures, binary data and unsupported converted files are reported as `conversion-failed`.

Unreadable folders and failed or oversized files remain visible as index gaps.
`index` lists each gap in `skipped` and exits 1 while gaps remain. Reading replies include `gaps.items`,
`gaps.count`, `gaps.total` and `gaps.empty`. The state view puts this object under `index.gaps`.
Text replies and briefs warn about known gaps.
`doctor` names each affected path and explains recovery.
Stored gaps remain visible with `--no-refresh`, after a restart, and with read-only state.
Files below unavailable folders keep their stored search results until Brain can check them.
Those results may be outdated. Newly excluded files are still removed from search.
Restore folder access, repair failed documents, or reduce oversized files. Then run:

```sh
brain index --full --format json
```

A successful folder scan clears its gap. Successful indexing or confirmed removal clears a file gap.

Successful conversions are cached by format, conversion version and source SHA-256 checksum in `converted/` under state.
Unchanged sources reuse that text. A full index removes unused cached text and incomplete temporary files.
Repository source files over 1,048,576 bytes are reported as `oversize`.
The trusted notes folder keeps indexing each project's growing note file beyond that source limit.
Excluded folders, secrets, symbolic links and special files are skipped before opening.

JSON replies use ASCII escapes for non-ASCII text and end with one newline.
Without `--format json`, commands print readable text. Errors always print JSON.
Their `next` commands use the current platform's shell.
Their `next_argv` arrays run without a shell. Stateful recovery includes the chosen state folder when it is safe to print.
Search accepts `--shell sh`, `--shell powershell` or `--shell cmd` to choose the printed form.

## State folders

| System | Default state folder |
| --- | --- |
| macOS | `~/Library/Application Support/Brain` |
| Windows | `%LOCALAPPDATA%\Brain` |
| Linux | `$XDG_DATA_HOME/brain`, default `~/.local/share/brain` |

State selection follows `--state-dir DIR`, then `BRAIN_HOME`, then saved settings, then the system default.
These overrides select a folder for that command. They do not move existing data or change saved settings.
Both overrides must be absolute paths. Repository source files are only read.
Database, notes, conversion cache, run receipts and state locks stay inside the state folder.
A selected external config and its adjacent lock use the chosen config folder. Explicit schedule installation also writes its previewed host destinations.
Symbolic links in state file paths are refused before reading or writing through them.
SQLite files with multiple hard links are also refused, including during read-only access.
On macOS and Linux, an existing state folder must belong to the caller and deny group and other access.
Brain refuses a shared folder without changing its permissions. New state folders use mode 0700 and database files use mode 0600.
When that folder cannot be written, reading commands use committed data and report `read_only:true`.
They include committed changes in SQLite's write-ahead log, the file holding recent database writes.
Their index age describes the stored scan time.
Their first recovery command is `doctor`.
Known network paths and remote drives are refused before state files are opened.

## Move the data folder

Inspect the current selection and preview a move:

```sh
brain settings --format json
brain settings relocate "$HOME/BrainData" --format json
```

Apply the same move explicitly:

```sh
brain settings relocate "$HOME/BrainData" --write --format json
brain settings --format json
brain search "your keywords" --format json
brain schedule status --format json
```

Brain preserves saved notes and configuration, rebuilds the destination index, and verifies it before selecting that location.
Configured sources must remain readable for rebuilding. Brain keeps the original data if verification fails.
Source documents stay in place. An external configuration stays at its original path.
Brain updates its owned daily job and preserves its time. Other jobs remain unchanged.
The relocation reply keeps the original job plan in `relocation.schedule` for recovery.
Use `brain schedule status` to inspect the active job after the move.

The previous data remains available as a read-only copy after success. Commands using that old location refuse writes so new notes cannot be stranded there.
If a move stops partway through, retry the same explicit relocation command. Reading commands do not repair interrupted moves.
Avoid editing or deleting either location while a move is incomplete.
Existing hooks that explicitly name the previous folder need your update. `brain setup codex` or `brain setup claude` prints current guidance.

Saved location settings live separately from movable data. Help and version commands do not read those settings.
A malformed or unsafe settings file causes a refusal instead of silently choosing another index.

## State and diagnosis

```sh
brain status --format json
brain projects --format json
brain recent --days 7 --limit 20 --format json
brain doctor --format json
brain help doctor
```

The state view reports stored counts. It marks pending changes as unknown until explicit indexing.
Bare invocation remains an alias for `status`, including `brain --format json`.
Projects lists folders, README purpose lines, file counts and last-indexed times.
Recent lists current files by modification time, newest first.
Each answer uses one database snapshot, so concurrent indexing cannot mix its counts and rows.

Doctor reports problems and their recovery commands.
Permission or storage failures return `index-unavailable` without replacing the database.
Restore access or available storage before retrying. Only confirmed damage or a missing schema permits automatic rebuilding.
Opening writable state can tighten owned database files to private mode 0600.
Other reported problems need their printed recovery steps.
It checks SQLite, Go, storage, permissions, configuration, sources and index integrity.
It exits 1 when it finds problems and 3 when healthy. The result object lists the problems in either case.
An unidentified filesystem is reported as unproved local storage.
Harness detection uses environment markers and is best effort. Doctor never reads harness settings.
For an unwritable state folder, doctor prints the folder and sandbox guidance.
You apply any settings change. The printed `next` commands remain executable commands.
`brain help doctor --state-dir DIR` prints the exact guidance for the chosen folder.
If the default state folder is also remote, recovery offers `help init` so you can choose local storage.

## Notes and document fields

```sh
brain note "Inspect the atlas valve every 30 days" --project atlas --format json
brain search "atlas valve inspection" --kind note --no-refresh --format json
```

Note replies have `path`, `entry_id` and `indexed` fields.
The entry id opens the first chunk containing note text, including for accepted long notes.
Each entry has a UTC heading in `notes/<project>.md`. Separate locks keep simultaneous notes.
The write flushes its temporary file, replaces the note file, and flushes the folder on Unix.
Windows retries a briefly held file. Windows durability is experimental.
The note command creates the index when needed, so it works after `init` before a separate `index` command.
Without `--project`, it uses the current source's project, or `general` outside every source.
Existing project names keep their recorded case.
Project names hold at most 100 characters. Reserved file names and trailing dots or spaces are refused on every system.
Notes accept up to 65,536 input bytes before trimming or redaction. Each submission becomes one dated entry. Brain creates searchable chunks internally. Saving and indexing run before the command returns.
The entry id identifies the first useful text chunk. An operating system can reject a large command argument before Brain runs.
Above Brain's limit, no note is saved. The error offers `help note` without repeating the input.
If `indexed` is false, the note is saved. Run `brain index` to index it. Saving it again creates another entry.
If a save cannot be confirmed, check the notes before retrying. A folder flush can fail after the file was replaced.
Repeating `note` creates a separate dated journal entry. Notes deliberately do not collapse repeated saves.
A saved note records memory. It never grants approval for another action.

A Markdown front matter block contributes only `name` and `description` to its first chunk's heading.
Heading labels hold at most 1,500 characters. Full source headings remain searchable in chunk text.
Oversized front matter values also remain in chunk text. Refreshing an older index applies this bound.
Other front matter fields are excluded from search.
Files under `superseded` or `archive` remain searchable with half the usual score.
Downloaded trees listed in `third_party` stay excluded until their pattern is in the configuration's `include` list.
Included trees have kind `third-party` and half the usual score.
At first `init`, `--include PATTERN` marks matching files as third-party even without a `third_party` rule. For an existing configuration, add a `third_party` rule, then add its exact pattern to `include` when you want it searched.

## Project maps

```sh
brain map atlas
brain map atlas --no-refresh --format json
```

A map shows the indexed README purpose, PLAN milestone status, unchecked PLAN
and GOAL boxes, and three newest saved notes. Project names match without case.
The text page has fewer than 60 lines. It shows at most 20 status lines,
120 characters each. Commit subjects and note text hold at most 200 characters.
Cut lists report both the shown count and the total.
Text escapes line breaks in displayed paths. JSON keeps the exact paths.
If a whole recovery command would exceed the page limit, text asks for JSON
with the exact next and next_argv commands.

Maps read chunked text, the stored sections. A source line longer than 1,500
characters can become several indexed lines. This can change unchecked-box counts.
Fenced examples do not count as unfinished tasks or select a milestone status section.
Code inside the selected status section stays in its displayed text.

Map uses stored document facts. It never opens PLAN or GOAL source files to refresh them.
An unwritable state returns stored facts with `read_only:true`, the index age, and a runnable doctor command.

Git is optional. Map locates Git on PATH, then calls that absolute executable
read-only for the project's latest commit. Doctor reports that path or its
absence. Git absence leaves a healthy index healthy and returns `commit:null`.
A folder without its own repository also returns no commit. Git receives no
inherited Git variables. Fetching, protocols, signature programs, file-monitor
hooks and pagers are disabled. Brain never writes repository Git files.

## Install the skill and session hook

Put `brain`, or `brain.exe` on Windows, on the harness's PATH.
The generated skill stays identical across systems and uses that command.
Hook entries use the running binary's absolute path and the selected state folder.

1. Run `brain setup HARNESS` to print the folders, hook entry, allowance and trust step for your system.
2. Run `brain setup HARNESS --write` after every install or upgrade to refresh only the skill in your user skill folder.
3. Review existing settings. Merge the printed hook entry and allowance without replacing other entries.
4. Follow the printed trust step, restart the caller, and verify the brief appears.

Setup reports `Skill current` in text and `skill_current` for each JSON setup item.
True means a safe copied skill exactly matches the skill generated by this binary.
Missing, stale, unreadable, oversized, or linked destinations report false.
Preview reads the selected skill safely and writes no files. An explicit `--write` copies only `SKILL.md` and then reports true.
A standalone link follows the repository skill directly. Setup does not follow or overwrite that link; use the link instructions below and skip copy refresh for it.

Use `claude`, `codex`, `copilot` or `vscode` for HARNESS. `setup all` prints four sets of instructions.
Add `--state-dir DIR` when the caller uses a separate local state folder.
Brain never applies settings, installs hooks, or changes trust.

To install just the skill from a checkout on macOS or Linux, link the generated folder into a host's skill folder:

```sh
for skill_host in "$HOME/.claude/skills" "$HOME/.agents/skills"; do
  if [ -e "$skill_host/brain" ] || [ -L "$skill_host/brain" ]; then
    printf 'Review the existing skill destination: %s\n' "$skill_host/brain" >&2
    exit 1
  fi
done
for skill_host in "$HOME/.claude/skills" "$HOME/.agents/skills"; do
  mkdir -p "$skill_host"
  ln -s "$PWD/skills/brain" "$skill_host/brain"
done
```

Run these commands from the checkout. The checks refuse existing files, directories, and dangling links before either skill destination changes.
Review an existing destination before replacing it.
Use the host's actual skill folder if it differs from these examples. `setup HARNESS` prints the supported default locations.
A host linked to this repository already sees the generated skill; skip copy installation for that host.

The table follows `setup`'s output. On Unix, `~` is the user home. On Windows, it is `%USERPROFILE%`.
Every skill folder below contains `brain/SKILL.md`.

| Harness | System | User skill folder | User hook file | Hook command and brief |
| --- | --- | --- | --- | --- |
| Claude Code | macOS | `~/.claude/skills` | `~/.claude/settings.json` | `command`, text |
| Claude Code | Linux | `~/.claude/skills` | `~/.claude/settings.json` | `command`, text |
| Claude Code | Windows | `~\.claude\skills` | `~\.claude\settings.json` | executable with `args`, text |
| Codex | macOS | `~/.agents/skills` | `~/.codex/hooks.json` | `command`, text |
| Codex | Linux | `~/.agents/skills` | `~/.codex/hooks.json` | `command`, text |
| Codex | Windows | `~\.agents\skills` | `~\.codex\hooks.json` | `commandWindows` through cmd, text |
| Copilot CLI | macOS | `~/.copilot/skills` | `~/.copilot/hooks/brain-copilot.json` | `bash`, Copilot JSON |
| Copilot CLI | Linux | `~/.copilot/skills` | `~/.copilot/hooks/brain-copilot.json` | `bash`, Copilot JSON |
| Copilot CLI | Windows | `~\.copilot\skills` | `~\.copilot\hooks\brain-copilot.json` | `powershell`, Copilot JSON |
| VS Code | macOS | `~/.agents/skills` | `~/.copilot/hooks/brain-vscode.json` | `osx`, VS Code JSON |
| VS Code | Linux | `~/.agents/skills` | `~/.copilot/hooks/brain-vscode.json` | `linux`, VS Code JSON |
| VS Code | Windows | `~\.agents\skills` | `~\.copilot\hooks\brain-vscode.json` | `windows`, VS Code JSON |

Claude Code: review `/hooks`. When its sandbox is enabled, merge the selected state folder into `sandbox.filesystem.allowWrite`.
Native Windows has no Claude Code sandbox. WSL uses the Linux build and its separate Linux home.

Codex: trust the entry through `/hooks`, and trust it again after every edit.
Setup guidance merges the state path into `[sandbox_workspace_write] writable_roots` in `~/.codex/config.toml`.
Managed permission profiles may require their owner to allow state. Local settings may not override managed permissions.
Codex 0.162 and earlier also used `~/.codex/skills`. Setup copies only the documented `.agents` skill.
Codex desktop hooks may not run. Use the skill and run search directly when the brief is absent.

Copilot CLI: review the user hook and run `/skills reload`. Use version 1.0.89 or newer.
Windows needs PowerShell 6 or newer. Only top-level `additionalContext` becomes session context.
Copilot cloud runs elsewhere and cannot use this computer's corpus.

VS Code: enable `chat.useHooks` and reload the window for the skill.
Hooks are Preview and run on the first prompt. Its JSON uses `hookSpecificOutput.additionalContext`.
VS Code hook support is experimental. The Windows entry assumes PowerShell.
Check a fresh session. Use the skill and search directly if no brief appears.
Copilot CLI and VS Code share the user hook folder. Enable only the intended caller's entry for a session.
Their two printed entries are alternatives. No hook sandbox allowance is documented for either caller.

`brain brief` prints three lines: stored index facts, the current project and the command to run.
It opens a read-only snapshot, reports the stored scan age, and never refreshes sources or probes writes.
Search also uses stored facts. The brief prints recovery guidance when indexing or state repair is needed.
`brain --brief` remains the compatible alias for installed hooks.
Whole commands keep their quoted spaces. If commands cannot fit under 1,000 characters, the brief gives plain help guidance.
When a committed write-ahead log lacks its shared-memory file, a bounded private memory snapshot preserves committed answers without recreating that file.
That fallback accepts at most 268,435,456 bytes for the database plus log and for the assembled database.
The measured 10,000-file database plus log is 231,809,080 bytes.
A larger orphan log needs writable recovery before a read-only answer. The fallback refuses rather than omitting committed data.
Before init or indexing, or after index damage, it prints recovery guidance and exits 0.
`--format copilot` wraps the lines in `additionalContext`.
`--format vscode` wraps them in `hookSpecificOutput`, with `hookEventName: SessionStart`.

`brain help --skill` prints the skill. `scripts/generate-skill` updates its tracked copy.
The normal check lane rejects skill drift after a help change or a hand edit.

