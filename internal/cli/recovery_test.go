package cli

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBriefUnavailableIndex(t *testing.T) {
	for _, statement := range []string{
		"ALTER TABLE meta RENAME COLUMN value TO incompatible_value",
		"DROP TABLE chunks",
	} {
		t.Run(statement, func(t *testing.T) {
			root := t.TempDir()
			source, state := filepath.Join(root, "source"), filepath.Join(root, "state")
			if err := os.Mkdir(source, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("# Keeper\n\nStored control."), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("BRAIN_HOME", state)
			var output bytes.Buffer
			run := func(args ...string) string {
				t.Helper()
				output.Reset()
				if code := Run(args, &output); code != 0 {
					t.Fatal(code, output.String())
				}
				return output.String()
			}
			run("init", "--source", source)
			run("index")
			if text := run("--brief"); strings.Contains(text, "not ready") {
				t.Fatal("healthy control was unavailable", text)
			}
			db, err := sql.Open("sqlite", filepath.Join(state, "index.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			_, err = db.Exec(statement)
			if closed := db.Close(); err != nil || closed != nil {
				t.Fatal(err, closed)
			}
			text := run("--brief")
			if !strings.Contains(text, `"help" "doctor"`) || strings.Contains(text, "--full") {
				t.Fatal("unrecognized failure offered a rebuild", text)
			}
			if lines := strings.Count(strings.TrimSpace(text), "\n") + 1; lines != 3 {
				t.Fatal("brief length changed", lines)
			}
		})
	}
}

func TestDatabasePermissionRecoveryGuidance(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes")
	}
	root := t.TempDir()
	source, state := filepath.Join(root, "source"), filepath.Join(root, "state")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("# Keeper\n\nStored permission control."), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BRAIN_HOME", state)
	var output bytes.Buffer
	for _, args := range [][]string{{"init", "--source", source}, {"index"}} {
		output.Reset()
		if code := Run(args, &output); code != 0 {
			t.Fatal(args, code, output.String())
		}
	}
	path := filepath.Join(state, "index.sqlite")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(path, 0600) })
	for _, args := range [][]string{{"index", "--full"}, {"doctor"}} {
		t.Run(args[0], func(t *testing.T) {
			output.Reset()
			code := Run(append(args, "--format", "json"), &output)
			var reply refusal
			if err := json.Unmarshal(output.Bytes(), &reply); err != nil {
				t.Fatal(err, output.String())
			}
			if code != 1 || reply.Error.Code != "index-unavailable" {
				t.Fatal(code, output.String())
			}
			if len(reply.Error.NextArgv) == 0 {
				t.Fatal("missing recovery", output.String())
			}
			for _, command := range reply.Error.NextArgv {
				if len(command) < 2 || command[1] != "doctor" && command[1] != "help" {
					t.Fatal("operational failure offered a rebuild", command)
				}
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("permission failure changed the database", err)
			}
		})
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Chmod(path+suffix, 0600); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
	}
	output.Reset()
	if code := Run([]string{"index", "--format", "json"}, &output); code != 3 {
		t.Fatal(code, output.String())
	}
	output.Reset()
	if code := Run([]string{"doctor", "--format", "json"}, &output); code != 3 {
		t.Fatal("restored permissions did not restore healthy diagnosis", code, output.String())
	}
}
