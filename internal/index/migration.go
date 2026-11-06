package index

import (
	"brain/internal/corpus"
	"brain/internal/notes"
	"brain/internal/platform"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type migration struct {
	Version int    `json:"version"`
	Policy  string `json:"policy"`
}

func MigrateLocked(state string, config corpus.Config) (Report, error) {
	return migrateLocked(state, config, func(string) error { return nil })
}

func migrateLocked(state string, config corpus.Config, boundary func(string) error) (Report, error) {
	report := Report{Skipped: []corpus.Skipped{}}
	if err := platform.CheckPrivateState(state); err != nil {
		return report, err
	}
	if err := CheckSchedulePrivacy(state, config.Privacy()); err != nil {
		return report, err
	}
	scheduleLock, err := platform.Acquire(state, "schedule", true)
	if err != nil {
		return report, err
	}
	defer scheduleLock.Close()
	if err := CheckSchedulePrivacy(state, config.Privacy()); err != nil {
		return report, err
	}
	lock, err := platform.Acquire(state, "notes", true)
	if err != nil {
		return report, err
	}
	defer lock.Close()
	root, err := os.OpenRoot(state)
	if err != nil {
		return report, err
	}
	defer root.Close()
	for _, name := range append([]string{privacyStage, "converted", "notes"}, sqliteFiles...) {
		if err = platform.SafeStatePath(state, name); err != nil {
			return report, err
		}
	}
	marker, err := platform.ReadPrivateFile(state, privacyMarker, 65536)
	if err == nil {
		var prior migration
		if json.Unmarshal(marker, &prior) != nil || prior.Version != 1 || prior.Policy == "" {
			return report, ErrPrivacyRefresh
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return report, err
	} else {
		if _, problem := root.Lstat(privacyStage); !errors.Is(problem, os.ErrNotExist) {
			return report, platform.ErrStateUnsafe
		}
	}
	encoded, _ := json.Marshal(migration{1, config.Privacy().Fingerprint()})
	if err = platform.WriteState(state, privacyMarker, encoded); err != nil {
		return report, err
	}
	if err = boundary("marked"); err != nil {
		return report, err
	}
	if err = root.RemoveAll(privacyStage); err != nil {
		return report, err
	}
	if err = root.Mkdir(privacyStage, 0700); err != nil {
		return report, err
	}
	stage := filepath.Join(state, privacyStage)
	if err = stageNotes(state, stage, config); err != nil {
		return report, err
	}
	if err = boundary("notes-staged"); err != nil {
		return report, err
	}
	db, err := Open(stage, true, false, false)
	if err != nil {
		return report, err
	}
	config.AllowUnavailableRoots = true
	report, err = RefreshLocked(db, stage, config, true)
	if err == nil {
		var status string
		err = db.QueryRow("PRAGMA quick_check").Scan(&status)
		if err == nil && status != "ok" {
			err = ErrMissing
		}
	}
	if err == nil {
		_, err = db.Exec(FTSCheck)
	}
	if err == nil {
		_, err = db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	}
	closed := db.Close()
	if err == nil {
		err = closed
	}
	if err != nil {
		return report, err
	}
	if err = boundary("database-staged"); err != nil {
		return report, err
	}
	entries, err := os.ReadDir(filepath.Join(stage, "notes"))
	if err != nil {
		return report, err
	}
	for _, entry := range entries {
		name := filepath.Join("notes", entry.Name())
		data, err := platform.ReadPrivateFile(stage, name, int64(^uint64(0)>>1))
		if err != nil {
			return report, err
		}
		if err = platform.WriteState(state, name, data); err != nil {
			return report, err
		}
		if err = boundary("note-published"); err != nil {
			return report, err
		}
	}
	if err = boundary("notes-published"); err != nil {
		return report, err
	}
	if err = root.RemoveAll("converted"); err != nil {
		return report, err
	}
	if err = boundary("cache-removed"); err != nil {
		return report, err
	}
	if _, err = root.Stat(filepath.Join(privacyStage, "converted")); err == nil {
		if err = root.Rename(filepath.Join(privacyStage, "converted"), "converted"); err != nil {
			return report, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return report, err
	}
	if err = boundary("cache-published"); err != nil {
		return report, err
	}
	for _, name := range sqliteFiles {
		if err = root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			return report, err
		}
		if err = boundary("old-" + name + "-removed"); err != nil {
			return report, err
		}
	}
	if err = root.Rename(filepath.Join(privacyStage, "index.sqlite"), "index.sqlite"); err != nil {
		return report, err
	}
	if err = boundary("database-published"); err != nil {
		return report, err
	}
	if err = root.RemoveAll(privacyStage); err != nil {
		return report, err
	}
	if err = boundary("stage-removed"); err != nil {
		return report, err
	}
	if err = root.Remove(privacyMarker); err != nil {
		return report, err
	}
	return report, nil
}

func stageNotes(state, stage string, config corpus.Config) error {
	if err := os.MkdirAll(filepath.Join(stage, "notes"), 0700); err != nil {
		return err
	}
	entries, err := os.ReadDir(filepath.Join(state, "notes"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !notes.Project(strings.TrimSuffix(name, filepath.Ext(name))) || filepath.Ext(name) != ".md" || config.Privacy().Match(name) {
			return ErrPrivacyPath
		}
		data, err := platform.ReadPrivateFile(state, filepath.Join("notes", name), int64(^uint64(0)>>1))
		if err != nil {
			return err
		}
		text := config.Privacy().Apply(string(data)).Text
		if err = platform.WriteState(stage, filepath.Join("notes", name), []byte(text)); err != nil {
			return err
		}
	}
	return nil
}
