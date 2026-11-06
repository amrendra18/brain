package cli

import (
	"brain/internal/corpus"
	"brain/internal/platform"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigPrecedence(t *testing.T) {
	base := t.TempDir()
	state := filepath.Join(base, "state")
	source := filepath.Join(base, "source")
	os.Mkdir(source, 0700)
	t.Setenv("BRAIN_HOME", state)
	var out bytes.Buffer
	run := func(args ...string) int { out.Reset(); return Run(append(args, "--format", "json"), &out) }
	if code := run("init", "--source", source); code != 0 {
		t.Fatal(code, out.String())
	}
	envConfig := filepath.Join(base, "environment.json")
	flagConfig := filepath.Join(base, "flag.json")
	t.Setenv("BRAIN_CONFIG", envConfig)
	if code := run("init", "--source", source); code != 0 {
		t.Fatal(code, out.String())
	}
	t.Chdir(base)
	if code := run("init", "--source", source, "--config", "flag.json"); code != 0 {
		t.Fatal(code, out.String())
	}
	var init initReply
	json.Unmarshal(out.Bytes(), &init)
	if init.Config != flagConfig {
		t.Fatal(init)
	}
	if code := run("exclude", "add", "env-only"); code != 0 {
		t.Fatal(code, out.String())
	}
	if code := run("exclude", "list", "--config", flagConfig); code != 0 || strings.Contains(out.String(), "env-only") {
		t.Fatal(code, out.String())
	}
	if code := run("exclude", "list"); code != 0 || !strings.Contains(out.String(), "env-only") {
		t.Fatal(code, out.String())
	}
	if code := run("sources", "list", "--config", filepath.Join(base, "absent")); code != 1 {
		t.Fatal(code, out.String())
	}
	for _, value := range []string{"", "\x00"} {
		if code := run("sources", "list", "--config", value); code != 2 {
			t.Fatal(code, out.String())
		}
	}
	t.Setenv("BRAIN_CONFIG", "")
	if code := run("sources", "list"); code != 2 {
		t.Fatal(code, out.String())
	}
	if code := run("sources", "list", "--config", flagConfig); code != 0 {
		t.Fatal(code, out.String())
	}
	if code := Run([]string{"sources", "--help", "--config", "\x00"}, &out); code != 0 {
		t.Fatal(code, out.String())
	}
	if code := Run([]string{"--version"}, &out); code != 0 {
		t.Fatal(code, out.String())
	}
}

func TestNextKeepsConfig(t *testing.T) {
	base := t.TempDir()
	state := filepath.Join(base, "state")
	config := filepath.Join(base, "selected.json")
	source := filepath.Join(base, "source")
	os.Mkdir(source, 0700)
	t.Setenv("BRAIN_HOME", state)
	t.Setenv("BRAIN_CONFIG", config)
	var out bytes.Buffer
	if code := Run([]string{"init", "--source", source}, &out); code != 0 {
		t.Fatal(code, out.String())
	}
	for _, args := range [][]string{{"search", "--wrong", "--config", config}, {"search", "missing"}, {"sources", "add", source}, {"exclude", "add", "*.draft"}, {"sources", "move", source, source + "-absent"}} {
		out.Reset()
		Run(append(args, "--format", "json"), &out)
		var reply struct {
			NextArgv [][]string `json:"next_argv"`
			Error    Failure    `json:"error"`
		}
		if err := json.Unmarshal(out.Bytes(), &reply); err != nil {
			t.Fatal(err, out.String())
		}
		argv := reply.NextArgv
		if len(argv) == 0 {
			argv = reply.Error.NextArgv
		}
		if len(argv) == 0 {
			t.Fatal("missing recovery", out.String())
		}
		for _, words := range argv {
			for flag, value := range map[string]string{"--config": config, "--state-dir": state} {
				count := 0
				for i, word := range words {
					if word == flag {
						count++
						if i+1 == len(words) || words[i+1] != value {
							t.Fatal(words)
						}
					}
				}
				if count != 1 {
					t.Fatal(flag, words)
				}
			}
			var result bytes.Buffer
			code := Run(words[1:], &result)
			if code == 2 {
				t.Fatal("recovery invalid", words, result.String())
			}
		}
	}
	c := call{command: "search", state: state, config: config, shell: "sh", flags: map[string]string{"--config": config, "--state-dir": state, "--format": "json"}}
	_, argv := c.next(c.again("--full")...)
	for _, flag := range []string{"--config", "--state-dir", "--format"} {
		count := 0
		for _, word := range argv {
			if word == flag {
				count++
			}
		}
		if count != 1 {
			t.Fatal(argv)
		}
	}
}

func TestSourceCommandsWithoutIndex(t *testing.T) {
	base := t.TempDir()
	source, other := filepath.Join(base, "source"), filepath.Join(base, "other")
	os.Mkdir(source, 0700)
	os.Mkdir(other, 0700)
	config := filepath.Join(base, "selected.json")
	state := filepath.Join(base, "state")
	t.Setenv("BRAIN_HOME", state)
	t.Setenv("BRAIN_CONFIG", config)
	var out bytes.Buffer
	run := func(args ...string) int { out.Reset(); return Run(append(args, "--format", "json"), &out) }
	if run("init", "--source", source) != 0 {
		t.Fatal(out.String())
	}
	os.WriteFile(filepath.Join(state, "index.sqlite"), []byte("broken database"), 0600)
	for _, step := range []struct {
		words []string
		code  int
	}{
		{[]string{"sources", "list"}, 0}, {[]string{"sources", "add", other, "--project", "other"}, 0},
		{[]string{"sources", "add", other, "--project", "other"}, 3}, {[]string{"sources", "remove", source}, 0},
		{[]string{"sources", "remove", source}, 3}, {[]string{"sources", "move", other, source}, 0},
		{[]string{"sources", "move", source, source}, 3}, {[]string{"exclude", "list"}, 0}, {[]string{"third-party", "list"}, 0},
		{[]string{"sources", "list", "--project", "bad"}, 2}, {[]string{"sources"}, 2}, {[]string{"sources", "wrong"}, 2},
		{[]string{"sources", "remove", source}, 0}, {[]string{"sources", "list"}, 0},
	} {
		if code := run(step.words...); code != step.code {
			t.Fatal(step, code, out.String())
		}
		if step.code == 0 || step.code == 3 {
			var got configReply
			if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.Count != got.Total || got.Empty != (got.Count == 0) {
				t.Fatal(got, err)
			}
		}
	}
	if !strings.Contains(out.String(), `"sources":[]`) {
		t.Fatal(out.String())
	}
	data, _ := os.ReadFile(filepath.Join(state, "index.sqlite"))
	if string(data) != "broken database" {
		t.Fatal("management changed index")
	}
	if code := run("exclude", "add", "new-rule", "--state-dir", filepath.Join(base, "missing")); code != 0 {
		t.Fatal(code, out.String())
	}
	if _, err := os.Stat(filepath.Join(base, "missing")); !os.IsNotExist(err) {
		t.Fatal("created unrelated state", err)
	}
	for _, args := range [][]string{{"sources", "list"}, {"exclude", "list"}, {"third-party", "list"}} {
		if Run(args, closedWriter{}) != 1 {
			t.Fatal(args)
		}
		out.Reset()
		if code := Run(args, &out); code != 0 {
			t.Fatal(code, out.String())
		}
	}
}

func TestSelectedConfigDoctorBriefSetup(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "source")
	os.Mkdir(source, 0700)
	state := filepath.Join(base, "state")
	config := filepath.Join(base, "selected.json")
	t.Setenv("BRAIN_HOME", state)
	t.Setenv("BRAIN_CONFIG", config)
	t.Setenv("HOME", base)
	t.Setenv("USERPROFILE", base)
	var out bytes.Buffer
	for _, args := range [][]string{{"init", "--source", source}, {"index"}, {"doctor"}, {"brief"}, {"setup", "all"}} {
		out.Reset()
		code := Run(append(args, "--format", "json"), &out)
		if code != 0 && code != 3 {
			t.Fatal(code, args, out.String())
		}
		if strings.Contains(out.String(), "state-uninitialised") {
			t.Fatal(out.String())
		}
		if args[0] == "setup" && !strings.Contains(out.String(), "--config") {
			t.Fatal("hook lost config", out.String())
		}
	}
	if _, err := os.Stat(filepath.Join(state, "config.json")); !os.IsNotExist(err) {
		t.Fatal("wrote fallback config", err)
	}
	target := filepath.Join(base, "untouched")
	os.WriteFile(target, []byte("keep"), 0600)
	os.Remove(config + ".lock")
	os.Symlink(target, config+".lock")
	out.Reset()
	if code := Run([]string{"exclude", "add", "draft"}, &out); code != 1 {
		t.Fatal(code, out.String())
	}
	data, _ := os.ReadFile(target)
	if string(data) != "keep" {
		t.Fatal("changed lock target")
	}
}

