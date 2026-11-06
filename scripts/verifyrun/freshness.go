package verifyrun

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type freshnessReply struct {
	Age      *int64 `json:"index_age_s"`
	Complete *bool  `json:"index_complete"`
}

func checkFreshness(data []byte, minimum int64) error {
	var r freshnessReply
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	if r.Age == nil || *r.Age < minimum || r.Complete == nil || !*r.Complete {
		return errors.New("read lost complete-index age or advanced full-source freshness")
	}
	return nil
}

func checkStoredStatus(data []byte) error {
	var r struct {
		Index struct {
			PendingKnown  *bool  `json:"pending_known"`
			IndexComplete *bool  `json:"index_complete"`
			Age           *int64 `json:"index_age_s"`
		} `json:"index"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	if r.Index.PendingKnown == nil || *r.Index.PendingKnown || r.Index.IndexComplete == nil || !*r.Index.IndexComplete || r.Index.Age == nil {
		return errors.New("status must distinguish stored freshness from unknown pending files")
	}
	return nil
}

func checkSavedNote(data []byte, state string) (string, string, error) {
	var r struct {
		Path    string `json:"path"`
		ID      string `json:"entry_id"`
		Indexed bool   `json:"indexed"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return "", "", err
	}
	relative, err := filepath.Rel(filepath.Join(state, "notes"), r.Path)
	if err != nil || !filepath.IsLocal(relative) || relative == "." || r.ID == "" || !r.Indexed {
		return "", "", errors.New("saved note must be immediately indexed inside selected state")
	}
	return r.Path, r.ID, nil
}

func (d *driver) freshness(attempt int) (problem error) {
	j, err := d.newFeatureJourney("freshness", attempt)
	if err != nil {
		return err
	}
	source, err := j.document("harbor", "# Harbor\n\nAmber otters carry the original ledger.\n")
	if err != nil {
		return err
	}
	unchanged, err := j.document("retained", "# Retained\n\nViolet herons preserve unrelated files.\n")
	if err != nil {
		return err
	}
	if err := j.writeConfig([]map[string]any{{"path": source, "project": "harbor"}, {"path": unchanged, "project": "retained"}}); err != nil {
		return err
	}
	preserveHome, err := j.preserve(fmt.Sprintf("freshness-%d-home", attempt), j.home)
	if err != nil {
		return err
	}
	defer func() { problem = errors.Join(problem, preserveHome()) }()
	if _, err := j.call(0, "index"); err != nil {
		return err
	}
	data, err := j.call(0, "search", "amber otters")
	if err != nil {
		return err
	}
	oldID, err := checkCitation(data, filepath.Join(source, "README.md"), "")
	if err != nil {
		return err
	}
	data, err = j.call(0, "search", "violet herons")
	if err != nil {
		return err
	}
	retainedID, err := checkCitation(data, filepath.Join(unchanged, "README.md"), "")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("# Harbor\n\nCopper falcons carry the revised ledger.\n"), 0600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(source, "ADDED.md"), []byte("# Added\n\nSilver turtles reveal explicit indexing.\n"), 0600); err != nil {
		return err
	}
	preserveSources, err := j.preserve(fmt.Sprintf("freshness-%d-sources", attempt), j.sources)
	if err != nil {
		return err
	}
	defer func() { problem = errors.Join(problem, preserveSources()) }()
	for _, args := range [][]string{{"search", "amber otters"}, {"show", oldID}, {"projects"}, {"recent"}, {"map", "harbor"}, {"status"}, {"doctor"}, {"--brief"}} {
		expected := 0
		if args[0] == "doctor" {
			expected = 3
		}
		data, err := j.call(expected, args...)
		if err != nil {
			return err
		}
		if args[0] == "status" {
			if err := checkStoredStatus(data); err != nil {
				return err
			}
		}
		if args[0] == "--brief" {
			var r struct {
				Text string `json:"text"`
				Age  *int64 `json:"index_age_s"`
			}
			if err := json.Unmarshal(data, &r); err != nil {
				return err
			}
			if len(strings.Split(strings.TrimSpace(r.Text), "\n")) != 3 || r.Age == nil {
				return errors.New("brief must keep three lines and numeric index age")
			}
		}
	}
	time.Sleep(1100 * time.Millisecond)
	data, err = j.call(0, "search", "amber otters")
	if err != nil {
		return err
	}
	if _, err := checkCitation(data, filepath.Join(source, "README.md"), oldID); err != nil {
		return err
	}
	if err := checkFreshness(data, 1); err != nil {
		return err
	}
	var before freshnessReply
	if err := json.Unmarshal(data, &before); err != nil {
		return err
	}
	data, err = j.call(0, "note", "Coral foxes record this isolated note.", "--project", "harbor")
	if err != nil {
		return err
	}
	notePath, noteID, err := checkSavedNote(data, j.state)
	if err != nil {
		return err
	}
	data, err = j.call(0, "search", "coral foxes")
	if err != nil {
		return err
	}
	if _, err := checkCitation(data, notePath, noteID); err != nil {
		return err
	}
	if err := checkFreshness(data, *before.Age); err != nil {
		return err
	}
	for _, probe := range []struct{ query, path, id string }{{"amber otters", filepath.Join(source, "README.md"), oldID}, {"violet herons", filepath.Join(unchanged, "README.md"), retainedID}} {
		data, err := j.call(0, "search", probe.query)
		if err != nil {
			return err
		}
		if _, err := checkCitation(data, probe.path, probe.id); err != nil {
			return err
		}
	}
	for _, query := range []string{"copper falcons", "silver turtles"} {
		data, err := j.call(0, "search", query)
		if err != nil {
			return err
		}
		if err := checkEmptySearch(data); err != nil {
			return err
		}
	}
	if _, err := j.call(0, "index"); err != nil {
		return err
	}
	for _, probe := range []struct{ query, path string }{{"copper falcons", filepath.Join(source, "README.md")}, {"silver turtles", filepath.Join(source, "ADDED.md")}, {"coral foxes", notePath}, {"violet herons", filepath.Join(unchanged, "README.md")}} {
		data, err := j.call(0, "search", probe.query)
		if err != nil {
			return err
		}
		if _, err := checkCitation(data, probe.path, ""); err != nil {
			return err
		}
	}
	data, err = j.call(0, "search", "amber otters")
	if err != nil {
		return err
	}
	if err := checkEmptySearch(data); err != nil {
		return err
	}
	d.summary.SideEffects[fmt.Sprintf("freshness_%d", attempt+1)] = "Ordinary reads retained old source results; note indexed alone without resetting full-source age; explicit index revealed changed and added documents. Source and home snapshots unchanged."
	return nil
}
