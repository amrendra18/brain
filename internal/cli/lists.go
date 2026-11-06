package cli

import (
	"brain/internal/corpus"
	"brain/internal/index"
	"brain/internal/platform"
	"brain/internal/state"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
)

type viewReply struct {
	state.View
	ReadOnly bool       `json:"read_only,omitempty"`
	Next     []string   `json:"next"`
	NextArgv [][]string `json:"next_argv"`
}
type projectsReply struct {
	Count    int             `json:"count"`
	Total    int             `json:"total"`
	Empty    bool            `json:"empty"`
	Projects []state.Project `json:"projects"`
	metadata
}
type recentReply struct {
	Count     int              `json:"count"`
	Total     int              `json:"total"`
	Empty     bool             `json:"empty"`
	Days      int              `json:"days"`
	Documents []state.Document `json:"documents"`
	metadata
}
type doctorReply struct {
	Redactions []index.Redaction `json:"redactions"`
	Problems   []Failure         `json:"problems"`
	Checks     []state.Check     `json:"checks"`
	Count      int               `json:"count"`
	Total      int               `json:"total"`
	Empty      bool              `json:"empty"`
}

func helpTopic(name string) bool {
	if name == "--version" {
		return true
	}
	_, ok := commandDefinition(name)
	return ok
}
func (call *call) unwritable() int {
	return call.failure("state-unwritable", "The state folder cannot be written. doctor prints folder and sandbox guidance.", 1, []string{"doctor"})
}
func (call *call) localRecovery() []string {
	if call.configExplicit {
		if remote, known := platform.RemoteFolder(call.config); remote || !known {
			return []string{"help", "init"}
		}
	}
	home, _ := os.UserHomeDir()
	folder, err := platform.StateFolder(runtime.GOOS, home, map[string]string{"LOCALAPPDATA": os.Getenv("LOCALAPPDATA"), "XDG_DATA_HOME": os.Getenv("XDG_DATA_HOME")}, "")
	if err != nil {
		return []string{"help", "init"}
	}
	if remote, known := platform.RemoteFolder(folder); remote || !known {
		return []string{"help", "init"}
	}
	return []string{"init", "--state-dir", folder}
}

