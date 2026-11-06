package verifyrun

import (
	"brain/internal/corpus"
	"brain/scripts/toolchain"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Command struct {
	Argv       []string `json:"argv"`
	Exit       *int     `json:"exit"`
	Started    bool     `json:"started"`
	Stdin      string   `json:"stdin"`
	DurationMS int64    `json:"duration_ms"`
	Stdout     string   `json:"stdout"`
	Stderr     string   `json:"stderr"`
}

type driver struct {
	repo       string
	evidence   string
	binary     string
	builtSHA   string
	summary    *Summary
	readSource func(string) ([]byte, error)
	runBuild   func(*exec.Cmd) ([]byte, error)
}

func (d *driver) prepare(binary string) error {
	for _, name := range []string{"logs", "traces", "screens"} {
		if err := os.Mkdir(filepath.Join(d.evidence, name), 0700); err != nil {
			return err
		}
	}
	absolute, err := filepath.Abs(binary)
	if err != nil {
		return err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return fmt.Errorf("binary unavailable; run scripts/build: %w", err)
	}
	if !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode()&0111 == 0) {
		return fmt.Errorf("binary must be an executable regular file")
	}
	scratch, err := os.MkdirTemp("", "brain-verify-")
	if err != nil {
		return err
	}
	d.summary.Scratch = scratch
	resolved, err := filepath.EvalSymlinks(scratch)
	if err != nil {
		return err
	}
	d.summary.Scratch = resolved
	// Execute the copied bytes so a later replacement of the source path cannot
	// change which file the command logs and digest describe.
	d.binary = filepath.Join(resolved, "brain"+filepath.Ext(absolute))
	input, err := os.Open(absolute)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(d.binary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return err
	}
	data, err := os.ReadFile(d.binary)
	if err != nil {
		return err
	}
	d.summary.BinarySHA256 = fmt.Sprintf("%x", sha256.Sum256(data))
	if d.builtSHA != "" && d.summary.BinarySHA256 != d.builtSHA {
		return errors.New("built executable changed before verification")
	}
	return nil
}

