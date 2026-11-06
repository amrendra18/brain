package schedule

import (
	"brain/internal/platform"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
)

const receiptName = "schedule.json"

type operation string

const removing operation = "removing"

type receipt struct {
	ID      string            `json:"id"`
	System  string            `json:"system"`
	Spec    Spec              `json:"spec"`
	Owned   map[string]string `json:"owned"`
	Outcome operation         `json:"outcome"`
	Problem string            `json:"problem,omitempty"`
	Status  Status            `json:"status"`
}

// Current discovers the saved schedule without contacting a scheduler or creating state.
func Current(state string) (Plan, bool, error) {
	r, err := receiptAt(state)
	if err != nil || r.Outcome == "" {
		return Plan{}, false, err
	}
	p, err := planForReceipt(r)
	return p, err == nil, err
}

func planForReceipt(r receipt) (Plan, error) {
	return Preview(r.System, r.Spec)
}

func readReceipt(p Plan) (receipt, error) {
	r, err := receiptAt(p.Spec.State)
	if err == nil && r.Outcome != "" && (r.ID != p.ID || r.System != p.System || r.Spec.Home != p.Spec.Home) {
		return receipt{}, ErrCollision
	}
	return r, err
}

func receiptAt(state string) (receipt, error) {
	if err := platform.CheckPrivateState(state); errors.Is(err, os.ErrNotExist) {
		return receipt{}, nil
	} else if err != nil {
		return receipt{}, ErrFiles
	}
	data, err := platform.ReadPrivateFile(state, receiptName, processLimit)
	if errors.Is(err, os.ErrNotExist) {
		return receipt{}, nil
	}
	if err != nil {
		return receipt{}, ErrFiles
	}
	return decodeReceipt(state, data)
}

func decodeReceipt(state string, data []byte) (receipt, error) {
	var r receipt
	if json.Unmarshal(data, &r) != nil || r.Spec.State != state {
		return receipt{}, ErrCollision
	}
	if r.Outcome != "prepared" && r.Outcome != "installed" && r.Outcome != "failed" && r.Outcome != removing {
		return receipt{}, ErrFiles
	}
	plan, err := planForReceipt(r)
	if err != nil || plan.ID != r.ID {
		return receipt{}, ErrFiles
	}
	for file, digest := range r.Owned {
		valid := false
		for _, artifact := range plan.Artifacts {
			valid = valid || file == artifact.Path
		}
		decoded, err := hex.DecodeString(digest)
		if !valid || err != nil || len(decoded) != sha256.Size {
			return receipt{}, ErrFiles
		}
	}
	return r, nil
}

func saveReceipt(p Plan, r receipt) error {
	data, err := json.Marshal(r)
	if err != nil || len(data) >= processLimit || platform.WriteState(p.Spec.State, receiptName, append(data, '\n')) != nil {
		return ErrFiles
	}
	return nil
}

