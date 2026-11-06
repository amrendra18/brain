package main

import (
	"context"
	"encoding/json"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"brain/scripts/toolchain"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func writeFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	data, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, to, string(data))
	info, err := os.Stat(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(to, info.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	source := repoRoot(t)
	for _, file := range []string{"scripts/verify", "scripts/go-env", "scripts/verifybootstrap/main.go", "scripts/verifycmd/main.go"} {
		copyFile(t, filepath.Join(source, file), filepath.Join(root, file))
	}
	for _, directory := range []string{"scripts/verifyrun", "scripts/toolchain", "internal/corpus", "internal/platform", "internal/redact"} {
		entries, err := os.ReadDir(filepath.Join(source, directory))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".go") && !strings.HasSuffix(entry.Name(), "_test.go") {
				copyFile(t, filepath.Join(source, directory, entry.Name()), filepath.Join(root, directory, entry.Name()))
			}
		}
	}
	for _, file := range []string{"go.mod", "go.sum"} {
		copyFile(t, filepath.Join(source, file), filepath.Join(root, file))
	}
	writeFile(t, filepath.Join(root, "cmd/brain/main.go"), "package main\nfunc main() {}\n")
	git := exec.Command("git", "init", "-q")
	git.Dir = root
	if out, err := git.CombinedOutput(); err != nil {
		t.Fatalf("owned fixture git init: %v\n%s", err, out)
	}
	toolDir := filepath.Join(root, ".local/test-go/bin")
	if err := os.MkdirAll(toolDir, 0700); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(root, "build.log")
	// This reviewed shim records every compiler invocation before running the
	// real pinned toolchain. The wrapper itself remains the command under test.
	writeFile(t, filepath.Join(toolDir, "go"), "#!/bin/sh\nif [ \"$1\" != version ]; then printf '%s\\n' \"$*\" >> \"$BUILD_LOG\"; fi\nexec env GOROOT=\"$REAL_GO_ROOT\" \"$REAL_GO\" \"$@\"\n")
	if err := os.Chmod(filepath.Join(toolDir, "go"), 0700); err != nil {
		t.Fatal(err)
	}
	copyFile(t, filepath.Join(runtime.GOROOT(), "bin", "gofmt"), filepath.Join(toolDir, "gofmt"))
	writeFile(t, filepath.Join(root, ".local/go-root"), filepath.Dir(toolDir)+"\n")
	return root, log
}

func runWrapper(t *testing.T, root, log string) (int, string, []string) {
	t.Helper()
	cmd := exec.Command(filepath.Join(root, "scripts/verify"), "fast", "--feature", "cited-search")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "BUILD_LOG="+log, "REAL_GO="+toolchain.Binary(), "REAL_GO_ROOT="+runtime.GOROOT(), "BRAIN_HOME="+filepath.Join(root, "owned-state"))
	output, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else {
			t.Fatal(err)
		}
	}
	logged, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	return code, string(output), strings.Split(strings.TrimSpace(string(logged)), "\n")
}