func (d *driver) build(target string) error {
	read := d.readSource
	if read == nil {
		read = os.ReadFile
	}
	before, err := sourceTreeWithRead(d.repo, read)
	if err != nil {
		return fmt.Errorf("source identity unavailable: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return err
	}
	goBinary := toolchain.Binary()
	cmd := exec.Command(goBinary, "build", "-trimpath", "-buildvcs=false", "-o", target, "./cmd/brain")
	cmd.Dir = d.repo
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	run := d.runBuild
	if run == nil {
		run = (*exec.Cmd).CombinedOutput
	}
	if out, err := run(cmd); err != nil {
		return fmt.Errorf("target build failed: %w: %s", err, out)
	}
	after, err := sourceTreeWithRead(d.repo, read)
	if err != nil {
		return fmt.Errorf("source identity unavailable after build: %w", err)
	}
	if before != after {
		return errors.New("source tree changed during target build")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return err
	}
	d.builtSHA = fmt.Sprintf("%x", sha256.Sum256(data))
	d.summary.SourceTree = "sha256:" + after
	return nil
}

func sourceTree(root string) (string, error) {
	return sourceTreeWithRead(root, os.ReadFile)
}

func sourceTreeWithRead(root string, read func(string) ([]byte, error)) (string, error) {
	directories := []string{"cmd", "internal", "scripts/verifyrun", "scripts/verifycmd", "scripts/verifybootstrap", "scripts/toolchain"}
	files := []string{"scripts/check", "scripts/verify", "go.mod", "go.sum"}
	inputs := map[string]bool{}
	excluded := []string{}
	for _, directory := range directories {
		err := filepath.WalkDir(filepath.Join(root, directory), func(name string, entry fs.DirEntry, walkErr error) error {
			if errors.Is(walkErr, os.ErrNotExist) && name == filepath.Join(root, directory) {
				return nil
			}
			if walkErr != nil {
				return walkErr
			}
			relative, err := filepath.Rel(root, name)
			if err != nil {
				return err
			}
			if excludedBuildInput(relative) {
				if protectedCompilerInput(relative) {
					return fmt.Errorf("protected compiler input cannot be certified: %s", relative)
				}
				excluded = append(excluded, relative)
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("source symlink unsupported: %s", relative)
			}
			if entry.IsDir() {
				return nil
			}
			if !entry.Type().IsRegular() {
				return fmt.Errorf("source entry is not a regular file: %s", relative)
			}
			inputs[relative] = true
			return nil
		})
		if err != nil {
			return "", err
		}
	}
	for _, path := range files {
		if excludedBuildInput(path) {
			if protectedCompilerInput(path) {
				return "", fmt.Errorf("protected compiler input cannot be certified: %s", path)
			}
			continue
		}
		info, err := os.Lstat(filepath.Join(root, path))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("source symlink unsupported: %s", path)
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("source entry is not a regular file: %s", path)
		}
		inputs[path] = true
	}
	cmd := exec.Command("git", "ls-files", "-z", "--cached", "--",
		"cmd", "internal", "scripts/verifyrun", "scripts/verifycmd", "scripts/verifybootstrap", "scripts/toolchain", "scripts/check", "scripts/verify", "go.mod", "go.sum")
	cmd.Dir = root
	listed, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("the verifier needs a Git checkout and git on the command path: %w", err)
	}
	for _, path := range strings.Split(strings.TrimSuffix(string(listed), "\x00"), "\x00") {
		if path != "" && !excludedBuildInput(path) {
			if _, present := inputs[path]; !present {
				inputs[path] = false
			}
		}
	}
	if len(inputs) == 0 {
		return "", errors.New("no source files found")
	}
	paths := make([]string, 0, len(inputs))
	for path := range inputs {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	hash := sha256.New()
	embeds := map[string][]string{}
	includes := map[string][]string{}
	var assembly []string
	for _, path := range paths {
		if !inputs[path] {
			fmt.Fprintf(hash, "%s\x00missing\x00", filepath.ToSlash(path))
			continue
		}
		name := filepath.Join(root, path)
		info, err := os.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(hash, "%s\x00missing\x00", filepath.ToSlash(path))
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("source symlink unsupported: %s", path)
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("source entry is not a regular file: %s", path)
		}
		data, err := read(name)
		if err != nil {
			return "", err
		}
		if strings.HasSuffix(path, ".go") {
			parsed, err := parser.ParseFile(token.NewFileSet(), path, data, parser.ImportsOnly)
			if err != nil {
				return "", fmt.Errorf("Go source imports cannot be certified: %s: %w", path, err)
			}
			for _, imported := range parsed.Imports {
				value, err := strconv.Unquote(imported.Path.Value)
				if err != nil {
					return "", err
				}
				if excludedBuildInput(value) {
					return "", fmt.Errorf("protected import cannot be certified: %s", path)
				}
			}
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "//go:embed") {
					rest := strings.TrimPrefix(line, "//go:embed")
					if rest != "" && (rest[0] == ' ' || rest[0] == '\t') {
						embeds[filepath.Dir(path)] = append(embeds[filepath.Dir(path)], strings.TrimLeft(rest, " \t"))
					}
				}
			}
		}
		if strings.HasSuffix(path, ".s") {
			assembly = append(assembly, path)
		}
		for _, line := range strings.Split(string(data), "\n") {
			if directive, ok := assemblyIncludeDirective(line); ok {
				includes[path] = append(includes[path], directive)
			}
		}
		fmt.Fprintf(hash, "%s\x00%d\x00", filepath.ToSlash(path), info.Mode()&os.ModeType)
		hash.Write(data)
		hash.Write([]byte{0})
	}
	for directory, directives := range embeds {
		for _, candidate := range excluded {
			relative, err := filepath.Rel(directory, candidate)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				continue
			}
			for _, directive := range directives {
				patterns, ok := embedWords(directive)
				if !ok {
					return "", fmt.Errorf("protected embedded input cannot be certified: %s", candidate)
				}
				for _, pattern := range patterns {
					all := strings.HasPrefix(pattern, "all:")
					pattern = strings.TrimPrefix(pattern, "all:")
					if embeddedMatch(pattern, filepath.ToSlash(relative), all) || embeddedPatternBelow(pattern, filepath.ToSlash(relative)) {
						return "", fmt.Errorf("protected embedded input cannot be certified: %s", candidate)
					}
				}
			}
		}
	}
	seen := map[string]bool{}
	var checkIncludes func(string) error
	checkIncludes = func(name string) error {
		if seen[name] {
			return nil
		}
		seen[name] = true
		for _, directive := range includes[name] {
			words, ok := embedWords(directive)
			if !ok || len(words) == 0 || !strings.HasPrefix(directive, "\"") {
				return fmt.Errorf("assembly include cannot be certified: %s", name)
			}
			if filepath.IsAbs(words[0]) {
				return fmt.Errorf("assembly include path cannot be certified: %s", name)
			}
			for _, part := range strings.Split(filepath.ToSlash(words[0]), "/") {
				if part == ".." {
					return fmt.Errorf("assembly include path cannot be certified: %s", name)
				}
			}
			candidate := filepath.Clean(filepath.Join(filepath.Dir(name), words[0]))
			if excludedBuildInput(candidate) {
				for _, protected := range excluded {
					if protected == candidate || strings.HasPrefix(candidate, protected+string(filepath.Separator)) {
						return fmt.Errorf("protected assembly include cannot be certified: %s", candidate)
					}
				}
			}
			if inputs[candidate] {
				if err := checkIncludes(candidate); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, name := range assembly {
		if err := checkIncludes(name); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func assemblyIncludeDirective(line string) (string, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "#") {
		return "", false
	}
	line = strings.TrimSpace(strings.TrimPrefix(line, "#"))
	for strings.HasPrefix(line, "/*") {
		end := strings.Index(line, "*/")
		if end < 0 {
			return line, true
		}
		line = strings.TrimSpace(line[end+2:])
	}
	if !strings.HasPrefix(line, "include") {
		return "", false
	}
	line = strings.TrimPrefix(line, "include")
	for {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "/*") {
			return line, true
		}
		end := strings.Index(line, "*/")
		if end < 0 {
			return line, true
		}
		line = line[end+2:]
	}
}

func embedWords(line string) ([]string, bool) {
	var words []string
	for len(line) > 0 {
		line = strings.TrimLeft(line, " \t")
		if line == "" {
			break
		}
		if line[0] != '"' && line[0] != '`' {
			end := strings.IndexAny(line, " \t")
			if end < 0 {
				end = len(line)
			}
			words = append(words, line[:end])
			line = line[end:]
			continue
		}
		quote := line[0]
		end := 1
		for end < len(line) {
			if quote == '"' && line[end] == '\\' {
				end += 2
				continue
			}
			if line[end] == quote {
				break
			}
			end++
		}
		if end >= len(line) {
			return nil, false
		}
		word, err := strconv.Unquote(line[:end+1])
		if err != nil {
			return nil, false
		}
		words = append(words, word)
		line = line[end+1:]
	}
	return words, true
}

func embeddedMatch(pattern, candidate string, all bool) bool {
	parts := strings.Split(candidate, "/")
	for index := range parts {
		prefix := strings.Join(parts[:index+1], "/")
		if matched, err := path.Match(pattern, prefix); err != nil {
			return true
		} else if matched {
			if !all {
				for _, descendant := range parts[index+1:] {
					if strings.HasPrefix(descendant, ".") || strings.HasPrefix(descendant, "_") {
						return false
					}
				}
			}
			return true
		}
	}
	return false
}

func embeddedPatternBelow(pattern, directory string) bool {
	for index, char := range pattern {
		if char != '/' {
			continue
		}
		if matched, err := path.Match(pattern[:index], directory); err != nil || matched {
			return true
		}
	}
	return false
}

// Go accepts these ordinary-looking .go basenames on Unix, although the
// source identity must leave their protected bytes unread.
func protectedCompilerInput(path string) bool {
	name := filepath.Base(path)
	return (strings.HasSuffix(name, ".go") || strings.HasSuffix(name, ".s") || strings.HasSuffix(name, ".syso")) &&
		!strings.HasPrefix(name, ".") && !strings.HasPrefix(name, "_")
}

func excludedBuildInput(path string) bool {
	return corpus.NeverReadPath(path)
}

func (d *driver) command(home string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return d.invoke(ctx, d.binary, home, nil, args...)
}

func (d *driver) invoke(ctx context.Context, executable, home string, extraEnv []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "APPDATA=" + filepath.Join(home, "config"), "LOCALAPPDATA=" + filepath.Join(home, "data"), "XDG_CONFIG_HOME=" + filepath.Join(home, "config"), "XDG_DATA_HOME=" + filepath.Join(home, "data"), "TMPDIR=" + home, "TEMP=" + home, "TMP=" + home}
	for _, name := range []string{"PATH", "SystemRoot", "WINDIR"} {
		if value := os.Getenv(name); value != "" {
			cmd.Env = append(cmd.Env, name+"="+value)
		}
	}
	cmd.Env = append(cmd.Env, "BRAIN_HOME="+filepath.Join(home, "state"))
	cmd.Env = append(cmd.Env, extraEnv...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	err := cmd.Start()
	started := err == nil
	if started {
		err = cmd.Wait()
	}
	var code *int
	if cmd.ProcessState != nil {
		value := cmd.ProcessState.ExitCode()
		code = &value
	}
	record := Command{Argv: append([]string{executable}, args...), Exit: code, Started: started, Stdin: "closed", DurationMS: time.Since(start).Milliseconds(), Stdout: stdout.String(), Stderr: stderr.String()}
	d.summary.Commands = append(d.summary.Commands, record)
	number := len(d.summary.Commands)
	if writeErr := writeJSON(filepath.Join(d.evidence, "logs", fmt.Sprintf("%02d-command.json", number)), record); writeErr != nil {
		return nil, writeErr
	}
	if !started {
		return nil, fmt.Errorf("%w: %s did not start: %v", errTargetUnready, filepath.Base(executable), err)
	}
	if err != nil {
		return nil, fmt.Errorf("%s failed after launch: %w", filepath.Base(executable), err)
	}
	if stderr.Len() != 0 {
		return nil, fmt.Errorf("%s wrote unexpected stderr", filepath.Base(executable))
	}
	return stdout.Bytes(), nil
}

func snapshot(root string) (map[string]string, error) {
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			files[relative] = "directory"
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unexpected non-regular source file %s", relative)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[relative] = fmt.Sprintf("%x", sha256.Sum256(data))
		return nil
	})
	return files, err
}

