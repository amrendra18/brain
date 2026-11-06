package cli

import (
	"brain/internal/index"
	"brain/internal/notes"
	"brain/internal/platform"
	"brain/tests/testutil"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func setPrivateValues(t *testing.T, folder, path string, values ...string) {
	t.Helper()
	data, _ := json.Marshal(values)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(folder, "config.json")
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err = json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	config["private_values_file"] = path
	data, _ = json.Marshal(config)
	if err = os.WriteFile(name, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func privateStateBytes(t *testing.T, folder, value string) {
	t.Helper()
	err := filepath.WalkDir(folder, func(path string, item os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if item.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte(value)) {
			t.Errorf("protected fixture bytes in %s", filepath.Base(path))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPrivateValuesNeverIndexed(t *testing.T) {
	source, folder, run := freshnessFixture(t)
	value := "CedarConfidentialValue"
	path := filepath.Join(source, "values.md")
	setPrivateValues(t, folder, path, value)
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("# Harbor\n\nAllowed lighthouse "+value), 0600); err != nil {
		t.Fatal(err)
	}
	if code, out := run("index"); code != 0 {
		t.Fatal(code, out)
	}
	for _, args := range [][]string{{"search", value}, {"search", "lighthouse"}, {"projects"}, {"recent"}, {"status"}, {"brief"}, {"map", "source"}, {"doctor"}} {
		code, out := run(args...)
		if code != 0 && code != 3 {
			t.Fatal(args, code, out)
		}
		if strings.Contains(out, value) {
			t.Fatal("protected value in output", args)
		}
	}
	db, err := index.OpenStored(folder)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow("SELECT count(*) FROM files WHERE path='values.md'").Scan(&count); err != nil || count != 0 {
		t.Fatal("private control file indexed", count, err)
	}
	db.Close()
	privateStateBytes(t, folder, value)
	data, _ := os.ReadFile(path)
	if !bytes.Contains(data, []byte(value)) {
		t.Fatal("private control changed")
	}
}

func TestNoteAndMapRedaction(t *testing.T) {
	source, folder, run := freshnessFixture(t)
	token := "AK" + "IA" + strings.Repeat("Q", 16)
	if code, out := run("index"); code != 0 {
		t.Fatal(code, out)
	}
	testutil.Repository(t, source, "Client Harbor "+strings.Repeat("x", 175)+" "+token)
	if platform.GitPath() != "" {
		code, out := run("map", "source")
		if code != 0 || !strings.Contains(out, "Client Harbor") || strings.Contains(out, token[:10]) {
			t.Fatal(code, out)
		}
	}
	if code, out := run("note", "Allowed saved lighthouse "+token, "--project", "source"); code != 0 || strings.Contains(out, token) {
		t.Fatal(code, out)
	}
	for _, args := range [][]string{{"search", "saved lighthouse"}, {"map", "source"}, {"brief"}, {"doctor"}} {
		code, out := run(args...)
		if code != 0 && code != 3 || strings.Contains(out, token) {
			t.Fatal(args, code, out)
		}
	}
	code, out := run("note", strings.Repeat("x", notes.LimitBytes)+token, "--project", "source")
	var failure refusal
	if json.Unmarshal([]byte(out), &failure) != nil || code != 2 || failure.Error.Code != "note-too-long" || strings.Contains(out, token) {
		t.Fatal(code, out)
	}
	if len(failure.Error.NextArgv) != 1 || !strings.Contains(out, "help note") {
		t.Fatal("missing safe note help", out)
	}
	for _, argv := range failure.Error.NextArgv {
		var result bytes.Buffer
		if code := Run(argv[1:], &result); code != 0 {
			t.Fatal(code, result.String())
		}
	}
	privateStateBytes(t, folder, token)
}

func TestDoctorRedactions(t *testing.T) {
	source, _, run := freshnessFixture(t)
	token := "gh" + "p_" + strings.Repeat("q", 36)
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("# Harbor\n\nAllowed lighthouse "+token), 0600); err != nil {
		t.Fatal(err)
	}
	if code, out := run("index"); code != 0 {
		t.Fatal(code, out)
	}
	code, out := run("doctor")
	var reply doctorReply
	if err := json.Unmarshal([]byte(out), &reply); err != nil || code != 3 || len(reply.Redactions) != 1 || reply.Redactions[0].Path != "README.md" || strings.Join(reply.Redactions[0].Rules, ",") != "github-token" || strings.Contains(out, token) {
		t.Fatal(code, out, err)
	}
}

func TestPrivacyReadOnlyRefusal(t *testing.T) {
	_, folder, run := freshnessFixture(t)
	if code, out := run("index"); code != 0 {
		t.Fatal(code, out)
	}
	if err := testutil.Permissions(t, folder, 0500); err != nil {
		t.Fatal(err)
	}
	defer testutil.Permissions(t, folder, 0700)
	if code, out := run("search", "skylark"); code != 0 || !strings.Contains(out, `"read_only":true`) {
		t.Fatal(code, out)
	}
	testutil.Permissions(t, folder, 0700)
	setPrivateValues(t, folder, filepath.Join(t.TempDir(), "values.json"), "CedarConfidentialValue")
	testutil.Permissions(t, folder, 0500)
	for _, args := range [][]string{{"search", "skylark"}, {"show", "missing"}, {"projects"}, {"recent"}, {"status"}, {"map", "source"}, {"note", "allowed", "--project", "source"}} {
		code, out := run(args...)
		if code != 1 || !strings.Contains(out, "privacy-refresh-required") || strings.Contains(out, "Original skylark") {
			t.Fatal(args, code, out)
		}
	}
	if code, out := run("doctor"); code != 1 || !strings.Contains(out, "privacy-refresh-required") {
		t.Fatal(code, out)
	}
	if code, out := run("brief"); code != 0 || !strings.Contains(out, `"ready":false`) {
		t.Fatal(code, out)
	}
	if code, out := run("index"); code != 1 || !strings.Contains(out, "state-unwritable") {
		t.Fatal(code, out)
	}
}

func TestPrivacyProtectedPaths(t *testing.T) {
	if os.Getenv("BRAIN_PRIVACY_PATH_CHILD") == "1" {
		os.Exit(Run([]string{"search", "allowed", "--format", "json"}, os.Stdout))
	}
	t.Run("individual", func(t *testing.T) {
		source, folder, run := freshnessFixture(t)
		value := "CedarConfidentialValue"
		setPrivateValues(t, folder, filepath.Join(t.TempDir(), "values.json"), value)
		if err := os.WriteFile(filepath.Join(source, value+".md"), []byte("forbidden document"), 0600); err != nil {
			t.Fatal(err)
		}
		if code, out := run("index"); code != 1 || strings.Contains(out, value) || !strings.Contains(out, "privacy-protected-path") {
			t.Fatal(code, out)
		}
		if code, out := run("search", "skylark"); code != 0 || !strings.Contains(out, "Original skylark") || strings.Contains(out, value) {
			t.Fatal(code, out)
		}
		privateStateBytes(t, folder, value)
	})
	for _, kind := range []string{"source", "label", "state", "config", "note", "executable"} {
		t.Run(kind, func(t *testing.T) {
			source, folder, run := freshnessFixture(t)
			value := "CedarConfidentialValue"
			setPrivateValues(t, folder, filepath.Join(t.TempDir(), "values.json"), value)
			config := filepath.Join(folder, "config.json")
			args := []string{"index", "--state-dir", folder, "--config", config, "--format", "json"}
			switch kind {
			case "source", "label":
				data, _ := os.ReadFile(config)
				var cfg map[string]any
				json.Unmarshal(data, &cfg)
				sources := cfg["sources"].([]any)
				item := sources[0].(map[string]any)
				if kind == "source" {
					item["path"] = filepath.Join(source, value)
				} else {
					item["project"] = value
				}
				data, _ = json.Marshal(cfg)
				os.WriteFile(config, data, 0600)
			case "state":
				moved := filepath.Join(filepath.Dir(folder), value)
				if err := os.Rename(folder, moved); err != nil {
					t.Fatal(err)
				}
				folder = moved
				args[2] = folder
				args[4] = filepath.Join(folder, "config.json")
			case "config":
				moved := filepath.Join(filepath.Dir(folder), value+".json")
				if err := os.Rename(config, moved); err != nil {
					t.Fatal(err)
				}
				args[4] = moved
			case "note":
				args = []string{"note", "allowed note", "--project", value, "--state-dir", folder, "--format", "json"}
			case "executable":
				binary := filepath.Join(t.TempDir(), value)
				if filepath.Ext(os.Args[0]) == ".exe" {
					binary += ".exe"
				}
				data, err := os.ReadFile(os.Args[0])
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(binary, data, 0700); err != nil {
					t.Fatal(err)
				}
				command := exec.Command(binary, "-test.run=^TestPrivacyProtectedPaths$")
				command.Env = append(os.Environ(), "BRAIN_PRIVACY_PATH_CHILD=1", "PATH=")
				output, err := command.Output()
				var exit *exec.ExitError
				if err == nil || !errors.As(err, &exit) || exit.ExitCode() != 1 {
					t.Fatal(err, string(output))
				}
				var failed refusal
				if json.Unmarshal(output, &failed) != nil || failed.Error.Code != "privacy-protected-path" || len(failed.Error.NextArgv) != 0 || strings.Contains(string(output), value) {
					t.Fatal(string(output))
				}
				return
			}
			var out bytes.Buffer
			code := Run(args, &out)
			if code != 1 || !strings.Contains(out.String(), "privacy-protected-path") || strings.Contains(out.String(), value) {
				t.Fatal(kind, code, out.String())
			}
			_ = run
		})
	}
}

type privacyPausedWriter struct {
	buffer  bytes.Buffer
	entered chan struct{}
	release chan struct{}
}

func (w *privacyPausedWriter) Write(data []byte) (int, error) {
	close(w.entered)
	<-w.release
	return w.buffer.Write(data)
}

func TestPrivacyMigrationConcurrentCommands(t *testing.T) {
	_, folder, run := freshnessFixture(t)
	if code, out := run("index"); code != 0 {
		t.Fatal(code, out)
	}
	writer := &privacyPausedWriter{entered: make(chan struct{}), release: make(chan struct{})}
	reader := make(chan int, 1)
	go func() { reader <- Run([]string{"search", "skylark", "--format", "json"}, writer) }()
	select {
	case <-writer.entered:
	case <-time.After(time.Second):
		t.Fatal("reader never reached output")
	}
	setPrivateValues(t, folder, filepath.Join(t.TempDir(), "values.json"), "new-private-fixture")
	type result struct {
		code int
		text string
	}
	indexed := make(chan result, 1)
	go func() { code, out := run("index"); indexed <- result{code, out} }()
	select {
	case got := <-indexed:
		t.Fatal("migration crossed an active reader", got)
	case <-time.After(100 * time.Millisecond):
	}
	note := make(chan result, 1)
	go func() {
		code, out := run("note", "Concurrent allowed lighthouse", "--project", "source")
		note <- result{code, out}
	}()
	close(writer.release)
	if code := <-reader; code != 0 || !strings.Contains(writer.buffer.String(), "skylark") {
		t.Fatal(code, writer.buffer.String())
	}
	got := <-indexed
	if got.code != 0 {
		t.Fatal(got)
	}
	got = <-note
	if got.code == 1 && strings.Contains(got.text, "privacy-refresh-required") {
		got.code, got.text = run("note", "Concurrent allowed lighthouse", "--project", "source")
	}
	if got.code != 0 {
		t.Fatal(got)
	}
	data, err := os.ReadFile(filepath.Join(folder, "notes", "source.md"))
	if err != nil || strings.Count(string(data), "Concurrent allowed lighthouse") != 1 {
		t.Fatal(string(data), err)
	}
}

func TestPrivacySourceCitation(t *testing.T) {
	source, _, run := freshnessFixture(t)
	token := "AK" + "IA" + strings.Repeat("Q", 16)
	path := filepath.Join(source, "Client Harbor.md")
	original := "# Client Harbor\n\nAllowed lighthouse " + token + "\n"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if code, out := run("index"); code != 0 {
		t.Fatal(code, out)
	}
	code, out := run("search", "lighthouse")
	var result searchReply
	if err := json.Unmarshal([]byte(out), &result); err != nil || code != 0 || len(result.Results) != 1 {
		t.Fatal(code, out, err)
	}
	cited := result.Results[0]
	want, _ := filepath.EvalSymlinks(path)
	if cited.Path != want || cited.Heading != "Client Harbor" || strings.Contains(out, token) {
		t.Fatal(out)
	}
	data, err := os.ReadFile(cited.Path)
	if err != nil || string(data) != original {
		t.Fatal("citation did not recover unchanged original", err)
	}
	code, out = run("show", cited.ID)
	if code != 0 || strings.Contains(out, token) || !strings.Contains(out, "[redacted: aws-access-key]") {
		t.Fatal(code, out)
	}
}

func TestStateModesAndLocalOnly(t *testing.T) {
	_, folder, run := freshnessFixture(t)
	if code, out := run("index"); code != 0 {
		t.Fatal(code, out)
	}
	if code, out := run("note", "Allowed saved lighthouse", "--project", "source"); code != 0 {
		t.Fatal(code, out)
	}
	err := filepath.WalkDir(folder, func(path string, item os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if item.IsDir() {
			return platform.CheckPrivateState(path)
		}
		relative, err := filepath.Rel(folder, path)
		if err != nil {
			return err
		}
		return platform.CheckPrivateFile(folder, relative)
	})
	if err != nil {
		t.Fatal("owned state permissions", err)
	}
	remote := testutil.RemoteState(runtime.GOOS)
	for _, verb := range []string{"init", "index", "note"} {
		args := []string{verb, "--state-dir", remote}
		if verb == "note" {
			args = append(args, "Allowed text", "--project", "source")
		}
		code, out := run(args...)
		if code != 1 || !strings.Contains(out, "state-remote") {
			t.Fatal(verb, code, out)
		}
	}
}

func TestPrivacyStatelessInput(t *testing.T) {
	token := "AK" + "IA" + strings.Repeat("Q", 16)
	for _, args := range [][]string{{token}, {"help", token}, {"--version", "--format", token}} {
		var out bytes.Buffer
		if code := Run(args, &out); code != 2 || strings.Contains(out.String(), token) {
			t.Fatal(code, out.String())
		}
	}
}

func TestPrivacyMissingDatabaseKeepsLegacyNotesBlocked(t *testing.T) {
	_, folder, run := freshnessFixture(t)
	if err := os.MkdirAll(filepath.Join(folder, "notes"), 0700); err != nil {
		t.Fatal(err)
	}
	token := "AK" + "IA" + strings.Repeat("Q", 16)
	name := filepath.Join(folder, "notes", "source.md")
	if err := os.WriteFile(name, []byte("## 2026-01-07T00:00:00Z\n\nAllowed legacy lighthouse "+token+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	code, out := run("note", "Allowed new text", "--project", "source")
	if code != 1 || !strings.Contains(out, "privacy-refresh-required") {
		t.Fatal(code, out)
	}
	if code, out = run("index"); code != 0 {
		t.Fatal(code, out)
	}
	if code, out = run("note", "Allowed new text", "--project", "source"); code != 0 {
		t.Fatal(code, out)
	}
	data, err := os.ReadFile(name)
	if err != nil || strings.Count(string(data), "Allowed legacy lighthouse") != 1 || strings.Count(string(data), "Allowed new text") != 1 {
		t.Fatal(string(data), err)
	}
	privateStateBytes(t, folder, token)
}

func TestPrivacyMissingDatabaseBlocksArtifacts(t *testing.T) {
	for _, name := range []string{".privacy-stage", "index.sqlite-wal", "index.sqlite-shm", "index.sqlite-journal"} {
		t.Run(name, func(t *testing.T) {
			_, folder, run := freshnessFixture(t)
			path := filepath.Join(folder, name)
			token := "gh" + "p_" + strings.Repeat("Q", 36)
			if name == ".privacy-stage" {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(path, "leftover.txt")
			}
			if err := os.WriteFile(path, []byte("Legacy allowed context "+token), 0600); err != nil {
				t.Fatal(err)
			}
			code, out := run("note", "Allowed new note", "--project", "source")
			if code != 1 || !strings.Contains(out, "privacy-refresh-required") || strings.Contains(out, token) {
				t.Fatal(code, out)
			}
			if _, err := os.Stat(filepath.Join(folder, "notes", "source.md")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("note written before refresh", err)
			}
			if _, err := os.Stat(filepath.Join(folder, "index.sqlite")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("database initialized before refresh", err)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != "Legacy allowed context "+token {
				t.Fatal("refusal changed legacy artifact", err)
			}
		})
	}
	t.Run("empty state accepts sanitized note", func(t *testing.T) {
		_, folder, run := freshnessFixture(t)
		token := "gh" + "p_" + strings.Repeat("Q", 36)
		if code, out := run("note", "Allowed new lighthouse "+token, "--project", "source"); code != 0 {
			t.Fatal(code, out)
		}
		data, err := os.ReadFile(filepath.Join(folder, "notes", "source.md"))
		if err != nil || !strings.Contains(string(data), "Allowed new lighthouse") || strings.Contains(string(data), token) {
			t.Fatal("safe empty-state control", err)
		}
	})
}

func TestDoctorReportsAppliedOverlapLabel(t *testing.T) {
	source, folder, run := freshnessFixture(t)
	literal := strings.Repeat("Q", 20)
	token := "gh" + "p_" + strings.Repeat("Q", 36)
	setPrivateValues(t, folder, filepath.Join(t.TempDir(), "values.json"), literal)
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("# Harbor\n\nAllowed lighthouse "+token), 0600); err != nil {
		t.Fatal(err)
	}
	if code, out := run("index"); code != 0 {
		t.Fatal(code, out)
	}
	code, out := run("doctor")
	var report doctorReply
	if code != 3 || json.Unmarshal([]byte(out), &report) != nil || len(report.Redactions) != 1 || strings.Join(report.Redactions[0].Rules, ",") != "github-token" {
		t.Fatal(code, out)
	}
	if code, out := run("search", "lighthouse"); code != 0 || !strings.Contains(out, "lighthouse") || strings.Contains(out, literal) {
		t.Fatal(code, out)
	}
	privateStateBytes(t, folder, literal)
}

func TestPrivateValuesOutsideOwnedState(t *testing.T) {
	for _, name := range []string{"notes/control.md", "converted/control.json", "control.json"} {
		t.Run(name, func(t *testing.T) {
			_, folder, run := freshnessFixture(t)
			control := filepath.Join(folder, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(control), 0700); err != nil {
				t.Fatal(err)
			}
			setPrivateValues(t, folder, control, "ProtectedControlFixture")
			before, err := os.ReadFile(control)
			if err != nil {
				t.Fatal(err)
			}
			for _, verb := range []string{"index", "doctor", "search"} {
				args := []string{verb}
				if verb == "search" {
					args = append(args, "allowed")
				}
				code, out := run(args...)
				if code != 1 || !strings.Contains(out, "privacy-policy-invalid") || strings.Contains(out, "ProtectedControlFixture") {
					t.Fatal(verb, code, out)
				}
				after, err := os.ReadFile(control)
				if err != nil || !bytes.Equal(before, after) {
					t.Fatal("control input changed", err)
				}
			}
			if _, err := os.Stat(filepath.Join(folder, "index.sqlite")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("refusal created index", err)
			}
		})
	}
}
