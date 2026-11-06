package main

import (
	"brain/tests/testutil"
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func testStateSymlinks(t *testing.T) {
	for _, name := range []string{"notes", "notes/alpha.md", "locks", "locks/index.lock", "locks/notes.lock", "config.json", "converted", "converted/cache-file", "index.sqlite", "index.sqlite-wal", "index.sqlite-shm", "index.sqlite-journal", "notes/internal-link"} {
		t.Run(name, func(t *testing.T) {
			j := newJourney(t)
			j.put(t, "alpha/README.md", "# Keeper\n\nA lighthouse keeper.")
			const rtf = `{\rtf1 A converted lighthouse keeper.\par}`
			j.put(t, "alpha/talk.rtf", rtf)
			j.ready(t)
			outside := t.TempDir()
			sentinel := filepath.Join(outside, "sentinel.md")
			if err := os.WriteFile(sentinel, []byte("Outside sentinel stays unchanged."), 0600); err != nil {
				t.Fatal(err)
			}
			target := sentinel
			path := filepath.Join(j.state, filepath.FromSlash(name))
			if name == "converted/cache-file" {
				path = filepath.Join(j.state, "converted", fmt.Sprintf("%x.txt", sha256.Sum256([]byte(rtf))))
			}
			if name == "notes" || name == "locks" || name == "converted" {
				target = outside
			}
			if name == "notes/internal-link" {
				path = filepath.Join(j.state, "notes", "alpha.md")
				target = filepath.Join(j.state, "auth.json")
				if err := os.WriteFile(target, []byte("Owned excluded canary."), 0000); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { testutil.Permissions(t, target, 0600) })
			}
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Skip("symlink fixture cannot be created on this system: " + err.Error())
			}
			before := sourceSnapshot(t, outside)
			for _, arguments := range [][]string{{"note", "A harmless decision", "--project", "alpha"}, {"index", "--full"}, {"init", "--source", j.source}} {
				code, reply := j.run(t, arguments...)
				if arguments[0] == "note" && name != "converted" && name != "converted/cache-file" && code != 1 {
					t.Fatal("note accepted unsafe state path", name, code, reply)
				}
				if arguments[0] == "index" && (name == "index.sqlite" || name == "index.sqlite-wal" || name == "index.sqlite-shm" || name == "index.sqlite-journal" || name == "locks" || name == "locks/index.lock") && code != 1 {
					t.Fatal("index accepted unsafe path", name, code, reply)
				}
			}
			if after := sourceSnapshot(t, outside); !reflect.DeepEqual(before, after) {
				t.Fatal("symlink changed outside state", name)
			}
			if name == "notes/internal-link" {
				testutil.Permissions(t, target, 0600)
				data, err := os.ReadFile(target)
				if err != nil || !bytes.Equal(data, []byte("Owned excluded canary.")) {
					t.Fatal("internal excluded target changed", err)
				}
			}
		})
	}
}
