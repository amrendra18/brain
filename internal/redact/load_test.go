package redact

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadPrivateValues(t *testing.T) {
	folder := t.TempDir()
	path := filepath.Join(folder, "private-values.json")
	content := []byte(`["protected-literal", "unicode秘密", "protected-literal"]`)
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if result := policy.Apply("ordinary protected-literal unicode秘密"); result.Text != "ordinary [redacted: private-value] [redacted: private-value]" {
		t.Fatal("private values were not applied")
	}
	if policy.Match("ordinary control") {
		t.Fatal("allowed sibling text matched")
	}
	after, err := os.Stat(path)
	data, readErr := os.ReadFile(path)
	if err != nil || readErr != nil || string(data) != string(content) || !before.ModTime().Equal(after.ModTime()) || before.Mode() != after.Mode() {
		t.Fatal("loading changed control input")
	}
	if empty, err := Load(""); err != nil || empty.Fingerprint() != Default().Fingerprint() {
		t.Fatal("omitted values changed default policy")
	}
	if err := os.WriteFile(path, []byte(`[]`), 0600); err != nil {
		t.Fatal(err)
	}
	if empty, err := Load(path); err != nil || empty.Fingerprint() != Default().Fingerprint() {
		t.Fatal("empty values changed default policy")
	}
}

func TestLoadPrivateValuesRefusesUnsafeInputs(t *testing.T) {
	folder := t.TempDir()
	path := filepath.Join(folder, "private-values.json")
	good := []byte(`["protected-literal"]`)
	write := func(data []byte) {
		t.Helper()
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	refuse := func(input string) {
		t.Helper()
		policy, err := Load(input)
		if policy != nil || err != ErrPolicyInvalid || strings.Contains(err.Error(), folder) || strings.Contains(err.Error(), "protected-literal") {
			t.Fatal("unsafe input lacked a generic refusal")
		}
	}
	for _, input := range []string{"relative.json", path, "smb://unavailable/value", "//unavailable/value", string(filepath.Separator) + "bad\x00value"} {
		refuse(input)
	}
	refuse(folder)
	for _, data := range [][]byte{nil, []byte(`null`), []byte(`{}`), []byte(`[1]`), []byte(`[null]`), []byte(`["\ud800"]`), []byte(`["\udc00"]`), []byte(`["\ud800\u0041"]`), []byte(`[""]`), []byte(`["redacted"]`), []byte(`["value\u0000"]`), []byte(`["valid"] []`), {'[', '"', 255, '"', ']'}, []byte(`["` + strings.Repeat("X", 4097) + `"]`), []byte(strings.Repeat(" ", FileLimit+1))} {
		write(data)
		refuse(path)
	}
	values := make([]string, 10001)
	for i := range values {
		values[i] = "protected-literal"
	}
	data, _ := json.Marshal(values)
	write(data)
	refuse(path)
	write(good)
	if _, err := Load(path); err != nil {
		t.Fatal("allowed control failed", err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	refuse(path)
	if err := os.Chmod(path, 0000); err != nil {
		t.Fatal(err)
	}
	refuse(path)
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(folder, "linked")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	refuse(link)
	linkedParent := filepath.Join(t.TempDir(), "linked-parent")
	if err := os.Symlink(folder, linkedParent); err != nil {
		t.Fatal(err)
	}
	refuse(filepath.Join(linkedParent, filepath.Base(path)))
	hard := filepath.Join(folder, "hard")
	if err := os.Link(path, hard); err != nil {
		t.Fatal(err)
	}
	refuse(hard)
	refuse(path)
	if err := os.Remove(hard); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatal("allowed control failed after unsafe cases", err)
	}
}

func TestPrivateValuesBounds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "values.json")
	limitValue := strings.Repeat("X", 4096)
	data, _ := json.Marshal([]string{limitValue})
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	policy, err := Load(path)
	if err != nil || !policy.Match(limitValue) {
		t.Fatal("maximum length value refused", err)
	}
	data = append(data, []byte(strings.Repeat(" ", FileLimit-len(data)))...)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatal("maximum length file refused", err)
	}
	if err := os.WriteFile(path, append(data, ' '), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != ErrPolicyInvalid {
		t.Fatal("oversize file accepted")
	}
	values := make([]string, 10000)
	for i := range values {
		values[i] = "protected-literal"
	}
	data, _ = json.Marshal(values)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatal("maximum entry count refused", err)
	}
}

func TestPrivateValuesUnicodeEscapes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "values.json")
	for _, input := range []string{`["\ud83d\ude42"]`, `["\\ud800"]`, `["\u0041bc"]`, `["line\nbreak"]`} {
		if err := os.WriteFile(path, []byte(input), 0600); err != nil {
			t.Fatal(err)
		}
		policy, err := Load(path)
		if err != nil {
			t.Fatal("valid escaped value refused", err)
		}
		var values []string
		if err := json.Unmarshal([]byte(input), &values); err != nil {
			t.Fatal(err)
		}
		if !policy.Match(values[0]) {
			t.Fatal("escaped value did not match its decoded text")
		}
	}
}
