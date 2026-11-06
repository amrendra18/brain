package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func setupDriftItem(t *testing.T, j *journey, harness string, extra ...string) (int, map[string]any) {
	t.Helper()
	args := append([]string{"setup", harness}, extra...)
	code, reply := j.run(t, args...)
	if code != 0 {
		return code, reply
	}
	setups := reply["setups"].([]any)
	if len(setups) != 1 {
		t.Fatal("setup returned another item count", reply)
	}
	return code, setups[0].(map[string]any)
}

func generatedSkill(t *testing.T, j *journey) []byte {
	t.Helper()
	command := exec.Command(testBinary, "help", "--skill")
	command.Dir = j.base
	command.Env = j.environment
	data, err := command.Output()
	if err != nil || len(data) == 0 {
		t.Fatal("generated skill unavailable", err)
	}
	return data
}

func TestSetupReportsSkillDrift(t *testing.T) {
	j := newJourney(t)
	generated := generatedSkill(t, j)
	before := treeBytes(t, j.base)
	code, item := setupDriftItem(t, j, "claude")
	if code != 0 || item["skill_current"] != false || item["written"] != false || !reflect.DeepEqual(before, treeBytes(t, j.base)) {
		t.Fatal("missing skill preview changed files or reported current", code, item)
	}
	path := item["skill_file"].(string)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	stale := append([]byte{}, generated...)
	stale[len(stale)-1] ^= 1
	if err := os.WriteFile(path, stale, 0600); err != nil {
		t.Fatal(err)
	}
	before = treeBytes(t, j.base)
	_, item = setupDriftItem(t, j, "claude")
	if item["skill_current"] != false || !reflect.DeepEqual(before, treeBytes(t, j.base)) {
		t.Fatal("stale skill preview changed files or reported current", item)
	}
	if err := os.WriteFile(path, append(append([]byte{}, generated...), '!'), 0600); err != nil {
		t.Fatal(err)
	}
	_, item = setupDriftItem(t, j, "claude")
	if item["skill_current"] != false {
		t.Fatal("oversized skill reported current", item)
	}
	if err := os.WriteFile(path, generated, 0600); err != nil {
		t.Fatal(err)
	}
	before = treeBytes(t, j.base)
	_, item = setupDriftItem(t, j, "claude")
	if item["skill_current"] != true || !reflect.DeepEqual(before, treeBytes(t, j.base)) {
		t.Fatal("matching skill preview changed files or reported stale", item)
	}
	text := exec.Command(testBinary, "setup", "claude")
	text.Dir, text.Env = j.base, j.environment
	printed, err := text.Output()
	if err != nil || !strings.Contains(string(printed), "Skill current: true") {
		t.Fatal("plain setup omitted current state", err, string(printed))
	}
	if runtime.GOOS == "windows" {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	} else if err := os.Chmod(path, 0000); err != nil {
		t.Fatal(err)
	}
	_, item = setupDriftItem(t, j, "claude")
	if item["skill_current"] != false {
		t.Fatal("unreadable or nonregular skill reported current", item)
	}
	if runtime.GOOS == "windows" {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	} else if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("old copy"), 0600); err != nil {
		t.Fatal(err)
	}
	code, item = setupDriftItem(t, j, "claude", "--write")
	if code != 0 || item["written"] != true || item["skill_current"] != true {
		t.Fatal("refresh did not report a current copy", code, item)
	}
	if copied, err := os.ReadFile(path); err != nil || !bytes.Equal(copied, generated) {
		t.Fatal("refresh wrote different skill bytes", err)
	}
	for _, harness := range []string{"codex", "copilot"} {
		_, selected := setupDriftItem(t, j, harness)
		target := selected["skill_file"].(string)
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, generated, 0600); err != nil {
			t.Fatal(err)
		}
	}
	before = treeBytes(t, j.base)
	code, all := j.run(t, "setup", "all")
	if code != 0 || !reflect.DeepEqual(before, treeBytes(t, j.base)) {
		t.Fatal("all-host preview changed files", code, all)
	}
	setups := all["setups"].([]any)
	if len(setups) != 4 {
		t.Fatal("missing host", all)
	}
	for _, raw := range setups {
		item := raw.(map[string]any)
		if item["skill_current"] != true || item["written"] != false {
			t.Fatal("selected host did not report its existing copy", item)
		}
	}
}

func TestSetupUnsafeSkillPath(t *testing.T) {
	j := newJourney(t)
	generated := generatedSkill(t, j)
	outside := t.TempDir()
	outsideSkill := filepath.Join(outside, "SKILL.md")
	if err := os.WriteFile(outsideSkill, []byte("outside stays unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(j.base, ".claude")); err == nil {
		code, item := setupDriftItem(t, j, "claude")
		if code != 0 || item["skill_current"] != false {
			t.Fatal("linked parent preview was followed", code, item)
		}
		code, reply := j.run(t, "setup", "claude", "--write")
		if code != 1 || reply["error"].(map[string]any)["code"] != "state-unwritable" {
			t.Fatal("linked parent accepted for copy", code, reply)
		}
	}
	_, codex := setupDriftItem(t, j, "codex")
	codexPath := codex["skill_file"].(string)
	if err := os.MkdirAll(filepath.Dir(codexPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideSkill, codexPath); err == nil {
		code, item := setupDriftItem(t, j, "codex")
		if code != 0 || item["skill_current"] != false {
			t.Fatal("linked file preview was followed", code, item)
		}
		code, reply := j.run(t, "setup", "codex", "--write")
		if code != 1 || reply["error"].(map[string]any)["code"] != "state-unwritable" {
			t.Fatal("linked file accepted for copy", code, reply)
		}
	}
	if unchanged, err := os.ReadFile(outsideSkill); err != nil || string(unchanged) != "outside stays unchanged" {
		t.Fatal("outside skill changed", err)
	}
	code, item := setupDriftItem(t, j, "copilot", "--write")
	if code != 0 || item["skill_current"] != true || item["written"] != true {
		t.Fatal("allowed sibling copy refused", code, item)
	}
	if copied, err := os.ReadFile(item["skill_file"].(string)); err != nil || !bytes.Equal(copied, generated) {
		t.Fatal("allowed sibling copy differed", err)
	}
}
