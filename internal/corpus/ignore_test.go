package corpus

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNestedBrainIgnore(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"README.md", "root.md", "nested/root.md", "nested/drop.md", "nested/keep.md", "nested/deep/drop.md", "Case.md", "lower.md", "#literal.md", "!literal.md", "space .md"} {
		put(t, root, name, "allowed or excluded")
	}
	put(t, root, ".brainignore", "/root.md\n**/drop.md\nCase.md\n\\#literal.md\n\\!literal.md\nspace\\ .md\n")
	put(t, root, "nested/.brainignore", "*.md\n!keep.md\n!deep/\n")
	config := Default(root, []string{root}, nil)
	result, err := ScanWithPolicy(config)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"README.md", "nested/keep.md", "lower.md"} {
		if _, ok := result.Entries[filepath.Join(root, filepath.FromSlash(name))]; !ok {
			t.Fatal("allowed missing", name)
		}
	}
	for _, name := range []string{"root.md", "nested/root.md", "nested/drop.md", "nested/deep/drop.md", "Case.md", "#literal.md", "!literal.md", "space .md"} {
		if !result.Excluded(filepath.Join(root, filepath.FromSlash(name))) {
			t.Fatal("not excluded", name)
		}
	}
	if len(result.Entries) != 3 {
		t.Fatal(result.Entries)
	}
}

func TestIgnoreNegationBoundaries(t *testing.T) {
	root := t.TempDir()
	put(t, root, "README.md", "keeper")
	put(t, root, "closed/secret.md", "hidden")
	put(t, root, "closed/.brainignore", "!secret.md\n")
	put(t, root, ".env-doc.md", "hidden")
	put(t, root, "nested/denied.md", "hidden")
	put(t, root, ".brainignore", "closed/\n!closed/secret.md\n!.env-doc.md\n*.md\n!README.md\n!nested/\n")
	put(t, root, "nested/.brainignore", "!denied.md\n")
	result, err := ScanWithPolicy(Default(root, []string{root}, nil))
	if err != nil || len(result.Entries) != 2 {
		t.Fatal(result, err)
	}
	if !result.Excluded(filepath.Join(root, "closed/secret.md")) || !result.Excluded(filepath.Join(root, ".env-doc.md")) {
		t.Fatal("negation reopened boundary")
	}
	if _, ok := result.Entries[filepath.Join(root, "nested/denied.md")]; !ok {
		t.Fatal("child negation did not reopen a file")
	}
}

