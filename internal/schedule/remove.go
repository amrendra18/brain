package schedule

import (
	"brain/internal/platform"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
)

// RemoveOwned unregisters only the saved job and removes its verified artifacts.
// The receipt survives interruptions until both the native job and files are gone.
func RemoveOwned(p Plan, runner Runner) (bool, error) {
	if err := validate(p); err != nil {
		return false, err
	}
	if p.System != runtime.GOOS && runner == nil {
		return false, ErrUnsupported
	}
	previous, err := readReceipt(p)
	if err != nil {
		return false, err
	}
	if _, err := os.Lstat(p.Spec.State); errors.Is(err, os.ErrNotExist) {
		return removalAbsent(p, runner)
	}
	lock, err := platform.Acquire(p.Spec.State, "schedule", false)
	if errors.Is(err, platform.ErrLocked) {
		return false, ErrBusy
	}
	if err != nil {
		return false, ErrFiles
	}
	defer lock.Close()
	previous, err = readReceipt(p)
	if err != nil {
		return false, err
	}
	if previous.Outcome == "" {
		return removalAbsent(p, runner)
	}
	if !reflect.DeepEqual(previous.Spec, p.Spec) {
		return false, ErrCollision
	}
	artifacts, err := checkArtifacts(p, previous)
	if err != nil {
		return false, err
	}
	observed, err := inspectNative(p, runner)
	if err != nil {
		return false, err
	}
	if observed.Registered && (!observed.owned || !observed.matches) {
		return false, ErrCollision
	}
	if err := unchangedArtifacts(p, artifacts); err != nil {
		return false, err
	}
	current, err := readReceipt(p)
	if err != nil || !reflect.DeepEqual(previous, current) {
		return false, ErrCollision
	}
	previous.Outcome = removing
	previous.Problem = ""
	if err := saveReceipt(p, previous); err != nil {
		return false, err
	}
	for _, action := range removalActions(p, observed) {
		result, err := run(runner, action)
		if err != nil || result.ExitCode != 0 {
			return false, ErrActivation
		}
	}
	if p.System != "linux" {
		after, err := inspectNative(p, runner)
		if err != nil || after.Registered {
			return false, ErrActivation
		}
	}
	for _, artifact := range p.Artifacts {
		if artifacts.Files[artifact.Path] == nil {
			continue
		}
		root, name, err := artifactLocation(p, artifact)
		if err != nil {
			return false, err
		}
		if err = removeVerified(root, name, artifacts.Files[artifact.Path], artifacts.Hashes[artifact.Path]); err != nil {
			return false, err
		}
	}
	if p.System == "linux" {
		result, err := run(runner, invocation("systemctl", "--user", "daemon-reload"))
		if err != nil || result.ExitCode != 0 {
			return false, ErrActivation
		}
		after, err := inspectNative(p, runner)
		if err != nil || after.Registered {
			return false, ErrActivation
		}
	}
	info, err := os.Lstat(filepath.Join(p.Spec.State, receiptName))
	if err != nil {
		return false, ErrFiles
	}
	data, err := platform.ReadPrivateFile(p.Spec.State, receiptName, processLimit)
	if err != nil {
		return false, ErrFiles
	}
	current, err = decodeReceipt(p.Spec.State, data)
	if err != nil || !reflect.DeepEqual(previous, current) {
		return false, ErrCollision
	}
	if err := removeVerified(p.Spec.State, receiptName, info, digest(string(data))); err != nil {
		return false, err
	}
	return true, nil
}

func removalAbsent(p Plan, runner Runner) (bool, error) {
	if _, err := checkArtifacts(p, receipt{}); err != nil {
		return false, err
	}
	observed, err := inspectNative(p, runner)
	if err != nil {
		return false, err
	}
	if observed.Registered {
		return false, ErrCollision
	}
	return false, nil
}

func removalActions(p Plan, observed observation) []Invocation {
	if !observed.Registered {
		return nil
	}
	switch p.System {
	case "darwin":
		return []Invocation{p.Actions[0]}
	case "linux":
		var actions []Invocation
		if observed.units&timerUnit != 0 {
			actions = append(actions, invocation("systemctl", "--user", "disable", "--now", p.ID+".timer"))
		}
		if observed.units&serviceUnit != 0 {
			actions = append(actions, invocation("systemctl", "--user", "stop", p.ID+".service"))
		}
		return actions
	default:
		var actions []Invocation
		if observed.Running {
			actions = append(actions, invocation("schtasks.exe", "/End", "/TN", p.ID))
		}
		return append(actions, invocation("schtasks.exe", "/Delete", "/TN", p.ID, "/F"))
	}
}

func unchangedArtifacts(p Plan, before artifactState) error {
	for _, artifact := range p.Artifacts {
		root, name, err := artifactLocation(p, artifact)
		if err != nil {
			return err
		}
		if err := verifyRemoval(root, name, before.Files[artifact.Path], before.Hashes[artifact.Path]); err != nil {
			return err
		}
	}
	return nil
}

func verifyRemoval(root, name string, before os.FileInfo, hash string) error {
	data, err := platform.ReadPrivateFile(root, name, processLimit)
	if before == nil && errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return ErrFiles
	}
	info, err := os.Lstat(filepath.Join(root, name))
	if err != nil || before == nil || !os.SameFile(before, info) || digest(string(data)) != hash {
		return ErrCollision
	}
	return nil
}

func removeVerified(folder, name string, before os.FileInfo, hash string) error {
	if err := verifyRemoval(folder, name, before, hash); err != nil {
		return err
	}
	root, err := os.OpenRoot(folder)
	if err != nil {
		return ErrFiles
	}
	defer root.Close()
	if err := platform.SafeStatePath(folder, name); err != nil {
		return ErrFiles
	}
	info, err := root.Lstat(name)
	if err != nil || !os.SameFile(before, info) {
		return ErrCollision
	}
	if err := root.Remove(name); err != nil {
		return ErrFiles
	}
	if runtime.GOOS != "windows" {
		parent, err := root.Open(filepath.Dir(name))
		if err != nil {
			return ErrFiles
		}
		defer parent.Close()
		if err := parent.Sync(); err != nil {
			return ErrFiles
		}
	}
	return nil
}
