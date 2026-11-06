package cli

import (
	"brain/internal/corpus"
	"brain/internal/index"
	"brain/internal/platform"
	"brain/internal/query"
	"brain/internal/redact"
	"brain/internal/search"
	"brain/internal/settings"
	"brain/internal/state"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

type call struct {
	scopeResolved                 bool
	scopeErr                      error
	settingsRoot                  string
	mutationLease                 *platform.Lock
	policy                        *redact.Policy
	privacyLock                   *platform.Lock
	command, state, shell, config string
	configExplicit                bool
	flags                         map[string]string
	words, sources, include       []string
	output                        io.Writer
}
type metadata struct {
	Gaps          index.Gaps `json:"gaps"`
	IndexAgeS     int        `json:"index_age_s"`
	IndexComplete bool       `json:"index_complete"`
	ReadOnly      bool       `json:"read_only,omitempty"`
	Next          []string   `json:"next"`
	NextArgv      [][]string `json:"next_argv"`
}
type searchReply struct {
	search.Reply
	metadata
}
type showReply struct {
	*search.Chunk
	metadata
}
type initReply struct {
	State   string `json:"state_dir"`
	Config  string `json:"config"`
	Sources int    `json:"sources"`
}
type indexReply struct {
	New        int              `json:"new"`
	Updated    int              `json:"updated"`
	Removed    int              `json:"removed"`
	Unchanged  int              `json:"unchanged"`
	Chunks     int              `json:"chunks"`
	Duplicates int              `json:"duplicates_folded"`
	Count      int              `json:"count"`
	Total      int              `json:"total"`
	Empty      bool             `json:"empty"`
	Skipped    []corpus.Skipped `json:"skipped"`
	DurationMS int64            `json:"duration_ms"`
	DBBytes    int64            `json:"db_bytes"`
	Next       []string         `json:"next"`
	NextArgv   [][]string       `json:"next_argv"`
}

func (call *call) next(words ...string) (string, []string) {
	executable, _ := os.Executable()
	argv := append([]string{executable}, words...)
	state, config := call.state, call.config
	if len(words) >= 2 && words[0] == "help" && words[1] == "init" {
		remote, known := platform.RemoteFolder(state)
		unsafeConfig := call.configExplicit && platform.NetworkPath(runtime.GOOS, config)
		if remote || !known || unsafeConfig {
			return platform.Command(call.shell, argv), argv
		}
	}
	for i, word := range words {
		value, present := "", false
		if word == "--state-dir" && i+1 < len(words) {
			value, present = words[i+1], true
		}
		if strings.HasPrefix(word, "--state-dir=") {
			value, present = strings.TrimPrefix(word, "--state-dir="), true
		}
		if present && !call.configExplicit {
			config = filepath.Join(value, "config.json")
		}
	}
	for _, scope := range []struct{ flag, value string }{{"--state-dir", state}, {"--config", config}} {
		chosen := false
		for _, word := range words {
			if word == scope.flag || strings.HasPrefix(word, scope.flag+"=") {
				chosen = true
			}
		}
		if !chosen && scope.value != "" {
			argv = append(argv, scope.flag, scope.value)
		}
	}
	if (len(words) == 0 || words[0] != "help") && call.flags["--format"] == "json" {
		chosen := false
		for _, word := range words {
			if word == "--format" || strings.HasPrefix(word, "--format=") {
				chosen = true
			}
		}
		if !chosen {
			argv = append(argv, "--format", "json")
		}
	}
	if call.policy != nil {
		safe := true
		for _, arg := range argv {
			if call.policy.Match(arg) {
				safe = false
				break
			}
		}
		if !safe {
			argv = []string{executable, "help"}
			for _, arg := range argv {
				if call.policy.Match(arg) {
					return "", nil
				}
			}
		}
	}
	return platform.Command(call.shell, argv), argv
}
func (call *call) failure(code, message string, exit int, commands ...[]string) int {
	next, argv := []string{}, [][]string{}
	for _, words := range commands {
		text, args := call.next(words...)
		if len(args) > 0 {
			next = append(next, text)
			argv = append(argv, args)
		}
	}
	if WriteJSON(call.output, refusal{Failure{code, message, next, argv}}) != 0 {
		return 1
	}
	return exit
}
func (call *call) usage(code, message string) int {
	return call.failure(code, message, 2, []string{"help", call.command})
}

func (call *call) again(extra ...string) []string {
	words := append([]string{call.command}, call.words...)
	flags := []string{}
	for flag := range call.flags {
		if flag != "--state-dir" && flag != "--config" && flag != "--format" {
			flags = append(flags, flag)
		}
	}
	sort.Strings(flags)
	for _, flag := range flags {
		skip := false
		for _, one := range extra {
			if flag == one {
				skip = true
			}
		}
		if skip {
			continue
		}
		words = append(words, flag)
		if flag != "--full" && flag != "--no-refresh" && flag != "--write" {
			words = append(words, call.flags[flag])
		}
	}
	return append(words, extra...)
}
func (call *call) resolveScope(arguments []string) {
	if call.scopeResolved {
		return
	}
	call.scopeResolved = true
	values := commandFlags(call.command)
	for i := 0; i < len(arguments); i++ {
		name, value, given := strings.Cut(arguments[i], "=")
		needs, known := values[name]
		if !known || !needs {
			continue
		}
		if !given {
			i++
			if i == len(arguments) {
				break
			}
			value = arguments[i]
		}
		if name == "--state-dir" || name == "--config" || name == "--format" {
			call.flags[name] = value
		}
	}
	call.settingsRoot, call.scopeErr = settings.Root()
	if call.scopeErr != nil && call.flags["--state-dir"] == "" && os.Getenv("BRAIN_HOME") == "" {
		return
	}
	_, selected := call.flags["--config"]
	var scope settings.Scope
	scope, call.configExplicit, call.scopeErr = settings.Resolve(call.settingsRoot, call.flags["--state-dir"], call.flags["--config"], selected)
	call.state, call.config = scope.State, scope.Config

}

func (call *call) parse(arguments []string) int {
	call.resolveScope(arguments)
	values := commandFlags(call.command)
	for i := 0; i < len(arguments); i++ {
		word := arguments[i]
		if !strings.HasPrefix(word, "-") || word == "-" {
			call.words = append(call.words, word)
			continue
		}
		name, value, given := strings.Cut(word, "=")
		needs, valid := values[name]
		if !valid {
			flags := []string{}
			for flag := range values {
				flags = append(flags, flag)
			}
			sort.Strings(flags)
			return call.usage("unknown-flag", "Unknown flag "+inputName(name)+". Valid flags: "+strings.Join(flags, ", ")+".")
		}
		if !needs {
			if given {
				return call.usage("unexpected-argument", name+" takes no value.")
			}
			call.flags[name] = "true"
			continue
		}
		if !given {
			i++
			if i == len(arguments) {
				return call.usage("missing-argument", name+" needs a value.")
			}
			value = arguments[i]
		}
		if name == "--source" {
			call.sources = append(call.sources, value)
		} else if name == "--include" {
			call.include = append(call.include, value)
		} else {
			call.flags[name] = value
		}
	}
	if format, ok := call.flags["--format"]; ok && (call.command == "--brief" || call.command == "brief") {
		if format != "json" && format != "text" && format != "copilot" && format != "vscode" {
			return call.usage("invalid-value", "Invalid --format value "+inputName(format)+". Use text, json, copilot or vscode.")
		}
	} else if ok && format != "json" {
		return call.usage("invalid-value", "Invalid --format value "+inputName(format)+". Use json.")
	}
	if call.command == "search" {
		limit := 8
		if text, ok := call.flags["--limit"]; ok {
			var err error
			limit, err = strconv.Atoi(text)
			if err != nil || limit < 1 || limit > 100 {
				return call.usage("invalid-value", "Invalid --limit value "+inputName(text)+". Use a whole number from 1 to 100.")
			}
		}
		call.flags["--limit"] = strconv.Itoa(limit)
		if kind, ok := call.flags["--kind"]; ok {
			found := false
			for _, one := range corpus.Kinds {
				if one == kind {
					found = true
				}
			}
			if !found {
				return call.usage("invalid-value", "Invalid --kind value "+inputName(kind)+". Kinds: "+strings.Join(corpus.Kinds, ", ")+".")
			}
		}
		if shell, ok := call.flags["--shell"]; ok {
			if shell != "sh" && shell != "powershell" && shell != "cmd" {
				return call.usage("invalid-value", "Invalid --shell value "+inputName(shell)+". Use sh, powershell or cmd.")
			}
			call.shell = shell
		}
	}
	if call.command == "recent" {
		for _, item := range []struct {
			name              string
			fallback, maximum int
		}{{"--days", 7, 9999}, {"--limit", 20, 100}} {
			text, ok := call.flags[item.name]
			if !ok {
				text = strconv.Itoa(item.fallback)
			}
			value, err := strconv.Atoi(text)
			if err != nil || value < 1 || value > item.maximum {
				return call.usage("invalid-value", "Invalid "+item.name+" value "+inputName(text)+". Use a whole number from 1 to "+strconv.Itoa(item.maximum)+".")
			}
			call.flags[item.name] = text
		}
	}
	if call.command == "init" || call.command == "index" || call.command == "" || call.command == "projects" || call.command == "recent" || call.command == "doctor" || call.command == "--brief" || call.command == "brief" || call.command == "status" {
		if len(call.words) > 0 {
			return call.usage("unexpected-argument", "Unexpected argument "+inputName(call.words[0])+".")
		}
	}
	if call.command == "map" && len(call.words) == 0 {
		return call.usage("missing-argument", "map needs one indexed project name.")
	}
	if call.command == "map" && len(call.words) > 1 {
		return call.usage("unexpected-argument", "Unexpected argument "+inputName(call.words[1])+". map needs one project name.")
	}
	if call.command == "show" && len(call.words) == 0 {
		return call.usage("missing-argument", "show needs one chunk id from search.")
	}
	if call.command == "show" && len(call.words) > 1 {
		return call.usage("unexpected-argument", "Unexpected argument "+inputName(call.words[1])+". show needs one chunk id.")
	}
	if call.command == "note" && strings.TrimSpace(strings.Join(call.words, " ")) == "" {
		return call.usage("missing-argument", "note needs the text to save.")
	}
	if call.command == "note" && strings.ContainsRune(strings.Join(call.words, " "), 0) {
		return call.usage("invalid-value", "note text contains a null character. Remove it before saving.")
	}
	if call.command == "search" && strings.TrimSpace(strings.Join(call.words, " ")) == "" {
		return call.usage("query-empty", "search needs a query, for example lease timeout.")
	}
	if call.scopeErr != nil {
		return call.settingsFailure(call.scopeErr)
	}
	if call.command == "schedule" && (len(call.words) > 1 || len(call.words) == 1 && call.words[0] != "status") {
		return call.usage("unexpected-argument", "Use schedule or schedule status.")
	}

	return -1
}
func runState(command string, arguments []string, output io.Writer) (code int) {
	var buffer bytes.Buffer
	call := &call{command: command, flags: map[string]string{}, output: &buffer, shell: defaultShell()}
	err := call.loadPrivacy(arguments)
	defer func() {
		if call.privacyOutput(output, &buffer) != 0 {
			code = 1
		}
		call.closePrivacy()
	}()
	if err != nil {
		if call.scopeErr != nil {
			return call.settingsFailure(call.scopeErr)
		}
		return call.privacyFailure(err)
	}
	if code := call.parse(arguments); code != -1 {
		return code
	}
	defer call.endMutation()
	if command == "settings" {
		return call.settings()
	}
	if command == "sources" || command == "exclude" || command == "third-party" {
		for _, word := range append(append([]string{}, call.words...), call.flags["--project"]) {
			if call.policy.Match(word) {
				return call.privacyFailure(index.ErrPrivacyPath)
			}
		}
		if err := call.beginMutation(); err != nil {
			return call.settingsFailure(err)
		}
		if platform.CheckPrivateState(call.state) == nil {
			if err := settings.CheckRead(call.state); err != nil {
				return call.settingsFailure(err)
			}
		}
		return call.manage()
	}
	if command != "doctor" {
		if remote, _ := platform.RemoteFolder(call.state); remote {
			return call.remote()
		}
	}
	if command != "doctor" {
		if err := platform.CheckPrivateState(call.state); err != nil && !errors.Is(err, os.ErrNotExist) {
			return call.unsafeState()
		}
	}
	if platform.CheckPrivateState(call.state) == nil {
		if err := settings.CheckRead(call.state); err != nil {
			return call.settingsFailure(err)
		}
	}
	if command != "init" {
		if err := call.beginMutation(); err != nil {
			return call.settingsFailure(err)
		}
	}
	if command == "init" {
		return call.init()
	}
	if command == "doctor" {
		return call.doctor()
	}
	if command == "schedule" {
		return call.schedule()
	}
	if command == "index" {
		return call.index()
	}
	config, err := corpus.LoadFile(call.config, call.state)
	if err != nil {
		return call.configError(err)
	}
	if err = call.guardConfigPrivacy(config); err != nil {
		return call.databaseError(err)
	}
	if command == "" || command == "status" {
		return call.view(config)
	}
	if command == "projects" || command == "recent" {
		return call.list(config)
	}
	if command == "map" {
		return call.mapPage(config)
	}
	if command == "note" {
		return call.note(config)
	}
	return call.read(config)
}
func (call *call) init() int {
	for i, given := range call.sources {
		path, err := filepath.Abs(given)
		if err == nil {
			path, err = filepath.EvalSymlinks(path)
		}
		info, problem := os.Stat(path)
		if err != nil || problem != nil || !info.IsDir() || !platform.ReadableDir(path) {
			return call.usage("invalid-value", "--source needs a readable folder.")
		}
		call.sources[i] = path
	}
	for _, given := range call.include {
		if err := corpus.ValidateRule(given); err != nil {
			return call.usage("invalid-value", "Invalid --include pattern "+inputName(given)+". Use a relative filename pattern.")
		}
	}
	if err := call.beginMutation(); err != nil {
		return call.settingsFailure(err)
	}
	if err := platform.PrepareState(call.state); err != nil {
		if errors.Is(err, platform.ErrStateUnsafe) {
			return call.unsafeState()
		}
		return call.unwritable()
	}
	if !platform.Writable(call.state) {
		return call.unwritable()
	}
	root, err := os.Getwd()
	if err != nil {
		return call.usage("invalid-value", "The current folder is unavailable. Use --source.")
	}
	config := corpus.Default(root, call.sources, call.include)
	if err = call.privacyScope(config); err != nil {
		return call.privacyFailure(err)
	}
	for i := range call.sources {
		config.Sources[i].Literal = true
	}
	document, changed, err := corpus.Initialize(call.config, config)
	if err != nil {
		if errors.Is(err, corpus.ErrConfigInvalid) {
			return call.configError(err)
		}
		return call.configWriteError(err)
	}
	code := 3
	if changed {
		code = 0
	}
	reply := initReply{call.state, call.config, len(document.Config.Sources)}
	if call.flags["--format"] == "json" {
		if WriteJSON(call.output, reply) != 0 {
			return 1
		}
	} else {
		if _, err = writeText(call.output, fmt.Sprintf("State folder: %s\nConfig: %s\nSources: %d\n", reply.State, reply.Config, reply.Sources)); err != nil {
			return 1
		}
	}
	return code
}
func (call *call) configError(err error) int {
	if errors.Is(err, redact.ErrPolicyInvalid) {
		return call.privacyFailure(err)
	}
	if errors.Is(err, corpus.ErrConfigMissing) {
		return call.failure("state-uninitialised", "The selected config is missing. init writes it.", 1, []string{"init"})
	}
	return call.failure("config-invalid", "The selected config cannot be read or is invalid. Repair it or restore a saved copy. init preserves it.", 1, []string{"help", "doctor"})
}
func (call *call) unsafeState() int {
	return call.failure("state-unsafe", "The state folder cannot be verified as private. Choose a private local folder or ask its owner to repair access permissions.", 1, []string{"help", "doctor"})
}
func (call *call) databaseError(err error) int {
	if errors.Is(err, index.ErrPrivacyRefresh) || errors.Is(err, index.ErrPrivacyPath) || errors.Is(err, redact.ErrPolicyInvalid) {
		return call.privacyFailure(err)
	}
	if errors.Is(err, platform.ErrStateUnsafe) {
		return call.unsafeState()
	}
	if errors.Is(err, corpus.ErrIgnoreUnreadable) {
		return call.failure("ignore-unreadable", "An .brainignore file cannot be read safely. Repair its contents or access, then run index again.", 1, []string{"index"})
	}
	if errors.Is(err, corpus.ErrUnreadable) {
		commands := [][]string{{"doctor"}}
		if call.command == "search" || call.command == "show" {
			commands = append(commands, call.again("--no-refresh"))
		}
		return call.failure("source-unreadable", "A source folder is missing or cannot be read. Restore the folder or update the selected config.", 1, commands...)
	}
	if index.Locked(err) || errors.Is(err, platform.ErrLocked) {
		return call.failure("db-locked", "Brain could not get stable access to the index. Another process may be using it. Try again.", 1, call.again())
	}
	if errors.Is(err, index.ErrOldSQLite) {
		return call.failure("sqlite-too-old", "Brain needs SQLite 3.43.0 or newer. Rebuild with the pinned driver.", 1, []string{"help"})
	}
	if !index.Rebuildable(err) {
		return call.failure("index-unavailable", "The index operation failed without confirmed database damage. Check state-file permissions, free disk space and storage access.", 1, []string{"help", "doctor"})
	}
	return call.failure("index-missing", "The index is missing or cannot be used. index --full rebuilds it.", 1, []string{"index", "--full"})
}
func (call *call) index() (code int) {
	if _, err := os.Stat(call.state); errors.Is(err, os.ErrNotExist) {
		return call.failure("state-uninitialised", "The state folder is absent. init creates it.", 1, []string{"init"})
	}
	if !platform.Writable(call.state) {
		return call.unwritable()
	}
	required, err := index.PrivacyRequiredForIndex(call.state, call.policy, false)
	deadline := time.Now().Add(5 * time.Second)
	for (index.Locked(err) || errors.Is(err, platform.ErrLocked)) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		required, err = index.PrivacyRequiredForIndex(call.state, call.policy, false)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) && !index.Rebuildable(err) {
		return call.databaseError(err)
	}
	lease, err := platform.PrivacyLock(call.state, required, true)
	if err != nil {
		return call.databaseError(err)
	}
	defer lease.Close()
	lock, err := platform.Acquire(call.state, "index", true)
	if err != nil {
		return call.databaseError(err)
	}
	defer lock.Close()
	run := index.Run{StartedAt: time.Now().UTC(), Outcome: "running"}
	if err = index.SaveRun(call.state, run); err != nil {
		return call.databaseError(err)
	}
	finalized := false
	defer func() {
		if finalized {
			return
		}
		run.FinishedAt = time.Now().UTC()
		run.Outcome = "failed"
		_ = index.SaveRun(call.state, run)
	}()
	config, err := corpus.LoadFile(call.config, call.state)
	if err != nil {
		return call.configError(err)
	}
	if !required {
		needed, err := index.PrivacyRequiredForIndex(call.state, config.Privacy(), true)
		if err != nil {
			return call.databaseError(err)
		}
		if needed {
			return call.privacyFailure(index.ErrPrivacyRefresh)
		}
	}
	call.policy = config.Privacy()
	if err = call.privacyScope(config); err != nil {
		return call.privacyFailure(err)
	}
	run.ConfigFingerprint = index.Fingerprint(config)
	if err = index.SaveRun(call.state, run); err != nil {
		return call.databaseError(err)
	}
	started := time.Now()
	full := call.flags["--full"] == "true"
	var report index.Report
	if required {
		report, err = index.MigrateLocked(call.state, config)
		if err != nil {
			return call.databaseError(err)
		}
	}
	db, err := index.OpenLocked(call.state, full)
	if err != nil {
		return call.databaseError(err)
	}
	defer db.Close()
	if !required {
		report, err = index.RefreshLocked(db, call.state, config, full)
	}
	if err != nil {
		return call.databaseError(err)
	}
	_, chunks, folded, err := index.Totals(db)
	if err != nil {
		return call.databaseError(err)
	}
	run.FinishedAt = time.Now().UTC()
	run.Outcome = "complete"
	if len(report.Skipped) > 0 {
		run.Outcome = "failed"
	}
	if err = index.SaveRun(call.state, run); err != nil {
		return call.databaseError(err)
	}
	finalized = true
	reply := indexReply{
		New:        report.New,
		Updated:    report.Updated,
		Removed:    report.Removed,
		Unchanged:  report.Unchanged,
		Chunks:     chunks,
		Duplicates: folded,
		Count:      len(report.Skipped),
		Total:      len(report.Skipped),
		Empty:      len(report.Skipped) == 0,
		Skipped:    report.Skipped,
		DurationMS: time.Since(started).Milliseconds(),
		DBBytes:    index.Bytes(call.state),
		Next:       []string{},
		NextArgv:   [][]string{},
	}
	if len(report.Skipped) > 0 {
		text, argv := call.next("index", "--full")
		reply.Next = append(reply.Next, text)
		reply.NextArgv = append(reply.NextArgv, argv)
	}
	if call.flags["--format"] == "json" {
		if WriteJSON(call.output, reply) != 0 {
			return 1
		}
	} else {
		if _, err = writeText(call.output, fmt.Sprintf("New %d, updated %d, removed %d, unchanged %d.\nChunks %d, duplicates folded %d.\nIndex %d bytes, built in %d ms.\n", reply.New, reply.Updated, reply.Removed, reply.Unchanged, reply.Chunks, reply.Duplicates, reply.DBBytes, reply.DurationMS)); err != nil {
			return 1
		}
		for _, skip := range reply.Skipped {
			if _, err = writeText(call.output, fmt.Sprintf("Skipped (%s): %s\n", skip.Reason, skip.Path)); err != nil {
				return 1
			}
		}
	}
	if call.flags["--format"] != "json" && len(report.Skipped) > 0 {
		if _, err = writeText(call.output, fmt.Sprintln("Repair the listed folders or files, then run the next command.")); err != nil {
			return 1
		}
		if call.printNext(reply.Next) != 0 {
			return 1
		}
	}
	if len(report.Skipped) > 0 {
		return 1
	}
	if report.New+report.Updated+report.Removed+report.Purged == 0 {
		return 3
	}
	return 0
}
func (call *call) ready(config corpus.Config) (*index.Stored, metadata, error) {
	meta := metadata{Gaps: index.MergeGaps(), Next: []string{}, NextArgv: [][]string{}, ReadOnly: !platform.Writable(call.state)}
	db, err := index.OpenRead(call.state, meta.ReadOnly)
	if err != nil {
		return nil, meta, err
	}
	last, complete, err := index.ReadTime(db)
	meta.IndexComplete = complete
	if err != nil || last == 0 {
		db.Close()
		if err == nil {
			err = index.ErrMissing
		}
		return nil, meta, err
	}
	meta.Gaps, err = index.StoredGaps(db)
	if err != nil {
		db.Close()
		return nil, meta, err
	}
	meta.IndexAgeS = max(0, int(float64(time.Now().UnixNano())/1e9-last))
	if meta.ReadOnly {
		text, argv := call.next("doctor")
		meta.Next = append(meta.Next, text)
		meta.NextArgv = append(meta.NextArgv, argv)
	}
	if !meta.Gaps.Empty {
		text, argv := call.next("doctor")
		if len(meta.Next) == 0 || meta.Next[len(meta.Next)-1] != text {
			meta.Next = append(meta.Next, text)
			meta.NextArgv = append(meta.NextArgv, argv)
		}
	}
	return db, meta, nil
}
func (call *call) read(config corpus.Config) int {
	typed := strings.Join(call.words, " ")
	if call.command == "search" && strings.TrimSpace(typed) == "" {
		return call.failure("query-empty", "search needs a query, for example the next command.", 2, []string{"search", "return a lease"})
	}
	db, meta, err := call.ready(config)
	if err != nil {
		return call.databaseError(err)
	}
	defer db.Close()
	addNext := func(words ...string) {
		text, argv := call.next(words...)
		meta.Next = append(meta.Next, text)
		meta.NextArgv = append(meta.NextArgv, argv)
	}
	if call.command == "search" {
		known, err := search.Projects(db.DB)
		if err != nil {
			return call.databaseError(err)
		}
		plan := query.Make(typed, known)
		if plan == nil {
			return call.failure("query-empty", "The query has no letter or digit to search for.", 2, []string{"search", "return a lease"})
		}
		project := call.flags["--project"]
		if project != "" {
			found, err := state.ResolveProject(known, project)
			if err != nil {
				return call.ambiguousProject()
			}
			if found == "" {
				return call.failure("project-unknown", "No indexed project has that name. Remove --project or choose an indexed project.", 1, []string{"projects"}, []string{"search", typed})
			}
			project = found
		}
		limit, _ := strconv.Atoi(call.flags["--limit"])
		reply, err := search.Find(db.DB, plan, project, call.flags["--kind"], limit, call.flags["--full"] == "true")
		if err != nil {
			if index.Locked(err) {
				return call.databaseError(err)
			}
			printable := strings.Map(func(r rune) rune {
				if r < ' ' || r == '"' {
					return -1
				}
				return r
			}, typed)
			return call.failure("query-invalid", "FTS5 rejected the query. Search for one quoted phrase.", 2, []string{"search", `"` + printable + `"`})
		}
		if len(reply.Results) > 0 {
			addNext("show", reply.Results[0].ID)
		}
		for _, item := range reply.Results {
			if item.Truncated {
				addNext(call.again("--full")...)
				break
			}
		}
		if call.flags["--format"] == "json" {
			return WriteJSON(call.output, searchReply{reply, meta})
		}
		return writeSearch(call.output, reply, meta)
	}
	chunk, err := search.Show(db.DB, typed, call.flags["--full"] == "true")
	if err != nil {
		return call.databaseError(err)
	}
	if chunk == nil {
		heading := search.LastHeading(db.DB, typed)
		message := "No current chunk has that id."
		terms := "return a lease"
		if heading != "" {
			message += " Its last known heading was " + inputName(heading) + "."
			terms = heading
		}
		return call.failure("chunk-not-found", message, 1, []string{"search", terms})
	}
	if chunk.Truncated {
		addNext("show", typed, "--full")
	}
	if chunk.NextChunk != nil {
		addNext("show", *chunk.NextChunk)
	}
	if call.flags["--format"] == "json" {
		return WriteJSON(call.output, showReply{chunk, meta})
	}
	if _, err = writeText(call.output, fmt.Sprintf("%s\n%s\n\n%s\n\nIndex age %d s.\n", chunk.Path, chunk.Heading, chunk.Text, meta.IndexAgeS)); err != nil {
		return 1
	}
	if writeGaps(call.output, meta.Gaps) != 0 {
		return 1
	}
	if chunk.Prev != nil {
		previous, _ := call.next("show", *chunk.Prev)
		if _, err = writeText(call.output, fmt.Sprintf("Previous: %s\n", textCommand(previous))); err != nil {
			return 1
		}
	}
	for _, next := range meta.Next {
		if _, err = writeText(call.output, fmt.Sprintf("Next: %s\n", textCommand(next))); err != nil {
			return 1
		}
	}
	return 0
}
func writeSearch(output io.Writer, reply search.Reply, meta metadata) int {
	if _, err := writeText(output, fmt.Sprintf("%d of %d results for %s (%s query); index age %d s.\n", reply.Count, reply.Total, reply.Query.AsRun, reply.Query.Mode, meta.IndexAgeS)); err != nil {
		return 1
	}
	for i, item := range reply.Results {
		if _, err := writeText(output, fmt.Sprintf("\n%d. %s\n   ID: %s\n   %s, score %g\n%s\n", i+1, item.Path, item.ID, item.Heading, item.Score, item.Snippet)); err != nil {
			return 1
		}
		if len(item.AlsoIn) > 0 {
			if _, err := writeText(output, fmt.Sprintf("   Also in: %s\n", strings.Join(item.AlsoIn, ", "))); err != nil {
				return 1
			}
		}
	}
	if writeGaps(output, meta.Gaps) != 0 {
		return 1
	}
	for _, next := range meta.Next {
		if _, err := writeText(output, fmt.Sprintf("Next: %s\n", textCommand(next))); err != nil {
			return 1
		}
	}
	return 0
}

func writeGaps(output io.Writer, gaps index.Gaps) int {
	if gaps.Empty {
		return 0
	}
	if _, err := writeText(output, fmt.Sprintf("Index gaps: %d. Some source contents are missing or may be outdated. Run doctor for repair guidance.\n", gaps.Count)); err != nil {
		return 1
	}
	return 0
}

func (call *call) ambiguousProject() int {
	return call.failure("project-ambiguous", "Several indexed project names match without case. Assign distinct project labels to their sources in config.json, then run index.", 1, []string{"projects"})
}
