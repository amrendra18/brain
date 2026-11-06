package verifyrun

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"time"
)

type skillSetupResult struct {
	Count  int  `json:"count"`
	Total  int  `json:"total"`
	Empty  bool `json:"empty"`
	Setups []struct {
		SkillFile    string `json:"skill_file"`
		SkillCurrent *bool  `json:"skill_current"`
		Written      *bool  `json:"written"`
	} `json:"setups"`
}

func checkSkillSetup(data []byte, path string, current, written bool) error {
	var result skillSetupResult
	if err := json.Unmarshal(data, &result); err != nil {
		return fmt.Errorf("setup reply is invalid: %w", err)
	}
	if result.Count != 1 || result.Total != 1 || result.Empty || len(result.Setups) != 1 {
		return errors.New("setup reply must contain one skill destination")
	}
	item := result.Setups[0]
	if item.SkillFile != path || item.SkillCurrent == nil || *item.SkillCurrent != current || item.Written == nil || *item.Written != written {
		return errors.New("setup reply differs from installed skill state")
	}
	return nil
}

func onlySkillAdded(before, after map[string]string, path string) error {
	allowed := map[string]bool{}
	for parent := filepath.Dir(path); parent != "."; parent = filepath.Dir(parent) {
		allowed[parent] = true
	}
	allowed[path] = true
	for name, value := range before {
		if after[name] != value {
			return fmt.Errorf("setup changed an existing home entry: %s", name)
		}
	}
	for name := range after {
		if _, exists := before[name]; !exists && !allowed[name] {
			return fmt.Errorf("setup wrote outside its skill destination: %s", name)
		}
	}
	if _, present := after[path]; !present {
		return errors.New("setup did not create the skill file")
	}
	return nil
}

func (d *driver) skillDrift(attempt int) error {
	base := filepath.Join(d.summary.Scratch, fmt.Sprintf("skill-drift-%d", attempt))
	home := filepath.Join(base, "home")
	if err := os.MkdirAll(home, 0700); err != nil {
		return err
	}
	before, err := snapshot(home)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	generated, err := d.invoke(ctx, d.binary, home, nil, "help", "--skill")
	if err != nil {
		return err
	}
	if len(generated) == 0 {
		return errors.New("generated skill is empty")
	}
	unchanged, err := snapshot(home)
	if err != nil || !reflect.DeepEqual(before, unchanged) {
		return errors.Join(err, errors.New("skill help changed isolated home"))
	}
	skillFile := filepath.Join(home, ".claude", "skills", "brain", "SKILL.md")
	preview := func(current bool) error {
		prior, err := snapshot(home)
		if err != nil {
			return err
		}
		output, err := d.invoke(ctx, d.binary, home, nil, "setup", "claude", "--format", "json")
		if err != nil {
			return err
		}
		if err := checkSkillSetup(output, skillFile, current, false); err != nil {
			return err
		}
		later, err := snapshot(home)
		if err != nil || !reflect.DeepEqual(prior, later) {
			return errors.Join(err, errors.New("setup preview changed isolated home"))
		}
		return nil
	}
	if err := preview(false); err != nil {
		return err
	}
	beforeWrite, err := snapshot(home)
	if err != nil {
		return err
	}
	output, err := d.invoke(ctx, d.binary, home, nil, "setup", "claude", "--write", "--format", "json")
	if err != nil {
		return err
	}
	if err := checkSkillSetup(output, skillFile, true, true); err != nil {
		return err
	}
	afterWrite, err := snapshot(home)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(home, skillFile)
	if err != nil {
		return err
	}
	if err := onlySkillAdded(beforeWrite, afterWrite, relative); err != nil {
		return err
	}
	copied, err := os.ReadFile(skillFile)
	if err != nil || !bytes.Equal(copied, generated) {
		return errors.Join(err, errors.New("copied skill differs from generated help"))
	}
	if err := preview(true); err != nil {
		return err
	}
	if err := os.WriteFile(skillFile, append(append([]byte{}, copied...), []byte("\n# stale fixture\n")...), 0600); err != nil {
		return err
	}
	if err := preview(false); err != nil {
		return err
	}
	final, err := snapshot(home)
	if err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(d.evidence, "traces", fmt.Sprintf("skill-drift-%d-files.json", attempt+1)), map[string]any{
		"before": before, "after_copy": afterWrite, "after_stale_fixture": final,
	}); err != nil {
		return err
	}
	d.summary.SideEffects[fmt.Sprintf("skill_drift_%d", attempt+1)] = "Preview changed no owned home files; explicit copy wrote only SKILL.md; stale copy was reported without writes."
	return nil
}
