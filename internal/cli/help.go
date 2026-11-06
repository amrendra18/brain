package cli

import (
	"brain/internal/corpus"
	"brain/internal/platform"
	"fmt"
	"io"
	"sort"
	"strings"
)

const scheduleRecovery = `Privacy cleanup when preview is blocked:
1. You inspect schedule.json privately in the selected state folder. Its id names the job; system names the operating system.
2. You verify the native job id, selected spec.state and installed spec.executable. Check spec.config; config and time may differ after an interrupted update.
3. You locate definitions using spec.home and id: macOS uses Library/LaunchAgents/JOB_ID.plist; Linux uses .config/systemd/user/JOB_ID.service and JOB_ID.timer. Windows uses schedule-task.xml under spec.state.
4. You verify definitions before removal. Either their identity, command, arguments and time match spec, or their SHA-256 matches owned. The owned hashes describe prior bytes during updates; current definitions need not match those older hashes. Healthy receipts may have owned:null; verify the definitions directly in that case. Keep unverified jobs and files.
5. You unregister only the verified job. If it is already absent, confirm that absence. Replace USER_ID and JOB_ID locally.
   macOS: launchctl bootout gui/USER_ID/JOB_ID
   Linux: systemctl --user disable --now JOB_ID.timer; systemctl --user stop JOB_ID.service
   Windows: Unregister-ScheduledTask -TaskName JOB_ID
6. After the verified job is absent, you remove only the verified definitions. Linux then requires systemctl --user daemon-reload.
7. You remove schedule.json and run index with the same config and state after those checks and cleanup finish.
If the receipt is damaged or ownership cannot be verified, restore a trusted receipt copy or review ownership manually. Do not guess which job to remove.
Protected required paths must be relocated before installing a replacement schedule. This guidance never prints the receipt's private values.`

