package verifyrun

import (
	"brain/internal/corpus"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"brain/scripts/toolchain"
)

var binary string

func TestMain(m *testing.M) {
	if os.Getenv("BRAIN_VERIFY_CONFIG_FIXTURE") == "1" {
		os.Exit(configFixtureProcess())
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		panic(err)
	}
	scratch, err := os.MkdirTemp("", "brain-verify-build-")
	if err != nil {
		panic(err)
	}
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	binary = filepath.Join(scratch, "brain"+suffix)
	cmd := exec.Command(toolchain.Binary(), "build", "-trimpath", "-buildvcs=false", "-o", binary, "./cmd/brain")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		os.RemoveAll(scratch)
		fmt.Fprintf(os.Stderr, "build: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(scratch)
	os.Exit(code)
}

func execute(t *testing.T, args ...string) (int, Summary, string) {
	t.Helper()
	root := t.TempDir()
	var out bytes.Buffer
	args = append(args, "--feature", "cited-search")
	code := Run(root, args, &out)
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	evidence := lines[len(lines)-1]
	data, err := os.ReadFile(filepath.Join(evidence, "summary.json"))
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	var summary Summary
	if err := json.Unmarshal(data, &summary); err != nil {
		t.Fatal(err)
	}
	if code != summary.Exit {
		t.Fatalf("return %d versus summary %d", code, summary.Exit)
	}
	return code, summary, evidence
}

func TestVerifyExitCodes(t *testing.T) {
	for _, level := range []string{"fast", "full"} {
		code, result, _ := execute(t, level, "--binary", binary)
		want := 4
		if level == "full" {
			want = 8
		}
		if code != 0 || len(result.Commands) != want || result.Features[0].Status != "proven" {
			t.Fatalf("%s: %#v", level, result)
		}
	}
	code, result, _ := execute(t, "--binary", binary, "--expected-owner", "wrong-owner")
	if code != 1 || result.Features[0].Status != "failed" || !strings.Contains(result.Features[0].Reason, "wrong-owner") {
		t.Fatalf("wrong owner: %#v", result)
	}
	code, result, _ = execute(t, "--binary", filepath.Join(t.TempDir(), "absent"))
	if code != 3 || len(result.Commands) != 0 || result.Features[0].Status != "skipped" {
		t.Fatalf("missing binary: %#v", result)
	}
	code, result, _ = execute(t, "--binary", t.TempDir())
	if code != 3 || len(result.Commands) != 0 {
		t.Fatalf("directory binary: %#v", result)
	}
	for _, args := range [][]string{{"--help"}, {"quick"}, {"--feature", "absent"}, {"--expected-owner", ""}, {"--unknown"}, {"fast", "extra"}} {
		var out bytes.Buffer
		want := 2
		if args[0] == "--help" {
			want = 0
		}
		if got := Run(t.TempDir(), args, &out); got != want {
			t.Fatalf("%v got %d want %d: %s", args, got, want, out.String())
		}
	}
}

func TestVerifyEvidence(t *testing.T) {
	for _, owner := range []string{"harbor", "wrong-owner"} {
		_, summary, evidence := execute(t, "--binary", binary, "--expected-owner", owner)
		if !summary.ScratchRemoved {
			t.Fatal("scratch not removed")
		}
		if _, err := os.Stat(summary.Scratch); !os.IsNotExist(err) {
			t.Fatalf("scratch survives: %v", err)
		}
		for _, file := range []string{"summary.md", "summary.json", "index.html", "logs", "traces", "screens", "logs/01-command.json"} {
			if _, err := os.Stat(filepath.Join(evidence, file)); err != nil {
				t.Fatal(err)
			}
		}
		if len(summary.StandIns) != 0 {
			t.Fatal("positive journey used substitutes")
		}
		if owner == "harbor" && len(summary.SideEffects) != 1 {
			t.Fatal("source preservation was not recorded")
		}
		for _, record := range summary.Commands {
			if (!record.Started || record.Exit == nil || *record.Exit != 0) || record.DurationMS < 0 || record.Stdout == "" || record.Stdin != "closed" {
				t.Fatalf("bad record: %#v", record)
			}
		}
	}
}

func buildFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, path := range []string{"cmd/brain", ".local/bin"} {
		if err := os.MkdirAll(filepath.Join(root, path), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range map[string]string{"go.mod": "module gatefixture\n\ngo 1.27.1\n", ".gitignore": ".local/\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeFixtureProgram(t, root, "older")
	for _, args := range [][]string{{"init"}, {"add", "."}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return root
}

func writeFixtureProgram(t *testing.T, root, word string) {
	t.Helper()
	body := "package main\nimport \"fmt\"\nfunc main() { fmt.Println(\"" + word + "\") }\n"
	if err := os.WriteFile(filepath.Join(root, "cmd", "brain", "main.go"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestGateBindsBinaryToTree(t *testing.T) {
	root := buildFixture(t)
	target := filepath.Join(root, ".local", "bin", "brain")
	d := driver{repo: root, evidence: t.TempDir(), summary: &Summary{}}
	if err := d.build(target); err != nil {
		t.Fatal(err)
	}
	older, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	writeFixtureProgram(t, root, "current")
	d.summary = &Summary{}
	if err := d.build(target); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, older, 0700); err != nil {
		t.Fatal(err)
	}
	if err := d.prepare(target); err == nil || !strings.Contains(err.Error(), "changed before verification") {
		t.Fatalf("older replacement was accepted: %v", err)
	}
	if err := os.RemoveAll(d.summary.Scratch); err != nil {
		t.Fatal(err)
	}
	if err := d.build(target); err != nil {
		t.Fatal(err)
	}
	d.evidence = t.TempDir()
	if err := d.prepare(target); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(d.summary.Scratch)
	current, err := os.ReadFile(d.binary)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(older, current) || d.summary.BinarySHA256 != fmt.Sprintf("%x", sha256.Sum256(current)) {
		t.Fatal("gate retained the older executable or recorded the wrong digest")
	}
	identity, err := sourceTree(root)
	if err != nil || d.summary.SourceTree != "sha256:"+identity {
		t.Fatalf("source identity %q, error %v", d.summary.SourceTree, err)
	}
	got, err := exec.Command(d.binary).Output()
	if err != nil || string(got) != "current\n" {
		t.Fatalf("executed %q: %v", got, err)
	}
}

func TestSourceTreeDeletedTrackedFile(t *testing.T) {
	root := buildFixture(t)
	unused := filepath.Join(root, "cmd", "brain", "unused.go")
	if err := os.WriteFile(unused, []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	add := exec.Command("git", "add", "cmd/brain/unused.go")
	add.Dir = root
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}
	target := filepath.Join(root, ".local", "bin", "brain")
	d := driver{repo: root, summary: &Summary{}}
	if err := d.build(target); err != nil {
		t.Fatalf("ordinary-file build: %v", err)
	}
	before := d.summary.SourceTree
	if err := os.Remove(unused); err != nil {
		t.Fatal(err)
	}
	if err := d.build(target); err != nil {
		t.Fatalf("valid unstaged deletion refused: %v", err)
	}
	if d.summary.SourceTree == before {
		t.Fatal("deleted tracked file retained its earlier identity")
	}
}

func TestSourceTreeRejectsSymlink(t *testing.T) {
	root := buildFixture(t)
	path := filepath.Join(root, "cmd", "brain", "main.go")
	outside := filepath.Join(t.TempDir(), "linked.go")
	if err := os.WriteFile(outside, []byte("package main\nfunc main() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	d := driver{repo: root, summary: &Summary{}}
	target := filepath.Join(root, ".local", "bin", "brain")
	if err := d.build(target); err == nil || !strings.Contains(err.Error(), "source symlink unsupported") {
		t.Fatalf("source symlink was accepted: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("target was built before source refusal: %v", err)
	}
}

func TestSourceTreeAllowedAndExcludedInputs(t *testing.T) {
	root := buildFixture(t)
	first, err := sourceTree(root)
	if err != nil {
		t.Fatal(err)
	}
	ignored := filepath.Join(root, "cmd", "brain", ".env-test")
	if err := os.WriteFile(ignored, []byte("excluded test input"), 0000); err != nil {
		t.Fatal(err)
	}
	second, err := sourceTree(root)
	if err != nil || second != first {
		t.Fatalf("excluded file changed identity or was opened: %v", err)
	}
	writeFixtureProgram(t, root, "current")
	third, err := sourceTree(root)
	if err != nil || third == first {
		t.Fatalf("allowed source edit did not change identity: %v", err)
	}
}

func TestSourceTreeIgnoredCompiledInput(t *testing.T) {
	root := buildFixture(t)
	target := filepath.Join(root, ".local", "bin", "brain")
	d := driver{repo: root, summary: &Summary{}}
	if err := d.build(target); err != nil {
		t.Fatal(err)
	}
	firstTree, firstBinary := d.summary.SourceTree, d.builtSHA
	if err := os.WriteFile(filepath.Join(root, ".git", "info", "exclude"), []byte("cmd/brain/local.go\n"), 0600); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(root, "cmd", "brain", "local.go")
	code := "package main\nimport \"fmt\"\nfunc init() { fmt.Println(\"ignored input\") }\n"
	if err := os.WriteFile(local, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	if err := d.build(target); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(target).Output()
	if err != nil || string(output) != "ignored input\nolder\n" {
		t.Fatalf("ignored Go input did not affect target: %q, %v", output, err)
	}
	if d.summary.SourceTree == firstTree || d.builtSHA == firstBinary {
		t.Fatal("compiled ignored Go input did not change both source and executable digests")
	}
}

func TestVerifyRejectsProtectedCompiledInput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the protected name is not a Windows compiler input")
	}
	root := buildFixture(t)
	target := filepath.Join(root, ".local", "bin", "brain")
	protected := filepath.Join(root, "cmd", "brain", "NUL.go")
	visible := filepath.Join(root, "cmd", "brain", "visible.go")
	var protectedOpens, allowedReads, builds int
	observeRead := func(path string) ([]byte, error) {
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return nil, err
		}
		if excludedBuildInput(relative) {
			protectedOpens++
			return nil, fmt.Errorf("protected content open attempted: %s", relative)
		}
		if relative == filepath.Join("cmd", "brain", "visible.go") {
			allowedReads++
		}
		return os.ReadFile(path)
	}
	d := driver{repo: root, summary: &Summary{}, readSource: observeRead,
		runBuild: func(cmd *exec.Cmd) ([]byte, error) {
			builds++
			return cmd.CombinedOutput()
		}}
	if err := d.build(target); err != nil {
		t.Fatal(err)
	}
	baselineTree, baselineBinary := d.summary.SourceTree, d.builtSHA
	builds = 0
	code := "package main\nimport \"fmt\"\nfunc init() { fmt.Println(\"protected\") }\n"
	if err := os.WriteFile(protected, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	if err := d.build(target); err == nil || !strings.Contains(err.Error(), "protected compiler input cannot be certified") {
		t.Fatalf("protected compiler input was accepted: %v", err)
	}
	if protectedOpens != 0 || builds != 0 {
		t.Fatalf("protected input: %d content opens, %d target builds", protectedOpens, builds)
	}
	if err := os.Rename(protected, visible); err != nil {
		t.Fatal(err)
	}
	if err := d.build(target); err != nil {
		t.Fatal(err)
	}
	if allowedReads != 2 || builds != 1 || d.summary.SourceTree == baselineTree || d.builtSHA == baselineBinary {
		t.Fatalf("allowed control: %d reads, %d builds, source changed %t, executable changed %t", allowedReads, builds, d.summary.SourceTree != baselineTree, d.builtSHA != baselineBinary)
	}
	for _, name := range []string{"NUL.s", "NUL.syso"} {
		path := filepath.Join(root, "cmd", "brain", name)
		if err := os.WriteFile(path, []byte("protected fixture"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := d.build(target); err == nil || !strings.Contains(err.Error(), "protected compiler input cannot be certified") {
			t.Fatalf("%s was accepted: %v", name, err)
		}
		if protectedOpens != 0 || builds != 1 {
			t.Fatalf("%s: %d protected opens, %d total builds", name, protectedOpens, builds)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := observeRead(protected); err == nil || !strings.Contains(err.Error(), "protected content open attempted") {
		t.Fatalf("injected protected read escaped observer: %v", err)
	}
	if protectedOpens != 1 {
		t.Fatalf("injected protected read count: %d", protectedOpens)
	}
}

func TestVerifyProtectedNameParity(t *testing.T) {
	if excludedBuildInput("brain/tests/testutil") {
		t.Fatal("allowed repository test helper import was rejected")
	}
	for _, name := range []string{"CONIN$.go", "CONOUT$.go", "COM¹.go", "NUL .go", "private.KEY", ".ENV.local", "auth.JSON", "nul.go", "private.key", "visible.go"} {
		t.Run(name, func(t *testing.T) {
			root := buildFixture(t)
			candidate := filepath.Join(root, "cmd", "brain", name)
			relative := filepath.Join("cmd", "brain", name)
			if got, want := excludedBuildInput(relative), corpus.NeverReadPath(relative); got != want {
				t.Fatalf("source rule %t, runtime rule %t", got, want)
			}
			protected := corpus.NeverReadPath(relative)
			if want := name != "visible.go"; protected != want {
				t.Fatalf("%s: runtime protected %t, want %t", name, protected, want)
			}
			var protectedOpens, allowedReads, builds int
			observeRead := func(path string) ([]byte, error) {
				rel, err := filepath.Rel(root, path)
				if err != nil {
					return nil, err
				}
				if corpus.NeverReadPath(rel) {
					protectedOpens++
					return nil, fmt.Errorf("protected content open attempted: %s", rel)
				}
				if rel == relative {
					allowedReads++
				}
				return os.ReadFile(path)
			}
			d := driver{repo: root, summary: &Summary{}, readSource: observeRead,
				runBuild: func(cmd *exec.Cmd) ([]byte, error) {
					builds++
					return cmd.CombinedOutput()
				}}
			target := filepath.Join(root, ".local", "bin", "brain")
			if err := d.build(target); err != nil {
				t.Fatal(err)
			}
			baselineTree, baselineBinary := d.summary.SourceTree, d.builtSHA
			builds = 0
			content := "package main\nimport \"fmt\"\nfunc init() { fmt.Println(\"changed\") }\n"
			if !strings.HasSuffix(name, ".go") {
				content = "private fixture"
			}
			if err := os.WriteFile(candidate, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			err := d.build(target)
			if protected && protectedCompilerInput(name) {
				if err == nil || !strings.Contains(err.Error(), "protected compiler input cannot be certified") || builds != 0 {
					t.Fatalf("protected compiler input: %v, %d builds", err, builds)
				}
			} else if err != nil || builds != 1 {
				t.Fatalf("unused private or allowed input: %v, %d builds", err, builds)
			}
			if protectedOpens != 0 {
				t.Fatalf("protected content opened %d times", protectedOpens)
			}
			if !protected && (allowedReads != 2 || d.summary.SourceTree == baselineTree || d.builtSHA == baselineBinary) {
				t.Fatalf("allowed edit: %d reads, source changed %t, executable changed %t", allowedReads, d.summary.SourceTree != baselineTree, d.builtSHA != baselineBinary)
			}
			if protected {
				if _, err := observeRead(candidate); err == nil || protectedOpens != 1 {
					t.Fatalf("deliberate protected read escaped observer: %v, %d opens", err, protectedOpens)
				}
			}
		})
	}
}

func TestProtectedCompilerInputSuffixes(t *testing.T) {
	for _, name := range []string{"NUL.go", "NUL.s", "NUL.syso"} {
		if !excludedBuildInput(filepath.Join("cmd", "brain", name)) || !protectedCompilerInput(name) {
			t.Fatalf("protected compiler input was not classified: %s", name)
		}
	}
	if protectedCompilerInput("visible.go") == false {
		t.Fatal("allowed Go source was not recognized as compiler input")
	}
}

func TestVerifyRejectsProtectedAssemblyInclude(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("assembly fixture uses arm64 instructions")
	}
	root := buildFixture(t)
	target := filepath.Join(root, ".local", "bin", "brain")
	assembly := filepath.Join(root, "cmd", "brain", "value.s")
	protected := filepath.Join(root, "cmd", "brain", "secret.key")
	plain := "#include \"textflag.h\"\nTEXT ·value(SB), NOSPLIT, $0-8\n MOVD $1, R0\n MOVD R0, ret+0(FP)\n RET\n"
	if err := os.WriteFile(assembly, []byte(plain), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := sourceTree(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(protected, []byte("#define SECRET 2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	after, err := sourceTree(root)
	if err != nil || after != before {
		t.Fatalf("unused protected header changed identity: %v", err)
	}
	var protectedOpens, builds int
	d := driver{repo: root, summary: &Summary{},
		readSource: func(name string) ([]byte, error) {
			if excludedBuildInput(strings.TrimPrefix(name, root+string(filepath.Separator))) {
				protectedOpens++
				return nil, fmt.Errorf("protected content open attempted")
			}
			return os.ReadFile(name)
		},
		runBuild: func(cmd *exec.Cmd) ([]byte, error) {
			builds++
			return cmd.CombinedOutput()
		}}
	if err := d.build(target); err != nil {
		t.Fatalf("unused protected header blocked build: %v", err)
	}
	builds = 0
	used := strings.Replace(plain, "#include \"textflag.h\"", "#include \"textflag.h\"\n#include \"secret.key\"", 1)
	if err := os.WriteFile(assembly, []byte(used), 0600); err != nil {
		t.Fatal(err)
	}
	if err := d.build(target); err == nil || !strings.Contains(err.Error(), "protected assembly include cannot be certified") {
		t.Fatalf("protected assembly include was accepted: %v", err)
	}
	if protectedOpens != 0 || builds != 0 {
		t.Fatalf("protected assembly include: %d content opens, %d target builds", protectedOpens, builds)
	}
	parentHeader := filepath.Join(root, "secret.key")
	if err := os.WriteFile(parentHeader, []byte("#define SECRET 3\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, include := range []string{"../../secret.key", parentHeader} {
		body := strings.Replace(plain, "#include \"textflag.h\"", "#include \"textflag.h\"\n#include "+fmt.Sprintf("%q", include), 1)
		if err := os.WriteFile(assembly, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if err := d.build(target); err == nil || !strings.Contains(err.Error(), "assembly include path cannot be certified") {
			t.Fatalf("assembly include path %q was accepted: %v", include, err)
		}
		if protectedOpens != 0 || builds != 0 {
			t.Fatalf("assembly include path %q: %d content opens, %d target builds", include, protectedOpens, builds)
		}
	}
	bridge := filepath.Join(root, "cmd", "brain", "bridge.inc")
	if err := os.WriteFile(bridge, []byte("#include \"secret.key\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "cmd", "brain", "dist", "secret.key")
	if err := os.MkdirAll(filepath.Dir(nested), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nested, []byte("#define SECRET 4\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, include := range []string{"# include \"secret.key\"", "#include\"secret.key\"", "#include/* gap */\"secret.key\"", "#/* gap */include \"secret.key\"", "#include \"bridge.inc\"", "#include \"dist/secret.key\""} {
		body := strings.Replace(plain, "#include \"textflag.h\"", "#include \"textflag.h\"\n"+include, 1)
		if err := os.WriteFile(assembly, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if err := d.build(target); err == nil || !strings.Contains(err.Error(), "protected assembly include cannot be certified") {
			t.Fatalf("assembly directive %q was accepted: %v", include, err)
		}
		if protectedOpens != 0 || builds != 0 {
			t.Fatalf("assembly directive %q: %d content opens, %d target builds", include, protectedOpens, builds)
		}
	}
	if _, err := d.readSource(protected); err == nil || protectedOpens != 1 {
		t.Fatalf("injected protected assembly read escaped observer: %v, count %d", err, protectedOpens)
	}
}

func TestVerifyRejectsProtectedImportedDirectory(t *testing.T) {
	root := buildFixture(t)
	protected := filepath.Join(root, "internal", "dist", "value.go")
	if err := os.MkdirAll(filepath.Dir(protected), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(protected, []byte("package dist\nconst Value = 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var opens, builds int
	d := driver{repo: root, summary: &Summary{},
		readSource: func(name string) ([]byte, error) {
			if name == protected {
				opens++
				return nil, fmt.Errorf("protected content open attempted")
			}
			return os.ReadFile(name)
		},
		runBuild: func(cmd *exec.Cmd) ([]byte, error) {
			builds++
			return cmd.CombinedOutput()
		}}
	target := filepath.Join(root, ".local", "bin", "brain")
	if err := d.build(target); err != nil {
		t.Fatalf("unused excluded directory blocked build: %v", err)
	}
	builds = 0
	main := "package main\nimport (\"fmt\"; \"gatefixture/internal/dist\")\nfunc main() { fmt.Println(dist.Value) }\n"
	if err := os.WriteFile(filepath.Join(root, "cmd", "brain", "main.go"), []byte(main), 0600); err != nil {
		t.Fatal(err)
	}
	if err := d.build(target); err == nil || !strings.Contains(err.Error(), "protected import cannot be certified") {
		t.Fatalf("protected imported directory was accepted: %v", err)
	}
	if opens != 0 || builds != 0 {
		t.Fatalf("protected import: %d content opens, %d target builds", opens, builds)
	}
}

func TestVerifyRejectsBuildableProtectedAssembly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows cannot create a reserved NUL.s fixture")
	}
	root := buildFixture(t)
	allowed := filepath.Join(root, "cmd", "brain", "visible.s")
	content := "#include \"textflag.h\"\nTEXT ·dummy(SB), NOSPLIT, $0-0\n RET\n"
	if err := os.WriteFile(allowed, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, ".local", "bin", "brain")
	builds := 0
	d := driver{repo: root, summary: &Summary{}, runBuild: func(cmd *exec.Cmd) ([]byte, error) {
		builds++
		return cmd.CombinedOutput()
	}}
	if err := d.build(target); err != nil {
		t.Fatalf("allowed assembly did not build: %v", err)
	}
	if err := os.Rename(allowed, filepath.Join(root, "cmd", "brain", "NUL.s")); err != nil {
		t.Fatal(err)
	}
	builds = 0
	if err := d.build(target); err == nil || !strings.Contains(err.Error(), "protected compiler input cannot be certified") {
		t.Fatalf("buildable protected assembly was accepted: %v", err)
	}
	if builds != 0 {
		t.Fatalf("protected assembly started %d target builds", builds)
	}
}

func TestVerifyBuildDisablesCgo(t *testing.T) {
	root := buildFixture(t)
	t.Setenv("CGO_ENABLED", "1")
	protected := filepath.Join(root, "cmd", "brain", "secret.key")
	if err := os.WriteFile(protected, []byte("#define SECRET 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cgoSource := "package main\n/*\n#include \"secret.key\"\n*/\nimport \"C\"\nfunc main() { _ = C.SECRET }\n"
	if err := os.WriteFile(filepath.Join(root, "cmd", "brain", "main.go"), []byte(cgoSource), 0600); err != nil {
		t.Fatal(err)
	}
	var protectedOpens, builds int
	d := driver{repo: root, summary: &Summary{},
		readSource: func(name string) ([]byte, error) {
			if name == protected {
				protectedOpens++
				return nil, fmt.Errorf("protected content open attempted")
			}
			return os.ReadFile(name)
		},
		runBuild: func(cmd *exec.Cmd) ([]byte, error) {
			builds++
			if cmd.Env[len(cmd.Env)-1] != "CGO_ENABLED=0" {
				t.Fatalf("target build did not disable C integration: %v", cmd.Env[len(cmd.Env)-1])
			}
			return cmd.CombinedOutput()
		}}
	if err := d.build(filepath.Join(root, ".local", "bin", "brain")); err == nil || !strings.Contains(err.Error(), "target build failed") {
		t.Fatalf("C source built with C integration disabled: %v", err)
	}
	if builds != 1 || protectedOpens != 0 {
		t.Fatalf("C source control: %d builds, %d protected opens", builds, protectedOpens)
	}
}

func TestVerifyRejectsProtectedEmbeddedInput(t *testing.T) {
	root := buildFixture(t)
	target := filepath.Join(root, ".local", "bin", "brain")
	protected := filepath.Join(root, "cmd", "brain", "auth.json")
	visible := filepath.Join(root, "cmd", "brain", "visible.txt")
	baseline, err := sourceTree(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(protected, []byte("protected fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	ignored, err := sourceTree(root)
	if err != nil || ignored != baseline {
		t.Fatalf("ordinary excluded file changed source identity: %v", err)
	}
	var protectedOpens, allowedReads, builds int
	observeRead := func(name string) ([]byte, error) {
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return nil, err
		}
		if excludedBuildInput(relative) {
			protectedOpens++
			return nil, fmt.Errorf("protected content open attempted: %s", relative)
		}
		if name == visible {
			allowedReads++
		}
		return os.ReadFile(name)
	}
	d := driver{repo: root, summary: &Summary{}, readSource: observeRead,
		runBuild: func(cmd *exec.Cmd) ([]byte, error) {
			builds++
			return cmd.CombinedOutput()
		}}
	writeEmbed := func(pattern string) {
		t.Helper()
		body := "package main\nimport (\n _ \"embed\"\n \"fmt\"\n)\n//go:embed " + pattern + "\nvar value string\nfunc main() { fmt.Println(value) }\n"
		if err := os.WriteFile(filepath.Join(root, "cmd", "brain", "main.go"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeEmbed("auth.json")
	if err := d.build(target); err == nil || !strings.Contains(err.Error(), "protected embedded input cannot be certified") {
		t.Fatalf("protected embed was accepted: %v", err)
	}
	if protectedOpens != 0 || builds != 0 {
		t.Fatalf("protected embed: %d content opens, %d target builds", protectedOpens, builds)
	}
	tabSource := "package main\nimport _ \"embed\"\n//go:embed\tauth.json\nvar value string\nfunc main() { _ = value }\n"
	if err := os.WriteFile(filepath.Join(root, "cmd", "brain", "main.go"), []byte(tabSource), 0600); err != nil {
		t.Fatal(err)
	}
	if err := d.build(target); err == nil || !strings.Contains(err.Error(), "protected embedded input cannot be certified") {
		t.Fatalf("tab-separated protected embed was accepted: %v", err)
	}
	if protectedOpens != 0 || builds != 0 {
		t.Fatalf("tab-separated protected embed: %d content opens, %d target builds", protectedOpens, builds)
	}
	hidden := filepath.Join(root, "cmd", "brain", ".env")
	if err := os.WriteFile(hidden, []byte("hidden fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	writeEmbed(".env")
	if err := d.build(target); err == nil || !strings.Contains(err.Error(), "protected embedded input cannot be certified") {
		t.Fatalf("explicit hidden embed was accepted: %v", err)
	}
	if protectedOpens != 0 || builds != 0 {
		t.Fatalf("explicit hidden embed: %d content opens, %d target builds", protectedOpens, builds)
	}
	if _, err := observeRead(protected); err == nil || !strings.Contains(err.Error(), "protected content open attempted") {
		t.Fatalf("injected protected embed read escaped observer: %v", err)
	}
	if protectedOpens != 1 {
		t.Fatalf("injected protected embed read count: %d", protectedOpens)
	}
	if err := os.WriteFile(visible, []byte("visible A"), 0600); err != nil {
		t.Fatal(err)
	}
	writeEmbed("visible.txt")
	if err := d.build(target); err != nil {
		t.Fatalf("unrelated excluded file blocked allowed embed: %v", err)
	}
	firstTree, firstBinary := d.summary.SourceTree, d.builtSHA
	if err := os.WriteFile(visible, []byte("visible B"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := d.build(target); err != nil {
		t.Fatal(err)
	}
	if allowedReads != 4 || builds != 2 || d.summary.SourceTree == firstTree || d.builtSHA == firstBinary {
		t.Fatalf("allowed embed: %d reads, %d builds, source changed %t, executable changed %t", allowedReads, builds, d.summary.SourceTree != firstTree, d.builtSHA != firstBinary)
	}
	if err := os.Remove(protected); err != nil {
		t.Fatal(err)
	}
	wildcard := "package main\nimport (\"embed\"; \"fmt\")\n//go:embed *\nvar files embed.FS\nfunc main() { value, _ := files.ReadFile(\".env\"); fmt.Println(string(value)) }\n"
	if err := os.WriteFile(filepath.Join(root, "cmd", "brain", "main.go"), []byte(wildcard), 0600); err != nil {
		t.Fatal(err)
	}
	builds, protectedOpens = 0, 0
	if err := d.build(target); err == nil || !strings.Contains(err.Error(), "protected embedded input cannot be certified") {
		t.Fatalf("wildcard embedded hidden file was accepted: %v", err)
	}
	if protectedOpens != 0 || builds != 0 {
		t.Fatalf("wildcard hidden embed: %d protected opens, %d target builds", protectedOpens, builds)
	}
	for _, test := range []struct{ directory, pattern string }{
		{"dist", "dist/payload.txt"},
		{"dist", "dist/*"},
		{".local", ".local/payload.txt"},
	} {
		payload := filepath.Join(root, "cmd", "brain", test.directory, "payload.txt")
		if err := os.MkdirAll(filepath.Dir(payload), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(payload, []byte("protected fixture"), 0600); err != nil {
			t.Fatal(err)
		}
		body := "package main\nimport _ \"embed\"\n//go:embed " + test.pattern + "\nvar value string\nfunc main() { _ = value }\n"
		if strings.Contains(test.pattern, "*") {
			body = "package main\nimport \"embed\"\n//go:embed " + test.pattern + "\nvar files embed.FS\nfunc main() { _ = files }\n"
		}
		if err := os.WriteFile(filepath.Join(root, "cmd", "brain", "main.go"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if err := d.build(target); err == nil || !strings.Contains(err.Error(), "protected embedded input cannot be certified") {
			t.Fatalf("nested protected embed %q was accepted: %v", test.pattern, err)
		}
		if protectedOpens != 0 || builds != 0 {
			t.Fatalf("nested protected embed %q: %d protected opens, %d target builds", test.pattern, protectedOpens, builds)
		}
	}
}

func TestVerifyRecordsBinaryDigest(t *testing.T) {
	t.Run("default built target", func(t *testing.T) {
		root := buildFixture(t)
		var out bytes.Buffer
		if code := Run(root, []string{"--feature", "cited-search"}, &out); code != 1 {
			t.Fatalf("fixture journey should fail after launching its minimal target: %d, %s", code, &out)
		}
		lines := strings.Split(strings.TrimSpace(out.String()), "\n")
		data, err := os.ReadFile(filepath.Join(lines[len(lines)-1], "summary.json"))
		if err != nil {
			t.Fatal(err)
		}
		var summary Summary
		if err := json.Unmarshal(data, &summary); err != nil {
			t.Fatal(err)
		}
		built, err := os.ReadFile(filepath.Join(root, ".local", "bin", "brain"))
		if err != nil {
			t.Fatal(err)
		}
		identity, err := sourceTree(root)
		if err != nil {
			t.Fatal(err)
		}
		if summary.BinarySHA256 != fmt.Sprintf("%x", sha256.Sum256(built)) || summary.SourceTree != "sha256:"+identity || len(summary.Commands) == 0 || !summary.Commands[0].Started {
			t.Fatalf("default build evidence mismatch: %#v", summary)
		}
	})
	t.Run("explicit outside target", func(t *testing.T) {
		code, summary, _ := execute(t, "--binary", binary)
		data, err := os.ReadFile(binary)
		if err != nil {
			t.Fatal(err)
		}
		if code != 0 || summary.BinarySHA256 != fmt.Sprintf("%x", sha256.Sum256(data)) {
			t.Fatalf("executed binary digest is wrong: %q", summary.BinarySHA256)
		}
		if summary.SourceTree != "unverified (outside executable)" {
			t.Fatalf("outside source identity: %q", summary.SourceTree)
		}
	})
}

func TestEvidenceFailures(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".local"), []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := Run(root, nil, &out); code != 3 {
		t.Fatalf("evidence parent failure: %d", code)
	}
	if err := writeJSON(filepath.Join(t.TempDir(), "missing", "x.json"), Summary{}); err == nil {
		t.Fatal("missing directory accepted")
	}
	if err := writeJSON(filepath.Join(t.TempDir(), "x.json"), make(chan int)); err == nil {
		t.Fatal("invalid JSON accepted")
	}
	for _, name := range []string{"summary.json", "summary.md", "index.html"} {
		t.Run(name, func(t *testing.T) {
			d := driver{evidence: t.TempDir(), summary: &Summary{
				Features: []Feature{{"cited-search", "proven", "target journey completed"}},
			}}
			if err := os.Mkdir(filepath.Join(d.evidence, name), 0700); err != nil {
				t.Fatal(err)
			}
			if err := d.save(); err == nil {
				t.Fatal("blocked evidence accepted")
			}
			if d.summary.Exit != 1 {
				t.Fatalf("failed evidence retained exit %d", d.summary.Exit)
			}
			for _, human := range []string{"summary.md", "index.html"} {
				if human == name {
					continue
				}
				data, err := os.ReadFile(filepath.Join(d.evidence, human))
				if err != nil || !bytes.Contains(data, []byte("Exit 1.")) || !bytes.Contains(data, []byte("evidence write failed")) {
					t.Fatalf("%s does not report incomplete evidence: %s (%v)", human, data, err)
				}
			}
			if name != "summary.json" {
				data, err := os.ReadFile(filepath.Join(d.evidence, "summary.json"))
				if err != nil {
					t.Fatal(err)
				}
				var saved Summary
				if err := json.Unmarshal(data, &saved); err != nil {
					t.Fatal(err)
				}
				if saved.Exit != 1 || saved.Features[0].Status != "failed" ||
					!strings.Contains(saved.Features[0].Reason, "evidence") {
					t.Fatalf("incomplete evidence claims success: %s", data)
				}
			}
		})
	}
	if _, err := snapshot(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing snapshot accepted")
	}
}

func TestCommandRefusalIsRetained(t *testing.T) {
	d := driver{evidence: t.TempDir(), summary: &Summary{}}
	if err := d.prepare(binary); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(d.summary.Scratch)
	if _, err := d.command(d.summary.Scratch, "unknown-verification-command"); err == nil {
		t.Fatal("command refusal was accepted")
	}
	if len(d.summary.Commands) != 1 || (!d.summary.Commands[0].Started || d.summary.Commands[0].Exit == nil || *d.summary.Commands[0].Exit != 2) {
		t.Fatalf("refusal record: %#v", d.summary.Commands)
	}
	if _, err := os.Stat(filepath.Join(d.evidence, "logs", "01-command.json")); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyUnlaunchableBinary(t *testing.T) {
	target := filepath.Join(t.TempDir(), "brain.exe")
	if err := os.WriteFile(target, []byte("invalid executable bytes\n"), 0700); err != nil {
		t.Fatal(err)
	}
	code, summary, evidence := execute(t, "--binary", target)
	if code != 3 || summary.Features[0].Status != "skipped" {
		t.Fatalf("unlaunchable binary: %#v", summary)
	}
	if len(summary.Commands) != 1 || summary.Commands[0].Started || summary.Commands[0].Exit != nil {
		t.Fatalf("failed launch claimed execution: %#v", summary.Commands)
	}
	if !summary.ScratchRemoved {
		t.Fatal("unlaunchable target left scratch")
	}
	data, err := os.ReadFile(filepath.Join(evidence, "logs", "01-command.json"))
	if err != nil {
		t.Fatal(err)
	}
	var record Command
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	if record.Started || record.Exit != nil {
		t.Fatalf("failed launch log: %s", data)
	}
}
