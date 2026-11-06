package main

import (
	"brain/internal/cli"
	"brain/internal/platform"
	"brain/tests/testutil"
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

type journey struct {
	base, source, state string
	environment         []string
}

func newJourney(t *testing.T) *journey {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source, state := filepath.Join(base, "corpus"), filepath.Join(base, "state")
	if err = os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	return &journey{base, source, state, []string{"PATH=/usr/bin:/bin", "HOME=" + base, "USERPROFILE=" + base, "SystemRoot=" + os.Getenv("SystemRoot"), "BRAIN_HOME=" + state}}
}
func (j *journey) put(t *testing.T, name, text string) string {
	t.Helper()
	path := filepath.Join(j.source, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func (j *journey) run(t *testing.T, args ...string) (int, map[string]any) {
	t.Helper()
	formatted := false
	for _, arg := range args {
		if arg == "--format" || strings.HasPrefix(arg, "--format=") {
			formatted = true
		}
	}
	if !formatted {
		args = append(args, "--format", "json")
	}
	command := exec.Command(testBinary, args...)
	command.Dir = j.base
	command.Env = j.environment
	output, err := command.Output()
	code := 0
	if err != nil {
		if failure, ok := err.(*exec.ExitError); ok {
			code = failure.ExitCode()
			if len(failure.Stderr) > 0 {
				t.Fatal(string(failure.Stderr))
			}
		} else {
			t.Fatal(err)
		}
	}
	var reply map[string]any
	if err = json.Unmarshal(output, &reply); err != nil {
		t.Fatal(string(output), err)
	}
	return code, reply
}
func (j *journey) ready(t *testing.T) {
	t.Helper()
	if code, reply := j.run(t, "init", "--source", j.source); code != 0 {
		t.Fatal(code, reply)
	}
	if code, reply := j.run(t, "index"); code != 0 {
		t.Fatal(code, reply)
	}
}
func result(t *testing.T, reply map[string]any, n int) map[string]any {
	t.Helper()
	return reply["results"].([]any)[n].(map[string]any)
}

func TestIndexSearchShowJourney(t *testing.T) {
	j := newJourney(t)
	j.put(t, "alpha/README.md", "# Dock\n\nBeacon Harbor returns a lease.\n\n## Release\n\nUntie the boats.")
	j.put(t, "beta/README.md", "# Dock\n\nBeacon Harbor returns a lease.")
	j.ready(t)
	code, reply := j.run(t, "search", "beacon_harbor")
	if code != 0 || reply["count"] != float64(2) || reply["query"].(map[string]any)["mode"] != "and" {
		t.Fatal(code, reply)
	}
	item := result(t, reply, 0)
	if len(item["also_in"].([]any)) != 0 {
		t.Fatal(item)
	}
	code, reply = j.run(t, "show", item["id"].(string))
	if code != 0 || !strings.Contains(reply["text"].(string), "lease") || reply["index_age_s"] == nil {
		t.Fatal(code, reply)
	}
	code, reply = j.run(t, "search", "boats nonexisting")
	if code != 0 || reply["query"].(map[string]any)["mode"] != "or" {
		t.Fatal(code, reply)
	}
	code, reply = j.run(t, "index")
	if code != 3 || reply["unchanged"] != float64(2) {
		t.Fatal(code, reply)
	}
	code, reply = j.run(t, "init", "--source", j.source)
	if code != 3 {
		t.Fatal(code, reply)
	}
}
func TestIncrementalRefresh(t *testing.T) {
	j := newJourney(t)
	for n := 0; n < 300; n++ {
		j.put(t, fmt.Sprintf("project%d/docs/file%d.md", n%6, n), fmt.Sprintf("# File %d\n\nPlain words for file %d.", n, n))
	}
	j.ready(t)
	durations := []time.Duration{}
	for n := 0; n < 5; n++ {
		start := time.Now()
		code, reply := j.run(t, "search", "plain words")
		durations = append(durations, time.Since(start))
		if code != 0 || reply["total"] != float64(300) {
			t.Fatal(code, reply)
		}
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	rescan := durations[len(durations)/2]
	for n := 0; n < 5; n++ {
		j.put(t, fmt.Sprintf("project%d/docs/file%d.md", n%6, n), fmt.Sprintf("# File %d\n\nThe heron migrates at dawn, note %d.", n, n))
	}
	if code, reply := j.run(t, "search", "heron migrates dawn"); code != 0 || reply["total"] != float64(0) {
		t.Fatal(code, reply)
	}
	start := time.Now()
	if code, reply := j.run(t, "index"); code != 0 {
		t.Fatal(code, reply)
	}
	code, reply := j.run(t, "search", "heron migrates dawn")
	edited := time.Since(start)
	fmt.Fprintf(os.Stderr, "stored search over 300 files: %.3f ms (bar 100 ms); five-file explicit index/search: %.3f ms (bar 500 ms)\n", float64(rescan)/float64(time.Millisecond), float64(edited)/float64(time.Millisecond))
	if code != 0 || reply["total"] != float64(5) || reply["index_age_s"] != float64(0) || rescan >= 100*time.Millisecond || edited >= 500*time.Millisecond {
		t.Fatal(code, reply, rescan, edited)
	}
	db, err := sql.Open("sqlite", filepath.Join(j.state, "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("UPDATE meta SET value=value-100 WHERE key='scanned_at'"); err != nil {
		t.Fatal(err)
	}
	code, reply = j.run(t, "search", "heron", "--no-refresh")
	if code != 0 || reply["index_age_s"].(float64) < 100 {
		t.Fatal(code, reply)
	}
	code, reply = j.run(t, "search", "heron")
	if code != 0 || reply["index_age_s"].(float64) < 100 {
		t.Fatal("stored read changed index age", code, reply)
	}
	code, reply = j.run(t, "search", "heron", "--no-refresh")
	if code != 0 || reply["index_age_s"].(float64) < 100 {
		t.Fatal("no-refresh lost stored age", code, reply)
	}
}
func TestRelativePathsSameIds(t *testing.T) {
	ids := []string{}
	for n := 0; n < 2; n++ {
		j := newJourney(t)
		j.put(t, "alpha/README.md", "# Same\n\nA stable lighthouse keeper.")
		j.ready(t)
		code, reply := j.run(t, "search", "lighthouse")
		if code != 0 {
			t.Fatal(code, reply)
		}
		ids = append(ids, result(t, reply, 0)["id"].(string))
		db, err := sql.Open("sqlite", filepath.Join(j.state, "index.sqlite"))
		if err != nil {
			t.Fatal(err)
		}
		var path string
		err = db.QueryRow("SELECT path FROM files").Scan(&path)
		db.Close()
		if err != nil || path != "alpha/README.md" {
			t.Fatal(path, err)
		}
	}
	if ids[0] != ids[1] {
		t.Fatal(ids)
	}
}
func sourceSnapshot(t *testing.T, source string) map[string]string {
	t.Helper()
	found := map[string]string{}
	err := filepath.WalkDir(source, func(path string, item os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if item.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		hash := sha256.Sum256(data)
		found[path] = fmt.Sprintf("%x", hash)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}
func TestNoWritesOutsideState(t *testing.T) {
	t.Run("state symlinks", testStateSymlinks)
	j := newJourney(t)
	file := j.put(t, "alpha/README.md", "# Keeper\n\nThe lighthouse keeper tends the lamp.")
	testutil.Permissions(t, file, 0400)
	before := sourceSnapshot(t, j.source)
	j.ready(t)
	code, reply := j.run(t, "search", "lighthouse keeper")
	if code != 0 {
		t.Fatal(code, reply)
	}
	code, reply = j.run(t, "show", result(t, reply, 0)["id"].(string))
	if code != 0 {
		t.Fatal(code, reply)
	}
	if code, reply = j.run(t, "note", "A lighthouse decision", "--project", "alpha"); code != 0 || reply["indexed"] != true {
		t.Fatal(code, reply)
	}
	if after := sourceSnapshot(t, j.source); !reflect.DeepEqual(before, after) {
		t.Fatal("source files changed")
	}
	entries, err := os.ReadDir(j.base)
	if err != nil || len(entries) != 2 {
		t.Fatal(entries, err)
	}
	for _, entry := range entries {
		if entry.Name() != "corpus" && entry.Name() != "state" {
			t.Fatal("write outside state", entry.Name())
		}
	}
}
func TestCommandRefusals(t *testing.T) {
	j := newJourney(t)
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"show"}, "missing-argument"}, {[]string{"show", "id", "extra"}, "unexpected-argument"},
		{[]string{"init", "--unknown"}, "unknown-flag"}, {[]string{"index", "--full=yes"}, "unexpected-argument"}, {[]string{"search", "lease", "--limit", "101"}, "invalid-value"},
		{[]string{"search", "lease", "--kind", "missing"}, "invalid-value"}, {[]string{"search", "lease", "--shell", "missing"}, "invalid-value"},
		{[]string{"search", "lease", "--state-dir", "relative"}, "invalid-value"}, {[]string{"index"}, "state-uninitialised"},
		{[]string{"search", "lease", "--format=/private/tmp"}, "invalid-value"},
		{[]string{"unknown /private/tmp"}, "unknown-command"},
	} {
		code, reply := j.run(t, test.args...)
		error := reply["error"].(map[string]any)
		if code == 0 || error["code"] != test.want || strings.Contains(error["message"].(string), "/private/tmp") {
			t.Fatal(test.args, code, reply)
		}
	}
	j.put(t, "alpha/README.md", "# Lease\n\nReturn the lease.")
	j.ready(t)
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"search"}, "query-empty"}, {[]string{"search", "???"}, "query-empty"}, {[]string{"search", "lease", "--project", "unknown"}, "project-unknown"},
		{[]string{"show", "not-an-id"}, "chunk-not-found"},
	} {
		code, reply := j.run(t, test.args...)
		error := reply["error"].(map[string]any)
		if code == 0 || error["code"] != test.want {
			t.Fatal(test.args, code, reply)
		}
	}
}

func TestInvalidQueryHasRunnableRetry(t *testing.T) {
	j := newJourney(t)
	j.put(t, "alpha/README.md", "# Lease\n\nReturn the lease.")
	j.ready(t)
	t.Setenv("BRAIN_HOME", j.state)
	var output bytes.Buffer
	if code := cli.Run([]string{"search", "lease\x00id", "--format", "json"}, &output); code != 2 {
		t.Fatal(code, output.String())
	}
	var reply struct {
		Error struct {
			Code string     `json:"code"`
			Argv [][]string `json:"next_argv"`
		} `json:"error"`
	}
	if err := json.Unmarshal(output.Bytes(), &reply); err != nil || reply.Error.Code != "query-invalid" || len(reply.Error.Argv) != 1 {
		t.Fatal(reply, err)
	}
	argv := reply.Error.Argv[0]
	for _, word := range argv {
		if strings.ContainsRune(word, 0) {
			t.Fatal("retry contains NUL")
		}
	}
	command := exec.Command(testBinary, argv[1:]...)
	command.Dir = j.base
	command.Env = j.environment
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatal(string(output), err)
	}
}