func commandDescription(command, executable string) string {
	var usage, body, example string
	switch command {
	case "settings":
		usage = "settings [relocate DIR] [--write]"
		body = "Show the selected data folder and configuration. Bare commands use saved settings after flags and environment overrides.\n--write remembers an initial selection or confirms it unchanged. Use relocate DIR --write to move existing data.\nRelocation rebuilds and verifies the destination, preserves notes and the previous data, and updates the owned daily job.\nWithout --write, relocation only previews. Retry the same explicit command after an interrupted move."
		example = "settings --format json"
	case "schedule":
		usage = "schedule [status] [--time HH:MM] [--write]"
		body = "Preview a daily explicit index job. New jobs default to 01:00 local; existing jobs retain their chosen time. Preview writes no files and starts no processes.\nOnly --write installs the job. Existing identical healthy jobs return exit 3. The preview describes catch-up limits.\nThe job pins this executable, selected config and state.\nUse schedule status to inspect the applied time and native run result. Last index metadata also includes manual runs."
		body += "\n" + scheduleRecovery
		example = "schedule --format json"
	case "sources":
		usage = "sources list | add PATH [--project NAME] | remove PATH | move OLD_PATH NEW_PATH"
		body = "Manage source folders without indexing or moving their files. Move keeps source and chunk identities.\nList configured roots before registering a project. Register only projects outside every configured source.\nNew sources are literal folders. Pattern moves must keep their wildcard suffix. Run the printed index command after edits."
		example = "sources list --format json"
	case "exclude", "third-party":
		usage = command + " list | add RULE | remove RULE"
		body = "Manage relative filename patterns without indexing. Built-in safety exclusions cannot be removed.\nIdentical edits return exit 3. Run the printed index command after edits."
		example = command + " list --format json"
	case "init":
		usage = "init [--source PATH ...] [--include PATTERN ...]"
		body = "Choose source folders. With no --source, use the current folder only.\nExplicit --source replaces that default. Exit 3 if the selected config is already valid. Existing invalid config is preserved; repair it before retrying."
		example = "init --source . --format json"
		body += "\nConfig selection: --config FILE, then BRAIN_CONFIG, then a saved external config for the saved state, then the selected state folder config.json. Relative config paths use the current folder. External parents must exist.\nUse --state-dir DIR to choose an absolute local state folder when the default is remote or unavailable."
	case "index":
		usage = "index [--full]"
		body = "Index new and changed Markdown, DOCX and RTF files with built-in readers.\n--full rereads files, purges old deletions and unused conversion caches.\nConfigured source files over 1048576 bytes are skipped. Saved notes stay indexed as their files grow.\nUnsupported converted files are listed as conversion-failed. Exit 1 while skipped files remain; exit 3 when clean and nothing changed."
		example = "index --format json"
	case "search":
		usage = "search QUERY [--project P] [--kind K] [--limit N] [--full] [--no-refresh] [--shell sh|powershell|cmd]"
		body = "Find chunks with cited paths. Use two or three specific keywords, such as lease timeout. Reads use the stored index without scanning sources. Default limit: 8.\nQuery mode is and for required terms, or for alternatives, and expression for mixed operators or NOT.\nSnippets hold at most 600 bytes. --full returns whole chunks. --no-refresh remains accepted for compatibility.\nKinds: " + strings.Join(corpus.Kinds, ", ") + "."
		example = `search "lease timeout" --format json`
	case "show":
		usage = "show ID [--full] [--no-refresh]"
		body = "Read one chunk using an id from search, with its neighbouring chunk ids.\nText holds at most 1500 bytes. --full returns the whole text. --no-refresh remains accepted for compatibility."
		example = "show c2d5dfe26ef438b2 --format json"
	case "":
		usage = "[--state-dir DIR] [--config FILE] [--format json]"
		body = "Show stored index size, age and source counts. Pending source changes are unknown until explicit indexing.\nThe state view does not refresh the index. Exit 1 before init."
		example = "--format json"
	case "projects":
		usage = "projects"
		body = "Using the stored index, list project folders, README purpose, file counts and last-indexed times.\nA purpose holds at most 200 characters."
		example = "projects --format json"
	case "recent":
		usage = "recent [--days N] [--limit N]"
		body = "Using the stored index, list current files newest first. Default days: 7. Default limit: 20.\n--days accepts 1 through 9999; --limit accepts 1 through 100."
		example = "recent --days 3 --format json"
	case "doctor":
		usage = "doctor"
		body = "Check SQLite, Go, state storage, permissions, stored gaps, freshness, schedule and index integrity without scanning sources. Opening writable state may tighten database file permissions.\nExit 1 when problems are found; exit 3 when healthy. Checks needing an earlier result may be omitted.\nHarness detection uses environment markers and is best effort. Allow this local state folder in the harness sandbox.\nFor Codex, add its path to [sandbox_workspace_write] writable_roots in config.toml.\nFor Claude Code, review sandbox.filesystem.allowWrite when its sandbox is enabled.\nYou change those settings. Brain only prints guidance.\nFor config-invalid, repair the selected config or restore a saved copy. init preserves existing files.\nFor state-unsafe, choose a private local folder or ask its owner to repair access permissions."
		example = "doctor --format json"
		body += "\nhelp doctor --state-dir DIR prints guidance for the chosen folder."
	case "map":
		usage = "map PROJECT [--no-refresh]"
		body = "Using the stored index, show one project page of fewer than 60 lines.\nRead the README purpose, indexed PLAN and GOAL facts, and three newest saved notes.\nGit is optional and read-only. Project names match without case. --no-refresh remains accepted for compatibility."
		example = "map alpha --format json"
	case "note":
		usage = "note TEXT [--project P]"
		body = "Save one dated note under the state folder, then index only that note. Sources are not scanned.\nSave up to 65,536 input bytes as one note. Brain divides the saved note into searchable chunks.\nThe input limit applies before trimming or redaction. Your shell or operating system may allow fewer argument bytes.\nWithout --project, use the current source's project, or general outside every source.\nIf indexed is false, the note is saved. Run index to add the saved note. Do not save it twice.\nFor a note starting with a hyphen, add one space inside the quotes: note \" - exact bullet\".\nBrain removes that space before saving the bullet."
		example = `note "The lease timeout stays at 30 minutes" --project alpha --format json`
	case "--version":
		return "brain --version [--format json|text]\n  Show version, revision and modified as JSON before reading state.\n  Example: " + executable + " --version\n"
	case "help":
		return "brain help [COMMAND | --skill] [--format json|markdown] [--state-dir DIR] [--config FILE]\n  Show all help or one command's help. --skill prints the portable agent skill.\n  The skill uses brain on the harness's PATH. --help also shows all help.\n  Example: " + executable + " help --skill\n"
	case "--brief":
		return "brain --brief [--format text|json|copilot|vscode] [--state-dir DIR] [--config FILE]\n  Print three stored-index context lines without refreshing or reading source files.\n  State problems become recovery guidance with exit 0. Use copilot for Copilot CLI and vscode for VS Code.\n  Example: " + executable + " --brief\n"
	case "setup":
		return "brain setup [claude|codex|copilot|vscode|all] [--write] [--state-dir DIR] [--config FILE] [--format json]\n  Print user skill folders, skill_current, hook entries, allowances and trust steps.\n  skill_current compares only safe regular skill copies; missing or unreadable copies report false.\n  Merge printed entries into existing settings. Brain never writes hooks or settings.\n  --write copies only SKILL.md. Put brain on the harness's PATH for the portable skill.\n  Copilot CLI and VS Code share a hook folder. Choose the entry for the intended caller.\n  Example: " + executable + " setup all --format json\n"
	}
	if command == "" {
		return "brain " + usage + "\n  " + strings.ReplaceAll(body, "\n", "\n  ") + "\n  Example: " + executable + " " + example + "\n"
	}
	return "brain " + usage + " [--state-dir DIR] [--config FILE] [--format json]\n  " + strings.ReplaceAll(body, "\n", "\n  ") + "\n  Example: " + executable + " " + example + "\n"
}

