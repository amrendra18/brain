package index

import (
	"brain/internal/corpus"
	"brain/internal/platform"
	"brain/internal/redact"
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func privacyPolicy(t *testing.T, values ...string) *redact.Policy {
	t.Helper()
	path := filepath.Join(t.TempDir(), "values.json")
	data, _ := json.Marshal(values)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	policy, err := redact.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func assertPrivateBytes(t *testing.T, state string, values ...string) {
	t.Helper()
	err := filepath.WalkDir(state, func(path string, item os.DirEntry, problem error) error {
		if problem != nil {
			return problem
		}
		if item.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, value := range values {
			if bytes.Contains(data, []byte(value)) {
				t.Errorf("protected fixture bytes remain in %s", filepath.Base(path))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSecretRedactionBeforeStorage(t *testing.T) {
	source, state, db, config := fixture(t)
	token := "AK" + "IA" + strings.Repeat("Q", 16)
	write(t, source, "README.md", "# Harbor\n\nAllowed lighthouse "+token+" remains nearby.\n")
	refresh(t, db, state, config, false, 0)
	var text string
	if err := db.QueryRow("SELECT text FROM chunks WHERE text LIKE '%lighthouse%'").Scan(&text); err != nil || !strings.Contains(text, "[redacted: aws-access-key]") || !strings.Contains(text, "Allowed lighthouse") {
		t.Fatal(text, err)
	}
	rules, err := Redactions(db)
	if err != nil || len(rules) != 1 || len(rules[0].Rules) != 1 {
		t.Fatal(rules, err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	assertPrivateBytes(t, state, token)
	data, _ := os.ReadFile(filepath.Join(source, "README.md"))
	if !bytes.Contains(data, []byte(token)) {
		t.Fatal("source was changed")
	}
}

func TestConvertedCacheRedacted(t *testing.T) {
	source, state, db, config := fixture(t)
	value := "CedarConfidentialValue"
	config.Policy = privacyPolicy(t, value)
	write(t, source, "talk.rtf", `{\rtf1 Allowed lighthouse `+value+`\par}`)
	refresh(t, db, state, config, false, 0)
	refresh(t, db, state, config, true, 0)
	files, err := os.ReadDir(filepath.Join(state, "converted"))
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	data, err := os.ReadFile(filepath.Join(state, "converted", files[0].Name()))
	if err != nil || !bytes.Contains(data, []byte("Allowed lighthouse")) || !bytes.Contains(data, []byte("[redacted: private-value]")) {
		t.Fatal(string(data), err)
	}
	db.Close()
	assertPrivateBytes(t, state, value)
}

func TestPrivacyPolicyChangeRebuilds(t *testing.T) {
	source, state, db, config := fixture(t)
	value := "CedarConfidentialValue"
	write(t, source, "README.md", "# Harbor\n\nAllowed lighthouse "+value)
	refresh(t, db, state, config, false, 0)
	db.Close()
	config.Policy = privacyPolicy(t, value)
	if needed, err := PrivacyRequired(state, config.Privacy()); err != nil || !needed {
		t.Fatal(needed, err)
	}
	report, err := MigrateLocked(state, config)
	if err != nil || report.New != 1 {
		t.Fatal(report, err)
	}
	if needed, err := PrivacyRequired(state, config.Privacy()); err != nil || needed {
		t.Fatal(needed, err)
	}
	assertPrivateBytes(t, state, value)
}

func TestPrivacyMigrationInterrupted(t *testing.T) {
	stages := []string{"marked", "notes-staged", "database-staged", "note-published", "notes-published", "cache-removed", "cache-published", "old-index.sqlite-removed", "old-index.sqlite-wal-removed", "old-index.sqlite-shm-removed", "old-index.sqlite-journal-removed", "database-published", "stage-removed"}
	for _, stage := range stages {
		t.Run(stage, func(t *testing.T) {
			source, state, db, config := fixture(t)
			value := "CedarConfidentialValue"
			write(t, source, "README.md", "# Harbor\n\nAllowed lighthouse "+value)
			refresh(t, db, state, config, false, 0)
			if _, err := db.Exec("DELETE FROM meta WHERE key='privacy_fingerprint'; INSERT INTO retired(id,heading,retired_at) VALUES('old',?,1)", value); err != nil {
				t.Fatal(err)
			}
			db.Close()
			write(t, state, "notes/harbor.md", "## 2026-01-07T00:00:00Z\n\nAllowed saved note "+value+"\n")
			write(t, state, "converted/old.txt", value)
			config.Sources = append(config.Sources, corpus.Source{Path: filepath.Join(state, "notes"), Kind: "note"})
			config.Policy = privacyPolicy(t, value)
			stopped := errors.New("test interruption")
			_, err := migrateLocked(state, config, func(boundary string) error {
				if boundary == stage {
					return stopped
				}
				return nil
			})
			if !errors.Is(err, stopped) {
				t.Fatal("boundary was not reached", err)
			}
			if needed, err := PrivacyRequired(state, config.Privacy()); err != nil || !needed {
				t.Fatal("interrupted state was readable", needed, err)
			}
			if _, err = MigrateLocked(state, config); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(state, "notes", "harbor.md"))
			if err != nil || strings.Count(string(data), "Allowed saved note") != 1 {
				t.Fatal("note lost or duplicated", string(data), err)
			}
			assertPrivateBytes(t, state, value)
			reopened, err := OpenStored(state)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			var count int
			if err = reopened.QueryRow("SELECT count(*) FROM chunks WHERE text LIKE '%lighthouse%'").Scan(&count); err != nil || count != 1 {
				t.Fatal("allowed control lost", count, err)
			}
		})
	}
}

func TestPrivacyMigrationRefusals(t *testing.T) {
	_, state, db, config := fixture(t)
	db.Close()
	if err := os.Mkdir(filepath.Join(state, privacyStage), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateLocked(state, config); !errors.Is(err, platform.ErrStateUnsafe) {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(state, privacyStage))
	if err := os.WriteFile(filepath.Join(state, privacyMarker), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateLocked(state, config); !errors.Is(err, ErrPrivacyRefresh) {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(state, privacyMarker))
	write(t, state, "notes/unknown.txt", "allowed text")
	if _, err := MigrateLocked(state, config); !errors.Is(err, ErrPrivacyPath) {
		t.Fatal(err)
	}
}

func TestPrivacyMetadataFailures(t *testing.T) {
	_, state, db, config := fixture(t)
	if err := CheckPrivacy(db, config.Privacy()); !errors.Is(err, ErrPrivacyRefresh) {
		t.Fatal(err)
	}
	if rows, err := Redactions(db); err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
	db.Close()
	if _, err := Redactions(db); err == nil {
		t.Fatal("closed database accepted")
	}
	if err := setPrivacy(db, config.Privacy()); err == nil {
		t.Fatal("closed database accepted")
	}
	if err := preparePrivacy(db, config.Privacy()); err == nil {
		t.Fatal("closed database accepted")
	}
	broken, err := sql.Open("sqlite", filepath.Join(state, "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer broken.Close()
	if _, err = broken.Exec("DROP TABLE meta"); err != nil {
		t.Fatal(err)
	}
	if _, err := PrivacyRequired(state, config.Privacy()); err != nil && !errors.Is(err, ErrMissing) {
		t.Fatal(err)
	}
}

func TestPrivacyMigrationUnavailableSource(t *testing.T) {
	source, state, db, config := fixture(t)
	secret := "old-private-fixture"
	write(t, source, "README.md", "Old content "+secret)
	refresh(t, db, state, config, false, 0)
	if _, err := db.Exec("DELETE FROM meta WHERE key='privacy_fingerprint'"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if err := os.Rename(source, source+"-unavailable"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Rename(source+"-unavailable", source) })
	available := t.TempDir()
	write(t, available, "README.md", "Allowed lighthouse")
	config.Sources = append(config.Sources, corpus.Source{Path: available, Project: "ordinary-client"})
	config.Policy = privacyPolicy(t, secret)
	report, err := MigrateLocked(state, config)
	if err != nil || len(report.Skipped) != 1 || report.Skipped[0].Reason != "unreadable" {
		t.Fatal(report, err)
	}
	db, err = Open(state, false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	var text, project string
	if err = db.QueryRow("SELECT chunks.text,files.project FROM chunks JOIN files ON files.id=chunks.file").Scan(&text, &project); err != nil || !strings.Contains(text, "lighthouse") || project != "ordinary-client" {
		t.Fatal(text, project, err)
	}
	gaps, err := StoredGaps(db)
	if err != nil || len(gaps.Items) != 1 {
		t.Fatal(gaps, err)
	}
	db.Close()
	assertPrivateBytes(t, state, secret)
}

func TestPrivacyMigrationKilled(t *testing.T) {
	if state := os.Getenv("BRAIN_PRIVACY_KILL_STATE"); state != "" {
		data, err := os.ReadFile(os.Getenv("BRAIN_PRIVACY_KILL_CONFIG"))
		if err != nil {
			t.Fatal(err)
		}
		var config corpus.Config
		if err = json.Unmarshal(data, &config); err != nil {
			t.Fatal(err)
		}
		config.Policy, err = redact.Load(config.PrivateValuesFile)
		if err != nil {
			t.Fatal(err)
		}
		lease, err := platform.PrivacyLock(state, true, true)
		if err != nil {
			t.Fatal(err)
		}
		defer lease.Close()
		lock, err := platform.Acquire(state, "index", true)
		if err != nil {
			t.Fatal(err)
		}
		defer lock.Close()
		_, err = migrateLocked(state, config, func(stage string) error {
			if stage == "database-published" {
				if err := os.WriteFile(os.Getenv("BRAIN_PRIVACY_KILL_SIGNAL"), []byte("ready"), 0600); err != nil {
					return err
				}
				for {
					time.Sleep(time.Second)
				}
			}
			return nil
		})
		t.Fatal("child did not stop at boundary", err)
	}
	source, state, db, config := fixture(t)
	secret := "LegacyPrivateFixture"
	write(t, source, "README.md", "Allowed lighthouse "+secret)
	refresh(t, db, state, config, false, 0)
	if _, err := db.Exec("DELETE FROM meta WHERE key='privacy_fingerprint'"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	write(t, state, "notes/harbor.md", "## 2026-01-07T00:00:00Z\n\nAllowed saved note "+secret+"\n")
	config.Sources = append(config.Sources, corpus.Source{Path: filepath.Join(state, "notes"), Kind: "note"})
	folder := t.TempDir()
	config.PrivateValuesFile = filepath.Join(folder, "values.json")
	data, _ := json.Marshal([]string{secret})
	if err := os.WriteFile(config.PrivateValuesFile, data, 0600); err != nil {
		t.Fatal(err)
	}
	config.Policy, _ = redact.Load(config.PrivateValuesFile)
	configPath := filepath.Join(folder, "config.json")
	data, _ = json.Marshal(config)
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	signal := filepath.Join(folder, "ready")
	command := exec.Command(os.Args[0], "-test.run=^TestPrivacyMigrationKilled$")
	command.Env = append(os.Environ(), "BRAIN_PRIVACY_KILL_STATE="+state, "BRAIN_PRIVACY_KILL_CONFIG="+configPath, "BRAIN_PRIVACY_KILL_SIGNAL="+signal)
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { command.Process.Kill() })
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(signal); err == nil {
			break
		}
		if time.Now().After(deadline) {
			command.Process.Kill()
			command.Wait()
			t.Fatal("child boundary timeout", output.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("child was not killed")
	}
	if required, err := PrivacyRequired(state, config.Privacy()); err != nil || !required {
		t.Fatal(required, err)
	}
	lease, err := platform.PrivacyLock(state, true, false)
	if err != nil {
		t.Fatal("killed process retained privacy lease", err)
	}
	if _, err = MigrateLocked(state, config); err != nil {
		t.Fatal(err)
	}
	lease.Close()
	data, err = os.ReadFile(filepath.Join(state, "notes", "harbor.md"))
	if err != nil || strings.Count(string(data), "Allowed saved note") != 1 {
		t.Fatal(string(data), err)
	}
	assertPrivateBytes(t, state, secret)
}

func TestPrivacyRequiredReaderModes(t *testing.T) {
	for _, mode := range []string{"stored", "index-unlocked", "index-locked"} {
		t.Run(mode, func(t *testing.T) {
			source, state, db, config := fixture(t)
			write(t, source, "README.md", "# Harbor\n\nAllowed lighthouse.\n")
			refresh(t, db, state, config, false, 0)
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			check := PrivacyRequiredStored
			if mode != "stored" {
				check = func(state string, policy *redact.Policy) (bool, error) {
					return PrivacyRequiredForIndex(state, policy, mode == "index-locked")
				}
			}
			if mode == "index-locked" {
				lock, err := platform.Acquire(state, "index", false)
				if err != nil {
					t.Fatal(err)
				}
				defer lock.Close()
			}
			if required, err := check(state, config.Privacy()); err != nil || required {
				t.Fatal("current policy requires refresh", required, err)
			}
			changed := privacyPolicy(t, "lighthouse")
			if required, err := check(state, changed); err != nil || !required {
				t.Fatal("changed policy accepted stale content", required, err)
			}
			if err := os.WriteFile(filepath.Join(state, privacyMarker), []byte("interrupted migration"), 0600); err != nil {
				t.Fatal(err)
			}
			if required, err := check(state, config.Privacy()); err != nil || !required {
				t.Fatal("interrupted migration accepted", required, err)
			}
		})
	}
}