func TestVerifyScriptRejectsProtectedBootstrapInput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("wrapper fixture uses a POSIX compiler shim")
	}
	cases := []struct {
		name, file, content string
	}{
		{"direct", "scripts/verifyrun/NUL.go", "package verifyrun\nimport \"fmt\"\nfunc init() { fmt.Println(\"SYNTHETIC_PROTECTED_BOOTSTRAP_EXECUTED\") }\n"},
		{"reserved", "scripts/verifyrun/CONIN$.go", "package verifyrun\nimport \"fmt\"\nfunc init() { fmt.Println(\"SYNTHETIC_PROTECTED_BOOTSTRAP_EXECUTED\") }\n"},
		{"embedded", "scripts/verifyrun/visible.go", "package verifyrun\nimport _ \"embed\"\n//go:embed private.KEY\nvar embedded string\n"},
		{"embedded-after-declaration", "scripts/verifyrun/visible.go", "package verifyrun\nimport _ \"embed\"\nimport \"fmt\"\nvar before = 1\n//go:embed private.KEY\nvar embedded string\nfunc init() { fmt.Println(embedded) }\n"},
		{"imported", "scripts/verifyrun/visible.go", "package verifyrun\nimport _ \"brain/dist/private\"\n"},
		{"transitive-import", "internal/redact/visible.go", "package redact\nimport _ \"brain/dist/private\"\n"},
		{"changed-go-sum", "go.sum", "SYNTHETIC_PROTECTED_BOOTSTRAP_EXECUTED\n"},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			root, log := fixture(t)
			writeFile(t, filepath.Join(root, item.file), item.content)
			if strings.HasPrefix(item.name, "embedded") {
				writeFile(t, filepath.Join(root, "scripts/verifyrun/private.KEY"), "SYNTHETIC_PROTECTED_EMBED_BYTES")
			}
			code, output, builds := runWrapper(t, root, log)
			if code != 3 || len(builds) != 1 || !strings.Contains(builds[0], "./scripts/verifybootstrap/main.go") {
				t.Fatalf("exit %d, compiler invocations %q, output %q", code, builds, output)
			}
			if !strings.Contains(output, "protected verifier input cannot be certified") || strings.Contains(output, "SYNTHETIC_PROTECTED_BOOTSTRAP_EXECUTED") || strings.Contains(output, "SYNTHETIC_PROTECTED_EMBED_BYTES") || strings.Contains(output, root) {
				t.Fatalf("bad refusal: %q", output)
			}
		})
	}
}

func TestVerifyBootstrapInputs(t *testing.T) {
	root := repoRoot(t)
	file := filepath.Join(root, "scripts/verifybootstrap/main.go")
	parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, imported := range parsed.Imports {
		value := strings.Trim(imported.Path.Value, "\"")
		if strings.Contains(strings.Split(value, "/")[0], ".") || strings.HasPrefix(value, "brain/") {
			t.Fatalf("bootstrap has repository or external import: %s", value)
		}
	}
	wrapper, err := os.ReadFile(filepath.Join(root, "scripts/verify"))
	if err != nil {
		t.Fatal(err)
	}
	first := strings.Index(string(wrapper), "\"$go_bin\" build")
	if first < 0 {
		t.Fatal("wrapper has no compiler invocation")
	}
	line := strings.SplitN(string(wrapper[first:]), "\n", 2)[0]
	if !strings.Contains(line, "./scripts/verifybootstrap/main.go") || strings.Contains(line, "./scripts/verifycmd") {
		t.Fatal("the first compiler invocation does not name only the reviewed bootstrap file")
	}
}

func TestBootstrapReviewedTree(t *testing.T) {
	root := repoRoot(t)
	if err := check(root); err != nil {
		t.Fatalf("reviewed verifier module files refused: %v", err)
	}
}

func TestBootstrapModuleFileRefusals(t *testing.T) {
	for _, name := range []string{"missing", "changed-bytes", "symlink", "directory"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			top := filepath.Join(root, "go.sum")
			switch name {
			case "changed-bytes":
				writeFile(t, top, "unreviewed checksum\n")
			case "symlink":
				target := filepath.Join(root, "target")
				writeFile(t, target, "unreviewed checksum\n")
				if err := os.Symlink(target, top); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			case "directory":
				if err := os.Mkdir(top, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := checkModuleFile(root, "go.sum", reviewedSumSHA256); err == nil {
				t.Fatal("unreviewed module input accepted")
			}
		})
	}
}

