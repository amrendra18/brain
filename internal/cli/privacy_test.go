package cli

import (
	"brain/internal/platform"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestUnsafeStateRefusesBeforeAccess(t *testing.T) {
	for _, kind := range []string{"shared", "file", "link"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			folder := filepath.Join(root, "state")
			switch kind {
			case "shared":
				if runtime.GOOS == "windows" {
					t.Skip("POSIX modes only")
				}
				os.Mkdir(folder, 0700)
				os.Chmod(folder, 0755)
			case "file":
				os.WriteFile(folder, []byte("private-marker"), 0600)
			case "link":
				target := filepath.Join(root, "target")
				os.Mkdir(target, 0700)
				if err := os.Symlink(target, folder); err != nil {
					t.Skip("symlinks unavailable", err)
				}
			}
			for _, args := range [][]string{{"init"}, {"index"}, {"search", "private-marker"}, {"projects"}, {"recent"}, {"map", "alpha"}, {"note", "private-marker"}, {}} {
				var out bytes.Buffer
				if Run(append(args, "--state-dir", folder), &out) != 1 {
					t.Fatal(args, out.String())
				}
				var reply refusal
				if json.Unmarshal(out.Bytes(), &reply) != nil || reply.Error.Code != "state-unsafe" || strings.Contains(reply.Error.Message, folder) || strings.Contains(reply.Error.Message, "private-marker") {
					t.Fatal(out.String())
				}
			}
			var out bytes.Buffer
			if Run([]string{"--brief", "--state-dir", folder}, &out) != 0 || !strings.Contains(out.String(), "private") || strings.Contains(out.String(), "private-marker") {
				t.Fatal(out.String())
			}
			if kind == "file" {
				data, err := os.ReadFile(folder)
				if err != nil || string(data) != "private-marker" {
					t.Fatal(string(data), err)
				}
			} else {
				entries, err := os.ReadDir(folder)
				if err != nil || len(entries) != 0 {
					t.Fatal(entries, err)
				}
			}
		})
	}
}

func TestUnsafeDatabaseError(t *testing.T) {
	var out bytes.Buffer
	c := &call{command: "search", state: t.TempDir(), shell: "sh", flags: map[string]string{}, output: &out}
	if c.databaseError(platform.ErrStateUnsafe) != 1 || !strings.Contains(out.String(), "state-unsafe") {
		t.Fatal(out.String())
	}
}

func TestConfigObjectsPreserved(t *testing.T) {
	for _, kind := range []string{"directory", "link", "dangling"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			folder := filepath.Join(root, "state")
			os.Mkdir(folder, 0700)
			path := filepath.Join(folder, "config.json")
			if kind == "directory" {
				os.Mkdir(path, 0700)
			} else {
				target := filepath.Join(root, "target")
				if kind == "link" {
					os.WriteFile(target, []byte("private-marker"), 0600)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Skip("symlinks unavailable", err)
				}
			}
			before, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if Run([]string{"init", "--state-dir", folder}, &out) != 1 || !strings.Contains(out.String(), "config-invalid") {
				t.Fatal(out.String())
			}
			after, err := os.Lstat(path)
			if err != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() {
				t.Fatal("config object changed", err)
			}
		})
	}
}

func TestTextPathsEscaped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows rejects control characters in file names")
	}
	root := t.TempDir()
	source, folder := filepath.Join(root, "source\x1b\a"), filepath.Join(root, "state\x1b\a")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(source, "README.md"), []byte("# Lease\n\nReturn a lease."), 0600)
	os.WriteFile(filepath.Join(source, "broken\x1b\a.docx"), []byte("invalid test input"), 0600)
	t.Chdir(source)
	t.Setenv("HOME", filepath.Join(root, "home\x1b\a"))
	t.Setenv("USERPROFILE", filepath.Join(root, "home\x1b\a"))
	for _, args := range [][]string{{"init", "--source", source}, {"index"}, {}, {"recent"}, {"note", "Saved lease", "--project", "alpha"}, {"doctor"}, {"setup", "all"}, {"help", "doctor"}, {"--brief"}} {
		var out bytes.Buffer
		code := Run(append(args, "--state-dir", folder), &out)
		if len(args) > 0 && args[0] == "index" {
			if code != 1 {
				t.Fatal(args, code, out.String())
			}
		} else if code != 0 && code != 3 && !(len(args) > 0 && args[0] == "doctor" && code == 1) {
			t.Fatal(args, code, out.String())
		}
		if unsafeText(out.String()) {
			t.Fatalf("raw path controls in %v: %q", args, out.String())
		}
	}
	var out bytes.Buffer
	if Run([]string{"--brief", "--state-dir", folder, "--format", "copilot"}, &out) != 0 {
		t.Fatal(out.String())
	}
	var reply struct {
		AdditionalContext string `json:"additionalContext"`
	}
	if json.Unmarshal(out.Bytes(), &reply) != nil || !strings.Contains(reply.AdditionalContext, folder) || !briefKeepsCommands(t, reply.AdditionalContext, folder) {
		t.Fatal("JSON brief changed command path", out.String())
	}
}