func (call *call) remote() int {
	return call.failure("state-remote", "The state folder is on a network path or drive. Choose a local state folder.", 1, call.localRecovery())
}
func (call *call) view(config corpus.Config) int {
	readonly := !platform.Writable(call.state)
	db, err := index.OpenRead(call.state, readonly)
	if err != nil {
		if _, problem := os.Stat(filepath.Join(call.state, "index.sqlite")); !os.IsNotExist(problem) {
			return call.databaseError(err)
		}
	}
	var connection *sql.DB
	if db != nil {
		defer db.Close()
		connection = db.DB
	}
	view, missing, err := state.Snapshot(call.state, config, connection)
	if err != nil {
		return call.databaseError(err)
	}
	reply := viewReply{View: view, ReadOnly: readonly, Next: []string{}, NextArgv: [][]string{}}
	add := func(words ...string) {
		text, argv := call.next(words...)
		reply.Next = append(reply.Next, text)
		reply.NextArgv = append(reply.NextArgv, argv)
	}
	if len(missing) > 0 || readonly || !view.Index.Gaps.Empty {
		add("doctor")
	}
	if !view.Index.Present || view.Index.Pending > 0 {
		add("index")
	} else {
		add("search", "return a lease")
	}
	if call.flags["--format"] == "json" {
		return WriteJSON(call.output, reply)
	}
	summary := "Index: not built yet."
	if view.Index.Present {
		summary = fmt.Sprintf("Index: files %d, chunks %d, duplicates folded %d, %d bytes, last scan %d s ago.", view.Index.Files, view.Index.Chunks, view.Index.Duplicates, view.Index.DBBytes, *view.Index.IndexAgeS)
	}
	if view.Index.Present && !view.Index.IndexComplete {
		summary = fmt.Sprintf("Index: files %d, chunks %d; no complete source scan has finished.", view.Index.Files, view.Index.Chunks)
	}
	if _, err = writeText(call.output, fmt.Sprintf("State folder: %s\n%s\nPending changes: unknown until explicit indexing.\n", call.state, summary)); err != nil {
		return 1
	}
	for _, source := range view.Sources {
		if _, err = writeText(call.output, fmt.Sprintf("Files in %s: %d.\n", source.Path, source.Files)); err != nil {
			return 1
		}
	}
	if writeGaps(call.output, view.Index.Gaps) != 0 {
		return 1
	}
	return call.printNext(reply.Next)
}
func (call *call) printNext(commands []string) int {
	for _, command := range commands {
		if _, err := writeText(call.output, fmt.Sprintf("Next: %s\n", textCommand(command))); err != nil {
			return 1
		}
	}
	return 0
}
func (call *call) list(config corpus.Config) int {
	db, meta, err := call.ready(config)
	if err != nil {
		return call.databaseError(err)
	}
	defer db.Close()
	if call.command == "projects" {
		projects, err := state.Projects(db.DB, config)
		if err != nil {
			return call.databaseError(err)
		}
		reply := projectsReply{
			Count:    len(projects),
			Total:    len(projects),
			Empty:    len(projects) == 0,
			Projects: projects,
			metadata: meta,
		}
		if call.flags["--format"] == "json" {
			return WriteJSON(call.output, reply)
		}
		if _, err = writeText(call.output, fmt.Sprintf("%d projects; index age %d s.\n", reply.Count, meta.IndexAgeS)); err != nil {
			return 1
		}
		for _, project := range projects {
			if _, err = writeText(call.output, fmt.Sprintf("\n%s: %s\n  Files %d, last indexed %s.\n", project.Name, project.Path, project.Files, project.LastIndexed)); err != nil {
				return 1
			}
			if project.Purpose != "" {
				if _, err = writeText(call.output, fmt.Sprintf("  %s\n", project.Purpose)); err != nil {
					return 1
				}
			}
		}
		if writeGaps(call.output, meta.Gaps) != 0 {
			return 1
		}
		return call.printNext(meta.Next)
	}
	days, _ := strconv.Atoi(call.flags["--days"])
	limit, _ := strconv.Atoi(call.flags["--limit"])
	documents, total, err := state.Recent(db.DB, days, limit)
	if err != nil {
		return call.databaseError(err)
	}
	reply := recentReply{
		Count:     len(documents),
		Total:     total,
		Empty:     len(documents) == 0,
		Days:      days,
		Documents: documents,
		metadata:  meta,
	}
	if call.flags["--format"] == "json" {
		return WriteJSON(call.output, reply)
	}
	if _, err = writeText(call.output, fmt.Sprintf("%d of %d files changed in the last %d days; index age %d s.\n", reply.Count, total, days, meta.IndexAgeS)); err != nil {
		return 1
	}
	for _, document := range documents {
		if _, err = writeText(call.output, fmt.Sprintf("%s  %s\n", document.Modified, document.Path)); err != nil {
			return 1
		}
	}
	if writeGaps(call.output, meta.Gaps) != 0 {
		return 1
	}
	return call.printNext(meta.Next)
}
func (call *call) doctor() int {
	if err := call.guardPrivacy(); errors.Is(err, index.ErrPrivacyRefresh) {
		text, argv := call.next("index")
		next, args := []string{}, [][]string{}
		if len(argv) > 0 {
			next = append(next, text)
			args = append(args, argv)
		}
		reply := doctorReply{Problems: []Failure{{"privacy-refresh-required", "Stored content needs an explicit privacy refresh.", next, args}}, Checks: []state.Check{{Name: "privacy", OK: false, Detail: "Run index to replace unsafe stored content."}}, Count: 1, Total: 1, Empty: false, Redactions: []index.Redaction{}}
		if call.flags["--format"] == "json" {
			if WriteJSON(call.output, reply) != 0 {
				return 1
			}
		} else {
			if _, err := writeText(call.output, "Stored content needs an explicit privacy refresh. Run index.\n"); err != nil {
				return 1
			}
			call.printNext(next)
		}
		return 1
	} else if err != nil && !errors.Is(err, os.ErrNotExist) && !index.Rebuildable(err) {
		return call.databaseError(err)
	}
	problems, checks, err := state.Doctor(call.state, call.shell, call.config)
	if err != nil {
		return call.databaseError(err)
	}
	redactions := []index.Redaction{}
	if db, err := index.OpenStored(call.state); err == nil {
		redactions, err = index.Redactions(db)
		db.Close()
		if err != nil {
			return call.databaseError(err)
		}
	}
	reply := doctorReply{Redactions: redactions, Problems: []Failure{}, Checks: checks, Count: len(problems), Total: len(problems), Empty: len(problems) == 0}
	for _, problem := range problems {
		words := []string{"help", "doctor"}
		switch problem.Code {
		case "state-uninitialised":
			words = []string{"init"}
		case "index-missing", "index-stale", "index-failed":
			words = []string{"index"}
		case "schedule-failed":
			words = []string{"schedule"}
		case "index-damaged", "purge-overdue", "index-gaps":
			words = []string{"index", "--full"}
		case "state-remote", "state-location-unknown":
			words = call.localRecovery()
		}
		text, argv := call.next(words...)
		reply.Problems = append(reply.Problems, Failure{problem.Code, problem.Message, []string{text}, [][]string{argv}})
	}
	if call.flags["--format"] == "json" {
		if WriteJSON(call.output, reply) != 0 {
			return 1
		}
	} else {
		text := "No problem found.\n"
		if reply.Count > 0 {
			text = fmt.Sprintf("Problems found: %d.\n", reply.Count)
		}
		for _, problem := range reply.Problems {
			text += fmt.Sprintf("Problem %s: %s\n", problem.Code, problem.Message)
			for _, next := range problem.Next {
				text += "  Next: " + textCommand(next) + "\n"
			}
		}
		for _, entry := range redactions {
			text += fmt.Sprintf("Redacted %s: %v\n", entry.Path, entry.Rules)
		}
		text += "Checks:\n"
		for _, check := range checks {
			label := "ok"
			if !check.OK {
				label = "PROBLEM"
			}
			text += fmt.Sprintf("  %s  %s: %s\n", label, check.Name, check.Detail)
		}
		if _, err = writeText(call.output, fmt.Sprint(text)); err != nil {
			return 1
		}
	}
	if reply.Empty {
		return 3
	}
	return 1
}
