package cli

import (
	"brain/internal/index"
	"brain/internal/notes"
	"brain/internal/platform"
	"brain/scripts/toolchain"
	"brain/tests/testutil"
	"bytes"
	"database/sql"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func emittedCodes(t *testing.T, root string) []string {
	t.Helper()
	codes := map[string]bool{}
	for _, folder := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, folder), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if entry.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			add := func(value ast.Expr) {
				if literal, ok := value.(*ast.BasicLit); ok && literal.Kind == token.STRING {
					name, err := strconv.Unquote(literal.Value)
					if err == nil {
						codes[name] = true
					}
				}
			}
			ast.Inspect(file, func(node ast.Node) bool {
				if call, ok := node.(*ast.CallExpr); ok && len(call.Args) > 0 {
					name := ""
					switch called := call.Fun.(type) {
					case *ast.Ident:
						name = called.Name
					case *ast.SelectorExpr:
						name = called.Sel.Name
					}
					if name == "failure" || name == "usage" || name == "badInput" || name == "invalid" {
						add(call.Args[0])
						if name == "badInput" && len(call.Args) > 1 {
							add(call.Args[1])
						}
					}
				}
				if assignment, ok := node.(*ast.AssignStmt); ok {
					for i, left := range assignment.Lhs {
						if name, ok := left.(*ast.Ident); ok && name.Name == "code" && i < len(assignment.Rhs) {
							add(assignment.Rhs[i])
						}
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	names := []string{}
	for code := range codes {
		names = append(names, code)
	}
	sort.Strings(names)
	return names
}

func TestNextRunsWithoutBrainOnPATH(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	binary := filepath.Join(base, "brain")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command(toolchain.Binary(), "build", "-trimpath", "-buildvcs=false", "-o", binary, "./cmd/brain")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	elsewhere := filepath.Join(base, "elsewhere")
	os.Mkdir(elsewhere, 0700)
	environment := []string{"PATH=", "SHELL=/bin/sh", "HOME=" + base, "USERPROFILE=" + base, "LOCALAPPDATA=" + filepath.Join(base, "AppData", "Local"), "SystemRoot=" + os.Getenv("SystemRoot")}
	real := func(arguments ...string) (int, []byte) {
		t.Helper()
		command := exec.Command(binary, arguments...)
		command.Dir = elsewhere
		command.Env = environment
		var output, stderr bytes.Buffer
		command.Stdout = &output
		command.Stderr = &stderr
		err := command.Run()
		code := 0
		if err != nil {
			exit, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatal(err)
			}
			code = exit.ExitCode()
		}
		if stderr.Len() != 0 {
			t.Fatal(stderr.String())
		}
		return code, output.Bytes()
	}
	place := func() (string, string) {
		t.Helper()
		folder := t.TempDir()
		source, state := filepath.Join(folder, "corpus"), filepath.Join(folder, "state")
		os.Mkdir(source, 0700)
		os.WriteFile(filepath.Join(source, "README.md"), []byte("# Lease\n\nReturn a lease."), 0600)
		return source, state
	}
	ready := func() (string, string) {
		t.Helper()
		source, state := place()
		for _, words := range [][]string{{"init", "--source", source}, {"index"}} {
			code, data := real(append(words, "--state-dir", state, "--format", "json")...)
			if code != 0 {
				t.Fatal(code, string(data))
			}
		}
		return source, state
	}
	refuse := func(state string, words ...string) Failure {
		t.Helper()
		code, data := real(append(words, "--state-dir", state, "--format", "json")...)
		if code != 1 && code != 2 {
			t.Fatal(code, string(data))
		}
		var reply refusal
		if err := json.Unmarshal(data, &reply); err != nil {
			t.Fatal(err)
		}
		return reply.Error
	}
	boundary := func(state string, nul bool) Failure {
		t.Helper()
		var output bytes.Buffer
		c := &call{command: "search", words: []string{"lease"}, state: state, shell: "sh", flags: map[string]string{"--format": "json"}, output: &output}
		if nul {
			t.Setenv("BRAIN_HOME", state)
			if Run([]string{"search", "lease\x00id", "--format", "json"}, &output) != 2 {
				t.Fatal(output.String())
			}
		} else {
			if index.VersionOK("3.42.0") {
				t.Fatal("old version accepted")
			}
			c.databaseError(index.ErrOldSQLite)
		}
		var reply refusal
		json.Unmarshal(output.Bytes(), &reply)
		// In-process boundaries run under the test executable. Follow their arguments with the real app.
		shell := "sh"
		if runtime.GOOS == "windows" {
			shell = "powershell"
		}
		for i := range reply.Error.NextArgv {
			reply.Error.NextArgv[i][0] = binary
			reply.Error.Next[i] = platform.Command(shell, reply.Error.NextArgv[i])
		}
		return reply.Error
	}
	triggers := map[string]func() Failure{
		"harness-unknown":     func() Failure { _, state := place(); return refuse(state, "setup", "other") },
		"unknown-flag":        func() Failure { _, state := place(); return refuse(state, "index", "--nope") },
		"unknown-command":     func() Failure { _, state := place(); return refuse(state, "unknown") },
		"unexpected-argument": func() Failure { _, state := place(); return refuse(state, "index", "now") },
		"missing-argument":    func() Failure { _, state := place(); return refuse(state, "show") },
		"invalid-value":       func() Failure { _, state := place(); return refuse(state, "recent", "--days", "0") },
		"state-uninitialised": func() Failure { _, state := place(); return refuse(state, "search", "lease") },
		"privacy-policy-invalid": func() Failure {
			_, state := ready()
			data, err := os.ReadFile(filepath.Join(state, "config.json"))
			if err != nil {
				t.Fatal(err)
			}
			var config map[string]any
			json.Unmarshal(data, &config)
			values := filepath.Join(t.TempDir(), "values.json")
			config["private_values_file"] = values
			data, _ = json.Marshal(config)
			os.WriteFile(filepath.Join(state, "config.json"), data, 0600)
			failure := refuse(state, "search", "lease")
			os.WriteFile(values, []byte("[]"), 0600)
			return failure
		},
		"privacy-protected-path": func() Failure {
			_, state := ready()
			path := filepath.Join(state, "config.json")
			data, _ := os.ReadFile(path)
			var config map[string]any
			json.Unmarshal(data, &config)
			source := config["sources"].([]any)[0].(map[string]any)
			source["project"] = "AK" + "IA" + strings.Repeat("Q", 16)
			changed, _ := json.Marshal(config)
			os.WriteFile(path, changed, 0600)
			failure := refuse(state, "search", "lease")
			os.WriteFile(path, data, 0600)
			return failure
		},
		"privacy-refresh-required": func() Failure {
			_, state := ready()
			db, err := index.Open(state, false, false, false)
			if err != nil {
				t.Fatal(err)
			}
			_, err = db.Exec("DELETE FROM meta WHERE key='privacy_fingerprint'")
			db.Close()
			if err != nil {
				t.Fatal(err)
			}
			return refuse(state, "search", "lease")
		},
		"ignore-unreadable": func() Failure {
			source, state := ready()
			control := filepath.Join(source, ".brainignore")
			if err := os.Mkdir(control, 0700); err != nil {
				t.Fatal(err)
			}
			failure := refuse(state, "index")
			if err := os.Remove(control); err != nil {
				t.Fatal(err)
			}
			return failure
		},
		"config-invalid": func() Failure {
			_, state := ready()
			if runtime.GOOS != "windows" {
				original := state
				state += "\x1b\a"
				if err := os.Rename(original, state); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(state, "config.json"), []byte("{invalid"), 0600); err != nil {
				t.Fatal(err)
			}
			return refuse(state, "search", "lease")
		},
		"state-unsafe": func() Failure {
			_, state := place()
			if err := os.WriteFile(state, []byte("state is a file"), 0600); err != nil {
				t.Fatal(err)
			}
			return refuse(state, "init")
		},
		"index-missing": func() Failure {
			source, state := place()
			if code, data := real("init", "--source", source, "--state-dir", state); code != 0 {
				t.Fatal(string(data))
			}
			return refuse(state, "recent")
		},
		"index-unavailable": func() Failure {
			_, state := ready()
			db, err := sql.Open("sqlite", filepath.Join(state, "index.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec("ALTER TABLE meta RENAME COLUMN value TO incompatible_value"); err != nil {
				db.Close()
				t.Fatal(err)
			}
			db.Close()
			return refuse(state, "recent")
		},
		"query-empty":     func() Failure { _, state := ready(); return refuse(state, "search", "???") },
		"query-invalid":   func() Failure { _, state := ready(); return boundary(state, true) },
		"chunk-not-found": func() Failure { _, state := ready(); return refuse(state, "show", "0123456789abcdef") },
		"project-unknown": func() Failure { _, state := ready(); return refuse(state, "search", "lease", "--project", "nope") },
		"project-ambiguous": func() Failure {
			source, state := place()
			other := filepath.Join(filepath.Dir(source), "other", "CORPUS")
			if err := os.MkdirAll(other, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(other, "README.md"), []byte("A second lease."), 0600); err != nil {
				t.Fatal(err)
			}
			for _, words := range [][]string{{"init", "--source", source, "--source", other}, {"index"}} {
				if code, data := real(append(words, "--state-dir", state, "--format", "json")...); code != 0 {
					t.Fatal(code, string(data))
				}
			}
			return refuse(state, "search", "lease", "--project", "corpus")
		},
		"source-unreadable": func() Failure {
			source, state := ready()
			os.Rename(source, source+"-moved")
			return refuse(state, "index")
		},
		"schedule-activation-failed": func() Failure {
			_, state := ready()
			var output bytes.Buffer
			c := &call{command: "schedule", state: state, config: filepath.Join(state, "config.json"), shell: defaultShell(), flags: map[string]string{"--format": "json"}, output: &output}
			c.scheduleActivation("03:00")
			var reply refusal
			if err := json.Unmarshal(output.Bytes(), &reply); err != nil {
				t.Fatal(err)
			}
			for i := range reply.Error.NextArgv {
				reply.Error.NextArgv[i][0] = binary
				reply.Error.Next[i] = platform.Command(defaultShell(), reply.Error.NextArgv[i])
			}
			return reply.Error
		},
		"invalid-option": func() Failure { _, state := ready(); return refuse(state, "schedule", "status", "--write") },
		"relocation-pending": func() Failure {
			_, state := ready()
			os.WriteFile(filepath.Join(state, "relocation.json"), []byte(`{"id":"test-pending","destination":false}`), 0600)
			return refuse(state, "note", "A stale writer must refuse")
		},
		"selection-conflict": func() Failure {
			_, state := ready()
			code, data := real("settings", "--state-dir", state, "--write", "--format", "json")
			if code != 0 {
				t.Fatal(code, string(data))
			}
			_, other := ready()
			return refuse(other, "settings", "--write")
		},
		"settings-invalid": func() Failure {
			_, state := ready()
			return refuse(state, "settings", "relocate", state, "--write")
		},
		"state-busy": func() Failure {
			_, state := ready()
			lease, err := platform.StateLease(state, true, true)
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Close()
			return refuse(state, "note", "A concurrent writer must wait")
		},
		"schedule-unavailable": func() Failure { _, state := ready(); return refuse(state, "schedule", "--write") },
		"state-unwritable": func() Failure {
			_, state := ready()
			testutil.Permissions(t, state, 0500)
			t.Cleanup(func() { testutil.Permissions(t, state, 0700) })
			return refuse(state, "index")
		},
		"state-remote": func() Failure { return refuse(testutil.RemoteState(runtime.GOOS), "init") },
		"note-too-long": func() Failure {
			_, state := ready()
			return refuse(state, "note", strings.Repeat("x", notes.LimitBytes+1))
		},
		"notes-unwritable": func() Failure {
			_, state := ready()
			folder := filepath.Join(state, "notes")
			os.Mkdir(folder, 0700)
			if err := testutil.Permissions(t, folder, 0500); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { testutil.Permissions(t, folder, 0700) })
			return refuse(state, "note", "Unsaved decision")
		},
		"sqlite-too-old": func() Failure { _, state := ready(); return boundary(state, false) },
		"config-locked": func() Failure {
			_, state := ready()
			lock, err := platform.AcquireConfig(filepath.Join(state, "config.json"), false)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			return refuse(state, "exclude", "add", "*.draft")
		},
		"config-unwritable": func() Failure {
			_, state := ready()
			path := filepath.Join(state, "config.json.lock")
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
			return refuse(state, "exclude", "add", "*.draft")
		},
		"db-locked": func() Failure {
			source, state := ready()
			os.WriteFile(filepath.Join(source, "README.md"), []byte("# New\n\nThe lease changed."), 0600)
			holder, err := sql.Open("sqlite", filepath.Join(state, "index.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Close()
			if _, err = holder.Exec("BEGIN IMMEDIATE"); err != nil {
				t.Fatal(err)
			}
			failure := refuse(state, "index")
			if _, err = holder.Exec("ROLLBACK"); err != nil {
				t.Fatal(err)
			}
			return failure
		},
	}
	names := []string{}
	for name := range triggers {
		names = append(names, name)
	}
	sort.Strings(names)
	if got := emittedCodes(t, root); !reflect.DeepEqual(got, names) {
		t.Fatalf("each source refusal needs a trigger: source %v, triggers %v", got, names)
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			failure := triggers[name]()
			if failure.Code != name || len(failure.Next) == 0 || len(failure.Next) != len(failure.NextArgv) {
				t.Fatal(failure)
			}
			if strings.Contains(failure.Message, base) || strings.Contains(failure.Message, "//brain-test-server") {
				t.Fatal("absolute path in refusal", failure)
			}
			for i, argv := range failure.NextArgv {
				for _, word := range argv {
					if strings.ContainsRune(word, 0) {
						t.Fatal("NUL in retry")
					}
				}
				words := testutil.Shell(runtime.GOOS, os.Getenv("SystemRoot"), failure.Next[i])
				for _, command := range []*exec.Cmd{exec.Command(argv[0], argv[1:]...), exec.Command(words[0], words[1:]...)} {
					command.Dir = elsewhere
					command.Env = environment
					var output, stderr bytes.Buffer
					command.Stdout = &output
					command.Stderr = &stderr
					err := command.Run()
					if err != nil {
						exit, ok := err.(*exec.ExitError)
						if !ok {
							t.Fatal(err)
						}
						if exit.ExitCode() != 3 {
							doctorFindings := false
							if len(argv) > 1 && argv[1] == "doctor" && exit.ExitCode() == 1 {
								var result doctorReply
								doctorFindings = json.Unmarshal(output.Bytes(), &result) == nil && result.Count > 0 && len(result.Problems) == result.Count
							}
							var reply refusal
							if !doctorFindings && (json.Unmarshal(output.Bytes(), &reply) != nil || reply.Error.Code == "" || reply.Error.Code == name) {
								t.Fatal(failure.Next[i], err, output.String())
							}
						}
					}
					if stderr.Len() != 0 {
						t.Fatal(stderr.String())
					}
				}
			}
		})
	}
}