// The same definitions drive command discovery, help formats and flag validation.
type commandHelp struct {
	Name          string          `json:"name"`
	Inputs        string          `json:"inputs"`
	Outputs       string          `json:"outputs"`
	Preconditions string          `json:"preconditions"`
	Destinations  string          `json:"destinations"`
	Effects       string          `json:"effects"`
	ExitCodes     string          `json:"exit_codes"`
	Flags         map[string]bool `json:"flags"`
}

var commands = []commandHelp{
	{Name: "settings", Inputs: "Optional relocate DIR, --write, --state-dir DIR, --config FILE.", Outputs: "Selected data folder and configuration, saved selection, or relocation outcome.", Preconditions: "Private local settings and state. Relocation requires a valid indexable configuration and an unused safe destination.", Destinations: "Preview: none. With --write: private user settings, selected state, relocation destination and owned schedule artifacts.", Effects: "reads, writes own state, writes host schedule, process", Flags: map[string]bool{"--write": false}},
	{Name: "schedule", Inputs: "Optional status; --time HH:MM (new jobs default 01:00 local), --write. Omitted time preserves an existing schedule.", Outputs: "Schedule plan, written, changed and installed status when requested. Status separates native results from last index metadata.", Preconditions: "Valid selected config; existing private local state; canonical executable and home.", Destinations: "Preview: none. With --write: schedule definitions under home on macOS/Linux, or task XML under state on Windows. Receipt and lock under state on every platform.", Effects: "reads, writes own state, writes host schedule, process", Flags: map[string]bool{"--time": true, "--write": false}},
	{Name: "sources", Inputs: "list; add PATH [--project NAME]; remove PATH; move OLD_PATH NEW_PATH.", Outputs: "Sources with stable source_id, count, total, empty, changed and scoped index guidance.", Preconditions: "Valid selected config. Add and move need readable destinations.", Destinations: "Selected config, its adjacent .lock file and temporary files in its parent.", Effects: "reads, writes own state, writes workspace", Flags: map[string]bool{"--project": true}},
	{Name: "exclude", Inputs: "list; add RULE; remove RULE.", Outputs: "Rules, count, total, empty, changed and scoped index guidance.", Preconditions: "Valid selected config and valid relative patterns.", Destinations: "Selected config, its adjacent .lock file and temporary files in its parent.", Effects: "reads, writes own state, writes workspace"},
	{Name: "third-party", Inputs: "list; add RULE; remove RULE.", Outputs: "Rules, count, total, empty, changed and scoped index guidance.", Preconditions: "Valid selected config and valid relative patterns.", Destinations: "Selected config, its adjacent .lock file and temporary files in its parent.", Effects: "reads, writes own state, writes workspace"},
	{Name: "status", Inputs: "Optional --state-dir DIR; --format json. Bare invocation is an alias.", Outputs: "Stored index size, age, completeness, source counts and pending_known=false.", Preconditions: "A valid config.json; an index is optional.", Destinations: "<state>/index.sqlite and sidecars (permissions), <state>/locks/* and temporary write probes in <state>.", Effects: "reads, writes own state"},
	{Name: "init", Inputs: "Optional --source PATH (repeatable), --include PATTERN (repeatable); absolute paths accepted.", Outputs: "Selected state folder, config path and source count.", Preconditions: "Readable source folders; a private writable local state folder or parent.", Destinations: "Selected config, its adjacent .lock file, temporary files in its parent, and private state folders.", Effects: "reads, writes own state, writes workspace", Flags: map[string]bool{"--source": true, "--include": true}},
	{Name: "index", Inputs: "Configured sources; optional --full.", Outputs: "File and chunk counts, skipped paths, storage size and duration. Failed attempts remain visible to doctor.", Preconditions: "Valid config.json; private writable state; readable source folders.", Destinations: "<state>/index.sqlite and sidecars, <state>/index-run.json, <state>/locks/*, <state>/converted/* and temporary files in <state>.", Effects: "reads, writes own state", Flags: map[string]bool{"--full": false}},
	{Name: "search", Inputs: "Required QUERY; optional --project NAME, --kind KIND, --limit N, --full, --no-refresh, --shell sh|powershell|cmd.", Outputs: "Ranked cited chunks, index_age_s, index_complete, gaps and next commands.", Preconditions: "Valid selected config and usable stored index.", Destinations: "<state>/index.sqlite and sidecars (permissions), <state>/locks/* and temporary write probes in <state>.", Effects: "reads, writes own state", Flags: map[string]bool{"--project": true, "--kind": true, "--limit": true, "--full": false, "--no-refresh": false, "--shell": true}},
	{Name: "show", Inputs: "Required chunk ID; optional --full, --no-refresh.", Outputs: "Cited chunk, neighboring IDs, index_age_s, index_complete, gaps and next commands.", Preconditions: "Valid config.json and usable index.", Destinations: "<state>/index.sqlite and sidecars (permissions), <state>/locks/* and temporary write probes in <state>.", Effects: "reads, writes own state", Flags: map[string]bool{"--full": false, "--no-refresh": false}},
	{Name: "projects", Inputs: "Configured sources; optional --state-dir DIR; --format json.", Outputs: "Project names, README purpose, file counts, index_age_s and gaps.", Preconditions: "Valid config.json and usable index.", Destinations: "<state>/index.sqlite and sidecars (permissions), <state>/locks/* and temporary write probes in <state>.", Effects: "reads, writes own state"},
	{Name: "recent", Inputs: "Optional --days N (default 7), --limit N (default 20).", Outputs: "Recent indexed files, index_age_s and gaps.", Preconditions: "Valid config.json and usable index.", Destinations: "<state>/index.sqlite and sidecars (permissions), <state>/locks/* and temporary write probes in <state>.", Effects: "reads, writes own state", Flags: map[string]bool{"--days": true, "--limit": true}},
	{Name: "note", Inputs: "Required TEXT; optional --project NAME. A note records text, never an approval.", Outputs: "Saved note path, entry ID, and whether it was indexed.", Preconditions: "Valid config.json and writable private state. A missing index can be created.", Destinations: "<state>/notes/<project>.md, <state>/index.sqlite and sidecars, <state>/locks/*, <state>/converted/* and temporary files in <state>.", Effects: "reads, writes own state", Flags: map[string]bool{"--project": true}},
	{Name: "map", Inputs: "Required PROJECT; optional --no-refresh.", Outputs: "Project purpose, plan and goal facts, recent notes, index_age_s and gaps.", Preconditions: "Valid config.json and usable index.", Destinations: "<state>/index.sqlite and sidecars (permissions), <state>/locks/* and temporary write probes in <state>.", Effects: "reads, writes own state", Flags: map[string]bool{"--no-refresh": false}},
	{Name: "doctor", Inputs: "Optional --state-dir DIR; --format json.", Outputs: "Checks, findings and exact recovery commands.", Preconditions: "None; missing or unsafe state becomes a finding.", Destinations: "<state>/index.sqlite and sidecars (permissions), <state>/locks/* and temporary write probes in <state>.", Effects: "reads, writes own state, process"},
	{Name: "brief", Inputs: "Optional --format text|json|copilot|vscode; --state-dir DIR. --brief is an alias.", Outputs: "Three stored-index context lines, or one object with text, index_age_s and readiness guidance.", Preconditions: "None; missing or unsafe state becomes recovery guidance with exit 0.", Destinations: "None.", Effects: "reads"},
	{Name: "setup", Inputs: "Optional claude|codex|copilot|vscode|all (default all), --write, --state-dir DIR, --format json.", Outputs: "Skill destinations, skill_current for safe copied files, hook examples, allowances and trust guidance. Preview changes no files.", Preconditions: "Absolute HOME or USERPROFILE; --write requires a safe writable user skill folder.", Destinations: "Only with --write: <home>/.claude/skills/brain/SKILL.md, <home>/.agents/skills/brain/SKILL.md, or <home>/.copilot/skills/brain/SKILL.md, and temporary files in those folders. Hooks and settings are printed only.", Effects: "reads, writes host skill folder", Flags: map[string]bool{"--write": false}},
	{Name: "help", Inputs: "Optional COMMAND, --format json|markdown, or --skill. With doctor, --state-dir DIR adds folder guidance. --help always prints plain text.", Outputs: "Plain help, one help object, Markdown help, or the generated portable skill.", Preconditions: "None; no state is read.", Destinations: "None.", Effects: "reads", Flags: map[string]bool{"--format": true, "--skill": false, "--state-dir": true}},
}

