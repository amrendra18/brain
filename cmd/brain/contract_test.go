package main

import (
	"brain/scripts/toolchain"
	"bytes"
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func contractVerbs(t *testing.T) []string {
	t.Helper()
	code, output := call(t, "help", "--format", "json")
	var help struct {
		Verbs []string `json:"verbs"`
	}
	if code != 0 || json.Unmarshal([]byte(output), &help) != nil || len(help.Verbs) == 0 {
		t.Fatal(code, output)
	}
	return help.Verbs
}

func TestToolContract(t *testing.T) {
	for _, verb := range contractVerbs(t) {
		t.Run(verb, func(t *testing.T) {
			code, output := call(t, verb, "--help")
			if code != 0 {
				t.Fatal(code, output)
			}
			for _, label := range []string{"Inputs:", "Outputs:", "Preconditions:", "Destinations:", "Effects:", "Exit codes:"} {
				if !strings.Contains(output, label) {
					t.Fatal(label, output)
				}
			}
			code, output = call(t, verb, "--no-such-flag", "--format", "json")
			var reply struct {
				Error struct {
					Code    string   `json:"code"`
					Message string   `json:"message"`
					Next    []string `json:"next"`
				} `json:"error"`
			}
			if code != 2 || json.Unmarshal([]byte(output), &reply) != nil || reply.Error.Code != "unknown-flag" || !strings.Contains(reply.Error.Message, "Valid flags:") || reply.Error.Next == nil {
				t.Fatal(code, output)
			}
		})
	}
	code, output := call(t, "no-such-verb", "--format", "json")
	if code != 2 || !strings.Contains(output, "Valid verbs:") {
		t.Fatal(code, output)
	}
	for _, verb := range contractVerbs(t) {
		if !strings.Contains(output, verb) {
			t.Fatal("unknown verb guidance omits", verb, output)
		}
	}
}

func TestStateSummaryAlias(t *testing.T) {
	j := newJourney(t)
	for _, ready := range []bool{false, true} {
		if ready {
			j.put(t, "README.md", "# Beacon\n\nA lighthouse.")
			j.ready(t)
		}
		before := treeBytes(t, j.base)
		bareCode, bare := j.run(t)
		middle := treeBytes(t, j.base)
		namedCode, named := j.run(t, "status")
		after := treeBytes(t, j.base)
		if bareCode != namedCode || !reflect.DeepEqual(bare, named) || !reflect.DeepEqual(before, middle) || !reflect.DeepEqual(middle, after) {
			t.Fatal(ready, bareCode, namedCode, bare, named)
		}
	}
}

func TestDoctorExitContract(t *testing.T) {
	j := newJourney(t)
	code, reply := j.run(t, "doctor")
	if code != 1 || reply["count"].(float64) < 1 || reply["error"] != nil {
		t.Fatal(code, reply)
	}
	j.put(t, "README.md", "# Beacon\n\nA lighthouse.")
	j.ready(t)
	code, reply = j.run(t, "doctor")
	if code != 3 || reply["count"] != float64(0) || reply["empty"] != true {
		t.Fatal(code, reply)
	}
	if err := os.WriteFile(filepath.Join(j.state, "index.sqlite"), []byte("broken test input"), 0600); err != nil {
		t.Fatal(err)
	}
	code, reply = j.run(t, "doctor")
	if code != 1 || reply["count"] != float64(1) || reply["problems"].([]any)[0].(map[string]any)["code"] != "index-damaged" {
		t.Fatal(code, reply)
	}
}

func TestEffectsReadsOnly(t *testing.T) {
	j := newJourney(t)
	j.put(t, "README.md", "# Beacon\n\nA lighthouse.")
	j.ready(t)
	before := treeBytes(t, j.base)
	beforeModes := contractModes(t, j.base)
	for _, args := range [][]string{{"brief"}, {"--brief"}, {"brief", "--format=json"}, {"setup", "all", "--format=json"}, {"help"}, {"help", "--skill"}, {"--version"}} {
		command := exec.Command(testBinary, args...)
		command.Dir = j.base
		command.Env = j.environment
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatal(args, err, string(output))
		}
		if after := treeBytes(t, j.base); !reflect.DeepEqual(before, after) || !reflect.DeepEqual(beforeModes, contractModes(t, j.base)) {
			t.Fatal(args, "changed read-only fixture")
		}
	}
}

