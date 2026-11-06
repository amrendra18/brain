package verifyrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"time"
)

type featureJourney struct {
	driver                             *driver
	base, home, state, config, sources string
}

func (d *driver) newFeatureJourney(name string, attempt int) (*featureJourney, error) {
	base := filepath.Join(d.summary.Scratch, fmt.Sprintf("%s-%d", name, attempt))
	j := &featureJourney{d, base, filepath.Join(base, "home"), filepath.Join(base, "state"), filepath.Join(base, "selected.json"), filepath.Join(base, "sources")}
	for _, path := range []string{j.home, j.state, j.sources} {
		if err := os.MkdirAll(path, 0700); err != nil {
			return nil, err
		}
	}
	return j, nil
}

func (j *featureJourney) call(exit int, args ...string) ([]byte, error) {
	return j.callEnv(exit, nil, args...)
}

func (j *featureJourney) callEnv(exit int, env []string, args ...string) ([]byte, error) {
	args = append(args, "--config", j.config, "--state-dir", j.state, "--format", "json")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	previous := len(j.driver.summary.Commands)
	data, err := j.driver.invoke(ctx, j.driver.binary, j.home, env, args...)
	if len(j.driver.summary.Commands) != previous+1 {
		return nil, errors.Join(err, errors.New("command evidence missing"))
	}
	record := j.driver.summary.Commands[previous]
	if !record.Started || record.Exit == nil || *record.Exit != exit || record.Stderr != "" {
		return nil, fmt.Errorf("%v: expected exit %d; command result %+v: %w", args, exit, record, err)
	}
	if err != nil {
		var processExit *exec.ExitError
		if exit == 0 || data != nil || !errors.As(err, &processExit) || processExit.ExitCode() != exit {
			return nil, err
		}
	}
	return []byte(record.Stdout), nil
}

func (j *featureJourney) writeConfig(sources []map[string]any) error {
	return writeJSON(j.config, map[string]any{
		"sources": sources, "exclude": []string{}, "third_party": []string{}, "include": []string{},
		"future_setting": map[string]any{"keep": "top"},
		"options":        map[string]any{"max_file_bytes": 1048576, "inline_refresh_limit": 200, "purge_after_days": 7, "future_option": "keep"},
	})
}

func (j *featureJourney) document(folder, text string) (string, error) {
	root := filepath.Join(j.sources, folder)
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	return root, os.WriteFile(filepath.Join(root, "README.md"), []byte(text), 0600)
}

func (j *featureJourney) preserve(name, root string) (func() error, error) {
	before, err := snapshot(root)
	if err != nil {
		return nil, err
	}
	return func() error {
		after, err := snapshot(root)
		if err != nil {
			return err
		}
		writeErr := writeJSON(filepath.Join(j.driver.evidence, "traces", name+".json"), map[string]any{"before": before, "after": after})
		return errors.Join(writeErr, unchangedFiles(before, after))
	}, nil
}

func unchangedFiles(before, after map[string]string) error {
	if !reflect.DeepEqual(before, after) {
		return errors.New("unexpected source or home writes")
	}
	return nil
}

type citationReply struct {
	Count   int                                           `json:"count"`
	Age     *int64                                        `json:"index_age_s"`
	Results []struct{ ID, Path, Project, Snippet string } `json:"results"`
}

func checkCitation(data []byte, path, id string) (string, error) {
	var r citationReply
	if err := json.Unmarshal(data, &r); err != nil {
		return "", err
	}
	if r.Count != 1 || len(r.Results) != 1 || r.Age == nil || r.Results[0].ID == "" || r.Results[0].Path != path || (id != "" && r.Results[0].ID != id) {
		return "", errors.New("search did not preserve the expected citation and index age")
	}
	return r.Results[0].ID, nil
}

func checkEmptySearch(data []byte) error {
	var r citationReply
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	if r.Count != 0 || len(r.Results) != 0 || r.Age == nil {
		return errors.New("unindexed source became searchable")
	}
	return nil
}

func checkScopedNext(data []byte, config, state string, required bool) error {
	var r struct {
		Next  []string   `json:"next"`
		Argv  [][]string `json:"next_argv"`
		Error *struct {
			Next []string   `json:"next"`
			Argv [][]string `json:"next_argv"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	if r.Error != nil {
		r.Next = append(r.Next, r.Error.Next...)
		r.Argv = append(r.Argv, r.Error.Argv...)
	}
	if len(r.Next) != len(r.Argv) || (required && len(r.Argv) == 0) {
		return errors.New("scoped recovery commands missing")
	}
	for _, argv := range r.Argv {
		if len(argv) == 0 || !filepath.IsAbs(argv[0]) {
			return errors.New("recovery executable is not absolute")
		}
		for flag, value := range map[string]string{"--config": config, "--state-dir": state} {
			found := false
			for i := 1; i < len(argv); i++ {
				if argv[i] == flag && i+1 < len(argv) && argv[i+1] == value {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("recovery command lost %s", flag)
			}
		}
	}
	return nil
}
