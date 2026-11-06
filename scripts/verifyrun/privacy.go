package verifyrun

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	_ "modernc.org/sqlite"
)

func privacyFixtures() []string {
	return []string{"Cipher" + "PrivateOne", "Cipher" + "PrivateTwo", "gh" + "p_" + strings.Repeat("A", 36)}
}

func checkPrivacyJSON(data []byte, values []string) error {
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		return err
	}
	if object == nil {
		return errors.New("privacy reply is not an object")
	}
	var check func(any) error
	check = func(value any) error {
		switch item := value.(type) {
		case string:
			for _, secret := range values {
				if strings.Contains(item, secret) {
					return errors.New("reply retained a protected fixture value")
				}
			}
		case []any:
			for _, child := range item {
				if err := check(child); err != nil {
					return err
				}
			}
		case map[string]any:
			for key, child := range item {
				if err := check(key); err != nil {
					return err
				}
				if err := check(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return check(object)
}

func checkPrivacyRefusal(data []byte, config, state string, values []string) error {
	if err := checkPrivacyJSON(data, values); err != nil {
		return err
	}
	var r struct {
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	if r.Error == nil || r.Error.Code != "privacy-refresh-required" {
		return errors.New("unsafe state did not require an explicit privacy refresh")
	}
	return checkScopedNext(data, config, state, true)
}

func checkRedactions(data []byte) error {
	var r struct {
		Redactions []struct {
			Path  string   `json:"path"`
			Rules []string `json:"rules"`
		} `json:"redactions"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	rules := map[string]bool{}
	for _, entry := range r.Redactions {
		if entry.Path == "" || filepath.IsAbs(entry.Path) || len(entry.Rules) == 0 {
			return errors.New("redaction report lost relative file identity or rule names")
		}
		for _, rule := range entry.Rules {
			rules[rule] = true
		}
	}
	if !rules["private-value"] || !rules["github-token"] {
		return errors.New("doctor did not report both applied privacy rules")
	}
	return nil
}

func scanPrivacyState(state string, values []string) (int, error) {
	files := 0
	err := filepath.WalkDir(state, func(path string, entry fs.DirEntry, problem error) error {
		if problem != nil {
			return problem
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return errors.New("privacy state contains an unexpected nonregular file")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files++
		for _, value := range values {
			if bytes.Contains(data, []byte(value)) {
				return errors.New("persistent state retained a protected fixture value")
			}
		}
		return nil
	})
	if err == nil && files == 0 {
		err = errors.New("privacy state scan had no files")
	}
	return files, err
}

func checkPrivacyTables(state string, values []string) error {
	db, err := sql.Open("sqlite", filepath.Join(state, "index.sqlite"))
	if err != nil {
		return err
	}
	defer db.Close()
	for _, table := range []string{"files", "places", "chunks", "retired", "chunk_fts"} {
		rows, err := db.Query("SELECT * FROM " + table)
		if err != nil {
			return err
		}
		cols, err := rows.Columns()
		if err != nil {
			rows.Close()
			return err
		}
		for rows.Next() {
			fields := make([]any, len(cols))
			targets := make([]any, len(cols))
			for i := range fields {
				targets[i] = &fields[i]
			}
			if err := rows.Scan(targets...); err != nil {
				rows.Close()
				return err
			}
			for _, field := range fields {
				var data []byte
				switch text := field.(type) {
				case string:
					data = []byte(text)
				case []byte:
					data = text
				}
				for _, value := range values {
					if bytes.Contains(data, []byte(value)) {
						rows.Close()
						return errors.New("logical index retained a protected fixture value")
					}
				}
			}
		}
		problem := rows.Err()
		rows.Close()
		if problem != nil {
			return problem
		}
	}
	var controls int
	if err := db.QueryRow("SELECT count(*) FROM files WHERE path='values.md'").Scan(&controls); err != nil {
		return err
	}
	if controls != 0 {
		return errors.New("private-values control file became an indexed document")
	}
	return nil
}

func checkPrivacyNote(state, name, marker string) error {
	data, err := os.ReadFile(filepath.Join(state, "notes", name))
	if err != nil {
		return err
	}
	if bytes.Count(data, []byte(marker)) != 1 {
		return errors.New("migration lost or duplicated an allowed saved note")
	}
	return nil
}

func seedLegacyPrivacy(state, secret string) error {
	db, err := sql.Open("sqlite", filepath.Join(state, "index.sqlite"))
	if err != nil {
		return err
	}
	_, err = db.Exec("DELETE FROM meta WHERE key='privacy_fingerprint'; UPDATE chunks SET text=text || ?, heading=heading || ?; UPDATE places SET text=COALESCE(text,'') || ?; INSERT OR REPLACE INTO retired(id,heading,retired_at) VALUES('legacy-privacy-fixture',?,1)", secret, secret, secret, secret)
	err = errors.Join(err, db.Close())
	if err != nil {
		return err
	}
	for _, name := range []string{"converted/legacy-privacy.txt", "notes/legacy.md"} {
		path := filepath.Join(state, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte("# Legacy\n\nLegacy lanterns keep a saved note. "+secret+"\n"), 0600); err != nil {
			return err
		}
	}
	return nil
}

func (d *driver) privacy(attempt int) (problem error) {
	j, err := d.newFeatureJourney("privacy", attempt)
	if err != nil {
		return err
	}
	values := privacyFixtures()
	first, later, token := values[0], values[1], values[2]
	source, err := j.document("harbor", "# Harbor\n\nAmber otters keep allowed context. "+first+" "+token+"\n\nFuture context includes "+later+".\n")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(source, "letters.rtf"), []byte(`{\rtf1\ansi Converted kestrels keep useful text. `+first+" "+token+`}`), 0600); err != nil {
		return err
	}
	control := filepath.Join(source, "values.md")
	if err := writeJSON(control, []string{first}); err != nil {
		return err
	}
	if err := j.writeConfig([]map[string]any{{"path": source, "project": "harbor"}}); err != nil {
		return err
	}
	data, err := os.ReadFile(j.config)
	if err != nil {
		return err
	}
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		return err
	}
	config["private_values_file"] = control
	if err := writeJSON(j.config, config); err != nil {
		return err
	}
	configBefore, err := os.ReadFile(j.config)
	if err != nil {
		return err
	}
	defer func() {
		after, err := os.ReadFile(j.config)
		if err == nil && !bytes.Equal(configBefore, after) {
			err = errors.New("privacy commands changed selected config")
		}
		problem = errors.Join(problem, err)
	}()
	preserveSources, err := j.preserve(fmt.Sprintf("privacy-%d-initial-sources", attempt), j.sources)
	if err != nil {
		return err
	}
	defer func() { problem = errors.Join(problem, preserveSources()) }()
	preserveHome, err := j.preserve(fmt.Sprintf("privacy-%d-home", attempt), j.home)
	if err != nil {
		return err
	}
	defer func() { problem = errors.Join(problem, preserveHome()) }()
	protected := []string{first, token}
	call := func(exit int, args ...string) ([]byte, error) {
		data, err := j.call(exit, args...)
		if err != nil {
			return nil, err
		}
		return data, checkPrivacyJSON(data, protected)
	}
	if _, err := call(0, "index"); err != nil {
		return err
	}
	data, err = call(0, "search", "amber otters")
	if err != nil {
		return err
	}
	id, err := checkCitation(data, filepath.Join(source, "README.md"), "")
	if err != nil {
		return err
	}
	data, err = call(0, "show", id)
	if err != nil {
		return err
	}
	if !bytes.Contains(data, []byte("[redacted: private-value]")) || !bytes.Contains(data, []byte("[redacted: github-token]")) {
		return errors.New("protected text vanished without redaction markers")
	}
	data, err = call(0, "search", "converted kestrels")
	if err != nil {
		return err
	}
	if _, err := checkCitation(data, filepath.Join(source, "letters.rtf"), ""); err != nil {
		return err
	}
	data, err = call(0, "note", "Silver badgers keep saved context. "+first+" "+token+" "+later+"\n"+strings.Repeat("Ordinary saved context remains useful.\n", 100)+"Final pelican context.", "--project", "harbor")
	if err != nil {
		return err
	}
	savedPath, _, err := checkSavedNote(data, j.state)
	if err != nil {
		return err
	}
	saved, err := os.ReadFile(filepath.Join(j.state, "notes", "harbor.md"))
	if err != nil {
		return err
	}
	if bytes.Count(saved, []byte("## ")) != 1 || !bytes.Contains(saved, []byte("Final pelican context.")) {
		return errors.New("long note was split into entries or lost its final text")
	}
	data, err = call(0, "search", "final pelican")
	if err != nil {
		return err
	}
	if _, err := checkCitation(data, savedPath, ""); err != nil {
		return err
	}
	if _, err := call(2, "note", strings.Repeat("界", 65536/3+1)+first+" "+token, "--project", "harbor"); err != nil {
		return err
	}
	if _, err := call(0, "map", "harbor"); err != nil {
		return err
	}
	data, err = call(3, "doctor")
	if err != nil {
		return err
	}
	if err := checkRedactions(data); err != nil {
		return err
	}
	if err := checkPrivacyTables(j.state, protected); err != nil {
		return err
	}
	count, err := scanPrivacyState(j.state, protected)
	if err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(d.evidence, "traces", fmt.Sprintf("privacy-%d-initial-scan.json", attempt)), map[string]any{"files_scanned": count, "protected_values_found": 0}); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(j.state, 0500); err != nil {
			return err
		}
		_, readErr := call(0, "search", "amber otters")
		restoreErr := os.Chmod(j.state, 0700)
		if err := errors.Join(readErr, restoreErr); err != nil {
			return err
		}
	}
	if err := preserveSources(); err != nil {
		return err
	}
	if err := writeJSON(control, []string{first, later}); err != nil {
		return err
	}
	preserveSources, err = j.preserve(fmt.Sprintf("privacy-%d-changed-policy-sources", attempt), j.sources)
	if err != nil {
		return err
	}
	protected = values
	data, err = call(1, "search", "amber otters")
	if err != nil {
		return err
	}
	if err := checkPrivacyRefusal(data, j.config, j.state, protected); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(j.state, 0500); err != nil {
			return err
		}
		data, readErr := call(1, "search", "amber otters")
		restoreErr := os.Chmod(j.state, 0700)
		if err := errors.Join(readErr, restoreErr); err != nil {
			return err
		}
		if err := checkPrivacyRefusal(data, j.config, j.state, protected); err != nil {
			return err
		}
	}
	if _, err := call(0, "index"); err != nil {
		return err
	}
	data, err = call(0, "search", "silver badgers")
	if err != nil {
		return err
	}
	if _, err := checkCitation(data, filepath.Join(j.state, "notes", "harbor.md"), ""); err != nil {
		return err
	}
	if err := checkPrivacyTables(j.state, protected); err != nil {
		return err
	}
	if _, err := scanPrivacyState(j.state, protected); err != nil {
		return err
	}
	if err := seedLegacyPrivacy(j.state, token); err != nil {
		return err
	}
	if _, err := scanPrivacyState(j.state, protected); err == nil {
		return errors.New("legacy fixture did not seed protected persistent text")
	}
	data, err = call(1, "search", "amber otters")
	if err != nil {
		return err
	}
	if err := checkPrivacyRefusal(data, j.config, j.state, protected); err != nil {
		return err
	}
	if _, err := call(0, "index"); err != nil {
		return err
	}
	for query, path := range map[string]string{"amber otters": filepath.Join(source, "README.md"), "silver badgers": filepath.Join(j.state, "notes", "harbor.md"), "legacy lanterns": filepath.Join(j.state, "notes", "legacy.md")} {
		data, err = call(0, "search", query)
		if err != nil {
			return err
		}
		if _, err := checkCitation(data, path, ""); err != nil {
			return err
		}
	}
	if err := checkPrivacyTables(j.state, protected); err != nil {
		return err
	}
	for name, marker := range map[string]string{"harbor.md": "Silver badgers keep saved context.", "legacy.md": "Legacy lanterns keep a saved note."} {
		if err := checkPrivacyNote(j.state, name, marker); err != nil {
			return err
		}
	}
	count, err = scanPrivacyState(j.state, protected)
	if err != nil {
		return err
	}
	for _, name := range []string{"privacy-migration.json", ".privacy-stage"} {
		if _, err := os.Lstat(filepath.Join(j.state, name)); !errors.Is(err, os.ErrNotExist) {
			return errors.New("completed migration retained its marker or staging folder")
		}
	}
	if err := writeJSON(filepath.Join(d.evidence, "traces", fmt.Sprintf("privacy-%d-final-scan.json", attempt)), map[string]any{"files_scanned": count, "protected_values_found": 0, "legacy_seed": "Deliberate fixture mutation inserted raw database, cache and note text."}); err != nil {
		return err
	}
	limitation := "Current and changed-policy read-only checks ran."
	if runtime.GOOS == "windows" {
		limitation = "Read-only mode checks were not run on Windows; permission bits do not prove native access controls."
	}
	d.summary.SideEffects[fmt.Sprintf("privacy_%d", attempt+1)] = "Built-in tokens and private values were absent from replies and remaining state. Source and home snapshots stayed unchanged across deliberate policy edits. Policy refresh and seeded legacy migration retained allowed citations and saved notes. " + limitation
	return nil
}
