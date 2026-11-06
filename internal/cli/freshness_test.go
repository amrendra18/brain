package cli

import (
	"brain/internal/index"
	"brain/internal/platform"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func freshnessFixture(t *testing.T) (string, string, func(...string) (int, string)) {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join(root, "source")
	state := filepath.Join(root, "state")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("# Seed\n\nOriginal skylark."), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BRAIN_HOME", state)
	run := func(args ...string) (int, string) {
		var out bytes.Buffer
		code := Run(append(args, "--format", "json"), &out)
		return code, out.String()
	}
	if code, out := run("init", "--source", source); code != 0 {
		t.Fatal(code, out)
	}
	return source, state, run
}

func TestReadingCommandsNeverRefresh(t *testing.T) {
	source, folder, run := freshnessFixture(t)
	if code, out := run("index"); code != 0 {
		t.Fatal(code, out)
	}
	_, out := run("search", "skylark")
	var search searchReply
	if err := json.Unmarshal([]byte(out), &search); err != nil || len(search.Results) != 1 {
		t.Fatal(out, err)
	}
	db, err := index.Open(folder, false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	old := float64(time.Now().Add(-2 * time.Hour).Unix())
	if _, err = db.Exec("UPDATE meta SET value=? WHERE key='scanned_at'", old); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if err = os.Rename(source, source+"-offline"); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"search", "skylark"}, {"show", search.Results[0].ID}, {"projects"}, {"recent"}, {"map", "source"}, {"status"}, {"brief"}, {"doctor"}} {
		code, out := run(args...)
		want := 0
		if args[0] == "doctor" {
			want = 3
		}
		if code != want {
			t.Fatalf("%v: %d %s", args, code, out)
		}
		if args[0] != "doctor" && !strings.Contains(out, `"index_age_s":72`) {
			t.Fatalf("lost stored age %v: %s", args, out)
		}
		if args[0] == "status" && !strings.Contains(out, `"pending_known":false`) {
			t.Fatal(out)
		}
	}
}

func TestManualIndexDiscoversProject(t *testing.T) {
	source, _, run := freshnessFixture(t)
	if code, out := run("index"); code != 0 {
		t.Fatal(code, out)
	}
	folder := filepath.Join(source, "newproject")
	if err := os.Mkdir(folder, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "README.md"), []byte("# Arrival\n\nZircon voyager."), 0600); err != nil {
		t.Fatal(err)
	}
	if code, out := run("search", "zircon"); code != 0 || !strings.Contains(out, `"count":0`) {
		t.Fatal(code, out)
	}
	if code, out := run("index"); code != 0 {
		t.Fatal(code, out)
	}
	if code, out := run("search", "zircon"); code != 0 || !strings.Contains(out, `"count":1`) {
		t.Fatal(code, out)
	}
}

func TestNoteDoesNotScanSources(t *testing.T) {
	for _, first := range []bool{false, true} {
		t.Run(map[bool]string{false: "note-only", true: "existing-scan"}[first], func(t *testing.T) {
			source, folder, run := freshnessFixture(t)
			if first {
				if code, out := run("index"); code != 0 {
					t.Fatal(code, out)
				}
			}
			if err := os.Rename(source, source+"-offline"); err != nil {
				t.Fatal(err)
			}
			var before float64
			if first {
				db, _ := index.Open(folder, false, false, false)
				before, _ = index.ScannedAt(db)
				db.Close()
			}
			if code, out := run("note", "A violet memory.", "--project", "source"); code != 0 || !strings.Contains(out, `"indexed":true`) {
				t.Fatal(code, out)
			}
			if code, out := run("search", "violet"); code != 0 || !strings.Contains(out, `"count":1`) || !strings.Contains(out, `"index_complete":`+map[bool]string{false: "false", true: "true"}[first]) {
				t.Fatal(code, out)
			}
			db, err := index.Open(folder, false, false, false)
			if err != nil {
				t.Fatal(err)
			}
			after, err := index.ScannedAt(db)
			db.Close()
			if err != nil || before != after {
				t.Fatal(before, after, err)
			}
			if first {
				if code, out := run("search", "skylark"); code != 0 || !strings.Contains(out, `"count":1`) {
					t.Fatal(code, out)
				}
			}
		})
	}
}

