package main

import (
	"brain/scripts/toolchain"
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

var testBinary, repoRoot string

func TestMain(m *testing.M) {
	if os.Getenv("BRAIN_TEST_ARGV") == "1" {
		json.NewEncoder(os.Stdout).Encode(os.Args[1:])
		os.Exit(0)
	}
	if folder := os.Getenv("BRAIN_TEST_ORPHAN_WAL"); folder != "" {
		db, err := sql.Open("sqlite", filepath.Join(folder, "index.sqlite"))
		if err == nil {
			_, err = db.Exec("PRAGMA journal_mode=WAL; UPDATE meta SET value=123456789 WHERE key='scanned_at'")
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		// Exit before closing SQLite, like an interrupted index process.
		os.Exit(0)
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	repoRoot = root
	dir, err := os.MkdirTemp("", "brain-binary-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	testBinary = filepath.Join(dir, "brain")
	if runtime.GOOS == "windows" {
		testBinary += ".exe"
	}
	command := exec.Command(toolchain.Binary(), "build", "-trimpath", "-buildvcs=false", "-o", testBinary, "./cmd/brain")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		os.RemoveAll(dir)
		fmt.Fprintf(os.Stderr, "build test binary: %v\n%s", err, output)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func call(t *testing.T, args ...string) (int, string) {
	t.Helper()
	dir := t.TempDir()
	command := exec.Command(testBinary, args...)
	command.Dir = dir
	command.Env = []string{"PATH=", "BRAIN_HOME=" + filepath.Join(dir, "state")}
	var output, stderr bytes.Buffer
	command.Stdout, command.Stderr = &output, &stderr
	err := command.Run()
	if stderr.Len() != 0 {
		t.Fatalf("stderr: %s", stderr.String())
	}
	code := 0
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("command wrote files: %v, %v", entries, err)
	}
	return code, output.String()
}

func TestVersionBeforeState(t *testing.T) {
	for _, args := range [][]string{{"--version"}, {"--version", "--format", "json"}, {"--version", "--format=text"}} {
		code, output := call(t, args...)
		if code != 0 || output != "{\"version\":\"0.1.0-dev\",\"revision\":\"\",\"modified\":false}\n" {
			t.Fatal(code, output)
		}
	}
}

func TestUnknownFlagExit2(t *testing.T) {
	code, output := call(t, "--unknown")
	var result struct {
		Error struct {
			Code     string     `json:"code"`
			Message  string     `json:"message"`
			Next     []string   `json:"next"`
			NextArgv [][]string `json:"next_argv"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatal(err)
	}
	if code != 2 || result.Error.Code != "unknown-flag" || strings.Contains(result.Error.Message, "--unknown") || !strings.Contains(result.Error.Message, "Valid flags:") {
		t.Fatal(code, output)
	}
	if len(result.Error.Next) != 1 || len(result.Error.NextArgv) != 1 {
		t.Fatal(output)
	}
	command := exec.Command(result.Error.NextArgv[0][0], result.Error.NextArgv[0][1:]...)
	command.Dir = t.TempDir()
	command.Env = []string{"PATH="}
	if output, err := command.CombinedOutput(); err != nil || !bytes.Contains(output, []byte("Commands:")) {
		t.Fatal(err, string(output))
	}
	if strings.Contains(result.Error.Message, repoRoot) {
		t.Fatal("refusal message contains an absolute path")
	}
}

func TestHelpJourney(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"--help"}} {
		code, output := call(t, args...)
		if code != 0 || !strings.Contains(output, "--version") || !strings.Contains(output, "Example:") {
			t.Fatal(code, output)
		}
	}
}

func TestRefusalsHideAbsolutePaths(t *testing.T) {
	for _, args := range [][]string{{"/private/example"}, {"--version", "/private/example"}, {"--bad=/private/example"}, {"--version", "--format=/private/example"}} {
		code, output := call(t, args...)
		var result map[string]map[string]json.RawMessage
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			t.Fatal(err)
		}
		if code != 2 || bytes.Contains(result["error"]["message"], []byte("/private/example")) {
			t.Fatal(code, output)
		}
	}
	code, output := call(t, "--version", "--format=xml")
	if code != 2 || strings.Contains(output, "xml") || !strings.Contains(output, "--format") {
		t.Fatal(code, output)
	}
}

func TestPrintedNextRunsInShell(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("POSIX shell journey runs on Unix")
	}
	dir := t.TempDir()
	copyPath := filepath.Join(dir, "brain $cash `tick` 'quote' \"double\"")
	data, err := os.ReadFile(testBinary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copyPath, data, 0700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(copyPath, "--bad")
	command.Dir = dir
	command.Env = []string{"PATH=", "BRAIN_HOME=" + filepath.Join(dir, "state")}
	output, err := command.Output()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 2 {
		t.Fatal(err)
	}
	var result struct {
		Error struct {
			Next []string `json:"next"`
		} `json:"error"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatal(err)
	}
	command = exec.Command("/bin/sh", "-c", result.Error.Next[0])
	command.Dir = dir
	command.Env = []string{"PATH="}
	if output, err := command.CombinedOutput(); err != nil || !bytes.Contains(output, []byte("Commands:")) {
		t.Fatal(err, string(output))
	}
}

func TestModuleDependencyList(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{
		"modernc.org/sqlite":               "v1.60.1",
		"github.com/dustin/go-humanize":    "v1.0.1",
		"github.com/google/uuid":           "v1.6.0",
		"github.com/mattn/go-isatty":       "v0.0.24",
		"github.com/ncruces/go-strftime":   "v1.0.0",
		"github.com/remyoudompheng/bigfft": "v0.0.0-20230129092748-24d4a6f8daec",
		"golang.org/x/sys":                 "v0.48.0",
		"modernc.org/libc":                 "v1.77.1",
		"modernc.org/mathutil":             "v1.7.1",
		"modernc.org/memory":               "v1.12.1",
	}
	inRequire := false
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if fields[0] == "require" {
			fields = fields[1:]
			if len(fields) == 1 && fields[0] == "(" {
				inRequire = true
				continue
			}
		} else if fields[0] == ")" {
			inRequire = false
			continue
		} else if !inRequire {
			continue
		}
		if len(fields) < 2 || expected[fields[0]] != fields[1] {
			t.Fatalf("unapproved dependency: %s", line)
		}
		if fields[0] != "modernc.org/sqlite" && !strings.HasSuffix(strings.TrimSpace(line), "// indirect") {
			t.Fatalf("new direct dependency: %s", line)
		}
		if fields[0] == "modernc.org/sqlite" && strings.HasSuffix(strings.TrimSpace(line), "// indirect") {
			t.Fatal("SQLite must be the one direct dependency")
		}
		delete(expected, fields[0])
	}
	if len(expected) != 0 {
		t.Fatal("missing pinned dependencies", expected)
	}
}

func TestVersionWarm(t *testing.T) {
	call(t, "--version")
	durations := make([]time.Duration, 10)
	for i := range durations {
		started := time.Now()
		code, _ := call(t, "--version")
		durations[i] = time.Since(started)
		if code != 0 {
			t.Fatal(code)
		}
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	median := (durations[4] + durations[5]) / 2
	t.Logf("warm --version median: %.3f ms over 10 runs", float64(median)/float64(time.Millisecond))
	if median >= time.Second {
		t.Fatal("warm version took one second or more")
	}
}

func TestPinnedModuleCache(t *testing.T) {
	for name, want := range map[string]string{
		"go.mod": "8262eb3464fd66f4e78fcecabc252ec83532b34f",
		"go.sum": "8b50aa662c9c2840fd7832b09b003e4894345ee3",
	} {
		command := exec.Command("git", "hash-object", name)
		command.Dir = repoRoot
		data, err := command.Output()
		if err != nil || strings.TrimSpace(string(data)) != want {
			t.Fatalf("%s differs from the pinned module file: %v %s", name, err, data)
		}
	}
	command := exec.Command(toolchain.Binary(), "mod", "verify")
	command.Dir = repoRoot
	command.Env = append(os.Environ(), "GOENV=off", "GOTOOLCHAIN=local", "GOWORK=off", "GOPROXY=off", "GOFLAGS=-mod=readonly")
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatalf("pinned module cache did not verify offline: %v %s", err, data)
	}
}

func TestProductionLineCount(t *testing.T) {
	total := 0
	for _, folder := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(repoRoot, folder), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if entry.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if bytes.HasPrefix(data, []byte("// Code generated ")) {
				return nil
			}
			total += bytes.Count(data, []byte("\n"))
			if len(data) != 0 && data[len(data)-1] != '\n' {
				total++
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	fmt.Fprintf(os.Stderr, "production lines: %d; review changes with their behavior and tests\n", total)
}