func TestVerifyBootstrapSourceIdentity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("wrapper fixture uses a POSIX compiler shim")
	}
	root, log := fixture(t)
	identity := func() string {
		t.Helper()
		_, output, builds := runWrapper(t, root, log)
		if len(builds) < 3 {
			t.Fatalf("wrapper did not build preflight, verifier and target: %q\n%s", builds, output)
		}
		lines := strings.Split(strings.TrimSpace(output), "\n")
		evidence := lines[len(lines)-1]
		data, err := os.ReadFile(filepath.Join(evidence, "summary.json"))
		if err != nil {
			t.Fatalf("source evidence missing: %v\n%s", err, output)
		}
		var summary struct {
			SourceTree string `json:"source_tree"`
		}
		if err := json.Unmarshal(data, &summary); err != nil || summary.SourceTree == "" {
			t.Fatalf("source identity unavailable: %v, %q", err, data)
		}
		return summary.SourceTree
	}
	before := identity()
	bootstrap := filepath.Join(root, "scripts/verifybootstrap/main.go")
	file, err := os.OpenFile(bootstrap, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("\n// visible bootstrap source edit\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	after := identity()
	if before == after {
		t.Fatal("recorded source-tree identity ignored visible bootstrap source edit")
	}
}

func TestBootstrapClassifiesProtectedNames(t *testing.T) {
	for _, name := range []string{"NUL.go", "CONIN$.go", "CONOUT$.go", "COM¹.go", "NUL .go", "private.KEY", ".ENV.local", "auth.JSON"} {
		if !excluded(filepath.Join("scripts/verifyrun", name)) {
			t.Fatalf("protected name accepted: %s", name)
		}
	}
	if excluded("scripts/verifyrun/visible.go") {
		t.Fatal("visible source excluded")
	}
}

// A named pipe makes an attempted read observable even if the program ignores
// the bytes and error. The writer can open it only after a reader opens it.
func TestVerifyWrapperReadObserver(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("named-pipe read observer uses mkfifo")
	}
	for _, variant := range []string{"safe", "embedded-after-declaration", "redact-embedded", "broken-shell", "broken-bootstrap", "allowed-control"} {
		t.Run(variant, func(t *testing.T) {
			root, log := fixture(t)
			relative := "scripts/verifyrun/private.KEY"
			if variant == "redact-embedded" {
				relative = "internal/redact/private.KEY"
			}
			if variant == "allowed-control" {
				relative = "observer-allowed.txt"
			}
			pipe := filepath.Join(root, relative)
			if err := os.MkdirAll(filepath.Dir(pipe), 0700); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command("mkfifo", pipe).CombinedOutput(); err != nil {
				t.Fatalf("mkfifo: %v\n%s", err, out)
			}
			embedded := variant == "embedded-after-declaration" || variant == "redact-embedded"
			if embedded {
				// Test-only protected pipe. The observer would catch a compiler read
				// even if the program discarded the bytes.
				pkg := map[string]string{"embedded-after-declaration": "verifyrun", "redact-embedded": "redact"}[variant]
				writeFile(t, filepath.Join(root, filepath.Dir(relative), "visible.go"), "package "+pkg+"\nimport _ \"embed\"\nvar before = 1\n//go:embed private.KEY\nvar embedded string\n")
			}
			if variant == "broken-shell" || variant == "allowed-control" {
				wrapper := filepath.Join(root, "scripts/verify")
				data, err := os.ReadFile(wrapper)
				if err != nil {
					t.Fatal(err)
				}
				injected := "# test-only injected read, with bytes discarded\ncat \"$repo_root/" + relative + "\" >/dev/null\n"
				text := strings.Replace(string(data), "cd \"$repo_root\"\n", "cd \"$repo_root\"\n"+injected, 1)
				writeFile(t, wrapper, text)
				if err := os.Chmod(wrapper, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if variant == "broken-bootstrap" {
				bootstrap := filepath.Join(root, "scripts/verifybootstrap/main.go")
				data, err := os.ReadFile(bootstrap)
				if err != nil {
					t.Fatal(err)
				}
				text := strings.Replace(string(data), "if err := check(os.Args[1]); err != nil {", "// test-only injected read, with bytes discarded\n\t_, _ = os.ReadFile(filepath.Join(os.Args[1], \"scripts/verifyrun/private.KEY\"))\n\tif err := check(os.Args[1]); err != nil {", 1)
				writeFile(t, bootstrap, text)
			}
			opened := make(chan struct{}, 1)
			finished := make(chan struct{}, 1)
			go func() {
				file, err := os.OpenFile(pipe, os.O_WRONLY, 0)
				if err == nil {
					opened <- struct{}{}
					_, _ = file.Write([]byte("observed"))
					_ = file.Close()
				}
				finished <- struct{}{}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, filepath.Join(root, "scripts/verify"), "fast", "--feature", "cited-search")
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "BUILD_LOG="+log, "REAL_GO="+toolchain.Binary(), "REAL_GO_ROOT="+runtime.GOROOT(), "BRAIN_HOME="+filepath.Join(root, "owned-state"))
			output, _ := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("wrapper timed out: %v\n%s", ctx.Err(), output)
			}
			logged, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			builds := strings.Split(strings.TrimSpace(string(logged)), "\n")
			if embedded {
				if len(builds) != 1 || !strings.Contains(builds[0], "./scripts/verifybootstrap/main.go") || !strings.Contains(string(output), "protected verifier input cannot be certified") {
					t.Fatalf("protected embed compiled verifier or missed refusal: %q; output %s", builds, output)
				}
			} else if len(builds) != 4 || !strings.Contains(builds[0], "./scripts/verifybootstrap/main.go") || builds[1] != "mod verify" || !strings.Contains(builds[2], "./scripts/verifycmd") || !strings.Contains(builds[3], "./cmd/brain") {
				t.Fatalf("wrapper build order: %q; output %s", builds, output)
			}
			observed := false
			select {
			case <-opened:
				observed = true
			case <-time.After(100 * time.Millisecond):
			}
			if !observed {
				cleanup := make(chan error, 1)
				go func() {
					reader, err := os.OpenFile(pipe, os.O_RDONLY, 0)
					if err == nil {
						buffer := make([]byte, 16)
						_, _ = reader.Read(buffer)
						err = reader.Close()
					}
					cleanup <- err
				}()
				select {
				case err := <-cleanup:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("named-pipe cleanup timed out")
				}
			}
			select {
			case <-finished:
			case <-time.After(3 * time.Second):
				t.Fatal("observer did not stop")
			}
			wantRead := variant != "safe" && !embedded
			if observed != wantRead {
				t.Fatalf("variant %s: protected/allowed read observed %t, want %t; output %s", variant, observed, wantRead, output)
			}
		})
	}
}

