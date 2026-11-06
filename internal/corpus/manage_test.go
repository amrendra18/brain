package corpus

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func managedFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(root, "config.json")
	if _, changed, err := Initialize(config, Default(source, []string{source}, nil)); err != nil || !changed {
		t.Fatal(changed, err)
	}
	return config, source
}

func TestSourcesLifecycle(t *testing.T) {
	path, source := managedFixture(t)
	doc, err := ReadDocument(path)
	if err != nil || *doc.Config.Sources[0].SourceID != 0 {
		t.Fatal(doc, err)
	}
	before, _ := os.ReadFile(path)
	if _, changed, err := Edit(path, Operation{Collection: "sources", Action: "add", Value: source}); err != nil || changed {
		t.Fatal(changed, err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("identical add changed bytes")
	}
	if _, _, err := Edit(path, Operation{Collection: "sources", Action: "add", Value: source, Project: "conflict"}); !errors.Is(err, ErrOperation) {
		t.Fatal(err)
	}
	doc, changed, err := Edit(path, Operation{Collection: "sources", Action: "remove", Value: source})
	if err != nil || !changed || len(doc.Config.Sources) != 0 || doc.Config.NextSourceID != 1 {
		t.Fatal(doc, changed, err)
	}
	loaded, err := LoadFile(path, filepath.Dir(path))
	if err != nil || len(loaded.Sources) != 1 || loaded.Sources[0].Kind != "note" {
		t.Fatal(loaded, err)
	}
	doc, changed, err = Edit(path, Operation{Collection: "sources", Action: "add", Value: source})
	if err != nil || !changed || *doc.Config.Sources[0].SourceID != 1 || !doc.Config.Sources[0].Literal {
		t.Fatal(doc, changed, err)
	}
	if _, changed, err := Initialize(path, Default(source, []string{source}, nil)); err != nil || changed {
		t.Fatal(changed, err)
	}
}

func TestConfigPreservesUnknownFields(t *testing.T) {
	path, source := managedFixture(t)
	raw := fmt.Sprintf(` {"sources":[{"path":%q,"extra":{"one":[2,true]},"project":"kept"}],"exclude":[],"third_party":[],"options":{"max_file_bytes":99,"inline_refresh_limit":2,"purge_after_days":7,"nested":{"keep":true}},"unknown":{"deep":[1,2]}} `, source)
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if _, changed, err := Edit(path, Operation{Collection: "exclude", Action: "remove", Value: "absent"}); err != nil || changed {
		t.Fatal(changed, err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != raw {
		t.Fatal("no-op changed raw bytes")
	}
	target := filepath.Join(filepath.Dir(source), "target")
	os.Mkdir(target, 0700)
	doc, changed, err := Edit(path, Operation{Collection: "sources", Action: "move", Value: source, Target: target})
	if err != nil || !changed || *doc.Config.Sources[0].SourceID != 0 {
		t.Fatal(doc, changed, err)
	}
	data, _ = os.ReadFile(path)
	var old, now map[string]any
	json.Unmarshal([]byte(raw), &old)
	json.Unmarshal(data, &now)
	for _, key := range []string{"options", "unknown"} {
		if !reflect.DeepEqual(old[key], now[key]) {
			t.Fatal("lost", key)
		}
	}
	beforeSource := old["sources"].([]any)[0].(map[string]any)
	afterSource := now["sources"].([]any)[0].(map[string]any)
	if !reflect.DeepEqual(beforeSource["extra"], afterSource["extra"]) || afterSource["project"] != "kept" {
		t.Fatal(afterSource)
	}
}

func TestRulesLifecycle(t *testing.T) {
	path, _ := managedFixture(t)
	for _, collection := range []string{"exclude", "third-party"} {
		for _, step := range []struct {
			action, value string
			changed       bool
			invalid       bool
		}{{"add", "docs/*.draft", true, false}, {"add", "docs/*.draft", false, false}, {"remove", "docs/*.draft", true, false}, {"remove", "absent", false, false}, {"add", "[", false, true}, {"add", "../outside", false, true}, {"add", "/absolute", false, true}, {"add", "a\\b", false, true}, {"add", "", false, true}, {"add", "a\nb", false, true}, {"add", "a//b", false, true}, {"add", "a/./b", false, true}, {"move", "x", false, true}} {
			before, _ := os.ReadFile(path)
			_, changed, err := Edit(path, Operation{Collection: collection, Action: step.action, Value: step.value})
			if changed != step.changed || (err != nil) != step.invalid {
				t.Fatal(collection, step, changed, err)
			}
			after, _ := os.ReadFile(path)
			if !step.changed && string(before) != string(after) {
				t.Fatal("rejected/no-op changed bytes", step)
			}
		}
	}
	for _, rule := range NeverRead {
		if _, _, err := Edit(path, Operation{Collection: "exclude", Action: "remove", Value: rule}); !errors.Is(err, ErrOperation) {
			t.Fatal(rule, err)
		}
	}
}

func TestConfigValidation(t *testing.T) {
	path, source := managedFixture(t)
	template := `{"sources":%s,"options":{"max_file_bytes":1,"inline_refresh_limit":1,"purge_after_days":1}%s}`
	cases := []string{
		`null`, `[]`, `{"sources":[],"sources":[]}`, `{"sources":[`,
		fmt.Sprintf(template, `[null]`, ""),
		fmt.Sprintf(template, fmt.Sprintf(`[{"path":%q,"source_id":0},{"path":%q,"source_id":0}]`, source, source), ""),
		fmt.Sprintf(template, fmt.Sprintf(`[{"path":%q,"source_id":2}]`, source), `,"next_source_id":1`),
		fmt.Sprintf(template, fmt.Sprintf(`[{"path":%q,"source_id":18446744073709551615}]`, source), ""),
		fmt.Sprintf(template, fmt.Sprintf(`[{"path":%q,"unknown":{"a":1,"a":2}}]`, source), ""),
		fmt.Sprintf(template, `[{"path":"relative"}]`, ""),
		fmt.Sprintf(template, `[]`, "") + `{}`,
	}
	for _, input := range cases {
		os.WriteFile(path, []byte(input), 0600)
		if _, err := ReadDocument(path); !errors.Is(err, ErrConfigInvalid) {
			t.Fatal(input, err)
		}
		if _, _, err := Initialize(path, Default(source, []string{source}, nil)); !errors.Is(err, ErrConfigInvalid) {
			t.Fatal("init overwrote invalid", err)
		}
		after, _ := os.ReadFile(path)
		if string(after) != input {
			t.Fatal("invalid bytes changed")
		}
	}
}

func TestWildcardMove(t *testing.T) {
	path, source := managedFixture(t)
	target := filepath.Join(filepath.Dir(source), "new")
	os.Mkdir(target, 0700)
	config := Default(source, []string{filepath.Join(source, "pro*", "memory")}, nil)
	data, _ := json.Marshal(config)
	os.WriteFile(path, data, 0600)
	op := Operation{Collection: "sources", Action: "move", Value: config.Sources[0].Path, Target: filepath.Join(target, "pro*", "memory")}
	doc, changed, err := Edit(path, op)
	if err != nil || !changed || *doc.Config.Sources[0].SourceID != 0 || SourcePattern(doc.Config.Sources[0]) != true {
		t.Fatal(doc, changed, err)
	}
	op.Value = op.Target
	op.Target = filepath.Join(source, "other*", "memory")
	if _, _, err := Edit(path, op); !errors.Is(err, ErrOperation) {
		t.Fatal(err)
	}
}

func TestSharedConfigLock(t *testing.T) {
	if path := os.Getenv("BRAIN_TEST_SHARED_CONFIG"); path != "" {
		_, err := LoadFile(path, os.Getenv("BRAIN_TEST_STATE"))
		if err != nil {
			t.Fatal(err)
		}
		_, changed, err := Edit(path, Operation{Collection: "exclude", Action: "add", Value: os.Getenv("BRAIN_TEST_RULE")})
		if err != nil || !changed {
			t.Fatal(changed, err)
		}
		return
	}
	path, _ := managedFixture(t)
	const workers = 8
	commands := make([]*exec.Cmd, workers)
	outputs := make([]bytes.Buffer, workers)
	for i := range commands {
		commands[i] = exec.Command(os.Args[0], "-test.run=^TestSharedConfigLock$")
		commands[i].Stdout = &outputs[i]
		commands[i].Stderr = &outputs[i]
		commands[i].Env = append(os.Environ(), "BRAIN_TEST_SHARED_CONFIG="+path, "BRAIN_TEST_STATE="+t.TempDir(), fmt.Sprintf("BRAIN_TEST_RULE=rule%d", i))
		if err := commands[i].Start(); err != nil {
			t.Fatal(err)
		}
	}
	for i, command := range commands {
		if err := command.Wait(); err != nil {
			t.Fatal(err, outputs[i].String())
		}
	}
	doc, err := ReadDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, rule := range doc.Config.Exclude {
		if strings.HasPrefix(rule, "rule") {
			count++
		}
	}
	if count != workers {
		t.Fatal("lost updates", count)
	}
	if _, err := os.Stat(path + ".lock"); err != nil {
		t.Fatal("lock removed", err)
	}
}

func TestManagedSourceAliasesAndValidation(t *testing.T) {
	path, source := managedFixture(t)
	base := filepath.Dir(source)
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(source, alias); err != nil {
		t.Skip(err)
	}
	for _, op := range []Operation{
		{Collection: "sources", Action: "add", Value: alias},
		{Collection: "sources", Action: "move", Value: alias, Target: source},
	} {
		if _, changed, err := Edit(path, op); err != nil || changed {
			t.Fatal(op, changed, err)
		}
	}
	literal := filepath.Join(base, "pro[2026]*")
	if err := os.Mkdir(literal, 0700); err != nil {
		t.Skip(err)
	}
	doc, changed, err := Edit(path, Operation{Collection: "sources", Action: "add", Value: literal})
	if err != nil || !changed || SourcePattern(doc.Config.Sources[1]) {
		t.Fatal(doc, changed, err)
	}
	before, _ := os.ReadFile(path)
	for _, op := range []Operation{
		{Collection: "sources", Action: "add", Value: ""},
		{Collection: "sources", Action: "add", Value: path},
		{Collection: "sources", Action: "add", Value: source, Project: "bad\nname"},
		{Collection: "sources", Action: "move", Value: source, Target: literal},
		{Collection: "sources", Action: "move", Value: filepath.Join(base, "missing"), Target: source},
		{Collection: "sources", Action: "other", Value: source},
		{Collection: "unknown", Action: "add", Value: source},
	} {
		if _, _, err := Edit(path, op); !errors.Is(err, ErrOperation) {
			t.Fatal(op, err)
		}
		after, _ := os.ReadFile(path)
		if string(after) != string(before) {
			t.Fatal("rejected operation changed bytes")
		}
	}
	if _, changed, err := Edit(path, Operation{Collection: "sources", Action: "remove", Value: alias}); err != nil || !changed {
		t.Fatal(changed, err)
	}
	os.Remove(literal)
	if _, changed, err := Edit(path, Operation{Collection: "sources", Action: "move", Value: literal, Target: source}); err != nil || !changed {
		t.Fatal(changed, err)
	}
	os.Remove(source)
	if _, changed, err := Edit(path, Operation{Collection: "sources", Action: "remove", Value: source}); err != nil || !changed {
		t.Fatal(changed, err)
	}
}

func TestLegacySourceMigrationKeepsOwnership(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "corpus")
	child := filepath.Join(root, "project")
	put(t, child, "README.md", "overlapping keeper")
	path := filepath.Join(base, "config.json")
	config := Default(root, []string{root, child}, nil)
	data, _ := json.Marshal(config)
	os.WriteFile(path, data, 0600)
	before, err := LoadFile(path, filepath.Join(base, "state"))
	if err != nil {
		t.Fatal(err)
	}
	old, _, err := Scan(before)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Edit(path, Operation{Collection: "exclude", Action: "add", Value: "*.draft"}); err != nil {
		t.Fatal(err)
	}
	after, err := LoadFile(path, filepath.Join(base, "state"))
	if err != nil {
		t.Fatal(err)
	}
	now, _, err := Scan(after)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(child, "README.md")
	if old[file].Namespace != "source:1:" || now[file].Namespace != old[file].Namespace {
		t.Fatal(old, now)
	}
	if _, _, err := Edit(path, Operation{Collection: "sources", Action: "remove", Value: root}); err != nil {
		t.Fatal(err)
	}
	after, err = LoadFile(path, filepath.Join(base, "state"))
	if err != nil {
		t.Fatal(err)
	}
	now, _, err = Scan(after)
	if err != nil || now[file].Namespace != "source:1:" {
		t.Fatal(now, err)
	}
}

func TestConfigIdentityMetadataRefusals(t *testing.T) {
	path, source := managedFixture(t)
	for _, input := range []string{
		fmt.Sprintf(`{"sources":[{"path":%q,"source_id":null}],"options":{"max_file_bytes":1,"inline_refresh_limit":1,"purge_after_days":1}}`, source),
		`{"sources":[],"next_source_id":null,"options":{"max_file_bytes":1,"inline_refresh_limit":1,"purge_after_days":1}}`,
	} {
		os.WriteFile(path, []byte(input), 0600)
		if _, err := ReadDocument(path); !errors.Is(err, ErrConfigInvalid) {
			t.Fatal(err)
		}
	}
	input := `{"sources":[],"next_source_id":18446744073709551615,"options":{"max_file_bytes":1,"inline_refresh_limit":1,"purge_after_days":1}}`
	os.WriteFile(path, []byte(input), 0600)
	if _, _, err := Edit(path, Operation{Collection: "sources", Action: "add", Value: source}); !errors.Is(err, ErrOperation) {
		t.Fatal("counter wrapped", err)
	}
}

func TestManualSourceEditsKeepAllocatedIDs(t *testing.T) {
	path, source := managedFixture(t)
	rewrite := func(config Config) {
		t.Helper()
		data, err := json.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	doc, err := ReadDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(filepath.Dir(source), "other")
	os.Mkdir(other, 0700)
	doc.Config.Sources = append(doc.Config.Sources, Source{Path: other})
	rewrite(doc.Config)
	loaded, err := ReadDocument(path)
	if err != nil || *loaded.Config.Sources[0].SourceID != 0 || *loaded.Config.Sources[1].SourceID != 1 || loaded.Config.NextSourceID != 2 {
		t.Fatal(loaded, err)
	}
	if _, _, err := Edit(path, Operation{Collection: "sources", Action: "remove", Value: other}); err != nil {
		t.Fatal(err)
	}
	loaded, err = ReadDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	loaded.Config.Sources = append(loaded.Config.Sources, Source{Path: other})
	rewrite(loaded.Config)
	loaded, err = ReadDocument(path)
	if err != nil || *loaded.Config.Sources[1].SourceID != 2 {
		t.Fatal("reused removed id", loaded, err)
	}
	loaded.Config.Sources = []Source{{Path: source}}
	rewrite(loaded.Config)
	loaded, err = ReadDocument(path)
	if err != nil || *loaded.Config.Sources[0].SourceID != 3 {
		t.Fatal("all-manual replacement reused id", loaded, err)
	}
	legacy := Default(source, []string{source, other}, nil)
	rewrite(legacy)
	loaded, err = ReadDocument(path)
	if err != nil || *loaded.Config.Sources[0].SourceID != 0 || *loaded.Config.Sources[1].SourceID != 1 {
		t.Fatal("legacy namespaces changed", loaded, err)
	}
}

func TestConfigUnknownCaseMembersPreserved(t *testing.T) {
	path, source := managedFixture(t)
	raw := fmt.Sprintf(` {"sources":[{"path":%q,"extra":{"Path":1,"path":2},"Extra":{"source_id":3,"Source_ID":4}}],"exclude":[],"third_party":[],"options":{"max_file_bytes":99,"inline_refresh_limit":2,"purge_after_days":7,"extra":{"Sources":[],"sources":[1]},"Extra":{"max_file_bytes":1,"MAX_FILE_BYTES":2}},"future":{"Options":1,"options":2},"Future":{"Sources":3,"sources":4}} `, source)
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	document, err := ReadDocument(path)
	if err != nil || len(document.Config.Sources) != 1 || *document.Config.Sources[0].SourceID != 0 {
		t.Fatal(document, err)
	}
	if _, changed, err := Edit(path, Operation{Collection: "exclude", Action: "remove", Value: "absent"}); err != nil || changed {
		t.Fatal(changed, err)
	}
	unchanged, _ := os.ReadFile(path)
	if string(unchanged) != raw {
		t.Fatal("no-op changed unknown fields")
	}
	if _, changed, err := Edit(path, Operation{Collection: "exclude", Action: "add", Value: "*.draft"}); err != nil || !changed {
		t.Fatal(changed, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var before, after map[string]any
	if json.Unmarshal([]byte(raw), &before) != nil || json.Unmarshal(data, &after) != nil {
		t.Fatal("invalid JSON")
	}
	for _, key := range []string{"future", "Future", "options"} {
		if !reflect.DeepEqual(before[key], after[key]) {
			t.Fatal("changed unknown members", key, before[key], after[key])
		}
	}
	oldSource := before["sources"].([]any)[0].(map[string]any)
	newSource := after["sources"].([]any)[0].(map[string]any)
	for _, key := range []string{"extra", "Extra"} {
		if !reflect.DeepEqual(oldSource[key], newSource[key]) {
			t.Fatal("changed source unknown members", key)
		}
	}
}