func TestConfigManagementErrors(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "source")
	os.Mkdir(source, 0700)
	state := filepath.Join(base, "state")
	config := filepath.Join(base, "selected.json")
	t.Setenv("BRAIN_HOME", state)
	t.Setenv("BRAIN_CONFIG", config)
	var out bytes.Buffer
	if Run([]string{"init", "--source", source}, &out) != 0 {
		t.Fatal(out.String())
	}
	for _, args := range [][]string{{"exclude", "add", "draft"}, {"sources", "list"}} {
		if Run(args, closedWriter{}) != 1 {
			t.Fatal("closed writer", args)
		}
	}
	if _, err := corpus.ReadDocument(config); err != nil {
		t.Fatal(err)
	}
	lock, err := platform.AcquireConfig(config, false)
	if err != nil {
		t.Fatal(err)
	}
	c := call{command: "exclude", state: state, config: config, shell: "sh", flags: map[string]string{}, words: []string{"add", "draft"}, output: &out}
	if c.configWriteError(platform.ErrLocked) != 1 {
		t.Fatal(out.String())
	}
	lock.Close()
}

func TestRecoveryReplacesOnlyImplicitConfig(t *testing.T) {
	base := t.TempDir()
	state := filepath.Join(base, "state")
	explicit := filepath.Join(base, "selected.json")
	for _, mode := range []string{"implicit", "flag", "environment"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("BRAIN_HOME", state)
			c := call{command: "init", state: state, config: filepath.Join(state, "config.json"), shell: defaultShell(), flags: map[string]string{}}
			if mode != "implicit" {
				c.config = explicit
				c.configExplicit = true
			}
			if mode == "flag" {
				c.flags["--config"] = explicit
			}
			if mode == "environment" {
				t.Setenv("BRAIN_CONFIG", explicit)
			}
			replacement := filepath.Join(base, "replacement-"+mode)
			_, argv := c.next("init", "--state-dir", replacement, "--source", base)
			want := filepath.Join(replacement, "config.json")
			if mode != "implicit" {
				want = explicit
			}
			count := 0
			for i, word := range argv {
				if word == "--config" {
					count++
					if i+1 == len(argv) || argv[i+1] != want {
						t.Fatal(argv)
					}
				}
			}
			if count != 1 {
				t.Fatal(argv)
			}
			var output bytes.Buffer
			if code := Run(argv[1:], &output); code != 0 && code != 3 {
				t.Fatal(code, output.String())
			}
			if _, err := os.Stat(want); err != nil {
				t.Fatal("recovery did not initialize selected config", want, err)
			}
		})
	}
}