func TestContractClosedStdin(t *testing.T) {
	j := newJourney(t)
	j.put(t, "alpha/README.md", "# Beacon\n\nA lighthouse.")
	j.ready(t)
	_, found := j.run(t, "search", "lighthouse")
	id := result(t, found, 0)["id"].(string)
	invocations := []struct {
		args []string
		code int
	}{
		{[]string{"status"}, 0}, {[]string{"init", "--source", j.source}, 3}, {[]string{"index"}, 3},
		{[]string{"search", "lighthouse"}, 0}, {[]string{"show", id}, 0}, {[]string{"projects"}, 0},
		{[]string{"recent"}, 0}, {[]string{"map", "alpha"}, 0}, {[]string{"doctor"}, 3},
		{[]string{"brief"}, 0}, {[]string{"setup", "all"}, 0}, {[]string{"help"}, 0},
		{[]string{"note", "A saved decision"}, 0}, {[]string{"search"}, 2},
	}
	for _, invocation := range invocations {
		args := append(invocation.args, "--format=json")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		command := exec.CommandContext(ctx, testBinary, args...)
		command.Dir = j.base
		command.Env = j.environment
		// A nil Stdin is the operating system's null device, so no terminal exists.
		var output, stderr bytes.Buffer
		command.Stdout = &output
		command.Stderr = &stderr
		err := command.Run()
		cancel()
		code := 0
		if err != nil {
			if exit, ok := err.(*exec.ExitError); ok {
				code = exit.ExitCode()
			} else {
				t.Fatal(args, err)
			}
		}
		var reply map[string]any
		if code != invocation.code || json.Unmarshal(output.Bytes(), &reply) != nil || stderr.Len() != 0 {
			t.Fatal(args, code, output.String(), stderr.String())
		}
	}
}

func contractModes(t *testing.T, root string) map[string]os.FileMode {
	t.Helper()
	modes := map[string]os.FileMode{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		modes[relative] = info.Mode()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return modes
}

func TestBriefJSON(t *testing.T) {
	j := newJourney(t)
	j.put(t, "README.md", "# Beacon\n\nA lighthouse.")
	j.ready(t)
	for _, name := range []string{"brief", "--brief"} {
		code, reply := j.run(t, name)
		if code != 0 || reply["ready"] != true || reply["index_age_s"] == nil || strings.Count(reply["text"].(string), "\n") != 3 {
			t.Fatal(name, code, reply)
		}
	}
}

func TestContractMissingInput(t *testing.T) {
	for _, verb := range []string{"search", "show", "map", "note"} {
		code, output := call(t, verb, "--format", "json")
		var reply map[string]map[string]any
		if code != 2 || json.Unmarshal([]byte(output), &reply) != nil || reply["error"]["code"] == nil {
			t.Fatal(verb, code, output)
		}
	}
}

func TestHelpVersionNoStateReads(t *testing.T) {
	checkHelpVersionStateReads(t, false)
}

func checkHelpVersionStateReads(t *testing.T, startupRead bool) {
	t.Helper()
	root := t.TempDir()
	binary := filepath.Join(root, "brain-covered")
	if os.PathSeparator == '\\' {
		binary += ".exe"
	}
	goBinary := toolchain.Binary()
	replacements := map[string]string{}
	buildArgs := []string{"build", "-cover", "-coverpkg=brain/cmd/brain,brain/internal/cli,brain/internal/platform,brain/internal/corpus,brain/internal/index,brain/internal/state", "-o", binary}
	if startupRead {
		original := filepath.Join(repoRoot, "cmd", "brain", "main.go")
		source, err := os.ReadFile(original)
		if err != nil {
			t.Fatal(err)
		}
		needle := "func main() {"
		if strings.Count(string(source), needle) != 1 {
			t.Fatal("entrypoint changed; update the deliberate startup-read test input")
		}
		changed := strings.Replace(string(source), needle, needle+` _, _ = os.ReadFile(os.Getenv("BRAIN_HOME") + "/config.json");`, 1)
		replacement := filepath.Join(root, "main.go")
		if err = os.WriteFile(replacement, []byte(changed), 0600); err != nil {
			t.Fatal(err)
		}
		replacements[original] = replacement
		encoded, err := json.Marshal(struct{ Replace map[string]string }{replacements})
		if err != nil {
			t.Fatal(err)
		}
		overlay := filepath.Join(root, "overlay.json")
		if err = os.WriteFile(overlay, encoded, 0600); err != nil {
			t.Fatal(err)
		}
		buildArgs = append(buildArgs, "-overlay="+overlay)
	}
	buildArgs = append(buildArgs, "./cmd/brain")
	build := exec.Command(goBinary, buildArgs...)
	build.Dir = repoRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	j := newJourney(t)
	j.put(t, "README.md", "# Beacon\n\nA lighthouse.")
	j.ready(t)
	measured := func(name string, invocations [][]string, wantExit int) int {
		t.Helper()
		coverage := filepath.Join(root, name)
		if err := os.Mkdir(coverage, 0700); err != nil {
			t.Fatal(err)
		}
		for _, args := range invocations {
			command := exec.Command(binary, args...)
			command.Dir = j.base
			command.Env = append(append([]string{}, j.environment...), "GOCOVERDIR="+coverage)
			output, err := command.CombinedOutput()
			code := 0
			if err != nil {
				if failure, ok := err.(*exec.ExitError); ok {
					code = failure.ExitCode()
				} else {
					t.Fatal(args, err, string(output))
				}
			}
			if code != wantExit {
				t.Fatalf("%v exited %d; want %d: %s", args, code, wantExit, output)
			}
		}
		profile := filepath.Join(root, name+".cover")
		convert := exec.Command(goBinary, "tool", "covdata", "textfmt", "-i="+coverage, "-o="+profile)
		if output, err := convert.CombinedOutput(); err != nil {
			t.Fatal(err, string(output))
		}
		data, err := os.ReadFile(profile)
		if err != nil {
			t.Fatal(err)
		}
		reads, observed := 0, 0
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) != 3 {
				continue
			}
			path, coordinates, _ := strings.Cut(fields[0], ":")
			parts := strings.Split(coordinates, ".")
			start, err := strconv.Atoi(parts[0])
			if err != nil {
				t.Fatal(err)
			}
			count, err := strconv.Atoi(fields[2])
			if err != nil {
				t.Fatal(err)
			}
			if stateReadBlock(t, path, start, replacements) {
				observed++
				reads += count
				if name == "invalid" && count != 0 {
					t.Logf("invalid stateless command read block: %s:%d (%d)", path, start, count)
				}
			}
		}
		if observed == 0 {
			t.Fatal("coverage contained no state-read blocks")
		}
		return reads
	}
	if startupRead {
		if got := measured("startup-read", [][]string{{"--help"}}, 0); got == 0 {
			t.Fatal("detector missed the deliberately injected entrypoint read")
		} else {
			t.Logf("detector rejected the startup-read test input with %d observed read blocks", got)
		}
		return
	}
	commands := [][]string{{"--help"}, {"help"}, {"help", "--format=json"}, {"help", "--format=markdown"}, {"help", "--skill"}, {"--version"}, {"--version", "--format=json"}, {"--version", "--format=text"}, {"help", "doctor", "--state-dir", j.state}}
	for _, format := range []string{"json", "markdown"} {
		commands = append(commands, []string{"help", "doctor", "--state-dir", j.state, "--format", format}, []string{"help", "doctor", "--format", format, "--state-dir", j.state})
	}
	for _, verb := range contractVerbs(t) {
		commands = append(commands, []string{verb, "--help"}, []string{"help", verb})
	}
	if got := measured("help", commands, 0); got != 0 {
		t.Fatalf("help/version executed %d state-read blocks; want zero", got)
	}
	invalid := [][]string{{"unknown-stateless-verb"}, {"help", "unknown-stateless-topic"}, {"--version", "--unknown-stateless-flag"}}
	if got := measured("invalid", invalid, 2); got != 0 {
		t.Fatalf("invalid stateless commands executed %d state-read blocks; want zero", got)
	}
	if got := measured("status", [][]string{{"status", "--format=json"}}, 0); got == 0 {
		t.Fatal("status control recorded no state reads")
	} else {
		t.Logf("status control executed %d state-read blocks; help/version executed zero", got)
	}
}

