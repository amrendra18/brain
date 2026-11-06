package corpus

import (
	"brain/internal/platform"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

type Document struct {
	Config  Config
	fields  map[string]json.RawMessage
	sources []map[string]json.RawMessage
}

type Operation struct{ Collection, Action, Value, Target, Project string }

var ErrOperation = errors.New("invalid config operation")

func ResolvePath(flag string, present bool, state string) (string, error) {
	value := flag
	if !present {
		value, present = os.LookupEnv("BRAIN_CONFIG")
	}
	if !present {
		value = filepath.Join(state, "config.json")
	}
	if strings.TrimSpace(value) == "" || strings.ContainsRune(value, 0) {
		return "", ErrConfigInvalid
	}
	return filepath.Abs(value)
}

func checkJSON(dec *json.Decoder) error {
	token, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	for dec.More() {
		if delim == '{' {
			token, err = dec.Token()
			if err != nil {
				return err
			}
			name, ok := token.(string)
			if !ok || seen[name] {
				return ErrConfigInvalid
			}
			seen[name] = true
		}
		if err = checkJSON(dec); err != nil {
			return err
		}
	}
	_, err = dec.Token()
	return err
}

func ReadDocument(path string) (Document, error) {
	data, err := platform.ReadConfig(path)
	if errors.Is(err, os.ErrNotExist) {
		return Document{}, errors.Join(ErrConfigMissing, err)
	}
	if err != nil {
		return Document{}, errors.Join(ErrConfigInvalid, err)
	}
	return parseDocument(data)
}

func parseDocument(data []byte) (Document, error) {
	doc := Document{}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	if err := checkJSON(dec); err != nil {
		return doc, errors.Join(ErrConfigInvalid, err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return doc, ErrConfigInvalid
	}
	if json.Unmarshal(data, &doc.fields) != nil || doc.fields == nil {
		return doc, ErrConfigInvalid
	}
	if err := rejectKnownAliases(doc.fields, "sources", "next_source_id", "exclude", "third_party", "include", "options", "private_values_file"); err != nil {
		return doc, err
	}
	if json.Unmarshal(doc.fields["sources"], &doc.sources) != nil || doc.sources == nil {
		return doc, ErrConfigInvalid
	}
	for _, source := range doc.sources {
		if source == nil {
			return doc, ErrConfigInvalid
		}
		if err := rejectKnownAliases(source, "source_id", "path", "project", "kind", "literal"); err != nil {
			return doc, err
		}
		if raw, ok := source["source_id"]; ok && string(raw) == "null" {
			return doc, ErrConfigInvalid
		}
	}
	var options map[string]json.RawMessage
	if json.Unmarshal(doc.fields["options"], &options) != nil || options == nil {
		return doc, ErrConfigInvalid
	}
	if err := rejectKnownAliases(options, "max_file_bytes", "inline_refresh_limit", "purge_after_days"); err != nil {
		return doc, err
	}
	if json.Unmarshal(data, &doc.Config) != nil {
		return doc, ErrConfigInvalid
	}
	config := &doc.Config
	if value, present := doc.fields["private_values_file"]; present && (string(value) == "null" || !filepath.IsAbs(config.PrivateValuesFile) || strings.ContainsRune(config.PrivateValuesFile, 0)) {
		return doc, ErrConfigInvalid
	}
	if raw, ok := doc.fields["next_source_id"]; ok && string(raw) == "null" {
		return doc, ErrConfigInvalid
	}
	if config.Options.MaxFileBytes <= 0 || config.Options.InlineRefreshLimit <= 0 || config.Options.PurgeAfterDays <= 0 {
		return doc, ErrConfigInvalid
	}
	for _, source := range config.Sources {
		if !filepath.IsAbs(source.Path) || strings.ContainsRune(source.Path, 0) {
			return doc, ErrConfigInvalid
		}
	}
	_, counterPresent := doc.fields["next_source_id"]
	if err := assignSourceIDs(config, counterPresent); err != nil {
		return doc, err
	}
	return doc, nil
}

func rejectKnownAliases(fields map[string]json.RawMessage, names ...string) error {
	for field := range fields {
		for _, name := range names {
			if field != name && strings.EqualFold(field, name) {
				return ErrConfigInvalid
			}
		}
	}
	return nil
}

func assignSourceIDs(config *Config, counterPresent bool) error {
	used := map[uint64]bool{}
	next := config.NextSourceID
	managed := false
	for _, source := range config.Sources {
		if source.SourceID == nil {
			continue
		}
		managed = true
		id := *source.SourceID
		if used[id] || id == math.MaxUint64 {
			return ErrConfigInvalid
		}
		used[id] = true
		next = max(next, id+1)
	}
	if counterPresent && managed && config.NextSourceID < next {
		return ErrConfigInvalid
	}
	for i := range config.Sources {
		source := &config.Sources[i]
		if source.SourceID != nil {
			continue
		}
		id := uint64(i)
		if managed || counterPresent {
			id = next
		}
		if id == math.MaxUint64 {
			return ErrConfigInvalid
		}
		source.SourceID = &id
		next = max(next, id+1)
	}
	config.NextSourceID = next
	return nil
}

func (doc Document) bytes() ([]byte, error) {
	sources := make([]map[string]json.RawMessage, len(doc.Config.Sources))
	for i, source := range doc.Config.Sources {
		fields := doc.sources[i]
		if fields == nil {
			fields = map[string]json.RawMessage{}
		}
		fields["path"], _ = json.Marshal(source.Path)
		fields["source_id"], _ = json.Marshal(source.SourceID)
		if source.Literal {
			fields["literal"] = json.RawMessage("true")
		}
		if source.Project != "" {
			fields["project"], _ = json.Marshal(source.Project)
		}
		sources[i] = fields
	}
	doc.fields["sources"], _ = json.Marshal(sources)
	doc.fields["next_source_id"], _ = json.Marshal(doc.Config.NextSourceID)
	data, err := json.MarshalIndent(doc.fields, "", "  ")
	return append(data, '\n'), err
}

func Initialize(path string, config Config) (Document, bool, error) {
	path, err := platform.ConfigPath(path)
	if err != nil {
		return Document{}, false, err
	}
	lock, err := platform.AcquireConfig(path, true)
	if err != nil {
		return Document{}, false, err
	}
	defer lock.Close()
	doc, err := ReadDocument(path)
	if err == nil {
		return doc, false, nil
	}
	if !errors.Is(err, ErrConfigMissing) {
		return doc, false, err
	}
	data, err := json.Marshal(config)
	if err != nil {
		return doc, false, err
	}
	doc, err = parseDocument(data)
	if err != nil {
		return doc, false, err
	}
	data, err = doc.bytes()
	if err == nil {
		err = platform.WriteConfig(path, data)
	}
	return doc, err == nil, err
}

func Edit(path string, operation Operation) (Document, bool, error) {
	path, err := platform.ConfigPath(path)
	if err != nil {
		return Document{}, false, err
	}
	lock, err := platform.AcquireConfig(path, true)
	if err != nil {
		return Document{}, false, err
	}
	defer lock.Close()
	doc, err := ReadDocument(path)
	if err != nil {
		return doc, false, err
	}
	changed, err := doc.apply(operation)
	if err != nil || !changed {
		return doc, false, err
	}
	data, err := doc.bytes()
	if err == nil {
		err = platform.WriteConfig(path, data)
	}
	return doc, err == nil, err
}

func operationError(message string) error { return errors.Join(ErrOperation, errors.New(message)) }

func SourcePath(value string) (string, error) {
	if value == "" || strings.ContainsRune(value, 0) {
		return "", operationError("Use a readable source folder.")
	}
	path, err := filepath.Abs(value)
	if err == nil {
		path, err = filepath.EvalSymlinks(path)
	}
	info, statErr := os.Stat(path)
	if err != nil || statErr != nil || !info.IsDir() || !platform.ReadableDir(path) {
		return "", operationError("Use a readable source folder.")
	}
	return path, nil
}

func existingPath(value string) string {
	path, err := filepath.Abs(value)
	if err != nil {
		return value
	}
	if parent, err := filepath.EvalSymlinks(filepath.Dir(path)); err == nil {
		path = filepath.Join(parent, filepath.Base(path))
	}
	return filepath.Clean(path)
}

func (doc *Document) sourcePosition(value string) int {
	path := existingPath(value)
	for i, source := range doc.Config.Sources {
		if source.Path == path || existingPath(source.Path) == path {
			return i
		}
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		for i, source := range doc.Config.Sources {
			if source.Path == real {
				return i
			}
			if !SourcePattern(source) {
				resolved, err := filepath.EvalSymlinks(source.Path)
				if err == nil && resolved == real {
					return i
				}
			}
		}
	}
	return -1
}

func wildcardSuffix(path string) string {
	at := strings.IndexAny(path, "*?")
	if at < 0 {
		return ""
	}
	prefix := filepath.Dir(path[:at])
	suffix, _ := filepath.Rel(prefix, path)
	return suffix
}

func (doc *Document) apply(op Operation) (bool, error) {
	if op.Collection != "sources" {
		return doc.rule(op)
	}
	if op.Action != "add" && op.Action != "remove" && op.Action != "move" {
		return false, operationError("Use sources list, add, remove or move.")
	}
	if op.Value == "" {
		return false, operationError("Supply a source folder.")
	}
	position := doc.sourcePosition(op.Value)
	if op.Action == "remove" {
		if position < 0 {
			return false, nil
		}
		doc.Config.Sources = append(doc.Config.Sources[:position], doc.Config.Sources[position+1:]...)
		doc.sources = append(doc.sources[:position], doc.sources[position+1:]...)
		return true, nil
	}
	if strings.IndexFunc(op.Project, unicode.IsControl) >= 0 {
		return false, operationError("The project name contains a control character.")
	}
	if op.Action == "add" {
		path, err := SourcePath(op.Value)
		if err != nil {
			return false, err
		}
		if position >= 0 {
			if doc.Config.Sources[position].Project != op.Project {
				return false, operationError("This source already has a different project name.")
			}
			return false, nil
		}
		if doc.Config.NextSourceID == math.MaxUint64 {
			return false, operationError("No source identities remain.")
		}
		id := doc.Config.NextSourceID
		doc.Config.NextSourceID++
		doc.Config.Sources = append(doc.Config.Sources, Source{Path: path, Project: op.Project, Literal: true, SourceID: &id})
		doc.sources = append(doc.sources, map[string]json.RawMessage{})
		return true, nil
	}
	if position < 0 {
		return false, operationError("The old source is not configured. List sources first.")
	}
	source := &doc.Config.Sources[position]
	var target string
	if SourcePattern(*source) {
		target = existingPath(op.Target)
		suffix := wildcardSuffix(source.Path)
		if suffix == "" || wildcardSuffix(target) != suffix {
			return false, operationError("Keep the same wildcard suffix when moving a pattern source.")
		}
		prefix := filepath.Dir(target[:strings.IndexAny(target, "*?")])
		root, err := SourcePath(prefix)
		if err != nil {
			return false, err
		}
		target = filepath.Join(root, suffix)
	} else {
		var err error
		target, err = SourcePath(op.Target)
		if err != nil {
			return false, err
		}
	}
	if target == source.Path {
		return false, nil
	}
	if other := doc.sourcePosition(target); other >= 0 {
		if other == position {
			return false, nil
		}
		return false, operationError("The destination is already a configured source.")
	}
	source.Path = target
	return true, nil
}

func ValidateRule(rule string) error {
	if rule == "" || strings.TrimSpace(rule) != rule || strings.IndexFunc(rule, unicode.IsControl) >= 0 || strings.Contains(rule, "\\") || strings.HasPrefix(rule, "/") || strings.Contains(rule, ":") {
		return operationError("Use a relative rule with valid filename patterns.")
	}
	for _, part := range strings.Split(rule, "/") {
		if part == "" || part == "." || part == ".." {
			return operationError("Use a relative rule with valid filename patterns.")
		}
		if _, err := filepath.Match(part, ""); err != nil {
			return operationError("Use a relative rule with valid filename patterns.")
		}
	}
	return nil
}

func (doc *Document) rule(op Operation) (bool, error) {
	if op.Collection != "exclude" && op.Collection != "third-party" {
		return false, operationError("Choose sources, exclude or third-party.")
	}
	if op.Action != "add" && op.Action != "remove" {
		return false, operationError("Use list, add or remove.")
	}
	if err := ValidateRule(op.Value); err != nil {
		return false, err
	}
	if op.Action == "remove" && op.Collection == "exclude" {
		for _, rule := range NeverRead {
			if strings.EqualFold(rule, op.Value) {
				return false, operationError("Built-in safety rules cannot be removed.")
			}
		}
	}
	rules, key := &doc.Config.Exclude, "exclude"
	if op.Collection == "third-party" {
		rules, key = &doc.Config.ThirdParty, "third_party"
	}
	position := -1
	for i, rule := range *rules {
		if rule == op.Value {
			position = i
			break
		}
	}
	if op.Action == "add" {
		if position >= 0 {
			return false, nil
		}
		*rules = append(*rules, op.Value)
	} else {
		if position < 0 {
			return false, nil
		}
		*rules = append((*rules)[:position], (*rules)[position+1:]...)
	}
	doc.fields[key], _ = json.Marshal(*rules)
	return true, nil
}