var versionHelp = commandHelp{
	Name: "--version", Inputs: "Optional --format json|text. Both return JSON; text remains accepted for compatibility.",
	Outputs:       "One JSON object with version, revision and modified.",
	Preconditions: "None; no state is read and no process is started.",
	Destinations:  "None.", Effects: "reads",
	ExitCodes: "0 completed, 1 output unavailable, 2 bad input.",
	Flags:     map[string]bool{"--format": true},
}

func helpCommands(topic string) []commandHelp {
	if topic == "" {
		return commands
	}
	if command, ok := commandDefinition(topic); ok {
		return []commandHelp{command}
	}
	if topic == versionHelp.Name {
		return []commandHelp{versionHelp}
	}
	return []commandHelp{}
}

func commandDefinition(name string) (commandHelp, bool) {
	if name == "" {
		name = "status"
	}
	if name == "--brief" {
		name = "brief"
	}
	for _, command := range commands {
		if command.Name == name {
			return command, true
		}
	}
	return commandHelp{}, false
}

func commandFlags(name string) map[string]bool {
	if name == versionHelp.Name {
		return versionHelp.Flags
	}
	flags := map[string]bool{"--help": false, "--format": true, "--config": true}
	if name != "help" {
		flags["--state-dir"] = true
	}
	if command, ok := commandDefinition(name); ok {
		for flag, value := range command.Flags {
			flags[flag] = value
		}
	}
	return flags
}

