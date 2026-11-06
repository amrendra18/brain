package verifyrun

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func exclusionSnapshot(root string) (map[string]string, error) {
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		switch {
		case entry.IsDir():
			files[relative] = "directory"
		case entry.Type()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			files[relative] = "symlink:" + target
		case entry.Type().IsRegular():
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			files[relative] = fmt.Sprintf("%x", sha256.Sum256(data))
		default:
			return fmt.Errorf("unexpected fixture file type: %s", relative)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		files[relative] += fmt.Sprintf(" mode:%o", info.Mode())
		return nil
	})
	return files, err
}

func checkExcludedChunk(data []byte, markers ...string) error {
	var reply struct {
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &reply); err != nil {
		return err
	}
	if reply.Error == nil || reply.Error.Code != "chunk-not-found" {
		return errors.New("excluded chunk did not return a missing-chunk refusal")
	}
	var decoded any
	_ = json.Unmarshal(data, &decoded)
	var clean func(any) bool
	clean = func(value any) bool {
		switch value := value.(type) {
		case string:
			for _, marker := range markers {
				if strings.Contains(strings.ToLower(value), strings.ToLower(marker)) {
					return false
				}
			}
		case []any:
			for _, item := range value {
				if !clean(item) {
					return false
				}
			}
		case map[string]any:
			for key, item := range value {
				if !clean(key) || !clean(item) {
					return false
				}
			}
		}
		return true
	}
	if !clean(decoded) {
		return errors.New("excluded content survived in the refusal or recovery arguments")
	}
	return nil
}

func (d *driver) exclusions(attempt int) (problem error) {
	j, err := d.newFeatureJourney("exclusions", attempt)
	if err != nil {
		return err
	}
	files := map[string]string{
		"README.md":            "# Harbor\n\nAmber otters carry the allowed ledger.\n",
		"later.md":             "# SealedHeading\n\nHidden cedar records need later exclusion.\n",
		"nested/kept.md":       "# Kept\n\nViolet herons keep the permitted exception.\n",
		"other/anchored.md":    "# Anchor\n\nSilver turtles retain the unanchored sibling.\n",
		".brainignore":         "# ignorecontrolmarker\n/anchored.md\n**/hidden.md\n\\#secret.md\nblocked/\n!.ssh/\n",
		"nested/.brainignore":  "*.md\n!kept.md\n",
		"blocked/.brainignore": "!reopen.md\n",
	}
	for _, name := range []string{"id_notes.md", "credentials-notes.md", "archive.p12/vault.md", "archive.pfx/vault.md", ".netrc/vault.md", "archive.kdbx/vault.md", ".ssh/vault.md", ".gnupg/vault.md", "anchored.md", "deep/more/hidden.md", "#secret.md", "nested/rejected.md", "blocked/reopen.md"} {
		files[name] = "# Excluded\n\nGuarded badgers keep forbidden records.\n"
	}
	for name, body := range files {
		path := filepath.Join(j.sources, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			return err
		}
	}
	target := filepath.Join(j.base, "hostile.md")
	if err := os.WriteFile(target, []byte("# Hostile\n\nHostile foxes reveal followed links.\n"), 0600); err != nil {
		return err
	}
	if err := os.Symlink(target, filepath.Join(j.sources, "alias.md")); err != nil {
		return fmt.Errorf("hostile symlink fixture unavailable: %w", err)
	}
	if err := j.writeConfig([]map[string]any{{"path": j.sources, "project": "harbor"}}); err != nil {
		return err
	}
	preserveHome, err := j.preserve(fmt.Sprintf("exclusions-%d-home", attempt), j.home)
	if err != nil {
		return err
	}
	defer func() { problem = errors.Join(problem, preserveHome()) }()
	before, err := exclusionSnapshot(j.sources)
	if err != nil {
		return err
	}
	preserve := func(phase string) error {
		after, err := exclusionSnapshot(j.sources)
		if err != nil {
			return err
		}
		writeErr := writeJSON(filepath.Join(d.evidence, "traces", fmt.Sprintf("exclusions-%d-%s.json", attempt, phase)), map[string]any{"before": before, "after": after})
		data, err := os.ReadFile(target)
		if err != nil || string(data) != "# Hostile\n\nHostile foxes reveal followed links.\n" {
			return errors.Join(writeErr, err, errors.New("hostile link target changed"))
		}
		return errors.Join(writeErr, unchangedFiles(before, after))
	}
	defer func() { problem = errors.Join(problem, preserve("final")) }()
	if _, err := j.call(0, "index"); err != nil {
		return err
	}
	for query, path := range map[string]string{"amber otters": "README.md", "violet herons": "nested/kept.md", "silver turtles": "other/anchored.md"} {
		data, err := j.call(0, "search", query)
		if err != nil {
			return err
		}
		if _, err := checkCitation(data, filepath.Join(j.sources, filepath.FromSlash(path)), ""); err != nil {
			return err
		}
	}
	for _, query := range []string{"guarded badgers", "hostile foxes", "ignorecontrolmarker"} {
		data, err := j.call(0, "search", query)
		if err != nil {
			return err
		}
		if err := checkEmptySearch(data); err != nil {
			return fmt.Errorf("excluded query %q: %w", query, err)
		}
	}
	data, err := j.call(0, "search", "hidden cedar")
	if err != nil {
		return err
	}
	id, err := checkCitation(data, filepath.Join(j.sources, "later.md"), "")
	if err != nil {
		return err
	}
	data, err = j.call(0, "show", id)
	if err != nil {
		return err
	}
	if !strings.Contains(string(data), "SealedHeading") || !strings.Contains(string(data), "Hidden cedar") {
		return errors.New("pre-exclusion control did not reveal the stored heading and text")
	}
	if err := preserve("before-rule-change"); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(j.sources, ".brainignore"), []byte(files[".brainignore"]+"later.md\n"), 0600); err != nil {
		return err
	}
	before, err = exclusionSnapshot(j.sources)
	if err != nil {
		return err
	}
	if _, err := j.call(0, "index"); err != nil {
		return err
	}
	data, err = j.call(0, "search", "hidden cedar")
	if err != nil {
		return err
	}
	if err := checkEmptySearch(data); err != nil {
		return err
	}
	data, err = j.call(1, "show", id)
	if err != nil {
		return err
	}
	if err := checkExcludedChunk(data, "SealedHeading", "Hidden cedar"); err != nil {
		return err
	}
	if err := checkScopedNext(data, j.config, j.state, true); err != nil {
		return err
	}
	data, err = j.call(0, "search", "amber otters")
	if err != nil {
		return err
	}
	if _, err := checkCitation(data, filepath.Join(j.sources, "README.md"), ""); err != nil {
		return err
	}
	d.summary.SideEffects[fmt.Sprintf("exclusions_%d", attempt+1)] = "Sensitive names, nested ignores and a hostile symlink stayed unsearchable. Explicit indexing removed newly ignored text and heading. Allowed citations survived. Source, link target and home snapshots stayed unchanged across the intentional ignore edit. File-open observation belongs to the named product tests."
	return nil
}
