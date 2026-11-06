package verifyrun

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"
)

type contractProbe struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Result string `json:"result"`
}

func (d *driver) toolContract(attempt int) (problem error) {
	base := filepath.Join(d.summary.Scratch, fmt.Sprintf("contract-%d", attempt))
	home := filepath.Join(base, "home")
	source := filepath.Join(base, "sources")
	state := filepath.Join(home, "state")
	for _, folder := range []string{home, source, state} {
		if err := os.MkdirAll(folder, 0700); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("# Isolated contract source\n"), 0600); err != nil {
		return err
	}
	before, err := snapshot(base)
	if err != nil {
		return err
	}
	probes := []contractProbe{}
	probe := func(id, name string, check func() error) error {
		if err := check(); err != nil {
			probes = append(probes, contractProbe{id, name, "failed"})
			return fmt.Errorf("contract probe %s (%s): %w", id, name, err)
		}
		probes = append(probes, contractProbe{id, name, "holds"})
		return nil
	}
	defer func() {
		after, err := snapshot(base)
		if err != nil {
			problem = errors.Join(problem, err)
		} else {
			if !reflect.DeepEqual(before, after) {
				problem = errors.Join(problem, errors.New("contract probes changed isolated home, state, or sources"))
			} else {
				if problem == nil {
					probes = append(probes, contractProbe{"P8", "stateless files unchanged", "holds"})
				}
				d.summary.SideEffects[fmt.Sprintf("contract_%d", attempt+1)] = "Isolated home, state and sources remained unchanged during stateless commands."
			}
		}
		problem = errors.Join(problem, writeJSON(filepath.Join(d.evidence, "traces", fmt.Sprintf("contract-%d-files.json", attempt+1)), map[string]any{"before": before, "after": after}))
		problem = errors.Join(problem, writeJSON(filepath.Join(d.evidence, "traces", fmt.Sprintf("contract-%d-probes.json", attempt+1)), probes))
	}()
	var help, topHelp, skill []byte
	var verbs, flags []string
	if err := probe("P1", "version", func() error {
		data, err := d.contractCommand(home, source, 0, "--version")
		if err != nil {
			return err
		}
		return checkVersion(data)
	}); err != nil {
		return err
	}
	if err := probe("P2", "help for listed verbs", func() error {
		var err error
		topHelp, err = d.contractCommand(home, source, 0, "--help")
		if err != nil {
			return err
		}
		if err := checkTopLevelHelp(topHelp); err != nil {
			return err
		}
		help, err = d.contractCommand(home, source, 0, "help", "--format", "json")
		if err != nil {
			return err
		}
		verbs, err = checkHelpIndex(help)
		if err != nil {
			return err
		}
		for _, name := range verbs {
			data, err := d.contractCommand(home, source, 0, "help", name, "--format", "json")
			if err != nil {
				return err
			}
			if err := checkTopicHelp(data, name); err != nil {
				return err
			}
			if name == "help" {
				flags, err = helpFlags(data)
				if err != nil {
					return err
				}
			}
			plain, err := d.contractCommand(home, source, 0, name, "--help")
			if err != nil {
				return err
			}
			if err := checkPlainTopicHelp(plain, name); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if err := probe("P3", "unknown verb refusal", func() error {
		data, err := d.contractCommand(home, source, 2, "no-such-brain-command")
		if err != nil {
			return err
		}
		return checkBadInput(data, "unknown-command", d.binary, verbs)
	}); err != nil {
		return err
	}
	if err := probe("P4", "unknown flag refusal", func() error {
		data, err := d.contractCommand(home, source, 2, "help", "--no-such-brain-flag")
		if err != nil {
			return err
		}
		return checkBadInput(data, "unknown-flag", d.binary, flags)
	}); err != nil {
		return err
	}
	if err := probe("P5", "public help and skill", func() error {
		if err := checkPublicText(topHelp); err != nil {
			return err
		}
		if err := checkPublicText(help); err != nil {
			return err
		}
		plain, err := d.contractCommand(home, source, 0, "help")
		if err != nil {
			return err
		}
		if err := checkPublicText(plain); err != nil {
			return err
		}
		skill, err = d.contractCommand(home, source, 0, "help", "--skill")
		if err != nil {
			return err
		}
		return checkPublicText(skill)
	}); err != nil {
		return err
	}
	if err := probe("P6", "generated skill equality", func() error {
		tracked, err := os.ReadFile(filepath.Join(d.repo, "skills", "brain", "SKILL.md"))
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(skill, tracked) {
			return errors.New("generated skill differs from tracked skill")
		}
		return nil
	}); err != nil {
		return err
	}
	if err := probe("P7", "README first-use structure", func() error {
		readme, err := os.ReadFile(filepath.Join(d.repo, "README.md"))
		if err != nil {
			return err
		}
		return checkReadme(readme)
	}); err != nil {
		return err
	}
	return nil
}

func (d *driver) installTemporaryHome(attempt int) error {
	home := filepath.Join(d.summary.Scratch, fmt.Sprintf("install-%d", attempt))
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		return err
	}
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	target := filepath.Join(bin, "brain"+suffix)
	content, err := os.ReadFile(d.binary)
	if err != nil {
		return err
	}
	if err := os.WriteFile(target, content, 0700); err != nil {
		return err
	}
	// Resolve the command using only the PATH used for the installation check.
	path := bin
	resolved, err := lookupInPath("brain"+suffix, path)
	if err != nil {
		return err
	}
	before, err := snapshot(home)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := d.invoke(ctx, resolved, home, []string{"PATH=" + path}, "--version")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(out)) == "" || strings.Count(strings.TrimSpace(string(out)), "\n") != 0 {
		return fmt.Errorf("installed command did not print one version line")
	}
	after, err := snapshot(home)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(before, after) {
		return fmt.Errorf("installed version command changed its temporary home")
	}
	d.summary.SideEffects[fmt.Sprintf("install_%d", attempt+1)] = "Copied executable into owned home/.local/bin; PATH lookup and version worked; version changed no files."
	return writeJSON(filepath.Join(d.evidence, "traces", fmt.Sprintf("install-%d-files.json", attempt+1)), map[string]any{"before": before, "after": after})
}

func lookupInPath(name, path string) (string, error) {
	for _, folder := range filepath.SplitList(path) {
		target := filepath.Join(folder, name)
		info, err := os.Stat(target)
		if err == nil && info.Mode().IsRegular() && (runtime.GOOS == "windows" || info.Mode()&0111 != 0) {
			return target, nil
		}
	}
	return "", fmt.Errorf("installed executable is absent from temporary PATH")
}
