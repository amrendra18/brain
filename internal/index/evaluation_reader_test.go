package index

import (
	"brain/internal/platform"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestReadFileOutcomes(t *testing.T) {
	_, state, writer, _ := fixture(t)
	items := []struct {
		path, status string
		deleted      bool
	}{
		{"c/current.md", "current", false},
		{"b/bad.docx", "conversion-failed", false},
		{"a/old.md", "superseded", false},
		{"d/large.md", "oversize", false},
		{"e/removed.md", "current", true},
	}
	for _, item := range items {
		var deleted any
		if item.deleted {
			deleted = float64(1)
		}
		_, err := writer.Exec("INSERT INTO files(path,source,project,kind,status,size,mtime_ns,sha256,namespace,chunker_version,indexed_at,deleted_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)", item.path, "source-root", "project", "doc", item.status, 23, 1, "raw-digest", "source:0:", 1, 1, deleted)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	database := filepath.Join(state, "index.sqlite")
	before, err := os.Stat(database)
	if err != nil {
		t.Fatal(err)
	}
	for repeat := 0; repeat < 2; repeat++ {
		got, err := ReadFileOutcomes(state)
		if err != nil {
			t.Fatal(err)
		}
		want := []FileOutcome{
			{"source-root", "a/old.md", "source:0:", "project", "doc", "superseded", "raw-digest", 23},
			{"source-root", "b/bad.docx", "source:0:", "project", "doc", "conversion-failed", "raw-digest", 23},
			{"source-root", "c/current.md", "source:0:", "project", "doc", "current", "raw-digest", 23},
			{"source-root", "d/large.md", "source:0:", "project", "doc", "oversize", "raw-digest", 23},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("stored outcomes differ: got %#v, want %#v", got, want)
		}
		data, err := os.Stat(database)
		if err != nil || data.Size() != before.Size() || !data.ModTime().Equal(before.ModTime()) {
			t.Fatal("reader changed the index file", data, before, err)
		}
		lock, err := platform.Acquire(state, "index", false)
		if err != nil {
			t.Fatal("reader retained the index lock", err)
		}
		if err := lock.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReadFileOutcomesMissing(t *testing.T) {
	state := filepath.Join(t.TempDir(), "absent")
	if rows, err := ReadFileOutcomes(state); err == nil || rows != nil {
		t.Fatal("missing state accepted", rows, err)
	}
	if _, err := os.Lstat(state); !os.IsNotExist(err) {
		t.Fatal("reader created the missing state", err)
	}
}
