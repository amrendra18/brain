package verifyrun

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func checkConfigList(data []byte, key string, changed bool) error {
	var r struct {
		Sources []json.RawMessage `json:"sources"`
		Rules   []json.RawMessage `json:"rules"`
		Count   *int              `json:"count"`
		Total   *int              `json:"total"`
		Empty   *bool             `json:"empty"`
		Changed *bool             `json:"changed"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	items := r.Sources
	if key == "rules" {
		items = r.Rules
	}
	if items == nil || r.Count == nil || r.Total == nil || r.Empty == nil || r.Changed == nil || *r.Count != len(items) || *r.Total != len(items) || *r.Empty != (len(items) == 0) || *r.Changed != changed {
		return errors.New("config reply has inconsistent collection or change fields")
	}
	return nil
}

func checkUnknownConfig(data []byte) error {
	var r struct {
		Future  map[string]string            `json:"future_setting"`
		Options map[string]json.RawMessage   `json:"options"`
		Sources []map[string]json.RawMessage `json:"sources"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	if r.Future["keep"] != "top" || string(r.Options["future_option"]) != `"keep"` {
		return errors.New("unknown config members were lost")
	}
	for _, s := range r.Sources {
		if string(s["project"]) == `"retained"` && string(s["future_source"]) == `"keep"` {
			return nil
		}
	}
	return errors.New("unknown retained-source member was lost")
}

func (d *driver) configSources(attempt int) (problem error) {
	j, err := d.newFeatureJourney("config-sources", attempt)
	if err != nil {
		return err
	}
	first, err := j.document("a-first", "# First\n\nCopper badgers catalog maps.\n")
	if err != nil {
		return err
	}
	retained, err := j.document("b-retained", "# Retained\n\nViolet herons preserve citations.\n")
	if err != nil {
		return err
	}
	added, err := j.document("c-added", "# Added\n\nSilver turtles announce additions.\n")
	if err != nil {
		return err
	}
	if err := j.writeConfig([]map[string]any{{"path": first, "project": "first"}, {"path": retained, "project": "retained", "future_source": "keep"}}); err != nil {
		return err
	}
	preserveHome, err := j.preserve(fmt.Sprintf("config-sources-%d-home", attempt), j.home)
	if err != nil {
		return err
	}
	defer func() { problem = errors.Join(problem, preserveHome()) }()
	preserveSources, err := j.preserve(fmt.Sprintf("config-sources-%d-before-move", attempt), j.sources)
	if err != nil {
		return err
	}
	defer func() { problem = errors.Join(problem, preserveSources()) }()
	mutate := func(exit int, key string, args ...string) error {
		data, err := j.call(exit, args...)
		if err != nil {
			return err
		}
		if err = checkConfigList(data, key, exit == 0 && args[1] != "list"); err != nil {
			return err
		}
		return checkScopedNext(data, j.config, j.state, false)
	}
	if err := mutate(0, "sources", "sources", "list"); err != nil {
		return err
	}
	if _, err := j.call(0, "index"); err != nil {
		return err
	}
	data, err := j.call(0, "search", "violet herons", "--no-refresh")
	if err != nil {
		return err
	}
	id, err := checkCitation(data, filepath.Join(retained, "README.md"), "")
	if err != nil {
		return err
	}
	if err := mutate(0, "sources", "sources", "add", added, "--project", "added"); err != nil {
		return err
	}
	noOp := func(args ...string) error {
		before, err := os.ReadFile(j.config)
		if err != nil {
			return err
		}
		key := "rules"
		if args[0] == "sources" {
			key = "sources"
		}
		if err := mutate(3, key, args...); err != nil {
			return err
		}
		after, err := os.ReadFile(j.config)
		if err != nil {
			return err
		}
		if !bytes.Equal(before, after) {
			return errors.New("no-op rewrote selected config")
		}
		return nil
	}
	if err := noOp("sources", "add", added, "--project", "added"); err != nil {
		return err
	}
	data, err = j.call(0, "search", "silver turtles", "--no-refresh")
	if err != nil {
		return err
	}
	if err := checkEmptySearch(data); err != nil {
		return err
	}
	if _, err := j.call(0, "index"); err != nil {
		return err
	}
	data, err = j.call(0, "search", "silver turtles", "--no-refresh")
	if err != nil {
		return err
	}
	if _, err := checkCitation(data, filepath.Join(added, "README.md"), ""); err != nil {
		return err
	}
	if err := mutate(0, "sources", "sources", "remove", first); err != nil {
		return err
	}
	if err := noOp("sources", "remove", first); err != nil {
		return err
	}
	if _, err := j.call(0, "index"); err != nil {
		return err
	}
	data, err = j.call(0, "search", "violet herons", "--no-refresh")
	if err != nil {
		return err
	}
	if _, err := checkCitation(data, filepath.Join(retained, "README.md"), id); err != nil {
		return err
	}
	if err := preserveSources(); err != nil {
		return err
	}
	moved := filepath.Join(j.sources, "z-moved")
	if err := os.Rename(retained, moved); err != nil {
		return err
	}
	preserveSources, err = j.preserve(fmt.Sprintf("config-sources-%d-after-move", attempt), j.sources)
	if err != nil {
		return err
	}
	if err := mutate(0, "sources", "sources", "move", retained, moved); err != nil {
		return err
	}
	if _, err := j.call(0, "index"); err != nil {
		return err
	}
	data, err = j.call(0, "search", "violet herons", "--no-refresh")
	if err != nil {
		return err
	}
	if _, err := checkCitation(data, filepath.Join(moved, "README.md"), id); err != nil {
		return err
	}
	for _, verb := range []string{"exclude", "third-party"} {
		if err := mutate(0, "rules", verb, "list"); err != nil {
			return err
		}
		if err := mutate(0, "rules", verb, "add", "owned-test-pattern"); err != nil {
			return err
		}
		if err := noOp(verb, "add", "owned-test-pattern"); err != nil {
			return err
		}
		if err := mutate(0, "rules", verb, "remove", "owned-test-pattern"); err != nil {
			return err
		}
		if err := noOp(verb, "remove", "owned-test-pattern"); err != nil {
			return err
		}
	}
	before, err := os.ReadFile(j.config)
	if err != nil {
		return err
	}
	data, err = j.call(2, "exclude", "remove", ".env*")
	if err != nil {
		return err
	}
	if err := checkScopedNext(data, j.config, j.state, true); err != nil {
		return err
	}
	after, err := os.ReadFile(j.config)
	if err != nil {
		return err
	}
	if !bytes.Equal(before, after) {
		return errors.New("built-in rule refusal changed config")
	}
	if err := checkUnknownConfig(after); err != nil {
		return err
	}
	d.summary.SideEffects[fmt.Sprintf("config_sources_%d", attempt+1)] = "Selected config changed; sources and home remained unchanged across fixture relocation; no-op config bytes preserved."
	return nil
}