func commandNames() []string {
	names := []string{}
	for _, command := range commands {
		names = append(names, command.Name)
	}
	return names
}

func topicHelp(name, executable string) string {
	if name == "--version" {
		return commandDescription(name, executable) + fmt.Sprintf("  Inputs: %s\n  Outputs: %s\n  Preconditions: %s\n  Destinations: %s\n  Effects: %s\n  Exit codes: %s\n", versionHelp.Inputs, versionHelp.Outputs, versionHelp.Preconditions, versionHelp.Destinations, versionHelp.Effects, versionHelp.ExitCodes)
	}
	command, ok := commandDefinition(name)
	if !ok {
		return ""
	}
	descriptionName := name
	if name == "status" {
		descriptionName = ""
	}
	if name == "brief" {
		descriptionName = "--brief"
	}
	description := commandDescription(descriptionName, executable)
	if name == "status" {
		description = strings.Replace(description, "brain [", "brain status [", 1)
		description = strings.Replace(description, executable+" --format json", executable+" status --format json", 1)
	}
	if name == "brief" {
		description = strings.ReplaceAll(description, "--brief", "brief")
	}
	text := description + fmt.Sprintf("  Inputs: %s\n  Outputs: %s\n  Preconditions: %s\n  Destinations: %s\n  Effects: %s\n  Exit codes: %s\n", command.Inputs, command.Outputs, command.Preconditions, command.Destinations, command.Effects, exitCodes(command.Name))
	flags := commandFlags(name)
	names := []string{}
	for flag := range flags {
		names = append(names, flag)
	}
	sort.Strings(names)
	return text + "  Valid flags: " + strings.Join(names, ", ") + ".\n"
}