func TestSourceUnreadableKeepsOldIndex(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix read/search permission modes need Unix; moved-source refusals are proved separately on Windows")
	}
	j := newJourney(t)
	j.put(t, "alpha/README.md", "# Keeper\n\nThe lighthouse keeper tends the lamp.")
	j.ready(t)
	code, reply := j.run(t, "search", "lighthouse keeper")
	if code != 0 || reply["total"] != float64(1) {
		t.Fatal(code, reply)
	}
	t.Cleanup(func() { testutil.Permissions(t, j.source, 0700) })
	for _, mode := range []os.FileMode{0400, 0410} {
		if err := testutil.Permissions(t, j.source, mode); err != nil {
			t.Fatal(err)
		}
		if platform.ReadableDir(j.source) {
			testutil.Permissions(t, j.source, 0700)
			continue
		}
		code, reply = j.run(t, "index")
		if code != 1 || reply["error"].(map[string]any)["code"] != "source-unreadable" {
			t.Fatal(code, reply)
		}
		argv := reply["error"].(map[string]any)["next_argv"].([]any)
		if len(argv) != 1 {
			t.Fatal(reply)
		}
		code, reply = j.run(t, "search", "lighthouse keeper", "--no-refresh")
		if code != 0 || reply["total"] != float64(1) {
			t.Fatal(code, reply)
		}
		if err := testutil.Permissions(t, j.source, 0700); err != nil {
			t.Fatal(err)
		}
		code, reply = j.run(t, "search", "lighthouse keeper")
		if code != 0 || reply["total"] != float64(1) {
			t.Fatal(code, reply)
		}
	}
}