func (d *driver) citedSearch(owner string, attempt int) (problem error) {
	base := filepath.Join(d.summary.Scratch, fmt.Sprint(attempt))
	source := filepath.Join(base, "sources")
	home := filepath.Join(base, "home")
	state := filepath.Join(base, "state")
	document := filepath.Join(source, "harbor", "README.md")
	text := "# Harbor\n\nAmber otters carry the harbor ledger.\n"
	for _, path := range []string{filepath.Dir(document), home} {
		if err := os.MkdirAll(path, 0700); err != nil {
			return err
		}
	}
	if err := os.WriteFile(document, []byte(text), 0600); err != nil {
		return err
	}
	before, err := snapshot(source)
	if err != nil {
		return err
	}
	homeBefore, err := snapshot(home)
	if err != nil {
		return err
	}
	defer func() {
		after, err := snapshot(source)
		if err != nil {
			problem = errors.Join(problem, err)
			return
		}
		homeAfter, err := snapshot(home)
		if err != nil {
			problem = errors.Join(problem, err)
			return
		}
		if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(homeBefore, homeAfter) {
			problem = errors.Join(problem, fmt.Errorf("source or isolated home changed"))
		} else {
			d.summary.SideEffects[fmt.Sprintf("journey_%d", attempt+1)] = "Source and isolated home snapshots unchanged."
		}
		problem = errors.Join(problem, writeJSON(filepath.Join(d.evidence, "traces", fmt.Sprintf("%d-files.json", attempt+1)), map[string]any{"source_before": before, "source_after": after, "home_before": homeBefore, "home_after": homeAfter}))
	}()
	call := func(args ...string) ([]byte, error) {
		return d.command(home, append(args, "--state-dir", state, "--format", "json")...)
	}
	if _, err := call("init", "--source", source); err != nil {
		return err
	}
	for _, name := range []string{"config.json"} {
		if info, err := os.Stat(filepath.Join(state, name)); err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("init did not create %s", name)
		}
	}
	data, err := call("index")
	if err != nil {
		return err
	}
	var indexed struct {
		New    int `json:"new"`
		Chunks int `json:"chunks"`
	}
	if err := json.Unmarshal(data, &indexed); err != nil {
		return err
	}
	if indexed.New != 1 || indexed.Chunks < 1 {
		return fmt.Errorf("index reported %d new files and %d chunks", indexed.New, indexed.Chunks)
	}
	if info, err := os.Stat(filepath.Join(state, "index.sqlite")); err != nil || info.Size() == 0 {
		return fmt.Errorf("index did not create a nonempty database")
	}
	data, err = call("search", "amber otters", "--no-refresh")
	if err != nil {
		return err
	}
	var search struct {
		Count   int                                           `json:"count"`
		Age     *int                                          `json:"index_age_s"`
		Results []struct{ ID, Path, Project, Snippet string } `json:"results"`
	}
	if err := json.Unmarshal(data, &search); err != nil {
		return err
	}
	if search.Count != 1 || len(search.Results) != 1 || search.Age == nil {
		return fmt.Errorf("search must return one result and index age")
	}
	result := search.Results[0]
	if result.Project != owner || result.Path != document || result.ID == "" || !strings.Contains(result.Snippet, "Amber otters") {
		return fmt.Errorf("search citation mismatch; expected project %q, observed %q", owner, result.Project)
	}
	data, err = call("show", result.ID, "--no-refresh")
	if err != nil {
		return err
	}
	var shown struct {
		ID, Path, Text string
		Age            *int `json:"index_age_s"`
	}
	if err := json.Unmarshal(data, &shown); err != nil {
		return err
	}
	if shown.ID != result.ID || shown.Path != document || shown.Age == nil || strings.TrimSpace(shown.Text) != strings.TrimSpace(text) {
		return fmt.Errorf("show did not preserve the cited document and index age")
	}
	return nil
}