func exitCodes(name string) string {
	switch name {
	case "--version":
		return versionHelp.ExitCodes
	case "doctor":
		return "1 findings or check unavailable, 2 bad input, 3 healthy."
	case "index":
		return "0 completed, 1 skipped files or not completed, 2 bad input, 3 clean and nothing changed."
	case "schedule":
		return "0 completed, 1 not completed, 2 bad input, 3 identical healthy job already installed."
	case "settings", "init", "sources", "exclude", "third-party":
		return "0 completed, 1 not completed, 2 bad input, 3 nothing changed."
	default:
		return "0 completed, 1 not completed, 2 bad input."
	}
}

func helpJSON(output io.Writer, topic, folder, guidance string) int {
	selected := []commandHelp{}
	for _, command := range helpCommands(topic) {
		command.Flags = commandFlags(command.Name)
		command.ExitCodes = exitCodes(command.Name)
		selected = append(selected, command)
	}
	return WriteJSON(output, struct {
		Description string        `json:"description"`
		Verbs       []string      `json:"verbs"`
		Commands    []commandHelp `json:"commands"`
		StateDir    string        `json:"state_dir,omitempty"`
		Guidance    string        `json:"guidance,omitempty"`
	}{"Brain indexes and searches local documents without a model or network.", commandNames(), selected, folder, guidance})
}

func runHelp(args []string, output io.Writer) int {
	invalid := func(code, message string) int { return badInput(output, code, message) }
	format, topic, skill := "text", "", false
	chosen, scoped := "", false
	for i := 0; i < len(args); i++ {
		name, value, given := strings.Cut(args[i], "=")
		switch name {
		case "--format":
			if !given {
				i++
				if i == len(args) {
					return invalid("missing-argument", "--format needs json or markdown.")
				}
				value = args[i]
			}
			if value != "json" && value != "markdown" {
				return invalid("invalid-value", "Invalid --format value. Use json or markdown.")
			}
			format = value
		case "--state-dir", "--config":
			if !given {
				i++
				if i == len(args) {
					return invalid("missing-argument", name+" needs a value.")
				}
				value = args[i]
			}
			if name == "--state-dir" {
				chosen, scoped = value, true
			}
		case "--skill":
			if given {
				return invalid("unexpected-argument", "--skill takes no value.")
			}
			skill = true
		case "--help":
			if given {
				return invalid("unexpected-argument", "--help takes no value.")
			}
		default:
			if strings.HasPrefix(name, "-") && name != "--brief" && name != "--version" {
				return invalid("unknown-flag", "Unknown flag. Valid flags: --help, --format, --skill, --state-dir, --config.")
			}
			if topic != "" || !helpTopic(name) {
				return invalid("unknown-command", "Unknown help topic. Valid verbs: "+strings.Join(commandNames(), ", ")+".")
			}
			topic = name
		}
	}
	folder, guidance := "", ""
	if scoped {
		if skill {
			return invalid("unexpected-argument", "--skill cannot include --state-dir.")
		}
		var err error
		folder, err = platform.CurrentState(chosen)
		if err != nil || chosen == "" {
			return invalid("invalid-value", "Invalid --state-dir value. Use an absolute folder.")
		}
		if topic == "doctor" {
			guidance = "\nGuidance for this state folder:\n" + platform.WriteGuidance(folder) + "\n"
		}
	}
	if skill {
		if format != "text" || topic != "" {
			return invalid("unexpected-argument", "--skill prints the portable skill. Use help --skill without a topic or --format.")
		}
		if _, err := writeText(output, skillText()); err != nil {
			return 1
		}
		return 0
	}
	if format == "json" {
		if topic == "schedule" {
			guidance += "\n" + scheduleRecovery
		}
		return helpJSON(output, topic, folder, guidance)
	}
	if format == "markdown" {
		var text strings.Builder
		text.WriteString("# Brain\n\nIndexes and searches local documents without a model or network.\n\n")
		for _, command := range helpCommands(topic) {
			text.WriteString("## " + command.Name + "\n\n```text\n" + topicHelp(command.Name, "brain") + "```\n\n")
		}
		text.WriteString(guidance)
		if _, err := writeText(output, text.String()); err != nil {
			return 1
		}
		return 0
	}
	if topic != "" {
		_, err := writeText(output, topicHelp(topic, helpExecutable())+guidance)
		if err != nil {
			return 1
		}
		return 0
	}
	return help(output)
}