func TestIgnoreFilesNotIndexed(t *testing.T) {
	root := t.TempDir()
	put(t, root, "README.md", "keeper")
	put(t, root, ".brainignore", "# controlmarker\n")
	result, err := ScanWithPolicy(Default(root, []string{root}, nil))
	if err != nil || len(result.Entries) != 1 || !result.Excluded(filepath.Join(root, ".brainignore")) {
		t.Fatal(result, err)
	}
	for _, kind := range []string{"symlink", "directory", "unreadable", "oversize"} {
		t.Run(kind, func(t *testing.T) {
			folder := t.TempDir()
			target := put(t, folder, "outside.md", "keeper")
			control := filepath.Join(folder, ".brainignore")
			switch kind {
			case "symlink":
				if err := os.Symlink(target, control); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(control, 0700); err != nil {
					t.Fatal(err)
				}
			case "unreadable":
				if runtime.GOOS == "windows" {
					t.Skip("Unix mode check")
				}
				put(t, folder, ".brainignore", "*.md")
				os.Chmod(control, 0)
				defer os.Chmod(control, 0600)
			case "oversize":
				if err := os.WriteFile(control, make([]byte, 1048577), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := ScanWithPolicy(Default(folder, []string{folder}, nil)); !errors.Is(err, ErrIgnoreUnreadable) {
				t.Fatal("unsafe control accepted", err)
			}
		})
	}
}

func TestIgnorePatternSyntax(t *testing.T) {
	for _, test := range []struct {
		pattern, name   string
		directory, want bool
	}{
		{"/root.md", "root.md", false, true}, {"/root.md", "nested/root.md", false, false},
		{`folder\/secret.md`, "folder/secret.md", false, true}, {`\/root.md`, "root.md", false, false},
		{`\/root.md`, "nested/root.md", false, false}, {`folder\/`, "folder", true, false},
		{"\ufeffhidden.md", "hidden.md", false, true}, {"allowed.md\n\ufeffhidden.md", "hidden.md", false, false},
		{"a/***/b.md", "a/b.md", false, true}, {"a/****/b.md", "a/one/two/b.md", false, true},
		{"**/name.md", "name.md", false, true}, {"**/name.md", "a/b/name.md", false, true},
		{"a/**/b.md", "a/b.md", false, true}, {"a/**/b.md", "a/one/two/b.md", false, true},
		{"a/*/b.md", "a/one/two/b.md", false, false}, {"a/**", "a", true, false}, {"a/**", "a/b", false, true},
		{"cache/", "cache", true, true}, {"cache/", "cache", false, false},
		{"?.md", "é.md", false, false}, {"??.md", "é.md", false, true},
		{"???.md", "中.md", false, true}, {"????.md", "😀.md", false, true},
		{"foo/\n!foo\\/", "foo", true, true},
		{"[a-c]?.md", "b2.md", false, true}, {"[!a-c]?.md", "z2.md", false, true},
		{"[[:digit:]].md", "2.md", false, true}, {"[]a].md", "].md", false, true}, {`[\]].md`, "].md", false, true}, {`[a\-c].md`, "-.md", false, true}, {`[a\`, "a", false, false}, {"[[:broken]].md", "2.md", false, false}, {"name\\", "name", false, false},
		{"[z-a].md", "z.md", false, true}, {"[z-a].md", "a.md", false, false},
		{"*.md\n![[:word:]].md", "a.md", false, true}, {"*.md\n![[:ascii:]].md", "a.md", false, true},
		{"[[:alnum:]].md", "2.md", false, true}, {"[[:alpha:]].md", "A.md", false, true},
		{"[[:blank:]].md", "\t.md", false, true}, {"[[:cntrl:]].md", "\x7f.md", false, true},
		{"[[:graph:]].md", "!.md", false, true}, {"[[:lower:]].md", "a.md", false, true},
		{"[[:print:]].md", " .md", false, true}, {"[[:punct:]].md", "_.md", false, true},
		{"[[:upper:]].md", "A.md", false, true}, {"[[:xdigit:]].md", "F.md", false, true},
		{"[[:space:]].md", "\t.md", false, true}, {"[[:space:]].md", "\v.md", false, false},
		{"[[:space:]].md", "\f.md", false, false}, {"a.md\x00*.md", "a.md", false, true},
		{"*.md", "UPPER.MD", false, false},
		{"*.md", "line\nbreak.md", false, true}, {"\\#name.md", "#name.md", false, true},
		{"\\!name.md", "!name.md", false, true}, {"name.md   ", "name.md", false, true},
		{"name\\ ", "name ", false, true}, {"#ignored", "#ignored", false, false},
		{"!", "anything", false, false}, {"/", "anything", false, false}, {"[broken", "b", false, false},
	} {
		t.Run(test.pattern+test.name, func(t *testing.T) {
			rules := parseIgnore(test.pattern)
			root := t.TempDir()
			got := matchIgnoreScopes([]struct {
				base  string
				rules []ignoreRule
			}{{root, rules}}, filepath.Join(root, filepath.FromSlash(test.name)), test.directory)
			if got != test.want {
				t.Fatal(test, got)
			}
		})
	}
}

func TestIgnoreMatchesGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("Git is unavailable for the differential check")
	}
	root := t.TempDir()
	command := func(args ...string) *exec.Cmd {
		cmd := exec.Command(git, append([]string{"-C", root, "-c", "core.ignorecase=false", "-c", "core.excludesFile="}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "LC_ALL=C")
		return cmd
	}
	if output, err := command("init", "--quiet").CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	names := []string{"README.md", "a.md", "ab.md", "é.md", "中.md", "😀.md", "éa.md", "éé.md", "è.md", "UPPER.MD", "2.md", "-.md", "].md", "z.md", "Q.md", "_.md", "^.md", "[.md", " .md", "foo/private.md", "foo/deep/private.md", "a/b.md", "a/one/b.md", "a/one/two/b.md", "nested/é.md"}
	if runtime.GOOS != "windows" {
		names = append(names, `foo\/private.md`, `foo\\/private.md`, "\t.md", "\v.md", "\f.md")
	}
	for _, name := range names {
		put(t, root, name, "content")
	}
	patterns := []string{
		"?.md", "??.md", "???.md", "????.md", "é.md", "é?.md", "?é.md", "*é*.md", "é*.md",
		"[é].md", "[é]?.md", "[!a-z]?.md", "[[:alpha:]]?.md", "[[:digit:]].md", "[a-c]?.md", "[]a].md", `[a\-c].md`,
		"foo/", `foo\/`, "foo/\n!foo\\/", "foo/\n!foo/", `foo\/private.md`, "foo/**", "foo/**/private.md",
		"a/*/b.md", "a/**/b.md", "a/***/b.md", "**/??.md", "**é*.md", "*.md\n!??.md",
		"[[:word:]].md", "[[:ascii:]].md", "*.md\n![[:word:]].md", "*.md\n![[:ascii:]].md", "[![:word:]].md",
		"[z-a].md", "*.md\n![z-a].md", "[/[ab].md", "[/[ab]/b.md", "*.md\n![/[ab].md", "[a-b-c].md", "[--a].md", "[]-].md", "[a^].md", "[a[b].md",
		`[[:\][:upper:]].md`, `*.md` + "\n" + `![[:\][:upper:]].md`, "[[:space:]].md", "*.md\n![[:space:]].md", "[[:upper:][:lower:]].md", "[[:blank:]].md", "[[:punct:]].md", "a.md\x00*.md", "*.md\n!a.md\x00b.md",
		`foo\\/`, `foo\\\/`, `foo\\\\/`, "foo/\n!foo\\\\/", "[è-é]?.md", "[!é]?.md", "é**.md", "*中*.md", "*😀*.md",
	}
	for _, pattern := range patterns {
		t.Run(pattern, func(t *testing.T) {
			put(t, root, ".gitignore", pattern+"\n")
			put(t, root, ".brainignore", pattern+"\n")
			cmd := command("check-ignore", "--no-index", "--stdin", "-z")
			cmd.Stdin = strings.NewReader(strings.Join(names, "\x00") + "\x00")
			output, err := cmd.Output()
			if err != nil {
				var failure *exec.ExitError
				if !errors.As(err, &failure) || failure.ExitCode() != 1 {
					t.Fatal(err)
				}
			}
			ignored := map[string]bool{}
			for _, name := range strings.Split(string(output), "\x00") {
				ignored[name] = true
			}
			result, err := ScanWithPolicy(Default(root, []string{root}, nil))
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range names {
				path := filepath.Join(root, filepath.FromSlash(name))
				_, indexed := result.Entries[path]
				if indexed == ignored[name] || result.Excluded(path) != ignored[name] {
					t.Errorf("pattern %q, path %q: Git ignored=%t, Brain indexed=%t, excluded=%t", pattern, name, ignored[name], indexed, result.Excluded(path))
				}
			}
		})
	}
	t.Logf("matched Git for %d patterns and %d paths (%d comparisons)", len(patterns), len(names), len(patterns)*len(names))
}

func TestIgnoreSeparators(t *testing.T) {
	for input, want := range map[string]string{`folder\/secret.md`: "folder/secret.md", `folder\\/secret.md`: `folder\\/secret.md`, `folder\\\/secret.md`: `folder\\/secret.md`, `name\`: `name\`, `\!name`: `\!name`} {
		if got := ignoreSeparators(input); got != want {
			t.Fatalf("%q became %q, want %q", input, got, want)
		}
	}
}

func TestIgnoreFingerprint(t *testing.T) {
	root := t.TempDir()
	put(t, root, "allowed.md", "allowed")
	config := Default(root, []string{root}, nil)
	first, err := ScanWithPolicy(config)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := first.PolicyFingerprint()
	put(t, root, "empty/allowed.md", "allowed")
	second, err := ScanWithPolicy(config)
	if err != nil {
		t.Fatal(err)
	}
	if second.PolicyFingerprint() != fingerprint {
		t.Fatal("empty scope changed policy")
	}
	put(t, root, ".brainignore", "hidden.md\n")
	third, err := ScanWithPolicy(config)
	if err != nil {
		t.Fatal(err)
	}
	if third.PolicyFingerprint() == fingerprint {
		t.Fatal("changed policy reused fingerprint")
	}
	fourth, err := ScanWithPolicy(config)
	if err != nil {
		t.Fatal(err)
	}
	if third.PolicyFingerprint() != fourth.PolicyFingerprint() {
		t.Fatal("policy fingerprint is unstable")
	}
}

func TestIgnoreUnavailableDescendant(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission fixture")
	}
	root := t.TempDir()
	put(t, root, "README.md", "allowed")
	hidden := put(t, root, "closed/private.md", "hidden")
	put(t, root, ".brainignore", "closed/private.md\n")
	parent := filepath.Dir(hidden)
	if err := os.Chmod(parent, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(parent, 0700)
	result, err := ScanWithPolicy(Default(root, []string{root}, nil))
	if err != nil || len(result.Skipped) != 1 || len(result.Entries) != 1 || !result.Excluded(hidden) {
		t.Fatal(result, err)
	}
}

func TestIgnoreOverlappingSources(t *testing.T) {
	for _, parentFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "child-first", true: "parent-first"}[parentFirst], func(t *testing.T) {
			root := t.TempDir()
			put(t, root, "README.md", "allowed")
			child := filepath.Join(root, "child")
			target := put(t, root, "child/hidden.md", "hidden")
			put(t, root, ".brainignore", "child/\n")
			if err := os.Symlink(target, filepath.Join(child, ".brainignore")); err != nil {
				t.Fatal(err)
			}
			sources := []Source{{Path: child}, {Path: root}}
			if parentFirst {
				sources[0], sources[1] = sources[1], sources[0]
			}
			config := Default(root, []string{root}, nil)
			config.Sources = sources
			result, err := ScanWithPolicy(config)
			if err != nil || len(result.Entries) != 1 || !result.Excluded(target) {
				t.Fatal(result, err)
			}
		})
	}
}

func TestIgnoreSourceOwnership(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "child")
	target := put(t, root, "child/allowed.md", "allowed")
	for _, parentFirst := range []bool{false, true} {
		sources := []Source{{Path: child, Project: "child"}, {Path: root, Project: "parent"}}
		if parentFirst {
			sources[0], sources[1] = sources[1], sources[0]
		}
		config := Default(root, []string{root}, nil)
		config.Sources = sources
		result, err := ScanWithPolicy(config)
		if err != nil || len(result.Entries) != 1 || result.Entries[target].Project != sources[1].Project || result.Entries[target].Namespace != "source:1:" {
			t.Fatal(result, err)
		}
	}
}

func TestIgnoreWildcardSourceOwnership(t *testing.T) {
	root := t.TempDir()
	target := put(t, root, "open/nested/allowed.md", "allowed")
	hidden := put(t, root, "closed/nested/hidden.md", "hidden")
	put(t, root, ".brainignore", "/*/\n!/open/\n!/closed/nested/\n")
	for _, parentFirst := range []bool{false, true} {
		sources := []Source{{Path: filepath.Join(root, "*", "*"), Project: "wildcard"}, {Path: root, Project: "parent"}}
		wantSource, wantNamespace := root, "source:1:"
		if parentFirst {
			sources[0], sources[1] = sources[1], sources[0]
			wantSource, wantNamespace = filepath.Join(root, "open", "nested"), "source:1:open/nested"
		}
		config := Default(root, []string{root}, nil)
		config.Sources = sources
		result, err := ScanWithPolicy(config)
		if err != nil || len(result.Entries) != 1 || !result.Excluded(hidden) {
			t.Fatal(result, err)
		}
		entry := result.Entries[target]
		if entry.Project != sources[1].Project || entry.Source != wantSource || entry.Namespace != wantNamespace {
			t.Fatal("wildcard source ownership changed", parentFirst, entry)
		}
	}
}
