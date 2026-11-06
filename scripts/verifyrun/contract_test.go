package verifyrun

import (
	"brain/internal/platform"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func seedContractDocs(t *testing.T, root string) {
	t.Helper()
	for _, name := range []string{"README.md", "skills/brain/SKILL.md"} {
		data, err := os.ReadFile(filepath.Join("../..", name))
		if err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestContractReplyChecks(t *testing.T) {
	if err := checkVersion([]byte("{\"version\":\"0.1.0-dev\",\"revision\":\"test\",\"modified\":false}")); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{"{}", "{\"version\":\"x\"} {}", "not JSON", "{\"version\":\"dev\",\"revision\":\"test\",\"modified\":false}"} {
		if err := checkVersion([]byte(data)); err == nil {
			t.Fatalf("broken version accepted: %q", data)
		}
	}
	binary := filepath.Join(t.TempDir(), "brain")
	shell := "sh"
	if runtime.GOOS == "windows" {
		shell = "powershell"
	}
	recovery := func(command string, argv []string, code, message string) []byte {
		t.Helper()
		data, err := json.Marshal(map[string]any{"error": map[string]any{
			"code": code, "message": message, "next": []string{command}, "next_argv": [][]string{argv},
		}})
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	command := platform.Command(shell, []string{binary, "help"})
	valid := recovery(command, []string{binary, "help"}, "unknown-command", "Unknown input. Valid verbs: index, search.")
	if err := checkBadInput(valid, "unknown-command", binary, []string{"index", "search"}); err != nil {
		t.Fatal(err)
	}
	flag := recovery(command, []string{binary, "help"}, "unknown-flag", "Unknown flag. Valid flags: --help, --format.")
	if err := checkBadInput(flag, "unknown-flag", binary, []string{"--help", "--format"}); err != nil {
		t.Fatal(err)
	}
	if err := checkBadInput([]byte("{\"error\":{\"code\":\"other\"}}"), "unknown-command", binary, []string{"index"}); err == nil {
		t.Fatal("wrong refusal accepted")
	}
	for _, data := range [][]byte{
		recovery("brain help", []string{binary, "help"}, "unknown-command", "Valid verbs: index, search."),
		recovery(command, []string{"brain", "help"}, "unknown-command", "Valid verbs: index, search."),
		recovery(command, []string{binary}, "unknown-command", "Valid verbs: index, search."),
		recovery(command, []string{binary, "help"}, "unknown-command", "Valid verbs: index."),
	} {
		if err := checkBadInput(data, "unknown-command", binary, []string{"index", "search"}); err == nil {
			t.Fatal("incomplete refusal accepted")
		}
	}
	missingFlag := recovery(command, []string{binary, "help"}, "unknown-flag", "Valid flags: --other.")
	if err := checkBadInput(missingFlag, "unknown-flag", binary, []string{"--help", "--format"}); err == nil {
		t.Fatal("unknown flag message lacked a valid flag")
	}
	for _, effects := range []string{"reads, network", "reads, reads"} {
		if err := checkHelpSemantics(effects, "0 completed, 2 bad input."); err == nil {
			t.Fatal("invalid effect accepted")
		}
	}
	for _, codes := range []string{"0 completed, 9 bad input.", "0 completed, 2 bad input, 2 duplicate.", "0 completed."} {
		if err := checkHelpSemantics("reads", codes); err == nil {
			t.Fatal("invalid exit codes accepted")
		}
	}
	if err := checkPlainTopicHelp([]byte("brain search\nInputs: query\nOutputs: results\nEffects: reads\nExit codes: 0\n"), "search"); err != nil {
		t.Fatal(err)
	}
	if err := checkPlainTopicHelp([]byte("brain search\nInputs: query\nOutputs: results\n"), "search"); err == nil {
		t.Fatal("incomplete plain help accepted")
	}
	if err := checkTopLevelHelp([]byte("Brain 0.1.0-dev\nCommands:\nhelp\nExit codes: 0 completed\n")); err != nil {
		t.Fatal(err)
	}
	if err := checkTopLevelHelp([]byte(`{"commands":[]}`)); err == nil {
		t.Fatal("structured output accepted as plain top-level help")
	}
	if err := checkPublicText([]byte("Brain search")); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"Unrelated help", "Brain at /" + "Users/example/private/", "Brain at https://private.example/files"} {
		if err := checkPublicText([]byte(value)); err == nil {
			t.Fatal("private or unbranded help accepted")
		}
	}
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	if err := checkReadme(readme); err != nil {
		t.Fatal(err)
	}
	if err := checkReadme([]byte("# Brain\n")); err == nil {
		t.Fatal("incomplete README accepted")
	}
	reordered := "# Brain\n## Install and first search\n```sh\n./scripts/install --source /path/to/notes --write\n\"$HOME/.local/bin/brain\" --version\n\"$HOME/.local/bin/brain\" search sample\n```\n## What install writes\n## Requirements\n## Network\n## Remove\nBRAIN_HOME\n"
	if err := checkReadme([]byte(reordered)); err == nil {
		t.Fatal("install block with version before search was accepted")
	}
}

func TestContractJourney(t *testing.T) {
	root := t.TempDir()
	seedContractDocs(t, root)
	var out bytes.Buffer
	if got := Run(root, []string{"--feature", "tool-contract", "--binary", binary}, &out); got != 0 {
		t.Fatalf("contract exit %d: %s", got, out.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	evidence := lines[len(lines)-1]
	data, err := os.ReadFile(filepath.Join(evidence, "summary.json"))
	if err != nil {
		t.Fatal(err)
	}
	var summary Summary
	if err := json.Unmarshal(data, &summary); err != nil {
		t.Fatal(err)
	}
	if len(summary.Features) != 1 || summary.Features[0].Status != "proven" || len(summary.Commands) < 20 || summary.SideEffects["contract_1"] == "" || summary.SideEffects["install_1"] == "" {
		t.Fatalf("contract evidence incomplete: %#v", summary)
	}
	for _, command := range summary.Commands {
		if command.Stdin != "closed" {
			t.Fatal("command input was not closed")
		}
	}
	data, err = os.ReadFile(filepath.Join(evidence, "traces", "contract-1-probes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var probes []struct {
		ID     string `json:"id"`
		Result string `json:"result"`
	}
	if err := json.Unmarshal(data, &probes); err != nil {
		t.Fatal(err)
	}
	if len(probes) != 8 {
		t.Fatalf("got %d probes", len(probes))
	}
	for _, probe := range probes {
		if probe.Result != "holds" {
			t.Fatalf("failed probe %s", probe.ID)
		}
	}
}

func TestInstallTemporaryHome(t *testing.T) {
	d := driver{evidence: t.TempDir(), summary: &Summary{SideEffects: map[string]string{}}}
	if err := d.prepare(binary); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(d.summary.Scratch)
	if err := d.installTemporaryHome(0); err != nil {
		t.Fatal(err)
	}
	if len(d.summary.Commands) != 1 || len(d.summary.SideEffects) != 1 {
		t.Fatalf("missing installation evidence: %#v", d.summary)
	}
	if _, err := lookupInPath("absent", t.TempDir()); err == nil {
		t.Fatal("missing executable accepted")
	}
}

func TestBrokenContractBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture runs on Unix")
	}
	for _, fixture := range []struct{ name, script, wanted string }{
		{"bad-version", "#!/bin/sh\nprintf '%s\\n' '{}'\n", "version lacks"},
		{"writes-home", "#!/bin/sh\n: > \"$HOME/unexpected\"\nprintf '%s\\n' '{\"version\":\"0.1.0-dev\",\"revision\":\"test\",\"modified\":false}'\n", "changed isolated"},
		{"writes-source", "#!/bin/sh\n: > unexpected\nprintf '%s\\n' '{\"version\":\"0.1.0-dev\",\"revision\":\"test\",\"modified\":false}'\n", "changed isolated"},
		{"erase-later", "#!/bin/sh\nif [ \"$1\" = --version ]; then : > \"$HOME/transient\"; printf '%s\\n' '{\"version\":\"0.1.0-dev\",\"revision\":\"test\",\"modified\":false}'; else rm -f \"$HOME/transient\"; printf '%s\\n' '{}'; fi\n", "contract command changed isolated"},
		{"exits-one", "#!/bin/sh\nexit 1\n", "expected exit 0"},
		{"later-probe", "#!/bin/sh\nif [ \"$1\" = --version ]; then printf '%s\\n' '{\"version\":\"0.1.0-dev\",\"revision\":\"test\",\"modified\":false}'; else printf '%s\\n' '{}'; fi\n", "probe P2"},
		{"flood", "#!/bin/sh\nwhile :; do printf '%s\\n' 'xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx'; done\n", "exceeded 1 MiB"},
		{"child", "#!/bin/sh\nsleep 10 >/dev/null 2>&1 &\nprintf '%s\\n' '{\"version\":\"0.1.0-dev\",\"revision\":\"test\",\"modified\":false}'\n", "left a child process"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			fake := filepath.Join(t.TempDir(), "brain")
			if err := os.WriteFile(fake, []byte(fixture.script), 0700); err != nil {
				t.Fatal(err)
			}
			repo := t.TempDir()
			seedContractDocs(t, repo)
			d := driver{repo: repo, evidence: t.TempDir(), summary: &Summary{SideEffects: map[string]string{}}}
			if err := d.prepare(fake); err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(d.summary.Scratch)
			if err := d.toolContract(0); err == nil || !strings.Contains(err.Error(), fixture.wanted) {
				t.Fatalf("broken binary accepted: %v", err)
			}
			if fixture.name == "erase-later" && len(d.summary.Commands) != 1 {
				t.Fatal("verifier let a later command erase the first call's write")
			}
			if fixture.name == "later-probe" {
				data, err := os.ReadFile(filepath.Join(d.evidence, "traces", "contract-1-probes.json"))
				if err != nil {
					t.Fatal(err)
				}
				var probes []contractProbe
				if err := json.Unmarshal(data, &probes); err != nil {
					t.Fatal(err)
				}
				if len(probes) != 2 || probes[0].Result != "holds" || probes[1].Result != "failed" {
					t.Fatalf("later failure was not recorded: %#v", probes)
				}
				var output bytes.Buffer
				if got := Run(repo, []string{"--feature", "tool-contract", "--binary", fake}, &output); got != 1 {
					t.Fatalf("broken later probe returned %d: %s", got, output.String())
				}
			}
		})
	}
}

func TestVerifyAllMappedFeatures(t *testing.T) {
	var out bytes.Buffer
	root := t.TempDir()
	seedContractDocs(t, root)
	if got := Run(root, []string{"full", "--binary", binary}, &out); got != 0 {
		t.Fatalf("all mapped features: exit %d: %s", got, out.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	data, err := os.ReadFile(filepath.Join(lines[len(lines)-1], "summary.json"))
	if err != nil {
		t.Fatal(err)
	}
	var summary Summary
	if err := json.Unmarshal(data, &summary); err != nil {
		t.Fatal(err)
	}
	if len(summary.Features) != len(featureJourneys) || len(summary.Commands) < 22 || summary.SideEffects["skill_drift_1"] == "" || summary.SideEffects["skill_drift_2"] == "" {
		t.Fatalf("default full omitted a mapped feature: %#v", summary)
	}
	for i, item := range featureJourneys {
		if summary.Features[i].Name != item.name || summary.Features[i].Status != "proven" {
			t.Fatalf("unproved feature %s", item.name)
		}
	}
}

func TestStandaloneSkillLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX install block; native Windows execution remains deferred")
	}
	readme, err := os.ReadFile("../../docs/reference.md")
	if err != nil {
		t.Fatal(err)
	}
	_, section, found := strings.Cut(string(readme), "To install just the skill from a checkout")
	if !found {
		t.Fatal("standalone skill installation block is missing")
	}
	_, section, found = strings.Cut(section, "```sh\n")
	if !found {
		t.Fatal("standalone skill shell block is missing")
	}
	block, _, found := strings.Cut(section, "\n```")
	if !found {
		t.Fatal("standalone skill shell block is not closed")
	}
	for _, fixture := range []struct{ existing, host string }{
		{"absent", ".claude"}, {"copied", ".claude"}, {"dangling", ".claude"},
		{"copied", ".agents"}, {"dangling", ".agents"},
	} {
		t.Run(fixture.existing+"/"+fixture.host, func(t *testing.T) {
			existing := fixture.existing
			base := t.TempDir()
			checkout, home := filepath.Join(base, "checkout"), filepath.Join(base, "home")
			source := filepath.Join(checkout, "skills", "brain")
			destination := filepath.Join(home, fixture.host, "skills", "brain")
			for _, dir := range []string{source, filepath.Dir(destination)} {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("generated skill\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if existing == "copied" {
				if err := os.Mkdir(destination, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(destination, "SKILL.md"), []byte("existing skill\n"), 0600); err != nil {
					t.Fatal(err)
				}
			} else if existing == "dangling" {
				if err := os.Symlink(filepath.Join(base, "missing"), destination); err != nil {
					t.Fatal(err)
				}
			}
			command := exec.Command("sh", "-eu", "-c", block)
			command.Dir = checkout
			command.Env = append(os.Environ(), "HOME="+home)
			output, err := command.CombinedOutput()
			if existing == "absent" {
				if err != nil {
					t.Fatal(err, string(output))
				}
				for harness, host := range map[string]string{"claude": ".claude", "codex": ".agents"} {
					path := filepath.Join(home, host, "skills", "brain")
					setup := exec.Command(binary, "setup", harness, "--state-dir", filepath.Join(base, "state"), "--format", "json")
					setup.Env = append(os.Environ(), "HOME="+home, "USERPROFILE="+home)
					data, err := setup.Output()
					var result struct {
						Setups []struct {
							SkillFile string `json:"skill_file"`
						} `json:"setups"`
					}
					if err != nil || json.Unmarshal(data, &result) != nil || len(result.Setups) != 1 || result.Setups[0].SkillFile != filepath.Join(path, "SKILL.md") {
						t.Fatal("link differs from setup destination", harness, string(data), err)
					}
					link, err := os.Readlink(path)
					if err != nil {
						t.Fatal(err)
					}
					resolved, err := filepath.EvalSymlinks(link)
					wanted, wantErr := filepath.EvalSymlinks(source)
					if err != nil || wantErr != nil || resolved != wanted {
						t.Fatal(link, resolved, wanted, err, wantErr)
					}
					if data, err := os.ReadFile(filepath.Join(path, "SKILL.md")); err != nil || string(data) != "generated skill\n" {
						t.Fatal(string(data), err)
					}
				}
				return
			}
			if err == nil || !strings.Contains(string(output), "existing skill destination") {
				t.Fatal("existing installation was not refused", err, string(output))
			}
			otherHost := ".agents"
			if fixture.host == otherHost {
				otherHost = ".claude"
			}
			if _, err := os.Lstat(filepath.Join(home, otherHost)); !os.IsNotExist(err) {
				t.Fatal("refused installation changed another host", err)
			}
			if existing == "copied" {
				entries, err := os.ReadDir(destination)
				if err != nil || len(entries) != 1 || entries[0].Name() != "SKILL.md" {
					t.Fatal("refusal changed the existing installation", entries, err)
				}
				if data, err := os.ReadFile(filepath.Join(destination, "SKILL.md")); err != nil || string(data) != "existing skill\n" {
					t.Fatal(string(data), err)
				}
			} else if link, err := os.Readlink(destination); err != nil || link != filepath.Join(base, "missing") {
				t.Fatal("refusal changed the dangling link", link, err)
			}
		})
	}
}
