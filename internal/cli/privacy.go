package cli

import (
	"brain/internal/corpus"
	"brain/internal/index"
	"brain/internal/platform"
	"brain/internal/redact"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

func (call *call) loadPrivacy(arguments []string) error {
	call.resolveScope(arguments)
	call.policy = redact.Default()
	if call.scopeErr != nil {
		return call.scopeErr
	}
	document, err := corpus.ReadDocument(call.config)
	if err != nil {
		return nil
	}
	call.policy, err = redact.Load(document.Config.PrivateValuesFile)
	if err != nil {
		call.policy = redact.Default()
		return err
	}
	if err = call.privacyScope(document.Config); err != nil {
		return err
	}
	return nil
}

func (call *call) privacyScope(config corpus.Config) error {
	if config.PrivateValuesFile != "" {
		stateInfo, err := os.Stat(call.state)
		if err == nil {
			for parent := filepath.Dir(config.PrivateValuesFile); ; parent = filepath.Dir(parent) {
				info, err := os.Stat(parent)
				if err == nil && os.SameFile(stateInfo, info) {
					return redact.ErrPolicyInvalid
				}
				if parent == filepath.Dir(parent) {
					break
				}
			}
		}
	}
	executable, _ := os.Executable()
	for _, value := range []string{call.state, call.config, executable, config.PrivateValuesFile} {
		if call.policy.Match(value) {
			return index.ErrPrivacyPath
		}
	}
	for _, source := range config.Sources {
		if call.policy.Match(source.Path) || call.policy.Match(source.Project) {
			return index.ErrPrivacyPath
		}
	}
	return nil
}

func (call *call) privacyOutput(destination io.Writer, buffer *bytes.Buffer) int {
	var data any
	decoder := json.NewDecoder(bytes.NewReader(buffer.Bytes()))
	decoder.UseNumber()
	var trailing any
	if decoder.Decode(&data) == nil && decoder.Decode(&trailing) == io.EOF {
		var sanitize func(any) any
		sanitize = func(value any) any {
			switch value := value.(type) {
			case string:
				return call.policy.Apply(value).Text
			case []any:
				for i := range value {
					value[i] = sanitize(value[i])
				}
				return value
			case map[string]any:
				for key, one := range value {
					value[key] = sanitize(one)
				}
				return value
			default:
				return value
			}
		}
		return WriteJSON(destination, sanitize(data))
	}
	text := call.policy.Apply(buffer.String()).Text
	n, err := io.WriteString(destination, text)
	if err != nil || n != len(text) {
		return 1
	}
	return 0
}

func (call *call) privacyFailure(err error) int {
	if errors.Is(err, redact.ErrPolicyInvalid) {
		return call.failure("privacy-policy-invalid", "The private-values file is invalid or unsafe. Keep it outside the state folder and repair its format or permissions before retrying.", 1, []string{"help", "index"})
	}
	if errors.Is(err, index.ErrPrivacySchedule) {
		return call.failure("privacy-protected-path", "The saved schedule cannot be verified under the current privacy policy. Remove its native job and owned artifacts manually before removing its receipt, then retry index. Keep the receipt until cleanup is complete.", 1, []string{"help", "schedule"})
	}
	if errors.Is(err, index.ErrPrivacyPath) {
		return call.failure("privacy-protected-path", "A required path or project name contains protected text. Move or rename it before retrying. Recovery commands are omitted when they would reveal protected text.", 1, []string{"help", "index"})
	}
	return call.failure("privacy-refresh-required", "Stored content needs an explicit privacy refresh. Run index before reading or saving more notes.", 1, []string{"index"})
}

func (call *call) guardPrivacy() error {
	if err := index.CheckSchedulePrivacy(call.state, call.policy); err != nil {
		return err
	}
	if call.command == "note" {
		lock, err := platform.PrivacyLock(call.state, false, true)
		if err != nil {
			return err
		}
		call.privacyLock = lock
	}
	if _, err := os.Stat(filepath.Join(call.state, "index.sqlite")); errors.Is(err, os.ErrNotExist) {
		if _, err = platform.ReadPrivateFile(call.state, "privacy-migration.json", 65536); errors.Is(err, os.ErrNotExist) {
			for _, name := range []string{".privacy-stage", "index.sqlite-wal", "index.sqlite-shm", "index.sqlite-journal"} {
				if _, err := os.Lstat(filepath.Join(call.state, name)); err == nil {
					return index.ErrPrivacyRefresh
				} else if !errors.Is(err, os.ErrNotExist) {
					return err
				}
			}
			for _, name := range []string{"notes", "converted"} {
				if err := platform.SafeStatePath(call.state, name); err != nil {
					return err
				}
				entries, err := os.ReadDir(filepath.Join(call.state, name))
				if err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
				if len(entries) > 0 {
					return index.ErrPrivacyRefresh
				}
			}
			return nil
		}
	}
	if call.privacyLock == nil {
		lock, err := platform.PrivacyLock(call.state, false, false)
		if errors.Is(err, os.ErrNotExist) {
			return index.ErrPrivacyRefresh
		}
		if err != nil {
			return err
		}
		call.privacyLock = lock
	}
	check := index.PrivacyRequired
	if call.command == "brief" || call.command == "--brief" {
		check = index.PrivacyRequiredStored
	}
	required, err := check(call.state, call.policy)
	if err != nil {
		return err
	}
	if required {
		return index.ErrPrivacyRefresh
	}
	return nil
}

func (call *call) closePrivacy() {
	if call.privacyLock != nil {
		call.privacyLock.Close()
		call.privacyLock = nil
	}
}

func (call *call) guardConfigPrivacy(config corpus.Config) error {
	call.policy = config.Privacy()
	if err := call.privacyScope(config); err != nil {
		return err
	}
	return call.guardPrivacy()
}
