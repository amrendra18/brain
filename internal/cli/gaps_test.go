package cli

import (
	"archive/zip"
	"brain/internal/corpus"
	"brain/internal/index"
	"brain/tests/testutil"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestIndexGapsVisible(t *testing.T) {
	if os.Getenv("BRAIN_GAP_CHILD") == "1" {
		os.Exit(Run([]string{"search", "lighthouse", "--no-refresh", "--format", "json"}, os.Stdout))
	}
	root := t.TempDir()
	source, folder := filepath.Join(root, "source"), filepath.Join(root, "state")
	child := filepath.Join(source, "child")
	if err := os.MkdirAll(child, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(path, text string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(child, "old.md"), "# Keeper\n\nStored lighthouse evidence.")
	t.Setenv("BRAIN_HOME", folder)
	run := func(args ...string) map[string]any {
		t.Helper()
		var out bytes.Buffer
		code := Run(append(args, "--format", "json"), &out)
		var reply map[string]any
		if err := json.Unmarshal(out.Bytes(), &reply); err != nil {
			t.Fatal(err)
		}
		checkGapExit(t, args, code, reply)
		return reply
	}
	run("init", "--source", source)
	run("index")
	restore := gapFolder(t, child)
	fresh := filepath.Join(source, "fresh")
	if err := os.Mkdir(fresh, 0700); err != nil {
		t.Fatal(err)
	}
	restoreFresh := gapFolder(t, fresh)
	write(filepath.Join(source, "broken.docx"), "not a DOCX")
	write(filepath.Join(source, "large.md"), strings.Repeat("x", 1048577))
	check := func(reply map[string]any, key string, want int) {
		t.Helper()
		list, ok := reply[key].([]any)
		if !ok || len(list) != want || reply["count"] != float64(want) || reply["total"] != float64(want) || reply["empty"] != (want == 0) {
			t.Fatalf("expected %d gaps: %#v", want, reply)
		}
	}
	for i := 0; i < 2; i++ {
		result := run("index")
		check(result, "skipped", 4)
		if result["removed"] != float64(0) {
			t.Fatal("unseen descendant removed", result)
		}
		for _, args := range [][]string{{"search", "lighthouse"}, {"search", "lighthouse", "--no-refresh"}} {
			reply := run(args...)
			gaps, ok := reply["gaps"].(map[string]any)
			if !ok {
				t.Fatal("missing gaps", reply)
			}
			check(gaps, "items", 4)
			if reply["count"] != float64(1) {
				t.Fatal("stored child lost", reply)
			}
		}
		doctor := run("doctor")
		data, _ := json.Marshal(doctor)
		if !bytes.Contains(data, []byte("index-gaps")) || !bytes.Contains(data, []byte("conversion-failed")) {
			t.Fatal(doctor)
		}
	}
	db, err := index.Open(folder, false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	var alive int
	err = db.QueryRow("SELECT count(*) FROM files WHERE path='child/old.md' AND deleted_at IS NULL").Scan(&alive)
	db.Close()
	if err != nil || alive != 1 {
		t.Fatal("unseen descendant deleted", alive, err)
	}
	// Another process must see durable gaps without scanning.
	childProcess := exec.Command(os.Args[0], "-test.run=^TestIndexGapsVisible$")
	childProcess.Env = append(os.Environ(), "BRAIN_GAP_CHILD=1")
	output, err := childProcess.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(output))
	}
	var restarted map[string]any
	if err = json.Unmarshal(output, &restarted); err != nil {
		t.Fatal(err, string(output))
	}
	check(restarted["gaps"].(map[string]any), "items", 4)
	// Every text view names the gap, including the fixed brief adapters.
	id := run("search", "lighthouse")["results"].([]any)[0].(map[string]any)["id"].(string)
	for _, args := range [][]string{{"search", "lighthouse"}, {"show", id}, {"projects"}, {"recent"}, {"map", "child"}, {}, {"--brief"}, {"--brief", "--format", "copilot"}, {"--brief", "--format", "vscode"}} {
		var output bytes.Buffer
		if code := Run(args, &output); code != 0 || !strings.Contains(output.String(), "Index gaps: 4") {
			t.Fatal(args, code, output.String())
		}
	}
	// Read-only access keeps the known gap and stored child.
	if err := testutil.Permissions(t, folder, 0500); err != nil {
		t.Fatal(err)
	}
	readOnly := run("search", "lighthouse")
	check(readOnly["gaps"].(map[string]any), "items", 4)
	if readOnly["read_only"] != true {
		t.Fatal(readOnly)
	}
	if err := testutil.Permissions(t, folder, 0700); err != nil {
		t.Fatal(err)
	}
	restore()
	restoreFresh()
	// A skipped scan cannot declare repaired folders complete.
	check(run("search", "lighthouse", "--no-refresh")["gaps"].(map[string]any), "items", 4)
	var document bytes.Buffer
	archive := zip.NewWriter(&document)
	entry, err := archive.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = entry.Write([]byte("<document><body><p><r><t>Repaired conversion document.</t></r></p></body></document>")); err != nil {
		t.Fatal(err)
	}
	if err = archive.Close(); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(source, "broken.docx"), document.String())
	check(run("index"), "skipped", 1)
	if run("search", "conversion")["count"] != float64(1) {
		t.Fatal("repaired DOCX missing")
	}

	if err := os.Remove(filepath.Join(source, "broken.docx")); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(source, "large.md"), "# Small\n\nRepaired size.")
	check(run("index"), "skipped", 0)
	reply := run("search", "lighthouse")
	check(reply["gaps"].(map[string]any), "items", 0)
	if reply["count"] != float64(1) {
		t.Fatal("recovery lost source data", reply)
	}
}

