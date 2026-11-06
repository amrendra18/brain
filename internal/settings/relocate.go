package settings

import (
	"brain/internal/corpus"
	"brain/internal/index"
	"brain/internal/platform"
	"brain/internal/redact"
	"brain/internal/schedule"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type Phase string

const (
	Prepared  Phase = "prepared"
	Copied    Phase = "copied"
	Indexed   Phase = "indexed"
	Scheduled Phase = "scheduled"
	Published Phase = "published"
	Complete  Phase = "complete"
)

type Relocation struct {
	Version     int            `json:"version"`
	ID          string         `json:"id"`
	Source      Scope          `json:"source"`
	Destination Scope          `json:"destination"`
	Phase       Phase          `json:"phase"`
	Schedule    *schedule.Plan `json:"schedule,omitempty"`
}
type Hooks struct {
	After    func(Phase) error
	Schedule func(schedule.Plan, Scope) error
}

func contained(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && (relative == "." || filepath.IsLocal(relative))
}
func safeDestination(source, destination, root string) error {
	if overlappingPaths(root, source) || overlappingPaths(root, destination) || overlappingPaths(source, destination) {
		return ErrInvalid
	}
	canonical, err := filepath.EvalSymlinks(source)
	if err != nil {
		return err
	}
	if !validPath(destination) || contained(canonical, destination) || contained(destination, canonical) || contained(source, destination) || contained(destination, source) || contained(destination, root) || contained(root, destination) {
		return ErrInvalid
	}
	for parent := destination; ; parent = filepath.Dir(parent) {
		info, err := os.Lstat(parent)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.IsDir() {
			return ErrInvalid
		}
		real, err := filepath.EvalSymlinks(parent)
		if err != nil || real != parent {
			return ErrInvalid
		}
		break
	}
	return nil
}
func ReadRelocation(root string) (Relocation, bool, error) {
	data, err := platform.ReadPrivateFile(root, "relocation.json", 65536)
	if errors.Is(err, os.ErrNotExist) {
		return Relocation{}, false, nil
	}
	var tx Relocation
	if err != nil || decode(data, &tx) != nil || tx.Version != 1 || tx.ID == "" || !Selected(tx.Source).valid() || !Selected(tx.Destination).valid() {
		return tx, false, ErrInvalid
	}
	switch tx.Phase {
	case Prepared, Copied, Indexed, Scheduled, Published, Complete:
	default:
		return tx, false, ErrInvalid
	}
	return tx, true, nil
}
func saveRelocation(root string, tx Relocation) error {
	data, err := json.Marshal(tx)
	if err != nil {
		return err
	}
	return platform.WriteState(root, "relocation.json", append(data, '\n'))
}

type stateMarker struct {
	ID          string      `json:"id"`
	Destination bool        `json:"destination"`
	Transaction *Relocation `json:"transaction,omitempty"`
}

func marker(state, id string, destination bool) error {
	data, _ := json.Marshal(stateMarker{ID: id, Destination: destination})
	return platform.WriteState(state, "relocation.json", data)
}

func freeze(tx Relocation) error {
	data, err := json.Marshal(stateMarker{ID: tx.ID, Transaction: &tx})
	if err != nil {
		return err
	}
	return platform.WriteState(tx.Source.State, "relocation.json", data)
}

func initialRelocation(source Scope, destination Scope) (Relocation, bool, error) {
	data, err := platform.ReadPrivateFile(source.State, "relocation.json", 65536)
	if errors.Is(err, os.ErrNotExist) {
		return Relocation{}, false, nil
	}
	var saved stateMarker
	if err != nil || decode(data, &saved) != nil || saved.Destination || saved.Transaction == nil {
		return Relocation{}, false, ErrPending
	}
	tx := *saved.Transaction
	if tx.Version != 1 || tx.ID == "" || tx.ID != saved.ID || tx.Phase != Prepared || tx.Source != source || tx.Destination != destination {
		return Relocation{}, false, ErrPending
	}
	return tx, true, nil
}
func ownedDestination(state, id string) bool {
	data, err := platform.ReadPrivateFile(state, "relocation.json", 65536)
	var saved stateMarker
	return err == nil && decode(data, &saved) == nil && saved.ID == id
}
func ignored(name string) bool {
	switch name {
	case "locks", "index.sqlite", "index.sqlite-wal", "index.sqlite-shm", "index.sqlite-journal", "schedule.json", "schedule-task.xml", "relocation.json":
		return true
	}
	return false
}
func copyState(source, destination string, write bool, policy *redact.Policy) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == source {
			return nil
		}
		name, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if policy.Match(name) {
			return index.ErrPrivacyPath
		}
		first := strings.Split(name, string(filepath.Separator))[0]
		if ignored(first) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if first == "privacy-migration.json" || first == ".privacy-stage" {
			return ErrPending
		}
		if entry.IsDir() {
			if err = platform.CheckPrivateState(path); err != nil {
				return err
			}
			if write {
				return os.MkdirAll(filepath.Join(destination, name), 0700)
			}
			return nil
		}
		data, err := platform.ReadPrivateFile(source, name, 64<<20)
		if err != nil {
			return err
		}
		if policy.Match(string(data)) {
			return index.ErrPrivacyRefresh
		}
		if write {
			return platform.WriteState(destination, name, data)
		}
		return nil
	})
}
func PreviewRelocation(root string, source Scope, destination string) (Relocation, error) {
	return previewRelocation(root, source, destination, false)
}
func previewRelocation(root string, source Scope, destination string, locked bool) (Relocation, error) {
	destination = filepath.Clean(destination)
	if err := safeDestination(source.State, destination, root); err != nil {
		return Relocation{}, err
	}
	if err := platform.CheckPrivateState(source.State); err != nil {
		return Relocation{}, err
	}
	config, err := corpus.LoadFile(source.Config, source.State)
	if err != nil {
		return Relocation{}, err
	}
	checkPrivacy := index.PrivacyRequiredStored
	if locked {
		checkPrivacy = func(state string, policy *redact.Policy) (bool, error) {
			return index.PrivacyRequiredForIndex(state, policy, true)
		}
	}
	required, privacyErr := checkPrivacy(source.State, config.Privacy())
	if privacyErr != nil {
		return Relocation{}, privacyErr
	}
	if required {
		return Relocation{}, index.ErrPrivacyRefresh
	}
	if config.Privacy().Match(destination) {
		return Relocation{}, ErrInvalid
	}
	if config.PrivateValuesFile != "" && contained(destination, config.PrivateValuesFile) {
		return Relocation{}, ErrInvalid
	}
	if contained(source.State, source.Config) && source.Config != filepath.Join(source.State, "config.json") {
		return Relocation{}, ErrInvalid
	}
	target := Scope{State: destination, Config: source.Config}
	if source.Config == filepath.Join(source.State, "config.json") {
		target.Config = filepath.Join(destination, "config.json")
	}
	tx, exists, err := ReadRelocation(root)
	if err != nil {
		return tx, err
	}
	if exists && tx.Phase != Complete {
		if tx.Source != source || tx.Destination != target {
			return tx, ErrPending
		}
		return tx, nil
	}
	if !exists {
		if initial, found, problem := initialRelocation(source, target); problem != nil {
			return Relocation{}, problem
		} else if found {
			return initial, nil
		}
	}
	saved, registered, problem := Read(root)
	if problem != nil {
		return Relocation{}, problem
	}
	if registered && saved.Resolved() != source {
		return Relocation{}, ErrDifferent
	}
	if err = CheckMutation(source.State); err != nil {
		return Relocation{}, err
	}
	entries, err := os.ReadDir(destination)
	if err == nil {
		if problem := platform.CheckPrivateState(destination); problem != nil {
			return Relocation{}, problem
		}
	}
	if err == nil && len(entries) > 0 {
		return Relocation{}, ErrDifferent
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Relocation{}, err
	}
	if err = copyState(source.State, destination, false, config.Privacy()); err != nil {
		return Relocation{}, err
	}
	plan, has, err := schedule.Current(source.State)
	if err != nil {
		return Relocation{}, err
	}
	if has && (plan.Spec.State != source.State || plan.Spec.Config != source.Config) {
		return Relocation{}, ErrDifferent
	}
	tx = Relocation{Version: 1, ID: rand.Text(), Source: source, Destination: target, Phase: Prepared}
	if has {
		tx.Schedule = &plan
	}
	return tx, nil
}
func Relocate(root string, source Scope, destination string, hooks Hooks) (Relocation, error) {
	if overlappingPaths(root, source.State) || overlappingPaths(root, destination) {
		return Relocation{}, ErrInvalid
	}
	if err := platform.PrepareState(root); err != nil {
		return Relocation{}, err
	}
	owner, err := platform.Acquire(root, "settings", true)
	if err != nil {
		return Relocation{}, err
	}
	defer owner.Close()
	existing, has, err := ReadRelocation(root)
	if err != nil {
		return existing, err
	}
	if has && existing.Destination.State == destination && (source == existing.Source || source == existing.Destination) {
		if existing.Phase == Complete {
			return existing, nil
		}
		source = existing.Source
	}
	lease, err := platform.StateLease(source.State, true, true)
	if err != nil {
		return Relocation{}, err
	}
	defer lease.Close()
	legacy := []*platform.Lock{}
	defer func() {
		for i := len(legacy) - 1; i >= 0; i-- {
			legacy[i].Close()
		}
	}()
	privacy, err := platform.PrivacyLock(source.State, true, true)
	if err != nil {
		return Relocation{}, err
	}
	legacy = append(legacy, privacy)
	for _, name := range []string{"index", "notes"} {
		lock, err := platform.Acquire(source.State, name, true)
		if err != nil {
			return Relocation{}, err
		}
		legacy = append(legacy, lock)
	}
	configLock, err := platform.AcquireConfig(source.Config, true)
	if err != nil {
		return Relocation{}, err
	}
	legacy = append(legacy, configLock)
	scheduleLock, err := platform.Acquire(source.State, "schedule", true)
	if err != nil {
		return Relocation{}, err
	}
	tx, err := previewRelocation(root, source, destination, true)
	if err != nil {
		scheduleLock.Close()
		return tx, err
	}
	defer func() {
		if scheduleLock != nil {
			scheduleLock.Close()
		}
	}()
	advance := func(phase Phase) error {
		tx.Phase = phase
		if err := saveRelocation(root, tx); err != nil {
			return err
		}
		if hooks.After != nil {
			return hooks.After(phase)
		}
		return nil
	}
	selected, selectedExists, err := Read(root)
	if err != nil {
		return tx, err
	}
	published := selectedExists && selected.Resolved() == tx.Destination
	if published && tx.Phase != Published && tx.Phase != Complete {
		if err = advance(Published); err != nil {
			return tx, err
		}
	} else if !published && has && (tx.Phase == Copied || tx.Phase == Indexed || tx.Phase == Scheduled) {
		if err = os.Chmod(source.State, 0500); err != nil {
			return tx, err
		}
		if err = advance(Prepared); err != nil {
			return tx, err
		}
	}
	if tx.Phase == Prepared {
		if !ownedDestination(source.State, tx.ID) {
			if err = freeze(tx); err != nil {
				return tx, err
			}
		}
		if err = os.Chmod(source.State, 0500); err != nil {
			return tx, err
		}
		if err = advance(Prepared); err != nil {
			return tx, err
		}
		if !ownedDestination(destination, tx.ID) {
			entries, problem := os.ReadDir(destination)
			if problem == nil && len(entries) > 0 {
				return tx, ErrDifferent
			}
			if problem != nil && !errors.Is(problem, os.ErrNotExist) {
				return tx, problem
			}
			if err = platform.PrepareState(destination); err != nil {
				return tx, err
			}
			if err = marker(destination, tx.ID, true); err != nil {
				return tx, err
			}
		}
	}
	if tx.Phase != Published && !ownedDestination(destination, tx.ID) {
		return tx, ErrInvalid
	}
	targetLease, err := platform.StateLease(destination, true, true)
	if err != nil {
		return tx, err
	}
	defer targetLease.Close()
	if tx.Phase == Prepared {
		config, err := corpus.LoadFile(source.Config, source.State)
		if err != nil {
			return tx, err
		}
		if err = copyState(source.State, destination, true, config.Privacy()); err != nil {
			return tx, err
		}
		if err = advance(Copied); err != nil {
			return tx, err
		}
	}
	if tx.Phase == Copied {
		if err = rebuild(tx.Destination); err != nil {
			return tx, err
		}
		if err = advance(Indexed); err != nil {
			return tx, err
		}
	}
	if tx.Phase == Indexed {
		scheduleLock.Close()
		scheduleLock = nil
		if tx.Schedule != nil {
			if hooks.Schedule == nil {
				return tx, ErrPending
			}
			if err = transitionSchedule(tx, hooks.Schedule); err != nil {
				return tx, err
			}
		}
		if err = advance(Scheduled); err != nil {
			return tx, err
		}
	}
	if tx.Phase == Scheduled {
		if err = os.Chmod(source.State, 0500); err != nil {
			return tx, err
		}
		if err = Save(root, Selected(tx.Destination)); err != nil {
			return tx, err
		}
		if err = advance(Published); err != nil {
			return tx, err
		}
	}
	if tx.Phase == Published {
		selected, ok, problem := Read(root)
		if problem != nil || !ok || selected.Resolved() != tx.Destination {
			return tx, ErrInvalid
		}
		if err = os.Remove(filepath.Join(destination, "relocation.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
			return tx, err
		}
		if err = advance(Complete); err != nil {
			return tx, err
		}
	}
	return tx, nil
}

func transitionSchedule(tx Relocation, apply func(schedule.Plan, Scope) error) (err error) {
	if err = os.Chmod(tx.Source.State, 0700); err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.Chmod(tx.Source.State, 0500)) }()
	return apply(*tx.Schedule, tx.Destination)
}

func rebuild(scope Scope) error {
	config, err := corpus.LoadFile(scope.Config, scope.State)
	if err != nil {
		return err
	}
	db, err := index.Open(scope.State, true, true, false)
	if err != nil {
		return err
	}
	defer db.Close()
	report, err := index.Refresh(db, scope.State, config, true, 0)
	if err != nil {
		return err
	}
	if len(report.Skipped) > 0 {
		return corpus.ErrUnreadable
	}
	var integrity string
	if err = db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil {
		return err
	}
	if integrity != "ok" {
		return ErrInvalid
	}
	_, complete, err := index.ReadTime(db)
	if err != nil {
		return err
	}
	if !complete {
		return ErrInvalid
	}
	return index.CheckPrivacy(db, config.Privacy())
}
