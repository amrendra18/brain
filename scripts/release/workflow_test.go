package release

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// These strict types cover the workflow keys used here, from GitHub's workflow syntax.
type workflow struct {
	Name        string                     `json:"name"`
	On          map[string]json.RawMessage `json:"on"`
	Permissions map[string]string          `json:"permissions"`
	Jobs        map[string]job             `json:"jobs"`
}
type job struct {
	Runner   string `json:"runs-on"`
	Timeout  int    `json:"timeout-minutes"`
	Uses     string `json:"uses"`
	Needs    string `json:"needs"`
	Defaults struct {
		Run struct {
			Shell string `json:"shell"`
		} `json:"run"`
	} `json:"defaults"`
	Env      map[string]string `json:"env"`
	Strategy struct {
		FailFast bool `json:"fail-fast"`
		Matrix   struct {
			Include []struct{ Runner, Target string } `json:"include"`
		} `json:"matrix"`
	} `json:"strategy"`
	Steps []struct {
		Name, Run, Uses string
		With            map[string]any `json:"with"`
	} `json:"steps"`
}

func validateWorkflow(data []byte) (workflow, error) {
	var result workflow
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, err
	}
	if result.Name == "" || len(result.On) == 0 || len(result.Jobs) == 0 || result.Permissions["contents"] != "read" {
		return result, errors.New("missing workflow fields or writable token")
	}
	for event := range result.On {
		switch event {
		case "push", "pull_request", "workflow_call", "workflow_dispatch":
		default:
			return result, errors.New("unknown workflow event")
		}
	}
	pin := regexp.MustCompile(`^actions/(checkout|setup-go|upload-artifact)@[a-f0-9]{40}$`)
	for _, j := range result.Jobs {
		if j.Uses != "" {
			if j.Uses != "./.github/workflows/check.yml" || len(j.Steps) != 0 {
				return result, errors.New("invalid reusable workflow")
			}
			continue
		}
		if j.Runner == "" || j.Timeout < 1 || j.Timeout > 30 || j.Defaults.Run.Shell != "bash" || len(j.Steps) == 0 {
			return result, errors.New("invalid native job schema")
		}
		for _, step := range j.Steps {
			if (step.Run == "") == (step.Uses == "") {
				return result, errors.New("step requires exactly one run or uses")
			}
			if step.Uses != "" && !pin.MatchString(step.Uses) {
				return result, errors.New("action must have a fixed revision")
			}
		}
	}
	return result, nil
}

func TestWorkflowSchemaAndScripts(t *testing.T) {
	var check workflow
	for _, name := range []string{"check", "release"} {
		data, err := os.ReadFile(filepath.Join(root(t), ".github/workflows", name+".yml"))
		if err != nil {
			t.Fatal(err)
		}
		w, err := validateWorkflow(data)
		if err != nil {
			t.Fatal(name, err)
		}
		if name == "check" {
			check = w
		} else if w.Jobs["package"].Needs != "checks" || w.Jobs["checks"].Uses != "./.github/workflows/check.yml" {
			t.Fatal("packaging can bypass native checks")
		}
		for _, j := range w.Jobs {
			for _, step := range j.Steps {
				if strings.HasPrefix(step.Uses, "actions/setup-go@") && (step.With["go-version"] != "1.27.1" || step.With["cache"] != false) {
					t.Fatal("CI toolchain moved", step)
				}
				if strings.HasPrefix(step.Uses, "actions/checkout@") && (step.With["persist-credentials"] != false || step.With["fetch-depth"] != float64(0)) {
					t.Fatal("release checkout lacks clean history", step)
				}
				if step.Run != "" {
					words := strings.Fields(step.Run)
					if _, err := os.Stat(filepath.Join(root(t), words[0])); err != nil {
						t.Fatal(step.Run, err)
					}
				}
			}
		}
		broken := bytes.Replace(data, []byte(`"permissions"`), []byte(`"permissons"`), 1)
		if _, err := validateWorkflow(broken); err == nil {
			t.Fatal("unknown schema key accepted")
		}
	}
	native := check.Jobs["native"]
	var targets []string
	seen := map[string]bool{}
	for _, row := range native.Strategy.Matrix.Include {
		if seen[row.Runner] {
			t.Fatal("duplicate runner")
		}
		seen[row.Runner] = true
		targets = append(targets, row.Target)
	}
	want := append([]string(nil), Targets...)
	sort.Strings(want)
	sort.Strings(targets)
	if strings.Join(targets, ",") != strings.Join(want, ",") {
		t.Fatal(targets)
	}
	var steps []string
	for _, step := range native.Steps {
		steps = append(steps, step.Run)
	}
	if !strings.Contains(strings.Join(steps, "\n"), "scripts/prove-fts5\nscripts/check full") {
		t.Fatal("native FTS5 or full lane absent")
	}
	if _, ok := check.On["workflow_call"]; !ok {
		t.Fatal("check workflow is not reusable")
	}
	for _, script := range []string{"build", "package", "prepare-ci", "prove-fts5", "check", "go-env"} {
		command := exec.Command("sh", "-n", filepath.Join(root(t), "scripts", script))
		if data, err := command.CombinedOutput(); err != nil {
			t.Fatal(script, err, string(data))
		}
	}
	t.Log("strict JSON workflow schema, fixed action revisions, five native runners and executable script paths verified")
}

func TestPinnedCIEnvironment(t *testing.T) {
	command := exec.Command("sh", filepath.Join(root(t), "scripts/prepare-ci"), "--verify")
	if data, err := command.CombinedOutput(); err != nil || !bytes.Contains(data, []byte("CI pinned Go 1.27.1:")) {
		t.Fatal(err, string(data))
	}
	command = exec.Command("sh", filepath.Join(root(t), "scripts/prepare-ci"), "--install")
	command.Env = append(os.Environ(), "GITHUB_ACTIONS=false")
	if data, err := command.CombinedOutput(); err == nil || !bytes.Contains(data, []byte("usage: scripts/prepare-ci --verify")) {
		t.Fatal(err, string(data))
	}
	t.Log("CI uses setup-go's shared installation without copying a compiler into the checkout")
}

func TestPrepareCIRejectsExtraArguments(t *testing.T) {
	fixture := t.TempDir()
	scripts := filepath.Join(fixture, "scripts")
	if err := os.Mkdir(scripts, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root(t), "scripts/prepare-ci"))
	if err != nil {
		t.Fatal(err)
	}
	copied := filepath.Join(scripts, "prepare-ci")
	if err := os.WriteFile(copied, data, 0700); err != nil {
		t.Fatal(err)
	}
	fakeGo := filepath.Join(t.TempDir(), "go")
	if err := os.WriteFile(fakeGo, []byte("#!/bin/sh\nprintf 'fake Go must remain unused\\n'\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"--install", "extra"}, {"--verify", "extra"}, {}} {
		command := exec.Command("sh", append([]string{copied}, args...)...)
		command.Env = append(os.Environ(), "GITHUB_ACTIONS=true", "PATH="+filepath.Dir(fakeGo)+string(os.PathListSeparator)+os.Getenv("PATH"))
		data, err := command.CombinedOutput()
		exit, ok := err.(*exec.ExitError)
		if !ok || exit.ExitCode() != 2 || bytes.Contains(data, []byte("fake Go must remain unused")) {
			t.Fatal(args, err, string(data))
		}
		if _, err := os.Stat(filepath.Join(fixture, ".local")); !os.IsNotExist(err) {
			t.Fatal("invalid CI arguments created local files", err)
		}
	}
}
