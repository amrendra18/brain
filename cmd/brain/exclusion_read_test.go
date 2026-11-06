package main

import (
	"brain/internal/corpus"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const exclusionOpenPrefix = "BRAIN_TEST_OPEN:"

func TestExclusionsNeverRead(t *testing.T) {
	j, excluded := exclusionFixture(t)
	binary := exclusionObservedBinary(t, false)
	opened := exclusionObservedIndex(t, binary, j)
	allowed, rejected := exclusionOpenCounts(opened, j.source, excluded)
	if err := exclusionNoOpenProof(allowed, rejected); err != nil {
		t.Fatal(err)
	}
	for query, want := range map[string]bool{"lighthouse": true, "exclusionmarker": false} {
		code, reply := j.run(t, "search", query, "--no-refresh")
		if (code != 0 && code != 3) || (reply["count"].(float64) > 0) != want {
			t.Fatal(query, code, reply)
		}
	}
	t.Logf("observed %d allowed opens, zero excluded opens; allowed text searchable, excluded marker absent", allowed)
}

// This deliberately broken input must fail the same no-open predicate even
// though the scanner discards every excluded byte before returning candidates.
func TestExclusionObserverRejectsDiscardedRead(t *testing.T) {
	j, excluded := exclusionFixture(t)
	binary := exclusionObservedBinary(t, true)
	opened := exclusionObservedIndex(t, binary, j)
	allowed, rejected := exclusionOpenCounts(opened, j.source, excluded)
	if allowed == 0 || rejected < 12 {
		t.Fatalf("broken-input control observed %d allowed and %d excluded opens; want allowed > 0 and excluded >= 12", allowed, rejected)
	}
	if err := exclusionNoOpenProof(allowed, rejected); err == nil {
		t.Fatal("no-open proof accepted the deliberately broken input")
	}
	code, reply := j.run(t, "search", "exclusionmarker", "--no-refresh")
	if (code != 0 && code != 3) || reply["count"] != float64(0) {
		t.Fatal("broken input must discard the marker", code, reply)
	}
	t.Logf("deliberately broken input rejected: %d excluded opens, %d allowed opens, no indexed marker", rejected, allowed)
}

func exclusionNoOpenProof(allowed, rejected int) error {
	if allowed == 0 || rejected != 0 {
		return fmt.Errorf("observed %d allowed opens and %d excluded opens; want allowed > 0 and excluded = 0", allowed, rejected)
	}
	return nil
}

func exclusionFixture(t *testing.T) (*journey, []string) {
	t.Helper()
	j := newJourney(t)
	j.put(t, "alpha/README.md", "# Beacon\n\nA lighthouse.")
	j.put(t, "alpha/private.md", "exclusionmarker")
	j.put(t, "alpha/.brainignore", "private.md\n")
	excluded := []string{"alpha/.local", "alpha/.env-folder", "alpha/private.pem", "alpha/private.key", "alpha/auth.json", "alpha/node_modules", "alpha/dist", "alpha/.venv", "alpha/.stryker-tmp", "alpha/.claude/worktrees", "alpha/.git", "alpha/talk.jsonl", "alpha/id_identity", "alpha/bundle.p12", "alpha/bundle.pfx", "alpha/.netrc", "alpha/credentials-store", "alpha/vault.kdbx", "alpha/.ssh", "alpha/.gnupg"}
	for _, directory := range excluded {
		j.put(t, directory+"/hidden.md", "exclusionmarker")
	}
	excluded = append(excluded, "alpha/private.md")
	// Windows reserves these names before an ordinary file can be created.
	// The corpus unit table covers their classification on every host.
	if runtime.GOOS != "windows" {
		for _, name := range []string{"alpha/CON.md", "alpha/LPT1.md"} {
			j.put(t, name, "exclusionmarker")
			excluded = append(excluded, name)
		}
	}
	if code, reply := j.run(t, "init", "--source", j.source); code != 0 {
		t.Fatal(code, reply)
	}
	config := corpus.Default(j.source, []string{j.source}, nil)
	// Test each forbidden directory as a direct source too. The normal parent
	// scan must skip it before opening; direct roots must be rejected as well.
	for _, directory := range excluded {
		config.Sources = append(config.Sources, corpus.Source{Path: filepath.Join(j.source, filepath.FromSlash(directory))}, corpus.Source{Path: filepath.Join(j.source, filepath.FromSlash(directory), "*")})
	}
	config.Sources = append(config.Sources, corpus.Source{Path: filepath.Join(j.source, "alpha", ".local", "*")})
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(j.state, "config.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	return j, excluded
}

func exclusionOpenCounts(opened []string, source string, excluded []string) (allowed, rejected int) {
	for _, path := range opened {
		if filepath.Clean(path) == filepath.Join(source, "alpha", "README.md") {
			allowed++
		}
		for _, relative := range excluded {
			forbidden := filepath.Join(source, filepath.FromSlash(relative))
			if path == forbidden || strings.HasPrefix(path, forbidden+string(filepath.Separator)) {
				rejected++
				break
			}
		}
	}
	return allowed, rejected
}

func exclusionObservedIndex(t *testing.T, binary string, j *journey) []string {
	t.Helper()
	code, reply, opened := sourceObservedCommand(t, binary, j, "index")
	if code != 0 || reply["error"] != nil || reply["count"] != float64(0) {
		t.Fatal(code, reply)
	}
	return opened
}

func sourceObservedCommand(t *testing.T, binary string, j *journey, args ...string) (int, map[string]any, []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, append(args, "--format=json")...)
	command.Dir, command.Env = j.base, j.environment
	var output, stderr bytes.Buffer
	command.Stdout, command.Stderr = &output, &stderr
	code := 0
	if err := command.Run(); err != nil {
		if failure, ok := err.(*exec.ExitError); ok {
			code = failure.ExitCode()
		} else {
			t.Fatal(err, output.String(), stderr.String())
		}
	}
	var reply map[string]any
	if err := json.Unmarshal(output.Bytes(), &reply); err != nil {
		t.Fatal(err, output.String())
	}
	var opened []string
	for _, line := range strings.Split(strings.TrimSpace(stderr.String()), "\n") {
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, exclusionOpenPrefix) {
			t.Fatal("unexpected observer stderr", line)
		}
		path := strings.TrimPrefix(line, exclusionOpenPrefix)
		// Every command runs from the owned fixture. Normalize relative paths
		// without resolving source links, which would hide an unsafe open.
		if !filepath.IsAbs(path) {
			path = filepath.Join(j.base, path)
		}
		opened = append(opened, filepath.Clean(path))
	}
	return code, reply, opened
}

