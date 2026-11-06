package evaluaterun

import (
	"brain/internal/corpus"
	"brain/internal/platform"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

var readControlSource = platform.ReadSource
var readCorpusSource = platform.ReadSource
var scanCorpus = corpus.Scan

func digest(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

func systemAlias(path string) string {
	if runtime.GOOS == "darwin" {
		for _, pair := range [][2]string{{"/var", "/private/var"}, {"/tmp", "/private/tmp"}} {
			if path == pair[0] || strings.HasPrefix(path, pair[0]+string(filepath.Separator)) {
				return pair[1] + strings.TrimPrefix(path, pair[0])
			}
		}
	}
	return path
}
func containsPath(parent, child string) bool {
	parent, child = filepath.Clean(parent), filepath.Clean(child)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		parent, child = strings.ToLower(parent), strings.ToLower(child)
	}
	rel, err := filepath.Rel(parent, child)
	return err == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
func protectedPaths(repo string) []string {
	home, _ := os.UserHomeDir()
	return []string{filepath.Join(home, "Library", "Application Support", "Brain")}
}
func protectedOverlap(path, repo string) bool {
	for _, p := range protectedPaths(repo) {
		if containsPath(path, p) || containsPath(p, path) {
			return true
		}
	}
	return false
}
func checkedPath(path, repo string) (string, error) {
	if path == "" {
		return "", errors.New("path is empty")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	absolute = systemAlias(filepath.Clean(absolute))
	if protectedOverlap(absolute, repo) {
		return "", errors.New("protected path is forbidden")
	}
	walk := absolute
	for {
		info, err := os.Lstat(walk)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("linked path is forbidden")
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(walk)
		if parent == walk {
			break
		}
		walk = parent
	}
	return absolute, nil
}
func readControl(path, repo string, limit int64) ([]byte, string, error) {
	checked, err := checkedPath(path, repo)
	if err != nil {
		return nil, "", err
	}
	data, err := readControlSource(checked, limit)
	return data, checked, err
}
func loadQuestions(path, repo string, want int, keyword bool) (questionSet, string, string, error) {
	var set questionSet
	data, checked, err := readControl(path, repo, 2<<20)
	if err != nil {
		return set, "", "", err
	}
	if err = json.Unmarshal(data, &set); err != nil {
		return set, "", "", err
	}
	if err = validateQuestionSet(set, want, keyword); err != nil {
		return set, "", "", err
	}
	return set, digest(data), checked, nil
}

func validateQuestionSet(set questionSet, want int, keyword bool) error {
	if set.SchemaVersion != 1 || set.ScoringRule != scoringVersion || strings.TrimSpace(set.ExactNameRule) == "" {
		return errors.New("unsupported frozen question schema or scoring rule")
	}
	if len(set.Entries) != want {
		return fmt.Errorf("expected %d questions, got %d", want, len(set.Entries))
	}
	ids := map[string]bool{}
	for _, q := range set.Entries {
		if q.ID == "" || ids[q.ID] || strings.TrimSpace(q.Question) == "" || len(q.Owners) == 0 {
			return errors.New("question identity or owner is invalid")
		}
		ids[q.ID] = true
		if keyword {
			words := strings.Fields(q.Query)
			if len(words) < 2 || len(words) > 3 || len(words) != len(q.Keywords) {
				return errors.New("keyword query must have two or three words")
			}
			for i := range words {
				if words[i] != q.Keywords[i] {
					return errors.New("keyword query differs from frozen words")
				}
			}
		} else if q.Query != "" || !strings.HasSuffix(strings.TrimSpace(q.Question), "?") {
			return errors.New("sentence question is invalid")
		}
		if q.ExactName == nil || strings.TrimSpace(q.ExactNameReason) == "" {
			return errors.New("exact-name label or reason is missing")
		}
		submitted := q.Question
		if keyword {
			submitted = q.Query
		}
		if *q.ExactName {
			if q.ExactNameAnchor == "" || !strings.Contains(submitted, q.ExactNameAnchor) {
				return errors.New("exact-name anchor is absent from submitted text")
			}
		} else if q.ExactNameAnchor != "" {
			return errors.New("non-exact question has an exact-name anchor")
		}
		for _, owner := range q.Owners {
			if !validOwner(owner) {
				return errors.New("invalid owner path")
			}
		}
	}
	return nil
}
func validOwner(name string) bool {
	if name == "" || filepath.IsAbs(name) || strings.Contains(name, "\\") || strings.HasPrefix(name, "/") {
		return false
	}
	parts := strings.Split(name, "/")
	for _, p := range parts {
		if p == "" || p == "." || p == ".." || strings.Contains(p, ":") {
			return false
		}
	}
	return true
}
func loadConfig(path, repo string) (corpus.Config, map[string]json.RawMessage, string, string, error) {
	var config corpus.Config
	data, checked, err := readControl(path, repo, 2<<20)
	if err != nil {
		return config, nil, "", "", err
	}
	raw := map[string]json.RawMessage{}
	if err = json.Unmarshal(data, &raw); err != nil {
		return config, nil, "", "", err
	}
	if err = json.Unmarshal(data, &config); err != nil {
		return config, nil, "", "", err
	}
	if len(config.Sources) == 0 || config.Options.MaxFileBytes <= 0 || config.Options.InlineRefreshLimit <= 0 || config.Options.PurgeAfterDays <= 0 {
		return config, nil, "", "", errors.New("invalid product config")
	}
	for i, s := range config.Sources {
		if !filepath.IsAbs(s.Path) || strings.ContainsAny(s.Path, "*?") {
			return config, nil, "", "", errors.New("evaluation requires explicit absolute source roots")
		}
		root, err := checkedPath(s.Path, repo)
		if err != nil {
			return config, nil, "", "", err
		}
		if corpus.NeverReadPath(root) {
			return config, nil, "", "", errors.New("evaluation source is excluded by private-name rules")
		}
		if containsPath(root, repo) || containsPath(repo, root) {
			return config, nil, "", "", errors.New("evaluation source overlaps evaluator checkout")
		}
		config.Sources[i].Path = root
		// Product Load ignores caller-supplied source kinds before adding owned notes.
		config.Sources[i].Kind = ""
	}
	return config, raw, digest(data), checked, nil
}
func scanManifest(config corpus.Config) ([]manifestRow, []corpus.Skipped, map[string]corpus.Entry, error) {
	found, skipped, err := discoverManifest(config)
	if err != nil {
		return nil, nil, nil, err
	}
	rows := manifestFromFound(config, found)
	return rows, skipped, found, nil
}

func discoverManifest(config corpus.Config) (map[string]corpus.Entry, []corpus.Skipped, error) {
	return scanCorpus(config)
}

func manifestFromFound(config corpus.Config, found map[string]corpus.Entry) []manifestRow {
	keys := make([]string, 0, len(found))
	for key := range found {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	rows := make([]manifestRow, 0, len(keys))
	for _, key := range keys {
		e := found[key]
		row := manifestRow{Source: e.Source, Path: e.Relative, Namespace: e.Namespace, Project: e.Project, Kind: e.Kind, Size: e.Size, MtimeNS: e.MtimeNS, Status: "eligible"}
		if e.Size > config.Options.MaxFileBytes {
			row.Status = "oversized"
		} else {
			data, readErr := readCorpusSource(e.Path, config.Options.MaxFileBytes)
			if readErr != nil {
				row.Status = "unreadable"
			} else {
				row.SHA256 = digest(data)
			}
		}
		rows = append(rows, row)
	}
	return rows
}
func ownerMap(set questionSet, corpusRoot string, found map[string]corpus.Entry, rows []manifestRow) (map[string]string, error) {
	byAbs := map[string]manifestRow{}
	for _, row := range rows {
		byAbs[filepath.Join(row.Source, filepath.FromSlash(row.Path))] = row
	}
	out := map[string]string{}
	for _, q := range set.Entries {
		for _, owner := range q.Owners {
			absolute := filepath.Join(corpusRoot, filepath.FromSlash(owner))
			entry, ok := found[absolute]
			if !ok {
				return nil, fmt.Errorf("owner %s absent or excluded", owner)
			}
			row := byAbs[entry.Path]
			if row.Status != "eligible" || row.SHA256 == "" {
				return nil, fmt.Errorf("owner %s cannot be read", owner)
			}
			want := ""
			for _, proof := range q.Evidence {
				if proof.Path == owner {
					want = proof.SHA256
					break
				}
			}
			if want == "" || want != row.SHA256 {
				return nil, fmt.Errorf("owner %s hash changed", owner)
			}
			out[absolute] = owner
		}
	}
	return out, nil
}