// Install reconciles only this state's owned files and native job.
func Install(p Plan, runner Runner) (bool, error) {
	if err := validate(p); err != nil {
		return false, err
	}
	if p.System != runtime.GOOS && runner == nil {
		return false, ErrUnsupported
	}
	if err := platform.PrepareState(p.Spec.State); err != nil {
		return false, ErrFiles
	}
	lock, err := platform.Acquire(p.Spec.State, "schedule", false)
	if errors.Is(err, platform.ErrLocked) {
		return false, ErrBusy
	}
	if err != nil {
		return false, ErrFiles
	}
	defer lock.Close()
	previous, err := readReceipt(p)
	if err != nil {
		return false, err
	}
	if previous.Outcome == removing {
		return false, ErrBusy
	}
	artifacts, err := checkArtifacts(p, previous)
	if err != nil {
		return false, err
	}
	observed, err := inspectNative(p, runner)
	if err == nil && observed.Registered && (previous.Outcome == "" || !observed.owned) {
		return false, ErrCollision
	}
	if err == nil && artifacts.Identical && previous.Outcome == "installed" && reflect.DeepEqual(previous.Spec, p.Spec) && observed.Healthy {
		return false, nil
	}
	r := receipt{ID: p.ID, System: p.System, Spec: p.Spec, Outcome: "prepared", Owned: artifacts.Hashes}
	if problem := saveReceipt(p, r); problem != nil {
		return false, problem
	}
	fail := func(problem error) (bool, error) {
		r.Outcome = "failed"
		r.Problem = failureCode(problem)
		if saveReceipt(p, r) != nil {
			return false, ErrFiles
		}
		return false, problem
	}
	if err != nil {
		return fail(err)
	}
	for _, artifact := range p.Artifacts {
		root, name, err := artifactLocation(p, artifact)
		if err != nil {
			return fail(err)
		}
		if err = makeParents(root, name); err != nil {
			return fail(err)
		}
		if err = platform.WritePrivateFileIfUnchanged(root, name, []byte(artifact.Data), artifacts.Files[artifact.Path], artifacts.Hashes[artifact.Path]); errors.Is(err, platform.ErrFileChanged) {
			return fail(ErrCollision)
		} else if err != nil {
			return fail(ErrFiles)
		}
	}
	for _, call := range p.Actions {
		if call.When == "registered" && !observed.Registered {
			continue
		}
		result, err := run(runner, call)
		if err != nil || result.ExitCode != 0 {
			return fail(ErrActivation)
		}
	}
	observed, err = inspectNative(p, runner)
	if err != nil || !observed.Registered || !observed.Enabled || !observed.owned || !observed.matches {
		return fail(ErrActivation)
	}
	r.Outcome = "installed"
	r.Owned = nil
	r.Status = observed.Status
	r.Status.Installation = "installed"
	if err = saveReceipt(p, r); err != nil {
		return false, err
	}
	return true, nil
}

func artifactLocation(p Plan, artifact Artifact) (string, string, error) {
	root := p.Spec.Home
	if p.System == "windows" {
		root = p.Spec.State
	}
	name, err := filepath.Rel(root, artifact.Path)
	if err != nil || !filepath.IsLocal(name) || platform.SafeStatePath(root, name) != nil {
		return "", "", ErrFiles
	}
	return root, name, nil
}

func makeParents(folder, name string) error {
	if platform.SafeStatePath(folder, name) != nil {
		return ErrFiles
	}
	root, err := os.OpenRoot(folder)
	if err != nil {
		return ErrFiles
	}
	defer root.Close()
	if err = root.MkdirAll(filepath.Dir(name), 0700); err != nil {
		return ErrFiles
	}
	return nil
}

func digest(data string) string {
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}

type artifactState struct {
	Identical bool
	Hashes    map[string]string
	Files     map[string]os.FileInfo
}

func checkArtifacts(p Plan, r receipt) (artifactState, error) {
	state := artifactState{Identical: true, Hashes: map[string]string{}, Files: map[string]os.FileInfo{}}
	allowed := map[string][]string{}
	if r.Outcome != "" {
		old, err := planForReceipt(r)
		if err != nil {
			return state, ErrFiles
		}
		for _, a := range old.Artifacts {
			allowed[a.Path] = []string{digest(a.Data), r.Owned[a.Path]}
		}
	}
	for _, a := range p.Artifacts {
		root, name, err := artifactLocation(p, a)
		if err != nil {
			return state, err
		}
		info, infoErr := os.Lstat(a.Path)
		if infoErr != nil && !errors.Is(infoErr, os.ErrNotExist) {
			return state, ErrFiles
		}
		data, err := platform.ReadPrivateFile(root, name, processLimit)
		if errors.Is(err, os.ErrNotExist) {
			state.Identical = false
			continue
		}
		if err != nil || infoErr != nil {
			return state, ErrFiles
		}
		owned := false
		hash := digest(string(data))
		for _, previous := range allowed[a.Path] {
			owned = owned || hash == previous
		}
		if !owned {
			return state, ErrCollision
		}
		state.Hashes[a.Path] = hash
		state.Files[a.Path] = info
		state.Identical = state.Identical && string(data) == a.Data
	}
	return state, nil
}