func stateReadBlock(t *testing.T, path string, line int, replacements map[string]string) bool {
	t.Helper()
	if strings.HasPrefix(path, "brain/internal/corpus/") || strings.HasPrefix(path, "brain/internal/index/") || strings.HasPrefix(path, "brain/internal/state/") {
		return true
	}
	if !strings.HasPrefix(path, "brain/internal/platform/") && !strings.HasPrefix(path, "brain/internal/cli/") && !strings.HasPrefix(path, "brain/cmd/brain/") {
		return false
	}
	relative := strings.TrimPrefix(path, "brain/")
	fileSet := token.NewFileSet()
	source := filepath.Join(repoRoot, relative)
	if replacement := replacements[source]; replacement != "" {
		source = replacement
	}
	file, err := parser.ParseFile(fileSet, source, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range file.Decls {
		function, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if line < fileSet.Position(function.Pos()).Line || line > fileSet.Position(function.End()).Line {
			continue
		}
		switch function.Name.Name {
		case "StateFolder", "CurrentState", "NetworkPath", "Command", "ShellQuote", "CmdQuote", "PowerShellQuote":
			return false
		}
		reads := false
		ast.Inspect(function, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch selector.Sel.Name {
			case "OpenRoot", "Open", "OpenFile", "ReadFile", "ReadDir", "Stat", "Lstat", "Statfs", "Access":
				reads = true
			}
			return true
		})
		return reads
	}
	return false
}
