package index

import (
	"brain/internal/corpus"
	"brain/internal/notes"
	"brain/internal/platform"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// ReadTime distinguishes a complete source scan from a usable partial index.
func ReadTime(db Reader) (stamp float64, complete bool, err error) {
	stamp, err = ScannedAt(db)
	if err != nil || stamp != 0 {
		return stamp, stamp != 0, err
	}
	err = db.QueryRow("SELECT COALESCE(max(indexed_at),0) FROM files WHERE deleted_at IS NULL").Scan(&stamp)
	return stamp, false, err
}

// Note updates one owned note file. Other sources and scan times stay untouched.
func Note(db *sql.DB, state string, config corpus.Config, project string) error {
	if err := preparePrivacy(db, config.Privacy()); err != nil {
		return err
	}
	if !notes.Project(project) {
		return platform.ErrStateUnsafe
	}
	lock, err := platform.Acquire(state, "index", true)
	if err != nil {
		return err
	}
	defer lock.Close()
	state, err = filepath.EvalSymlinks(state)
	if err != nil {
		return err
	}
	name := filepath.Join("notes", project+".md")
	if err = platform.SafeStatePath(state, name); err != nil {
		return err
	}
	path := filepath.Join(state, name)
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return platform.ErrStateUnsafe
	}
	if err = upgradeIdentity(db); err != nil {
		return err
	}
	if err = upgradeText(db); err != nil {
		return err
	}
	entry := corpus.Entry{Path: path, Relative: project + ".md", Source: filepath.Join(state, "notes"), Project: project, Kind: "note", Namespace: "notes", Size: info.Size(), MtimeNS: info.ModTime().UnixNano()}
	items, _, _, err := changes(db, map[string]corpus.Entry{path: entry}, true)
	if err != nil {
		return err
	}
	batch := []work{}
	for _, item := range items {
		batch = append(batch, read(item, state, config, false))
	}
	_, err = apply(db, batch, nil, float64(time.Now().UnixNano())/1e9, 0)
	if err == nil {
		err = setPrivacy(db, config.Privacy())
	}
	return err
}

type Run struct {
	StartedAt         time.Time `json:"started_at"`
	FinishedAt        time.Time `json:"finished_at"`
	ConfigFingerprint string    `json:"config_fingerprint"`
	Outcome           string    `json:"outcome"`
}

func Fingerprint(config corpus.Config) string {
	config.NextSourceID = 0
	config.Sources = append([]corpus.Source{}, config.Sources...)
	for i := range config.Sources {
		if config.Sources[i].Kind == "note" {
			config.Sources[i].Path = "notes"
		}
		if config.Sources[i].Kind != "note" && config.Sources[i].SourceID == nil {
			id := uint64(i)
			config.Sources[i].SourceID = &id
		}
	}
	data, _ := json.Marshal(config)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func SaveRun(state string, run Run) error {
	data, err := json.Marshal(run)
	if err != nil {
		return err
	}
	return platform.WriteState(state, "index-run.json", append(data, '\n'))
}

func LastRun(state string) (Run, bool, error) {
	data, err := platform.ReadPrivateFile(state, "index-run.json", 65536)
	if errors.Is(err, os.ErrNotExist) {
		return Run{}, false, nil
	}
	if err != nil {
		return Run{}, false, err
	}
	var run Run
	err = json.Unmarshal(data, &run)
	if err == nil && (run.StartedAt.IsZero() || run.Outcome != "running" && run.Outcome != "failed" && run.Outcome != "complete") {
		err = ErrMissing
	}
	return run, true, err
}
