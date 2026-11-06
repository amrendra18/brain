package verifyrun

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func init() {
	if os.Getenv("BRAIN_VERIFY_STUB_FIXTURE") == "privacy" {
		os.Exit(privacyFixtureProcess())
	}
}

func TestPrivacyValidators(t *testing.T) {
	values := privacyFixtures()
	for _, tc := range []struct {
		data string
		ok   bool
	}{
		{`{"text":"allowed","number":1,"null":null,"nested":[{"safe":true}]}`, true},
		{`null`, false}, {`[]`, false}, {`{`, false},
		{`{"text":"CipherPrivateOne"}`, false},
		{`{"text":"Cipher\u0050rivateOne"}`, false},
		{`{"nested":[{"CipherPrivateOne":"safe"}]}`, false},
	} {
		if err := checkPrivacyJSON([]byte(tc.data), values); (err == nil) != tc.ok {
			t.Fatal(tc, err)
		}
	}
	good := `{"redactions":[{"path":"README.md","rules":["private-value","github-token"]}]}`
	if err := checkRedactions([]byte(good)); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{`{`, `{}`, `{"redactions":[{"path":"","rules":["private-value","github-token"]}]}`, `{"redactions":[{"path":"README.md","rules":[]}]}`, `{"redactions":[{"path":"README.md","rules":["private-value"]}]}`} {
		if err := checkRedactions([]byte(data)); err == nil {
			t.Fatal("accepted incomplete doctor report", data)
		}
	}
	root := t.TempDir()
	cfg := filepath.Join(root, "config.json")
	state := filepath.Join(root, "state")
	goodRefusal := privacyFixtureRefusal(cfg, state)
	if err := checkPrivacyRefusal(goodRefusal, cfg, state, values); err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{[]byte(`{}`), []byte(`{`), []byte(`{"error":{"code":"index-missing"}}`), []byte(`{"error":{"code":"privacy-refresh-required"}}`), bytes.ReplaceAll(goodRefusal, []byte("privacy-refresh-required"), []byte(values[0]))} {
		if err := checkPrivacyRefusal(data, cfg, state, values); err == nil {
			t.Fatal("accepted bad privacy refusal", string(data))
		}
	}
}