func TestVerifyWrapperAllowedFeature(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("wrapper is a POSIX shell program")
	}
	root := repoRoot(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(root, "scripts/verify"), "fast", "--feature", "cited-search")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "BRAIN_HOME="+filepath.Join(t.TempDir(), "state"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("allowed wrapper feature did not prove: %v\n%s", err, output)
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if !strings.Contains(lines[0], "cited-search: proven") {
		t.Fatalf("allowed feature result: %s", output)
	}
	data, err := os.ReadFile(filepath.Join(lines[len(lines)-1], "summary.json"))
	if err != nil {
		t.Fatal(err)
	}
	var summary struct {
		SourceTree string `json:"source_tree"`
		BinarySHA  string `json:"binary_sha256"`
		Features   []struct {
			Status string `json:"status"`
		} `json:"features"`
	}
	if err := json.Unmarshal(data, &summary); err != nil {
		t.Fatal(err)
	}
	if summary.SourceTree == "" || summary.BinarySHA == "" || len(summary.Features) != 1 || summary.Features[0].Status != "proven" {
		t.Fatalf("allowed wrapper did not build and prove target: %+v", summary)
	}
}

func TestBootstrapScannerCases(t *testing.T) {
	base := func(t *testing.T) string {
		t.Helper()
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "go.mod"), "module brain\n\ngo 1.27.1\n")
		writeFile(t, filepath.Join(root, "scripts/verifycmd/main.go"), "package main\nimport _ \"brain/scripts/verifyrun\"\nfunc main() {}\n")
		writeFile(t, filepath.Join(root, "scripts/verifyrun/run.go"), "package verifyrun\nimport \"fmt\"\nvar _ = fmt.Sprint\n")
		writeFile(t, filepath.Join(root, "internal/corpus/corpus.go"), "package corpus\nimport _ \"brain/internal/platform\"\n")
		writeFile(t, filepath.Join(root, "internal/platform/names.go"), "package platform\n")
		writeFile(t, filepath.Join(root, "internal/redact/policy.go"), "package redact\n")
		return root
	}
	cases := []struct {
		name, relative, content string
		refuse                  bool
	}{
		{"allowed", "scripts/verifyrun/visible.go", "package verifyrun\n", false},
		{"reviewed-redaction", "internal/corpus/visible.go", "package corpus\nimport _ \"brain/internal/redact\"\n", false},
		{"reviewed-sqlite", "scripts/verifyrun/visible.go", "package verifyrun\nimport _ \"modernc.org/sqlite\"\n", false},
		{"transitive-protected-import", "internal/redact/visible.go", "package redact\nimport _ \"brain/dist/private\"\n", true},
		{"transitive-unreviewed-import", "internal/redact/visible.go", "package redact\nimport _ \"brain/internal/notes\"\n", true},
		{"private-data-ignored", "scripts/verifyrun/private.KEY", "not Go source", false},
		{"mixed-case-data-ignored", "internal/corpus/auth.JSON", "not Go source", false},
		{"reserved-go", "scripts/verifyrun/CONIN$.go", "package verifyrun\n", true},
		{"protected-go", "scripts/verifyrun/NUL.go", "package verifyrun\n", true},
		{"embedded", "scripts/verifyrun/visible.go", "package verifyrun\nimport _ \"embed\"\n//go:embed private.KEY\nvar value string\n", true},
		{"embedded-after-declaration", "scripts/verifyrun/visible.go", "package verifyrun\nimport _ \"embed\"\nvar before = 1\n//go:embed private.KEY\nvar value string\n", true},
		{"protected-import", "scripts/verifyrun/visible.go", "package verifyrun\nimport _ \"brain/dist/private\"\n", true},
		{"unreviewed-import", "scripts/verifyrun/visible.go", "package verifyrun\nimport _ \"brain/internal/notes\"\n", true},
		{"external-import", "scripts/verifyrun/visible.go", "package verifyrun\nimport _ \"example.com/remote\"\n", true},
		{"cgo-import", "scripts/verifyrun/visible.go", "package verifyrun\nimport \"C\"\n", true},
		{"invalid-Go", "scripts/verifyrun/visible.go", "package verifyrun\nimport \"unterminated\n", true},
		{"C-input", "scripts/verifyrun/visible.c", "int private = 1;\n", true},
		{"assembly-input", "scripts/verifyrun/visible.s", "TEXT x(SB),$0\n", true},
		{"ignored-test", "scripts/verifyrun/visible_test.go", "package verifyrun\nimport _ \"brain/internal/notes\"\n", false},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			root := base(t)
			writeFile(t, filepath.Join(root, item.relative), item.content)
			err := checkDirectory(root, filepath.Dir(item.relative))
			if (err != nil) != item.refuse {
				t.Fatalf("refusal %t, want %t: %v", err != nil, item.refuse, err)
			}
		})
	}
	t.Run("missing-package", func(t *testing.T) {
		if err := check(t.TempDir()); err == nil {
			t.Fatal("missing verifier package accepted")
		}
	})
	t.Run("source-symlink", func(t *testing.T) {
		root := base(t)
		if err := os.Symlink(filepath.Join(root, "scripts/verifyrun/run.go"), filepath.Join(root, "scripts/verifyrun/linked.go")); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if err := check(root); err == nil {
			t.Fatal("source symlink accepted")
		}
	})
}

func TestBootstrapModuleIdentity(t *testing.T) {
	for _, name := range []string{"missing", "different", "directory", "symlink"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			module := filepath.Join(root, "go.mod")
			switch name {
			case "different":
				writeFile(t, module, "module brain/scripts/verifyrun\n")
			case "directory":
				if err := os.Mkdir(module, 0700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				target := filepath.Join(root, "target")
				writeFile(t, target, "module brain\n")
				if err := os.Symlink(target, module); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			}
			if err := check(root); err == nil || !strings.Contains(err.Error(), "module") {
				t.Fatal("module boundary not refused", err)
			}
		})
	}
}
