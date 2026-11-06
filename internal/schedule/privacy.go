package schedule

import (
	"brain/internal/platform"
	"brain/internal/redact"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

var ErrProtected = errors.New("the schedule contains protected text")

// CheckPrivacy checks both original paths and generated scheduler identities before writes.
func CheckPrivacy(plan Plan, policy *redact.Policy) error {
	data, err := json.Marshal(plan)
	if err != nil {
		return ErrFiles
	}
	return checkPrivateJSON(data, policy)
}

// CheckStoredPrivacy preserves the receipt because its paths prove native job ownership.
// Redacting or deleting those paths could strand a job or make its files appear foreign.
func CheckStoredPrivacy(state string, policy *redact.Policy) error {
	data, err := platform.ReadPrivateFile(state, receiptName, processLimit)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := checkPrivateJSON(data, policy); err != nil {
		return err
	}
	r, err := decodeReceipt(state, data)
	if errors.Is(err, ErrCollision) {
		canonical, problem := filepath.EvalSymlinks(state)
		if problem != nil {
			return problem
		}
		r, err = decodeReceipt(canonical, data)
	}
	if err != nil {
		return err
	}
	plan, err := planForReceipt(r)
	if err != nil {
		return err
	}
	return CheckPrivacy(plan, policy)
}

func checkPrivateJSON(data []byte, policy *redact.Policy) error {
	if policy.Match(string(data)) {
		return ErrProtected
	}
	if !json.Valid(data) {
		return ErrFiles
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return ErrFiles
		}
		if value, ok := token.(string); ok && policy.Match(value) {
			return ErrProtected
		}
	}
}
