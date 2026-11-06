package index

import (
	"brain/internal/platform"
	"brain/internal/redact"
	"brain/internal/schedule"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

var ErrPrivacyRefresh = errors.New("the index requires an explicit privacy refresh")
var ErrPrivacyPath = errors.New("an operational path contains protected text")
var ErrPrivacySchedule = fmt.Errorf("%w: the saved schedule needs manual repair", ErrPrivacyPath)

// CheckSchedulePrivacy never rewrites a receipt that may own a native job.
func CheckSchedulePrivacy(state string, policy *redact.Policy) error {
	if err := schedule.CheckStoredPrivacy(state, policy); err != nil {
		return ErrPrivacySchedule
	}
	return nil
}

const privacyMarker = "privacy-migration.json"
const privacyStage = ".privacy-stage"

func CheckPrivacy(db Reader, policy *redact.Policy) error {
	var fingerprint string
	err := db.QueryRow("SELECT value FROM meta WHERE key='privacy_fingerprint'").Scan(&fingerprint)
	if errors.Is(err, sql.ErrNoRows) || err == nil && fingerprint != policy.Fingerprint() {
		return ErrPrivacyRefresh
	}
	return err
}

func setPrivacy(db *sql.DB, policy *redact.Policy) error {
	_, err := db.Exec("INSERT OR REPLACE INTO meta(key,value) VALUES('privacy_fingerprint',?)", policy.Fingerprint())
	return err
}

func PrivacyRequired(state string, policy *redact.Policy) (bool, error) {
	// A closing writer can briefly leave WAL pages without shared memory.
	deadline := time.Now().Add(5 * time.Second)
	for {
		required, err := privacyRequired(state, policy, func(state string) (*Stored, error) { return OpenRead(state, true) })
		if required || !errors.Is(err, platform.ErrLocked) {
			return required, err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return required, err
		}
		time.Sleep(min(10*time.Millisecond, remaining))
		if !time.Now().Before(deadline) {
			return required, err
		}
	}
}

func PrivacyRequiredStored(state string, policy *redact.Policy) (bool, error) {
	return privacyRequired(state, policy, OpenStored)
}

func PrivacyRequiredForIndex(state string, policy *redact.Policy, locked bool) (bool, error) {
	return privacyRequired(state, policy, func(state string) (*Stored, error) {
		if !locked {
			return OpenRead(state, true)
		}
		if err := checkDatabase(state); err != nil {
			return nil, err
		}
		db, err := open(state, false, false, true, true, nil)
		if err != nil {
			return nil, err
		}
		return &Stored{DB: db}, nil
	})
}

func privacyRequired(state string, policy *redact.Policy, open func(string) (*Stored, error)) (bool, error) {
	if err := CheckSchedulePrivacy(state, policy); err != nil {
		return false, err
	}
	if _, err := platform.ReadPrivateFile(state, privacyMarker, 65536); err == nil {
		return true, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	db, err := open(state)
	if err != nil {
		if errors.Is(err, ErrMissing) || Rebuildable(err) {
			return true, err
		}
		return false, err
	}
	defer db.Close()
	err = CheckPrivacy(db, policy)
	if errors.Is(err, ErrPrivacyRefresh) {
		return true, nil
	}
	return false, err
}

func markedRules(text string) []string {
	found := []string{}
	for _, name := range []string{"aws-access-key", "aws-secret-key", "github-token", "google-api-key", "openai-token", "private-key", "private-value", "slack-token"} {
		if strings.Contains(text, "[redacted: "+name+"]") {
			found = append(found, name)
		}
	}
	return found
}

type Redaction struct {
	Path  string   `json:"path"`
	Rules []string `json:"rules"`
}

func Redactions(db Reader) ([]Redaction, error) {
	result := []Redaction{}
	rows, err := db.Query("SELECT files.path,redactions.rules FROM redactions JOIN files ON files.id=redactions.file WHERE files.deleted_at IS NULL ORDER BY files.source,files.path")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var entry Redaction
		var encoded string
		if err = rows.Scan(&entry.Path, &encoded); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(encoded), &entry.Rules); err != nil {
			return nil, err
		}
		if len(entry.Rules) > 0 {
			result = append(result, entry)
		}
	}
	return result, rows.Err()
}

func preparePrivacy(db *sql.DB, policy *redact.Policy) error {
	err := CheckPrivacy(db, policy)
	if !errors.Is(err, ErrPrivacyRefresh) {
		return err
	}
	var count int
	if err = db.QueryRow("SELECT count(*) FROM files").Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return ErrPrivacyRefresh
	}
	return nil
}
