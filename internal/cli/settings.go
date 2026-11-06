package cli

import (
	"brain/internal/corpus"
	"brain/internal/index"
	"brain/internal/platform"
	"brain/internal/redact"
	"brain/internal/schedule"
	"brain/internal/settings"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func (c *call) beginMutation() error {
	mutate := c.command == "init" || c.command == "index" || c.command == "note" || c.command == "doctor"
	if c.command == "setup" || c.command == "schedule" {
		mutate = c.flags["--write"] == "true"
	}
	if c.command == "sources" || c.command == "exclude" || c.command == "third-party" {
		mutate = len(c.words) > 0 && c.words[0] != "list"
	}
	if !mutate {
		return nil
	}
	if c.command == "doctor" {
		if remote, _ := platform.RemoteFolder(c.state); remote {
			return nil
		}
		if err := platform.CheckPrivateState(c.state); err != nil {
			return nil
		}
		if err := settings.CheckMutation(c.state); err != nil {
			return err
		}
		if info, err := os.Stat(c.state); err != nil || info.Mode().Perm()&0222 == 0 {
			return nil
		}
	}
	if c.command == "init" {
		if err := platform.PrepareState(c.state); err != nil {
			return err
		}
	}
	lease, err := platform.StateLease(c.state, false, true)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	c.mutationLease = lease
	if err = settings.CheckMutation(c.state); err != nil {
		c.endMutation()
		return err
	}
	return nil
}
func (c *call) endMutation() {
	if c.mutationLease != nil {
		c.mutationLease.Close()
		c.mutationLease = nil
	}
}
func (c *call) settingsFailure(err error) int {
	if errors.Is(err, index.ErrPrivacyRefresh) || errors.Is(err, index.ErrPrivacyPath) || errors.Is(err, redact.ErrPolicyInvalid) {
		return c.privacyFailure(err)
	}
	if errors.Is(err, settings.ErrScope) {
		return c.usage("invalid-value", "Choose an absolute local state folder and a nonempty config filename.")
	}
	if errors.Is(err, os.ErrPermission) {
		return c.unwritable()
	}
	if errors.Is(err, platform.ErrStateUnsafe) {
		return c.unsafeState()
	}
	if errors.Is(err, platform.ErrLocked) {
		return c.failure("state-busy", "Another command is changing this state. Retry when it finishes.", 1, c.again())
	}
	if errors.Is(err, settings.ErrPending) {
		return c.failure("relocation-pending", "This state has moved or its move needs recovery. Retry the original settings relocate command, or use the saved selection.", 1, []string{"help", "settings"})
	}
	if errors.Is(err, settings.ErrDifferent) {
		return c.failure("selection-conflict", "An existing selection or owned job uses different settings. Use settings relocate to move the existing selection.", 1, []string{"help", "settings"})
	}
	return c.failure("settings-invalid", "The selected settings cannot be read or changed safely. Repair settings permissions or retry the original relocation command.", 1, []string{"help", "settings"})
}
func (c *call) settings() int {
	if c.settingsRoot == "" {
		return c.settingsFailure(settings.ErrInvalid)
	}
	scope := settings.Scope{State: c.state, Config: c.config}
	if len(c.words) > 0 {
		if len(c.words) != 2 || c.words[0] != "relocate" {
			return c.usage("invalid-value", "Use settings or settings relocate DEST.")
		}
		destination, err := filepath.Abs(c.words[1])
		if err != nil {
			return c.settingsFailure(err)
		}
		var tx settings.Relocation
		if c.flags["--write"] == "true" {
			tx, err = settings.Relocate(c.settingsRoot, scope, destination, settings.Hooks{Schedule: relocateSchedule})
		} else {
			tx, err = settings.PreviewRelocation(c.settingsRoot, scope, destination)
		}
		if err != nil {
			return c.settingsFailure(err)
		}
		return WriteJSON(c.output, struct {
			Relocation settings.Relocation `json:"relocation"`
			Written    bool                `json:"written"`
		}{tx, c.flags["--write"] == "true"})
	}
	selected, exists, err := settings.Read(c.settingsRoot)
	if err != nil {
		return c.settingsFailure(err)
	}
	changed := false
	if c.flags["--write"] == "true" {
		if err = platform.PrepareState(c.settingsRoot); err != nil {
			return c.settingsFailure(err)
		}
		owner, err := platform.Acquire(c.settingsRoot, "settings", true)
		if err != nil {
			return c.settingsFailure(err)
		}
		defer owner.Close()
		selected, exists, err = settings.Read(c.settingsRoot)
		if err != nil {
			return c.settingsFailure(err)
		}
		if err = settings.Registration(c.settingsRoot, scope); err != nil {
			return c.settingsFailure(err)
		}
		lease, err := platform.StateLease(scope.State, true, true)
		if err != nil {
			return c.settingsFailure(err)
		}
		defer lease.Close()
		if err = settings.CheckMutation(scope.State); err != nil {
			return c.settingsFailure(err)
		}
		if _, err = corpus.LoadFile(scope.Config, scope.State); err != nil {
			return c.configError(err)
		}
		if !exists {
			if err = settings.Save(c.settingsRoot, settings.Selected(scope)); err != nil {
				return c.settingsFailure(err)
			}
			changed = true
		}
		exists = true
	}
	var saved *settings.Selection
	if exists {
		value := settings.Selection{Version: 1, Scope: selected.Resolved()}
		if changed {
			value = settings.Selection{Version: 1, Scope: scope}
		}
		saved = &value
	}
	reply := struct {
		RegistrationAllowed bool                `json:"registration_allowed"`
		SavedSelection      *settings.Selection `json:"saved_selection,omitempty"`
		Selection           settings.Selection  `json:"selection"`
		Saved               bool                `json:"saved"`
		Written             bool                `json:"written"`
		Changed             bool                `json:"changed"`
	}{settings.Registration(c.settingsRoot, scope) == nil, saved, settings.Selection{Version: 1, Scope: scope}, exists, c.flags["--write"] == "true", changed}
	if c.flags["--format"] == "json" {
		if WriteJSON(c.output, reply) != 0 {
			return 1
		}
	} else {
		if _, err := writeText(c.output, fmt.Sprintf("State folder: %s\nConfig: %s\nSaved: %t. Changed: %t.\n", scope.State, scope.Config, exists, changed)); err != nil {
			return 1
		}
	}
	if c.flags["--write"] == "true" && !changed {
		return 3
	}
	return 0
}
func relocateSchedule(old schedule.Plan, destination settings.Scope) error {
	if _, err := schedule.RemoveOwned(old, nil); err != nil {
		return err
	}
	spec := old.Spec
	spec.State = destination.State
	spec.Config = destination.Config
	next, err := schedule.Preview(old.System, spec)
	if err != nil {
		return err
	}
	_, err = schedule.Install(next, nil)
	return err
}
