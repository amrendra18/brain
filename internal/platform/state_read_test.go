package platform

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestReadStateRejectsSpecialFiles(t *testing.T) {
	if state := os.Getenv("BRAIN_TEST_STATE_READ"); state != "" {
		data, err := ReadState(state, os.Getenv("BRAIN_TEST_STATE_NAME"))
		if err != nil {
			fmt.Print("refused")
		} else {
			fmt.Print(string(data))
		}
		os.Exit(0)
	}
	state := t.TempDir()
	regular := filepath.Join(state, "entry.md")
	if err := os.WriteFile(regular, []byte("retained entry"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(state, "directory"), 0700); err != nil {
		t.Fatal(err)
	}
	check := func(state, name, want string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestReadStateRejectsSpecialFiles$")
		command.Env = append(os.Environ(), "BRAIN_TEST_STATE_READ="+state, "BRAIN_TEST_STATE_NAME="+name)
		output, err := command.CombinedOutput()
		if ctx.Err() != nil || err != nil || string(output) != want {
			t.Fatalf("state read %s: %q, %v, deadline %v", name, output, err, ctx.Err())
		}
	}
	check(state, "entry.md", "retained entry")
	check(state, "directory", "refused")
	check(state, "../entry.md", "refused")
	t.Run("symlink", func(t *testing.T) {
		if err := os.Symlink(regular, filepath.Join(state, "link")); err != nil {
			t.Skip("symlink fixture unavailable: " + err.Error())
		}
		check(state, "link", "refused")
	})
	t.Run("parent link", func(t *testing.T) {
		parent := t.TempDir()
		linked := filepath.Join(parent, "linked-state")
		if err := os.Symlink(state, linked); err != nil {
			t.Skip("symlink fixture unavailable: " + err.Error())
		}
		check(linked, "entry.md", "retained entry")
	})
	t.Run("named pipe", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("filesystem named-pipe fixture requires Unix")
		}
		path := filepath.Join(state, "pipe")
		if output, err := exec.Command("mkfifo", "-m", "600", path).CombinedOutput(); err != nil {
			t.Fatalf("create owned pipe: %s, %v", output, err)
		}
		before, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		check(state, "pipe", "refused")
		after, err := os.Lstat(path)
		if err != nil || after.Mode()&os.ModeNamedPipe == 0 || !os.SameFile(before, after) {
			t.Fatal("state read replaced the pipe", err)
		}
	})
	if data, err := os.ReadFile(regular); err != nil || string(data) != "retained entry" {
		t.Fatal("state reads changed the regular file", err)
	}
}