func TestPrivacyStateScanner(t *testing.T) {
	root := t.TempDir()
	values := privacyFixtures()
	if _, err := scanPrivacyState(root, values); err == nil {
		t.Fatal("empty state accepted")
	}
	path := filepath.Join(root, "file")
	if err := os.WriteFile(path, []byte("allowed"), 0600); err != nil {
		t.Fatal(err)
	}
	if n, err := scanPrivacyState(root, values); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if err := os.WriteFile(path, []byte(values[0]), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := scanPrivacyState(root, values); err == nil {
		t.Fatal("retained secret accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("absent", path); err == nil {
		if _, err := scanPrivacyState(root, values); err == nil {
			t.Fatal("link accepted")
		}
	}
	if _, err := scanPrivacyState(filepath.Join(root, "missing"), values); err == nil {
		t.Fatal("missing state accepted")
	}
}

func TestPrivacyTableAndLegacyFixtures(t *testing.T) {
	root := t.TempDir()
	values := privacyFixtures()
	if err := privacyFixtureDatabase(root, "current", false); err != nil {
		t.Fatal(err)
	}
	if err := checkPrivacyTables(root, values); err != nil {
		t.Fatal(err)
	}
	if err := seedLegacyPrivacy(root, values[2]); err != nil {
		t.Fatal(err)
	}
	if err := checkPrivacyTables(root, values); err == nil {
		t.Fatal("raw logical text accepted")
	}
	if _, err := scanPrivacyState(root, values); err == nil {
		t.Fatal("raw state bytes accepted")
	}
	if err := checkPrivacyNote(root, "legacy.md", "Legacy lanterns keep a saved note."); err != nil {
		t.Fatal(err)
	}
	if err := checkPrivacyNote(root, "missing.md", "control"); err == nil {
		t.Fatal("lost note accepted")
	}
	if err := os.WriteFile(filepath.Join(root, "notes", "legacy.md"), []byte("control control"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := checkPrivacyNote(root, "legacy.md", "control"); err == nil {
		t.Fatal("duplicated note accepted")
	}
	if err := privacyFixtureDatabase(root, "current", true); err != nil {
		t.Fatal(err)
	}
	if err := checkPrivacyTables(root, values); err == nil {
		t.Fatal("indexed private control accepted")
	}
	missing := t.TempDir()
	if err := checkPrivacyTables(missing, values); err == nil {
		t.Fatal("schema-less database accepted")
	}
	if err := seedLegacyPrivacy(missing, values[0]); err == nil {
		t.Fatal("schema-less legacy fixture accepted")
	}
}

func TestPrivacyFixtureJourney(t *testing.T) {
	d := stubFixtureDriver(t, "privacy", "")
	if err := d.privacy(0); err != nil {
		t.Fatal(err)
	}
	if d.summary.SideEffects["privacy_1"] == "" || len(d.summary.Commands) != 19 {
		t.Fatal("incomplete journey", len(d.summary.Commands), d.summary.SideEffects)
	}
}

func TestPrivacyRejectsBrokenCommands(t *testing.T) {
	for _, fault := range []string{"index", "search amber otters", "show", "search converted kestrels", "note", "map", "doctor", "missing-citation", "missing-marker", "bad-note", "split-note", "no-redactions", "state-leak", "wrong-refusal", "unscoped-refusal", "index-2", "index-3", "lost-note", "duplicate-note", "lost-legacy-note", "retained-marker", "retained-stage", "source-write", "config-write", "home-write"} {
		t.Run(fault, func(t *testing.T) {
			d := stubFixtureDriver(t, "privacy", fault)
			if err := d.privacy(0); err == nil {
				t.Fatal("accepted deliberately broken test input", fault)
			}
		})
	}
}

func TestPrivacyProductJourney(t *testing.T) {
	evidence := t.TempDir()
	for _, dir := range []string{"logs", "traces"} {
		if err := os.Mkdir(filepath.Join(evidence, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	scratch, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d := &driver{binary: binary, evidence: evidence, summary: &Summary{Scratch: scratch, SideEffects: map[string]string{}}}
	if err := d.privacy(0); err != nil {
		t.Fatal(err)
	}
}

func privacyFixtureRefusal(config, state string) []byte {
	executable := os.Getenv("BRAIN_VERIFY_LAUNCHER")
	if executable == "" {
		executable, _ = os.Executable()
	}
	data, _ := json.Marshal(map[string]any{"error": map[string]any{"code": "privacy-refresh-required", "message": "Run an explicit index to refresh privacy.", "next": []string{"index"}, "next_argv": [][]string{{executable, "index", "--config", config, "--state-dir", state}}}})
	return data
}

func privacyFixtureDatabase(state, fingerprint string, control bool) error {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Remove(filepath.Join(state, "index.sqlite") + suffix); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	db, err := sql.Open("sqlite", filepath.Join(state, "index.sqlite"))
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE meta(key TEXT PRIMARY KEY,value TEXT); CREATE TABLE files(id INTEGER,path TEXT); CREATE TABLE places(text TEXT); CREATE TABLE chunks(text TEXT,heading TEXT); CREATE TABLE retired(id TEXT PRIMARY KEY,heading TEXT,retired_at REAL); CREATE VIRTUAL TABLE chunk_fts USING fts5(text); INSERT INTO files VALUES(1,'README.md'); INSERT INTO chunks VALUES('Allowed context','Allowed heading'); INSERT INTO places VALUES('Allowed occurrence'); INSERT INTO chunk_fts VALUES('Allowed context'); INSERT INTO meta VALUES('privacy_fingerprint',?)`, fingerprint)
	if err == nil && control {
		_, err = db.Exec("INSERT INTO files VALUES(2,'values.md')")
	}
	return err
}

func privacyFixtureProcess() int {
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	args = args[1:]
	fault := os.Getenv("BRAIN_VERIFY_FAULT")
	if fault != "" && strings.HasPrefix(strings.Join(args, " "), fault) {
		fmt.Print(`{"broken_test_input":true}`)
		return 1
	}
	config, state := "", ""
	for i, arg := range args {
		if arg == "--config" {
			config = args[i+1]
		}
		if arg == "--state-dir" {
			state = args[i+1]
		}
	}
	var cfg struct {
		Sources           []struct{ Path string }
		PrivateValuesFile string `json:"private_values_file"`
	}
	data, err := os.ReadFile(config)
	if err != nil {
		return 1
	}
	if json.Unmarshal(data, &cfg) != nil {
		return 1
	}
	source := cfg.Sources[0].Path
	privateBytes, err := os.ReadFile(cfg.PrivateValuesFile)
	if err != nil {
		return 1
	}
	var private []string
	if json.Unmarshal(privateBytes, &private) != nil {
		return 1
	}
	digest := sha256.Sum256(privateBytes)
	fingerprint := hex.EncodeToString(digest[:])
	values := privacyFixtures()
	clean := func(text string) string {
		for _, value := range append(private, values[2]) {
			rule := "private-value"
			if value == values[2] {
				rule = "github-token"
			}
			text = strings.ReplaceAll(text, value, "[redacted: "+rule+"]")
		}
		return text
	}
	reply := func(value any, exit int) int { data, _ := json.Marshal(value); fmt.Print(string(data)); return exit }
	if args[0] != "index" {
		db, err := sql.Open("sqlite", filepath.Join(state, "index.sqlite"))
		stored := ""
		if err == nil {
			db.QueryRow("SELECT value FROM meta WHERE key='privacy_fingerprint'").Scan(&stored)
			db.Close()
		}
		if stored != fingerprint {
			if fault == "wrong-refusal" {
				fmt.Print(`{"error":{"code":"index-missing"}}`)
				return 1
			}
			if fault == "unscoped-refusal" {
				fmt.Print(`{"error":{"code":"privacy-refresh-required","next":[],"next_argv":[]}}`)
				return 1
			}
			fmt.Print(string(privacyFixtureRefusal(config, state)))
			return 1
		}
	}
	switch args[0] {
	case "index":
		counterPath := filepath.Join(state, "fixture-count")
		data, _ := os.ReadFile(counterPath)
		count := len(data) + 1
		if fault == fmt.Sprintf("index-%d", count) {
			fmt.Print(`{"broken_test_input":true}`)
			return 1
		}
		if err := privacyFixtureDatabase(state, fingerprint, false); err != nil {
			fmt.Fprint(os.Stderr, err)
			return 1
		}
		os.WriteFile(counterPath, []byte(strings.Repeat("x", count)), 0600)
		for _, folder := range []string{"notes", "converted"} {
			path := filepath.Join(state, folder)
			os.MkdirAll(path, 0700)
			names, _ := os.ReadDir(path)
			for _, name := range names {
				data, _ := os.ReadFile(filepath.Join(path, name.Name()))
				os.WriteFile(filepath.Join(path, name.Name()), []byte(clean(string(data))), 0600)
			}
		}
		os.WriteFile(filepath.Join(state, "converted", "current.txt"), []byte(clean("Converted kestrels "+values[0]+" "+values[2])), 0600)
		if fault == "state-leak" {
			os.WriteFile(filepath.Join(state, "retained.txt"), []byte(values[0]), 0600)
		}
		if fault == "source-write" {
			os.WriteFile(filepath.Join(source, "README.md"), []byte("changed"), 0600)
		}
		if fault == "config-write" {
			os.WriteFile(config, append(data, ' '), 0600)
		}
		if fault == "home-write" {
			os.WriteFile(filepath.Join(os.Getenv("HOME"), "unexpected"), []byte("changed"), 0600)
		}
		if count == 3 {
			if fault == "lost-note" {
				os.Remove(filepath.Join(state, "notes", "harbor.md"))
			}
			if fault == "duplicate-note" {
				name := filepath.Join(state, "notes", "harbor.md")
				data, _ := os.ReadFile(name)
				os.WriteFile(name, append(data, data...), 0600)
			}
			if fault == "lost-legacy-note" {
				os.Remove(filepath.Join(state, "notes", "legacy.md"))
			}
			if fault == "retained-marker" {
				os.WriteFile(filepath.Join(state, "privacy-migration.json"), []byte("{}"), 0600)
			}
			if fault == "retained-stage" {
				os.Mkdir(filepath.Join(state, ".privacy-stage"), 0700)
			}
		}
		return reply(map[string]any{"count": 0, "new": 2}, 0)
	case "search":
		path := filepath.Join(source, "README.md")
		switch args[1] {
		case "converted kestrels":
			path = filepath.Join(source, "letters.rtf")
		case "silver badgers", "final pelican":
			path = filepath.Join(state, "notes", "harbor.md")
		case "legacy lanterns":
			path = filepath.Join(state, "notes", "legacy.md")
		}
		if _, err := os.Stat(path); err != nil {
			return reply(map[string]any{"count": 0, "results": []any{}, "index_age_s": 0}, 0)
		}
		if fault == "missing-citation" {
			path = "wrong.md"
		}
		return reply(map[string]any{"count": 1, "index_age_s": 0, "results": []any{map[string]any{"id": "fixture-id", "path": path, "snippet": clean("Allowed " + values[0] + " " + values[2])}}}, 0)
	case "show":
		text := "Allowed [redacted: private-value] [redacted: github-token]"
		if fault == "missing-marker" {
			text = "Allowed"
		}
		return reply(map[string]any{"text": text}, 0)
	case "note":
		if len(args[1]) > 65536 {
			return reply(map[string]any{"error": map[string]any{"code": "note-too-long", "message": "No note saved. Run help note.", "next_argv": [][]string{{"help", "note"}}}}, 2)
		}
		path := filepath.Join(state, "notes", "harbor.md")
		os.MkdirAll(filepath.Dir(path), 0700)
		text := "## 2026-01-07T00:00:00Z\n\n" + clean(args[1])
		if fault == "split-note" {
			text += "\n## extra entry\n"
		}
		os.WriteFile(path, []byte(text), 0600)
		return reply(map[string]any{"path": path, "entry_id": "note-id", "indexed": fault != "bad-note"}, 0)
	case "doctor":
		rules := []string{"private-value", "github-token"}
		if fault == "no-redactions" {
			rules = nil
		}
		return reply(map[string]any{"redactions": []any{map[string]any{"path": "README.md", "rules": rules}}}, 3)
	case "map":
		return reply(map[string]any{"project": "harbor", "purpose": "Allowed map"}, 0)
	}
	return reply(map[string]any{"error": "unsupported fixture command"}, 2)
}
