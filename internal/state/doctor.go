package state

import (
	"brain/internal/corpus"
	"brain/internal/index"
	"brain/internal/platform"
	"brain/internal/schedule"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}
type Problem struct{ Code, Message string }

func Doctor(folder, shell string, selected ...string) ([]Problem, []Check, error) {
	problems, checks := []Problem{}, []Check{}
	check := func(name string, ok bool, detail, code, message string) bool {
		checks = append(checks, Check{name, ok, detail})
		if !ok {
			problems = append(problems, Problem{code, message})
		}
		return ok
	}
	reportGaps := func(all index.Gaps) {
		if !all.Empty {
			problems = append(problems, Problem{"index-gaps", "Some folders or files were not indexed. Restore folder access, repair converted files, or reduce oversized files. Then run index --full."})
			for _, gap := range all.Items {
				checks = append(checks, Check{"index-gap", false, gap.Path + ": " + gap.Reason})
			}
		}
	}
	memory, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return problems, checks, err
	}
	var sqlite string
	err = memory.QueryRow("SELECT sqlite_version()").Scan(&sqlite)
	memory.Close()
	if err != nil {
		return problems, checks, err
	}
	check("sqlite", index.VersionOK(sqlite), "SQLite "+sqlite+"; Brain needs 3.43.0 or newer.", "sqlite-too-old", "Brain needs SQLite 3.43.0 or newer. Rebuild with the pinned driver.")
	check("runtime", runtime.Version() == "go1.27.1", "This binary uses "+runtime.Version()+"; the pinned runtime is go1.27.1.", "runtime-unpinned", "The binary was built with another Go version. Rebuild with the pinned toolchain.")
	check("shell", shell == "sh" || shell == "powershell" || shell == "cmd", "Next commands use "+shell+" quoting.", "shell-unknown", "The shell form is unknown. Use next_argv without a shell.")
	git := platform.GitPath()
	detail := "Git is unavailable on PATH; project maps still show indexed facts."
	if git != "" {
		detail = "Project maps use optional Git at " + git + "."
	}
	check("git", true, detail, "", "")
	remote, known := platform.RemoteFolder(folder)
	if !known {
		check("state-local", false, "The filesystem could not be identified. Local storage is unproved.", "state-location-unknown", "The state folder's filesystem could not be identified. Choose a known local folder.")
	} else if !check("state-local", !remote, "SQLite needs local storage on one computer.", "state-remote", "The state folder is on a network path or drive. Choose a local folder.") {
		return problems, checks, nil
	}
	info, err := os.Stat(folder)
	if err != nil || !info.IsDir() {
		check("state-folder", false, "The state folder is absent or unavailable; init creates it.", "state-uninitialised", "The state folder is absent or unavailable. init creates it.")
		return problems, checks, nil
	}
	check("state-folder", true, "The state folder exists.", "state-uninitialised", "")
	if err := platform.CheckPrivateState(folder); err != nil {
		check("state-private", false, "The state folder must be private to its owner.", "state-unsafe", "The state folder is not private. Review its permissions before using it.")
		return problems, checks, nil
	}
	privacy := "The state folder has owner-only POSIX permissions."
	if runtime.GOOS == "windows" {
		privacy = "The state root passed path checks. Windows access permissions remain unverified."
	}
	check("state-private", true, privacy, "", "")
	writable := platform.ReadableDir(folder) && platform.Writable(folder)
	detail = "Brain can read and write the state folder."
	if !writable {
		detail = platform.WriteGuidance(folder)
	}
	if !check("state-writable", writable, detail, "state-unwritable", "The state folder cannot be written. Review the folder and sandbox allowance.") {
		if db, err := index.OpenStored(folder); err == nil {
			defer db.Close()
			if gaps, err := index.StoredGaps(db); err == nil {
				reportGaps(gaps)
			}
		} else if errors.Is(err, platform.ErrStateUnsafe) {
			check("index", false, "A database file has unsafe ownership, type or links.", "state-unsafe", "A database file is unsafe. Ask its owner to repair its permissions or links before using it.")
		}
		return problems, checks, nil
	}
	notes := filepath.Join(folder, "notes")
	info, err = os.Lstat(notes)
	notesOK := os.IsNotExist(err) || err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 && platform.ReadableDir(notes) && platform.Writable(notes)
	check("notes", notesOK, "The notes folder must allow reads and writes when it exists.", "notes-unwritable", "The notes folder cannot be read and written. Review its permissions.")
	configPath := filepath.Join(folder, "config.json")
	if len(selected) > 0 {
		configPath = selected[0]
	}
	config, err := corpus.LoadFile(configPath, folder)
	valid := err == nil
	code, message := "config-invalid", "The selected config cannot be read or is invalid. Repair it without replacing its sources and exclusions."
	if errors.Is(err, corpus.ErrConfigMissing) {
		code, message = "state-uninitialised", "The selected config is missing. init writes it."
	}
	check("config", valid, "config.json must list valid source folders and options.", code, message)
	run, present, runErr := index.LastRun(folder)
	check("index-run", runErr == nil && (!present || run.Outcome == "complete"), "The latest explicit index attempt must complete.", "index-failed", "The last index attempt has not completed, or its receipt is unreadable. Retry index after any active run finishes.")
	scheduleFolder := folder
	if canonical, err := filepath.EvalSymlinks(folder); err == nil {
		scheduleFolder = canonical
	}
	plan, scheduled, scheduleErr := schedule.Current(scheduleFolder)
	if scheduleErr != nil {
		check("schedule", false, "The schedule receipt cannot be read safely.", "schedule-failed", "The schedule receipt is unavailable. Review schedule setup.")
	} else if scheduled {
		status, err := schedule.Inspect(plan, nil)
		selectedConfig, configErr := platform.ConfigPath(configPath)
		check("schedule", err == nil && status.Healthy && configErr == nil && selectedConfig == plan.Spec.Config, "The daily job must use the selected config, be enabled, and have no known failed run.", "schedule-failed", "The daily index job is unavailable or failed. Review schedule setup and scheduler access.")
	} else {
		check("schedule", true, "No daily schedule is configured. Run index manually to refresh sources.", "", "")
	}
	if _, err = os.Stat(filepath.Join(folder, "index.sqlite")); os.IsNotExist(err) {
		check("index", false, "The index is absent; index builds it.", "index-missing", "The state folder has no index yet. index builds it.")
		return problems, checks, nil
	}
	db, err := index.Open(folder, false, false, false)
	if err != nil {
		if !errors.Is(err, platform.ErrStateUnsafe) && !index.Rebuildable(err) {
			return problems, checks, err
		}
		if errors.Is(err, platform.ErrStateUnsafe) {
			check("index", false, "A database file has unsafe ownership, type or links.", "state-unsafe", "A database file is unsafe. Ask its owner to repair its permissions or links before using it.")
		} else {
			check("index", false, "The index cannot be opened with the current schema.", "index-damaged", "The index cannot be used. index --full rebuilds it.")
		}
		return problems, checks, nil
	}
	defer db.Close()
	check("index", true, fmt.Sprintf("index.sqlite opens with schema %d.", index.Schema), "index-damaged", "")
	var status string
	err = db.QueryRow("PRAGMA quick_check").Scan(&status)
	if err != nil && !index.Rebuildable(err) {
		return problems, checks, err
	}
	if !check("quick-check", err == nil && status == "ok", "SQLite's quick check inspects database pages.", "index-damaged", "SQLite's quick check found damage. index --full rebuilds it.") {
		return problems, checks, nil
	}
	tx, err := db.Begin()
	if err == nil {
		_, err = tx.Exec(index.FTSCheck)
		tx.Rollback()
	}
	if err != nil && !index.Rebuildable(err) {
		return problems, checks, err
	}
	if !check("fts-integrity", err == nil, "The FTS5 integrity check compares text and search entries.", "index-damaged", "The FTS5 integrity check found damage. index --full rebuilds it.") {
		return problems, checks, nil
	}
	last, err := index.ScannedAt(db)
	if err != nil {
		return problems, checks, err
	}
	age := max(0, int(float64(time.Now().UnixNano())/1e9-last))
	check("last-scan", last != 0, "A complete source scan must finish before source freshness is known.", "index-missing", "No complete source scan has finished. Run index.")
	if last != 0 {
		check("freshness", age <= 86400, fmt.Sprintf("The last complete scan finished %d s ago; daily indexing is overdue after 86400 s.", age), "index-stale", "The last complete scan is more than one day old. Run index when sources are available.")
	}
	if !valid {
		return problems, checks, nil
	}
	stored, err := index.StoredGaps(db)
	if err != nil {
		return problems, checks, err
	}
	reportGaps(stored)
	var fingerprint string
	err = db.QueryRow("SELECT value FROM meta WHERE key='config_fingerprint'").Scan(&fingerprint)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return problems, checks, err
	}
	check("config-current", fingerprint == "" || fingerprint == index.Fingerprint(config), "The selected config must match the last complete scan.", "index-stale", "The selected config changed after the last complete scan. Run index.")
	var old int
	err = db.QueryRow("SELECT count(*) FROM files WHERE deleted_at < ?", float64(time.Now().Unix())-float64(config.Options.PurgeAfterDays)*86400).Scan(&old)
	if err != nil {
		return problems, checks, err
	}
	check("purge", old == 0, fmt.Sprintf("Files deleted more than %d days ago and still indexed: %d.", config.Options.PurgeAfterDays, old), "purge-overdue", "Old deleted files remain indexed. index --full purges them.")
	return problems, checks, nil
}