func exclusionObservedBinary(t *testing.T, mutant bool, extra ...map[string]map[string]string) string {
	t.Helper()
	root := t.TempDir()
	replacements := map[string]string{}
	originals := map[string][32]byte{}
	add := func(path string, injections map[string]string) {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		originals[path] = sha256.Sum256(data)
		modified := string(data)
		for needle, insertion := range injections {
			if strings.Count(modified, needle) != 1 {
				t.Fatal("observer boundary changed", path, needle)
			}
			modified = strings.Replace(modified, needle, needle+insertion, 1)
		}
		copyPath := filepath.Join(root, "overlay-"+filepath.Base(path))
		if err = os.WriteFile(copyPath, []byte(modified), 0600); err != nil {
			t.Fatal(err)
		}
		replacements[path] = copyPath
	}
	t.Cleanup(func() {
		for path, before := range originals {
			data, err := os.ReadFile(path)
			if err != nil || sha256.Sum256(data) != before {
				t.Errorf("observer changed original bytes: %s (%v)", path, err)
			}
		}
	})
	trace := func(expression string) string {
		return "\nprintln(\"" + exclusionOpenPrefix + "\" + " + expression + ")\n"
	}
	goRoot, err := filepath.EvalSymlinks(runtime.GOROOT())
	if err != nil {
		t.Fatal(err)
	}
	fileOS := "unix"
	if runtime.GOOS == "windows" {
		fileOS = "windows"
	}
	add(filepath.Join(goRoot, "src", "os", "file_"+fileOS+".go"), map[string]string{
		"func openFileNolog(name string, flag int, perm FileMode) (*File, error) {": trace("name"),
		"func openDirNolog(name string) (*File, error) {":                           trace("name"),
	})
	add(filepath.Join(goRoot, "src", "os", "root.go"), map[string]string{
		"func OpenRoot(name string) (*Root, error) {":                                    trace("name"),
		"func (r *Root) OpenFile(name string, flag int, perm FileMode) (*File, error) {": trace("r.Name() + string(PathSeparator) + name"),
		"func (r *Root) OpenRoot(name string) (*Root, error) {":                          trace("r.Name() + string(PathSeparator) + name"),
	})
	add(filepath.Join(repoRoot, "internal", "platform", "source_"+runtime.GOOS+".go"), map[string]string{
		"func openSource(path string) (*os.File, error) {": trace("path"),
	})
	if mutant {
		add(filepath.Join(repoRoot, "internal", "corpus", "corpus.go"), map[string]string{
			"if policy.privateControl(path) || isIgnoreName(itemName(item, path)) || policy.ignored(path, item != nil && item.IsDir()) || platform.ReservedName(itemName(item, path)) || match(parts, rules) || source.Kind != \"note\" && excludedRoot(path, config.Exclude) || item != nil && item.Type()&os.ModeSymlink != 0 {": "\nif item != nil && item.IsDir() {\n data, err := os.ReadFile(filepath.Join(path, \"hidden.md\"))\n if err != nil || string(data) != \"exclusionmarker\" { panic(\"discarded-read control failed\") }\n}\n",
		})
	}
	for _, edits := range extra {
		for path, injections := range edits {
			add(path, injections)
		}
	}
	data, err := json.Marshal(map[string]any{"Replace": replacements})
	if err != nil {
		t.Fatal(err)
	}
	overlay := filepath.Join(root, "overlay.json")
	if err = os.WriteFile(overlay, data, 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "brain-observed")
	goBinary := filepath.Join(goRoot, "bin", "go")
	if runtime.GOOS == "windows" {
		binary += ".exe"
		goBinary += ".exe"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, goBinary, "build", "-buildvcs=false", "-overlay="+overlay, "-o", binary, "./cmd/brain")
	command.Dir = repoRoot
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	return binary
}

