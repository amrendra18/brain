package corpus

import (
	"brain/internal/platform"
	"brain/internal/redact"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

var NeverRead = []string{".local", ".env*", "*.pem", "*.key", "auth.json", "node_modules", "dist", ".venv", ".stryker-tmp", ".claude/worktrees", ".git", "*.jsonl", "id_*", "*.p12", "*.pfx", ".netrc", "credentials*", "*.kdbx", ".ssh", ".gnupg"}
var Kinds = []string{"readme", "plan", "prd", "skill", "rules", "memory", "doc", "transcript", "note", "third-party"}
var ErrConfigMissing = errors.New("config.json is missing")
var ErrConfigInvalid = errors.New("config.json cannot be read or is invalid")
var ErrUnreadable = errors.New("a source folder cannot be read")

type Source struct {
	SourceID *uint64 `json:"source_id,omitempty"`
	Path     string  `json:"path"`
	Project  string  `json:"project,omitempty"`
	Kind     string  `json:"kind,omitempty"`
	Literal  bool    `json:"literal,omitempty"`
}
type Options struct {
	MaxFileBytes       int64 `json:"max_file_bytes"`
	InlineRefreshLimit int   `json:"inline_refresh_limit"`
	PurgeAfterDays     int   `json:"purge_after_days"`
}
type Config struct {
	AllowUnavailableRoots bool           `json:"-"`
	PrivateValuesFile     string         `json:"private_values_file,omitempty"`
	Policy                *redact.Policy `json:"-"`
	NextSourceID          uint64         `json:"next_source_id,omitempty"`
	Sources               []Source       `json:"sources"`
	Exclude               []string       `json:"exclude"`
	ThirdParty            []string       `json:"third_party"`
	Include               []string       `json:"include"`
	Options               Options        `json:"options"`
}
type Entry struct {
	Path, Relative, Source, Project, Kind, Namespace string
	Size, MtimeNS                                    int64
	Superseded                                       bool
}
type Skipped struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

func (config Config) Privacy() *redact.Policy {
	if config.Policy != nil {
		return config.Policy
	}
	return redact.Default()
}

func Default(root string, sources, include []string) Config {
	config := Config{Policy: redact.Default(), Exclude: append([]string{}, NeverRead...), ThirdParty: []string{}, Include: append([]string{}, include...), Options: Options{1048576, 200, 7}}
	if len(sources) == 0 {
		config.Sources = []Source{{Path: root, Literal: true}}
	} else {
		for _, source := range sources {
			config.Sources = append(config.Sources, Source{Path: source})
		}
	}
	return config
}
func Load(state string) (Config, error) {
	return LoadFile(filepath.Join(state, "config.json"), state)
}

func LoadFile(path, state string) (Config, error) {
	return LoadFileChecked(path, state, nil)
}

// LoadFileChecked validates parsed paths before loading the private values file.
// The check and load use the same parsed config document.
func LoadFileChecked(path, state string, check func(Config) error) (Config, error) {
	document, err := ReadDocument(path)
	if err != nil {
		return Config{}, err
	}
	config := document.Config
	if check != nil {
		if err := check(config); err != nil {
			return Config{}, err
		}
	}
	config.Policy, err = redact.Load(config.PrivateValuesFile)
	if err != nil {
		return Config{}, err
	}
	if config.PrivateValuesFile != "" {
		config.PrivateValuesFile, err = filepath.EvalSymlinks(config.PrivateValuesFile)
		if err != nil {
			return Config{}, redact.ErrPolicyInvalid
		}
	}
	for i := range config.Sources {
		config.Sources[i].Kind = ""
	}
	config.Sources = append(config.Sources, Source{Path: filepath.Join(state, "notes"), Kind: "note"})
	return config, nil
}

func match(parts []string, rules []string) bool {
	for _, rule := range rules {
		run := strings.Split(strings.ToLower(rule), "/")
		if len(run) > len(parts) {
			continue
		}
		matched := true
		for i, pattern := range run {
			ok, _ := filepath.Match(pattern, strings.ToLower(parts[len(parts)-len(run)+i]))
			if !ok {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func matchAncestor(parts []string, rules []string) bool {
	for end := 1; end <= len(parts); end++ {
		if match(parts[:end], rules) {
			return true
		}
	}
	return false
}
func excludedRoot(root string, rules []string) bool {
	parts := strings.Split(filepath.ToSlash(root), "/")
	if matchAncestor(parts, rules) {
		return true
	}
	for _, part := range parts {
		if platform.ReservedName(part) {
			return true
		}
	}
	return false
}
func Kind(path string) string {
	name := strings.ToUpper(filepath.Base(path))
	if strings.HasSuffix(name, ".RTF") {
		return "transcript"
	}
	if strings.HasSuffix(name, ".DOCX") {
		return "doc"
	}
	if strings.EqualFold(filepath.Base(filepath.Dir(path)), "memory") {
		return "memory"
	}
	switch name {
	case "AGENTS.MD", "CLAUDE.MD":
		return "rules"
	case "SKILL.MD":
		return "skill"
	}
	if strings.HasPrefix(name, "README") {
		return "readme"
	}
	for _, word := range strings.FieldsFunc(strings.TrimSuffix(name, ".MD"), func(r rune) bool { return !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') }) {
		if word == "PRD" {
			return "prd"
		}
		if word == "PLAN" {
			return "plan"
		}
	}
	return "doc"
}
func Scan(config Config) (map[string]Entry, []Skipped, error) {
	result, err := ScanWithPolicy(config)
	return result.Entries, result.Skipped, err
}

// Older configs infer patterns from the path. An explicit literal choice from
// init stays literal after the folder disappears. The owned notes root is literal.
func SourcePattern(source Source) bool {
	return source.Kind != "note" && !source.Literal && strings.ContainsAny(source.Path, "*?")
}

// SourceRoots applies the ignore rules already loaded by scanning.
func SourceRoots(config Config, source Source, policy *ScanResult) ([]string, []Skipped, error) {
	if !SourcePattern(source) {
		return []string{source.Path}, nil, nil
	}
	return expandRoots(source.Path, scanRules(config), policy)
}

// MatchesRoot compares an indexed root with its configured source. A bracketed
// ancestor remains literal even when a later path component contains a wildcard.
func MatchesRoot(source Source, root string) bool {
	if !SourcePattern(source) {
		return filepath.Clean(source.Path) == filepath.Clean(root)
	}
	want := strings.Split(filepath.ToSlash(filepath.Clean(source.Path)), "/")
	got := strings.Split(filepath.ToSlash(filepath.Clean(root)), "/")
	if len(want) != len(got) {
		return false
	}
	for i, part := range want {
		if strings.ContainsAny(part, "*?") {
			if !matchSourcePart(part, got[i]) {
				return false
			}
		} else if part != got[i] {
			return false
		}
	}
	return true
}

// matchSourcePart keeps the established leading character-class form, such as
// [ap]*. Brackets inside a named component are ordinary filename characters.
func matchSourcePart(pattern, name string) bool {
	if strings.HasPrefix(pattern, "[") && strings.IndexByte(pattern, ']') > 1 {
		matched, err := filepath.Match(pattern, name)
		return err == nil && matched
	}
	want, got := []rune(pattern), []rune(name)
	old := make([]bool, len(got)+1)
	old[0] = true
	for _, char := range want {
		next := make([]bool, len(old))
		for i := range next {
			switch char {
			case '*':
				next[i] = old[i] || i > 0 && next[i-1]
			case '?':
				next[i] = i > 0 && old[i-1]
			default:
				next[i] = i > 0 && old[i-1] && char == got[i-1]
			}
		}
		old = next
	}
	return old[len(got)]
}

func scan(config Config, policy *ScanResult) (map[string]Entry, []Skipped, error) {
	found, skipped := map[string]Entry{}, []Skipped{}
	if config.PrivateValuesFile != "" {
		var err error
		policy.privateFile, err = os.Stat(config.PrivateValuesFile)
		if err != nil {
			return nil, nil, ErrUnreadable
		}
	}
	rules := scanRules(config)
	order := make([]int, len(config.Sources))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		return strings.Count(filepath.Clean(config.Sources[order[i]].Path), string(filepath.Separator)) < strings.Count(filepath.Clean(config.Sources[order[j]].Path), string(filepath.Separator))
	})
	owners := map[string]int{}
	for _, sourceNumber := range order {
		source := config.Sources[sourceNumber]
		if source.Kind != "note" && excludedRoot(source.Path, rules) {
			continue
		}
		wildcard := SourcePattern(source)
		roots, gaps, err := SourceRoots(config, source, policy)
		skipped = append(skipped, gaps...)
		if err != nil {
			return nil, nil, ErrUnreadable
		}
		for _, root := range roots {
			expansion := ""
			if wildcard {
				prefix := source.Path[:strings.IndexAny(source.Path, "*?")]
				prefix = filepath.Dir(prefix)
				expansion, _ = filepath.Rel(prefix, root)
			}
			identity := uint64(sourceNumber)
			if source.SourceID != nil {
				identity = *source.SourceID
			}
			namespace := "source:" + strconv.FormatUint(identity, 10) + ":" + filepath.ToSlash(expansion)
			if source.Kind == "note" {
				namespace = "notes"
			}
			if source.Kind != "note" && excludedRoot(root, rules) || policy.ignored(root, true) {
				continue
			}
			info, err := os.Lstat(root)
			if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !platform.ReadableDir(root) {
				if wildcard || source.Kind == "note" {
					if err != nil && !os.IsNotExist(err) || err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
						skipped = append(skipped, Skipped{root, "unreadable"})
					}
					continue
				}
				if config.AllowUnavailableRoots {
					skipped = append(skipped, Skipped{root, "unreadable"})
					continue
				}
				return nil, nil, ErrUnreadable
			}
			err = filepath.WalkDir(root, func(path string, item fs.DirEntry, problem error) error {
				if path == root {
					if problem != nil {
						return ErrUnreadable
					}
					return policy.loadIgnore(path)
				}
				if config.Privacy().Match(path) {
					skipped = append(skipped, Skipped{"[protected path]", "privacy-protected-path"})
					if item != nil && item.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
				relative, err := filepath.Rel(root, path)
				if err != nil {
					return err
				}
				parts := strings.Split(filepath.ToSlash(relative), "/")
				if policy.privateControl(path) || isIgnoreName(itemName(item, path)) || policy.ignored(path, item != nil && item.IsDir()) || platform.ReservedName(itemName(item, path)) || match(parts, rules) || source.Kind != "note" && excludedRoot(path, config.Exclude) || item != nil && item.Type()&os.ModeSymlink != 0 {
					if item != nil && item.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
				if problem != nil {
					skipped = append(skipped, Skipped{path, "unreadable"})
					if item != nil && item.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
				if item.IsDir() {
					if !platform.ReadableDir(path) {
						skipped = append(skipped, Skipped{path, "unreadable"})
						return filepath.SkipDir
					}
					return policy.loadIgnore(path)
				}
				suffix := strings.ToLower(filepath.Ext(path))
				if suffix != ".md" && suffix != ".rtf" && suffix != ".docx" {
					return nil
				}
				status, err := item.Info()
				if err != nil {
					skipped = append(skipped, Skipped{path, "unreadable"})
					return nil
				}
				if !status.Mode().IsRegular() {
					return nil
				}
				project := filepath.Base(root)
				if len(parts) > 1 {
					project = parts[0]
				}
				if source.Project != "" {
					project = source.Project
				}
				kind := Kind(path)
				if matchAncestor(parts, config.Include) {
					kind = "third-party"
				}
				if source.Kind == "note" {
					kind = "note"
					project = strings.TrimSuffix(item.Name(), filepath.Ext(item.Name()))
				}
				old := false
				for _, part := range parts[:len(parts)-1] {
					if strings.EqualFold(part, "archive") || strings.EqualFold(part, "superseded") {
						old = true
					}
				}
				if owner, exists := owners[path]; exists && owner > sourceNumber {
					return nil
				}
				owners[path] = sourceNumber
				found[path] = Entry{
					Path:       path,
					Relative:   filepath.ToSlash(relative),
					Source:     root,
					Project:    project,
					Kind:       kind,
					Namespace:  namespace,
					Size:       status.Size(),
					MtimeNS:    status.ModTime().UnixNano(),
					Superseded: old,
				}
				return nil
			})
			if err != nil {
				return nil, nil, err
			}
		}
	}
	sort.Slice(skipped, func(i, j int) bool { return skipped[i].Path < skipped[j].Path })
	return found, skipped, nil
}
func itemName(item fs.DirEntry, path string) string {
	if item != nil {
		return item.Name()
	}
	return filepath.Base(path)
}

func scanRules(config Config) []string {
	rules := append(append([]string{}, NeverRead...), config.Exclude...)
	for _, rule := range config.ThirdParty {
		included := false
		for _, allow := range config.Include {
			if rule == allow {
				included = true
			}
		}
		if !included {
			rules = append(rules, rule)
		}
	}
	return rules
}

// Excluded checks names without opening any source path.
func Excluded(config Config, path string) bool {
	for _, source := range config.Sources {
		if source.Kind == "note" {
			relative, err := filepath.Rel(source.Path, path)
			if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				return excludedRoot(relative, scanRules(config))
			}
		}
	}
	return excludedRoot(path, scanRules(config))
}

// NeverReadPath applies the built-in private-name and reserved-name rules.
func NeverReadPath(path string) bool {
	return excludedRoot(path, NeverRead)
}

// expandRoots records unavailable parents that filepath.Glob silently omits.
func expandRoots(pattern string, rules []string, policy *ScanResult) ([]string, []Skipped, error) {
	prefix := filepath.Dir(pattern[:strings.IndexAny(pattern, "*?")])
	relative, err := filepath.Rel(prefix, pattern)
	if err != nil {
		return nil, nil, err
	}
	roots, gaps := []string{prefix}, []Skipped{}
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		next := []string{}
		for _, root := range roots {
			if excludedRoot(root, rules) || policy.ignored(root, true) {
				continue
			}
			info, err := os.Lstat(root)
			if os.IsNotExist(err) || err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
				continue
			}
			if err != nil || !platform.ReadableDir(root) {
				gaps = append(gaps, Skipped{root, "unreadable"})
				continue
			}
			candidate := filepath.Join(root, part)
			if strings.ContainsAny(part, "*?") {
				entries, err := os.ReadDir(root)
				if err != nil {
					gaps = append(gaps, Skipped{root, "unreadable"})
					continue
				}
				for _, entry := range entries {
					if matchSourcePart(part, entry.Name()) {
						next = append(next, filepath.Join(root, entry.Name()))
					}
				}
			} else if _, err := os.Lstat(candidate); err == nil {
				next = append(next, candidate)
			} else if !os.IsNotExist(err) {
				gaps = append(gaps, Skipped{candidate, "unreadable"})
			}
		}
		roots = next
	}
	return roots, gaps, nil
}
