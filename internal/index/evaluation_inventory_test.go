package index

import (
	"brain/internal/platform"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestReadChunkInventory(t *testing.T) {
	_, state, db, _ := fixture(t)
	insert := func(path string, deleted bool) int64 {
		t.Helper()
		var gone any
		if deleted {
			gone = float64(1)
		}
		result, err := db.Exec("INSERT INTO files(path,source,project,kind,status,size,mtime_ns,sha256,namespace,chunker_version,indexed_at,deleted_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)", path, "source-root", "project", "doc", "current", 8, 1, "raw", "source:0:", 1, 1, gone)
		if err != nil {
			t.Fatal(err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	first := insert("first.md", false)
	second := insert("second.md", false)
	deleted := insert("removed.md", true)
	for _, row := range []struct {
		id, text, aliases string
		file              int64
	}{
		{"z-id", "text absent from lexical matches", `[{"source":"source-root","path":"second.md"}]`, first},
		{"a-id", "second full text", `[]`, second},
		{"removed-id", "old text", `[]`, deleted},
	} {
		if _, err := db.Exec("INSERT INTO chunks(id,file,heading,text,bytes,dedupe_key,also_in,project,kind) VALUES(?,?,?,?,?,?,?,?,?)", row.id, row.file, "heading", row.text, len(row.text), row.id, row.aliases, "project", "doc"); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for repeat := 0; repeat < 2; repeat++ {
		got, err := ReadChunkInventory(state)
		if err != nil {
			t.Fatal(err)
		}
		want := []InventoryChunk{
			{ID: "a-id", Heading: "heading", Text: "second full text", Project: "project", Kind: "doc", Primary: Reference{Source: "source-root", Path: "second.md"}, AlsoIn: []Reference{}},
			{ID: "z-id", Heading: "heading", Text: "text absent from lexical matches", Project: "project", Kind: "doc", Primary: Reference{Source: "source-root", Path: "first.md"}, AlsoIn: []Reference{{Source: "source-root", Path: "second.md"}}},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("inventory differs: got %#v, want %#v", got, want)
		}
		reader, err := OpenStored(state)
		if err != nil {
			t.Fatal(err)
		}
		_, count, _, err := Totals(reader.DB)
		if closeErr := reader.Close(); err == nil {
			err = closeErr
		}
		if err != nil || count != len(got) {
			t.Fatalf("inventory and totals differ: %d, %d, %v", len(got), count, err)
		}
		lock, err := platform.Acquire(state, "index", false)
		if err != nil {
			t.Fatal("inventory retained a reader", err)
		}
		if err := lock.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReadChunkInventoryMissing(t *testing.T) {
	state := filepath.Join(t.TempDir(), "absent")
	if chunks, err := ReadChunkInventory(state); err == nil || chunks != nil {
		t.Fatal("missing state accepted", chunks, err)
	}
	if _, err := os.Lstat(state); !os.IsNotExist(err) {
		t.Fatal("reader created missing state", err)
	}
}