func TestSensitiveNamesNeverRead(t *testing.T) {
	j, excluded := exclusionFixture(t)
	j.put(t, "alpha/id_secret.md", "exclusionmarker")
	excluded = append(excluded, "alpha/id_secret.md", "alpha/hostile.md")
	target := filepath.Join(t.TempDir(), "target.md")
	if err := os.WriteFile(target, []byte("exclusionmarker"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(j.source, "alpha/hostile.md")); err != nil {
		t.Fatal(err)
	}
	binary := exclusionObservedBinary(t, false)
	opened := exclusionObservedIndex(t, binary, j)
	allowed, rejected := exclusionOpenCounts(opened, j.source, excluded)
	if err := exclusionNoOpenProof(allowed, rejected); err != nil {
		t.Fatal(err)
	}
	for _, name := range opened {
		if name == target {
			t.Fatal("hostile link target opened")
		}
	}
	code, reply := j.run(t, "search", "lighthouse")
	if code != 0 || reply["count"] != float64(1) {
		t.Fatal("allowed sibling", code, reply)
	}
	t.Logf("observed %d allowed opens and zero sensitive-name or hostile-link opens", allowed)
}

func TestIgnoreRulesNeverRead(t *testing.T) {
	j := newJourney(t)
	j.put(t, "alpha/README.md", "lighthouse")
	j.put(t, "alpha/hidden.md", "exclusionmarker")
	j.put(t, "alpha/closed/secret.md", "exclusionmarker")
	j.put(t, "alpha/closed/.brainignore", "!secret.md")
	j.put(t, "alpha/é.md", "exclusionmarker")
	j.put(t, "alpha/foo/private.md", "exclusionmarker")
	j.put(t, "alpha/.brainignore", "\ufeffhidden.md\nclosed/\n!closed/secret.md\n??.md\nfoo/\n!foo\\/\n")
	for _, class := range []string{"word", "ascii"} {
		j.put(t, "alpha/"+class+"/a.md", "exclusionmarker")
		j.put(t, "alpha/"+class+"/.brainignore", "*.md\n![[:"+class+":]].md\n")
	}
	j.put(t, "alpha/malformed/A.md", "exclusionmarker")
	j.put(t, "alpha/malformed/.brainignore", `[[:\][:upper:]].md`+"\n")
	j.put(t, "alpha/slashclass/a.md", "exclusionmarker")
	j.put(t, "alpha/slashclass/.brainignore", "[/[ab].md\n")
	j.put(t, "alpha/reverse/z.md", "exclusionmarker")
	j.put(t, "alpha/reverse/.brainignore", "[z-a].md\n")
	j.put(t, "alpha/nulbyte/a.md", "exclusionmarker")
	j.put(t, "alpha/nulbyte/.brainignore", "a.md\x00*.md\n")
	target := filepath.Join(t.TempDir(), "target.md")
	if err := os.WriteFile(target, []byte("exclusionmarker"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(j.source, "alpha/hostile.md")); err != nil {
		t.Fatal(err)
	}
	if code, reply := j.run(t, "init", "--source", j.source); code != 0 {
		t.Fatal(code, reply)
	}
	binary := exclusionObservedBinary(t, false)
	opened := exclusionObservedIndex(t, binary, j)
	allowed, rejected := exclusionOpenCounts(opened, j.source, []string{"alpha/hidden.md", "alpha/closed", "alpha/hostile.md", "alpha/é.md", "alpha/foo", "alpha/word/a.md", "alpha/ascii/a.md", "alpha/reverse/z.md", "alpha/nulbyte/a.md", "alpha/slashclass/a.md", "alpha/malformed/A.md"})
	if err := exclusionNoOpenProof(allowed, rejected); err != nil {
		t.Fatal(err)
	}
	for _, name := range opened {
		if name == target {
			t.Fatal("hostile link target opened")
		}
	}
	for query, want := range map[string]float64{"lighthouse": 1, "exclusionmarker": 0} {
		code, reply := j.run(t, "search", query)
		if code != 0 || reply["count"] != want {
			t.Fatal(query, code, reply)
		}
	}
	t.Logf("observed %d allowed opens and zero ignore-excluded opens; excluded marker absent", allowed)
}

func TestIgnoredWildcardNeverRead(t *testing.T) {
	binary := exclusionObservedBinary(t, false)
	for _, pattern := range []string{"closed/*", "*/*"} {
		for _, parentFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/parent-first=%t", pattern, parentFirst), func(t *testing.T) {
				j := newJourney(t)
				j.put(t, "alpha/README.md", "allowed lighthouse")
				j.put(t, "alpha/closed/nested/hidden.md", "exclusionmarker")
				j.put(t, "alpha/open/nested/allowed.md", "siblingbeacon")
				j.put(t, "alpha/.brainignore", "/*/\n!/open/\n!/closed/nested/\n")
				target := filepath.Join(t.TempDir(), "target.md")
				if err := os.WriteFile(target, []byte("exclusionmarker"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(j.source, "alpha/closed/.brainignore")); err != nil {
					t.Fatal(err)
				}
				if code, reply := j.run(t, "init", "--source", j.source); code != 0 {
					t.Fatal(code, reply)
				}
				config := corpus.Default(j.source, []string{j.source}, nil)
				config.Sources = append(config.Sources, corpus.Source{Path: filepath.Join(j.source, "alpha", filepath.FromSlash(pattern))})
				if !parentFirst {
					config.Sources[0], config.Sources[1] = config.Sources[1], config.Sources[0]
				}
				data, err := json.Marshal(config)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(filepath.Join(j.state, "config.json"), data, 0600); err != nil {
					t.Fatal(err)
				}
				opened := exclusionObservedIndex(t, binary, j)
				allowed, rejected := exclusionOpenCounts(opened, j.source, []string{"alpha/closed"})
				if err := exclusionNoOpenProof(allowed, rejected); err != nil {
					t.Fatal(err)
				}
				for _, name := range opened {
					if name == target {
						t.Fatal("hostile link target opened")
					}
				}
				for query, want := range map[string]float64{"lighthouse": 1, "siblingbeacon": 1, "exclusionmarker": 0} {
					code, reply := j.run(t, "search", query)
					if code != 0 || reply["count"] != want {
						t.Fatal(query, code, reply)
					}
				}
				t.Logf("observed %d allowed opens and zero ignored-directory opens; allowed sibling searchable", allowed)
			})
		}
	}
}