func TestIndexGapsPendingAndWildcard(t *testing.T) {
	for _, ancestor := range []bool{false, true} {
		t.Run(map[bool]string{false: "root", true: "parent"}[ancestor], func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "memories")
			folder := filepath.Join(root, "state")
			memory := filepath.Join(source, "alpha", "memory")
			if err := os.MkdirAll(memory, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(memory, "MEMORY.md"), []byte("# Beacon\n\nStored lighthouse memory."), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("BRAIN_HOME", folder)
			run := func(args ...string) map[string]any {
				t.Helper()
				var out bytes.Buffer
				code := Run(append(args, "--format", "json"), &out)
				var reply map[string]any
				if err := json.Unmarshal(out.Bytes(), &reply); err != nil {
					t.Fatal(err)
				}
				checkGapExit(t, args, code, reply)
				return reply
			}
			run("init", "--source", source)
			config := corpus.Default(source, []string{filepath.Join(source, "*", "memory")}, nil)
			config.Options.InlineRefreshLimit = 1
			save := func() {
				t.Helper()
				data, err := json.Marshal(config)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(filepath.Join(folder, "config.json"), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			save()
			run("index")
			blocked := memory
			if ancestor {
				blocked = filepath.Dir(memory)
			}
			restore := gapFolder(t, blocked)
			other := filepath.Join(root, "other")
			if err := os.Mkdir(other, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(other, "new.md"), []byte("# New\n\nChanged keeper."), 0600); err != nil {
				t.Fatal(err)
			}
			config.Sources = append(config.Sources, corpus.Source{Path: other})
			save()
			for _, args := range [][]string{{"index"}, {"search", "lighthouse"}, {"search", "lighthouse", "--no-refresh"}} {
				result := run(args...)
				key := "gaps"
				var gaps map[string]any
				if args[0] == "index" {
					gaps = result
					key = "skipped"
				} else {
					gaps = result[key].(map[string]any)
					key = "items"
					if result["count"] != float64(1) {
						t.Fatal("stored memory lost", result)
					}
				}
				if gaps["count"] != float64(1) || len(gaps[key].([]any)) != 1 || gaps[key].([]any)[0].(map[string]any)["path"] != blocked {
					t.Fatal("wildcard gap lost", result)
				}
			}
			doctor := run("doctor")
			data, _ := json.Marshal(doctor)
			if !bytes.Contains(data, []byte("index-gaps")) {
				t.Fatal(doctor)
			}
			// A newly excluded file must be removed even below the inaccessible parent.
			config.Exclude = append(config.Exclude, "MEMORY.md")
			save()
			result := run("index")
			if result["removed"] != float64(1) || run("search", "lighthouse")["count"] != float64(0) {
				t.Fatal("excluded content retained", result)
			}
			restore()
			config.Exclude = corpus.NeverRead
			save()
			run("index")
			result = run("search", "lighthouse")
			if result["count"] != float64(1) || result["gaps"].(map[string]any)["count"] != float64(0) {
				t.Fatal("wildcard recovery failed", result)
			}
		})
	}
}

func TestIndexGapsRetainOwnedNotes(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	folder := filepath.Join(root, ".local", "share", "brain")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BRAIN_HOME", folder)
	run := func(args ...string) map[string]any {
		t.Helper()
		var out bytes.Buffer
		code := Run(append(args, "--format", "json"), &out)
		var reply map[string]any
		if err := json.Unmarshal(out.Bytes(), &reply); err != nil {
			t.Fatal(err)
		}
		checkGapExit(t, args, code, reply)
		return reply
	}
	run("init", "--source", source)
	run("index")
	run("note", "Stored lighthouse note.", "--project", "alpha")
	before := run("search", "lighthouse", "--no-refresh")
	if before["count"] != float64(1) {
		t.Fatal(before)
	}
	restore := gapFolder(t, filepath.Join(folder, "notes"))
	for i := 0; i < 2; i++ {
		result := run("index")
		if result["removed"] != float64(0) || result["count"] != float64(1) {
			t.Fatal("owned note removed", result)
		}
		result = run("search", "lighthouse", "--no-refresh")
		if result["count"] != float64(1) || result["gaps"].(map[string]any)["count"] != float64(1) {
			t.Fatal("owned note lost", result)
		}
	}
	restore()
	run("index")
	result := run("search", "lighthouse")
	if result["count"] != float64(1) || result["gaps"].(map[string]any)["count"] != float64(0) {
		t.Fatal("owned note recovery failed", result)
	}
}

func TestDoctorReadOnlyShowsStoredGaps(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	folder := filepath.Join(root, "state")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "broken.docx"), []byte("not a DOCX"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BRAIN_HOME", folder)
	var output bytes.Buffer
	for _, args := range [][]string{{"init", "--source", source}, {"index"}} {
		output.Reset()
		code := Run(append(args, "--format", "json"), &output)
		want := 0
		if args[0] == "index" {
			want = 1
		}
		if code != want {
			t.Fatal(args, code, output.String())
		}
	}
	before, err := os.ReadFile(filepath.Join(folder, "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if err = testutil.Permissions(t, folder, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := testutil.Permissions(t, folder, 0700); err != nil {
			t.Error(err)
		}
	})
	output.Reset()
	if code := Run([]string{"doctor", "--format", "json"}, &output); code != 1 {
		t.Fatal(code, output.String())
	}
	var reply doctorReply
	if err = json.Unmarshal(output.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	for _, problem := range reply.Problems {
		codes[problem.Code] = true
	}
	if !codes["state-unwritable"] || !codes["index-gaps"] || !strings.Contains(output.String(), "conversion-failed") {
		t.Fatal(output.String())
	}
	after, err := os.ReadFile(filepath.Join(folder, "index.sqlite"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("read-only doctor changed database", err)
	}
}

func checkGapExit(t *testing.T, args []string, code int, reply map[string]any) {
	t.Helper()
	if len(args) > 0 && args[0] == "index" && reply["count"].(float64) > 0 {
		if code != 1 || reply["error"] != nil {
			t.Fatal(args, code, reply)
		}
		return
	}
	if code != 0 && code != 3 && !(len(args) > 0 && args[0] == "doctor" && code == 1) {
		t.Fatal(args, code, reply)
	}
}