func TestFailedIndexKeepsSuccessfulTime(t *testing.T) {
	source, folder, run := freshnessFixture(t)
	if code, out := run("index"); code != 0 {
		t.Fatal(code, out)
	}
	db, _ := index.Open(folder, false, false, false)
	before, _ := index.ScannedAt(db)
	db.Close()
	if err := os.Rename(source, source+"-offline"); err != nil {
		t.Fatal(err)
	}
	if code, out := run("index"); code != 1 {
		t.Fatal(code, out)
	}
	db, _ = index.Open(folder, false, false, false)
	after, _ := index.ScannedAt(db)
	db.Close()
	if before != after {
		t.Fatal(before, after)
	}
	if code, out := run("doctor"); code != 1 || !strings.Contains(out, "index-failed") {
		t.Fatal(code, out)
	}
	if err := os.WriteFile(filepath.Join(folder, "config.json"), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if code, out := run("index"); code != 1 || !strings.Contains(out, "config-invalid") {
		t.Fatal(code, out)
	}
	receipt, exists, err := index.LastRun(folder)
	if err != nil || !exists || receipt.Outcome != "failed" {
		t.Fatal(receipt, exists, err)
	}
}

func TestExplicitIndexLoadsConfigAfterLock(t *testing.T) {
	source, folder, run := freshnessFixture(t)
	if code, out := run("index"); code != 0 {
		t.Fatal(code, out)
	}
	receiptBefore, err := os.ReadFile(filepath.Join(folder, "index-run.json"))
	if err != nil {
		t.Fatal(err)
	}
	lock, err := platform.Acquire(folder, "index", true)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	type result struct {
		code int
		out  string
	}
	finished := make(chan result, 1)
	go func() { code, out := run("index"); finished <- result{code, out} }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		stack := make([]byte, 1<<20)
		stack = stack[:runtime.Stack(stack, true)]
		if bytes.Contains(stack, []byte("brain/internal/platform.Acquire(")) && bytes.Contains(stack, []byte("brain/internal/cli.(*call).index(")) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("index did not reach its lock")
		}
		time.Sleep(time.Millisecond)
	}
	receiptDuring, err := os.ReadFile(filepath.Join(folder, "index-run.json"))
	if err != nil || !bytes.Equal(receiptBefore, receiptDuring) {
		t.Fatal("waiting index changed its receipt", err)
	}
	other := filepath.Join(filepath.Dir(source), "later")
	if err = os.Mkdir(other, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(other, "README.md"), []byte("# Later\n\nA turquoise selection."), 0600); err != nil {
		t.Fatal(err)
	}
	if code, out := run("sources", "move", source, other); code != 0 {
		t.Fatal(code, out)
	}
	lock.Close()
	got := <-finished
	if got.code != 0 {
		t.Fatal(got)
	}
	if code, out := run("search", "turquoise"); code != 0 || !strings.Contains(out, `"count":1`) {
		t.Fatal(code, out)
	}
	if code, out := run("search", "skylark"); code != 0 || !strings.Contains(out, `"count":0`) {
		t.Fatal(code, out)
	}
}

func TestDoctorStoredFreshnessWarnings(t *testing.T) {
	_, folder, run := freshnessFixture(t)
	if code, out := run("note", "Note before any full scan.", "--project", "memo"); code != 0 {
		t.Fatal(code, out)
	}
	if code, out := run("doctor"); code != 1 || !strings.Contains(out, "index-missing") {
		t.Fatal(code, out)
	}
	if code, out := run("index"); code != 0 {
		t.Fatal(code, out)
	}
	db, err := index.Open(folder, false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("UPDATE meta SET value=value-86401 WHERE key='scanned_at'"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if code, out := run("doctor"); code != 1 || !strings.Contains(out, "index-stale") {
		t.Fatal(code, out)
	}
	if code, out := run("index"); code != 3 {
		t.Fatal(code, out)
	}
	if code, out := run("doctor"); code != 3 {
		t.Fatal(code, out)
	}
	if code, out := run("exclude", "add", "*.draft"); code != 0 {
		t.Fatal(code, out)
	}
	if code, out := run("doctor"); code != 1 || !strings.Contains(out, "index-stale") {
		t.Fatal(code, out)
	}
}

func TestBriefThreeLines(t *testing.T) {
	_, _, run := freshnessFixture(t)
	for _, complete := range []bool{false, true} {
		command := []string{"note", "An isolated first note.", "--project", "memo"}
		if complete {
			command = []string{"index"}
		}
		if code, out := run(command...); code != 0 {
			t.Fatal(code, out)
		}
		code, out := run("brief")
		var reply struct {
			Text          string
			Ready         bool
			IndexComplete bool `json:"index_complete"`
			Age           int  `json:"index_age_s"`
		}
		if code != 0 || json.Unmarshal([]byte(out), &reply) != nil || !reply.Ready || reply.IndexComplete != complete || len(strings.Split(strings.TrimSpace(reply.Text), "\n")) != 3 {
			t.Fatal(code, out)
		}
	}
}

func TestFreshnessStateAliases(t *testing.T) {
	_, folder, run := freshnessFixture(t)
	canonical, err := filepath.EvalSymlinks(folder)
	if err != nil {
		t.Fatal(err)
	}
	parentAlias := filepath.Join(t.TempDir(), "parent-alias")
	if err = os.Symlink(filepath.Dir(canonical), parentAlias); err != nil {
		t.Skip("owned state alias unavailable")
	}
	alias := filepath.Join(parentAlias, filepath.Base(canonical))
	if code, out := run("note", "Before canonical indexing.", "--project", "memo"); code != 0 {
		t.Fatal(code, out)
	}
	if code, out := run("index", "--state-dir", canonical); code != 0 {
		t.Fatal(code, out)
	}
	if code, out := run("doctor", "--state-dir", alias); code != 3 {
		t.Fatal(code, out)
	}
	if code, out := run("note", "After canonical indexing.", "--project", "memo", "--state-dir", alias); code != 0 || !strings.Contains(out, `"indexed":true`) {
		t.Fatal(code, out)
	}
	if code, out := run("doctor", "--state-dir", canonical); code != 3 {
		t.Fatal(code, out)
	}
	db, err := index.Open(folder, false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var files int
	if err = db.QueryRow("SELECT count(*) FROM files WHERE kind='note' AND deleted_at IS NULL").Scan(&files); err != nil || files != 1 {
		t.Fatal("state aliases duplicated an owned note", files, err)
	}
}

func TestDoctorFirstAttemptFailures(t *testing.T) {
	_, folder, run := freshnessFixture(t)
	if err := os.WriteFile(filepath.Join(folder, "config.json"), []byte("bad JSON"), 0600); err != nil {
		t.Fatal(err)
	}
	if code, out := run("index"); code != 1 {
		t.Fatal(code, out)
	}
	if code, out := run("doctor"); code != 1 || !strings.Contains(out, "index-failed") || !strings.Contains(out, "index-missing") || !strings.Contains(out, "config-invalid") {
		t.Fatal(code, out)
	}
}
