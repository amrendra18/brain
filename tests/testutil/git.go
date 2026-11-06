package testutil

import (
	"bytes"
	"compress/zlib"
	"crypto/sha1"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Repository writes owned fixture objects without running Git's write commands.
func Repository(t *testing.T, folder, subject string, signature ...string) string {
	t.Helper()
	git := filepath.Join(folder, ".git")
	for _, name := range []string{"refs/heads", "objects"} {
		if err := os.MkdirAll(filepath.Join(git, filepath.FromSlash(name)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for name, text := range map[string]string{"HEAD": "ref: refs/heads/main\n", "config": "[core]\n repositoryformatversion = 0\n bare = false\n"} {
		if err := os.WriteFile(filepath.Join(git, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if subject == "" {
		return git
	}
	store := func(kind string, body []byte) string {
		t.Helper()
		data := append([]byte(fmt.Sprintf("%s %d\x00", kind, len(body))), body...)
		name := fmt.Sprintf("%x", sha1.Sum(data))
		folder := filepath.Join(git, "objects", name[:2])
		if err := os.MkdirAll(folder, 0700); err != nil {
			t.Fatal(err)
		}
		var compressed bytes.Buffer
		writer := zlib.NewWriter(&compressed)
		if _, err := writer.Write(data); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(folder, name[2:]), compressed.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		return name
	}
	tree := store("tree", nil)
	person := "Fixture <fixture> 1760300000 +0200"
	header := "tree " + tree + "\nauthor " + person + "\ncommitter " + person + "\n"
	if len(signature) > 0 {
		header += "gpgsig " + strings.ReplaceAll(signature[0], "\n", "\n ") + "\n"
	}
	commit := store("commit", []byte(header+"\n"+subject+"\n"))
	if err := os.WriteFile(filepath.Join(git, "refs", "heads", "main"), []byte(commit+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return git
}
