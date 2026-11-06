package verifyrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type settingsReply struct {
	Selection struct {
		State  string `json:"state_dir"`
		Config string `json:"config"`
	} `json:"selection"`
	Saved bool `json:"saved"`
}

func checkSettings(data []byte, state, config string) error {
	var reply settingsReply
	if json.Unmarshal(data, &reply) != nil || !reply.Saved || reply.Selection.State != state || reply.Selection.Config != config {
		return errors.New("fresh process did not resolve the saved data and configuration")
	}
	return nil
}

func (j *featureJourney) bare(exit int, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	before := len(j.driver.summary.Commands)
	_, err := j.driver.invoke(ctx, j.driver.binary, j.home, []string{"BRAIN_HOME="}, append(args, "--format", "json")...)
	if len(j.driver.summary.Commands) != before+1 {
		return nil, errors.Join(err, errors.New("fresh command evidence missing"))
	}
	record := j.driver.summary.Commands[before]
	if !record.Started || record.Exit == nil || *record.Exit != exit || record.Stderr != "" {
		return nil, fmt.Errorf("fresh command expected exit %d, got %+v: %w", exit, record, err)
	}
	return []byte(record.Stdout), nil
}

func (d *driver) settingsRelocation(attempt int) (problem error) {
	j, err := d.newFeatureJourney("settings-relocation", attempt)
	if err != nil {
		return err
	}
	defer func() { problem = errors.Join(problem, os.Chmod(j.state, 0700)) }()
	source, err := j.document("harbor", "# Harbor\n\nRelocation preserves lighthouse documents.\n")
	if err != nil {
		return err
	}
	if err = j.writeConfig([]map[string]any{{"path": source, "project": "harbor"}}); err != nil {
		return err
	}
	preserve, err := j.preserve(fmt.Sprintf("settings-%d-sources", attempt), j.sources)
	if err != nil {
		return err
	}
	defer func() { problem = errors.Join(problem, preserve()) }()
	if _, err = j.call(0, "index"); err != nil {
		return err
	}
	if _, err = j.call(0, "settings", "--write"); err != nil {
		return err
	}
	data, err := j.bare(0, "settings")
	if err != nil {
		return err
	}
	if err = checkSettings(data, j.state, j.config); err != nil {
		return err
	}
	if _, err = j.bare(0, "note", "Relocation preserves amber notebooks.", "--project", "harbor"); err != nil {
		return err
	}
	note := filepath.Join("notes", "harbor.md")
	before, err := os.ReadFile(filepath.Join(j.state, note))
	if err != nil {
		return err
	}
	configBefore, err := os.ReadFile(j.config)
	if err != nil {
		return err
	}
	destination := filepath.Join(j.base, "moved data")
	snapshotBefore, err := snapshot(j.base)
	if err != nil {
		return err
	}
	if _, err = j.bare(0, "settings", "relocate", destination); err != nil {
		return err
	}
	snapshotAfter, err := snapshot(j.base)
	if err != nil {
		return err
	}
	if err = unchangedFiles(snapshotBefore, snapshotAfter); err != nil {
		changed := []string{}
		for path, before := range snapshotBefore {
			if snapshotAfter[path] != before {
				changed = append(changed, path)
			}
		}
		for path := range snapshotAfter {
			if _, ok := snapshotBefore[path]; !ok {
				changed = append(changed, path)
			}
		}
		return fmt.Errorf("relocation preview changed files: %v", changed)
	}
	if _, err = j.bare(0, "settings", "relocate", destination, "--write"); err != nil {
		return err
	}
	data, err = j.bare(0, "settings")
	if err != nil {
		return err
	}
	if err = checkSettings(data, destination, j.config); err != nil {
		return err
	}
	for _, location := range []string{j.state, destination} {
		after, err := os.ReadFile(filepath.Join(location, note))
		if err != nil || string(after) != string(before) {
			return errors.New("relocation lost or changed saved note bytes")
		}
	}
	configAfter, err := os.ReadFile(j.config)
	if err != nil || string(configAfter) != string(configBefore) {
		return errors.New("relocation changed the external configuration")
	}
	data, err = j.bare(0, "search", "amber notebooks")
	if err != nil {
		return err
	}
	if _, err = checkCitation(data, filepath.Join(destination, note), ""); err != nil {
		return err
	}
	if _, err = j.call(1, "note", "A stale writer must refuse.", "--project", "harbor"); err != nil {
		return err
	}
	after, err := os.ReadFile(filepath.Join(j.state, note))
	if err != nil || string(after) != string(before) {
		return errors.New("a stale writer changed retained notes")
	}
	d.summary.SideEffects[fmt.Sprintf("settings_relocation_%d", attempt+1)] = "fresh processes used saved selection; preview wrote nothing; source, external config and original note bytes stayed unchanged; destination search cited moved notes; stale mutation refused"
	return nil
}
