package main

import (
	"brain/internal/platform"
	"brain/scripts/toolchain"
	"brain/tests/testutil"
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestBriefAliasCWD(t *testing.T) {
	j := newJourney(t)
	j.put(t, "alpha/README.md", "# Alpha\n\nAlpha keeps the stored lease.")
	j.ready(t)
	db, err := sql.Open("sqlite", filepath.Join(j.state, "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("UPDATE meta SET value=? WHERE key='scanned_at'", float64(time.Now().UnixNano())/1e9-120)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	before := treeBytes(t, j.state)
	j.put(t, "alpha/README.md", "# Alpha\n\nA changed source must wait for search.")
	alias := filepath.Join(j.base, "project alias")
	if err = os.Symlink(filepath.Join(j.source, "alpha"), alias); err != nil {
		t.Skip("owned directory alias unavailable: " + err.Error())
	}
	for _, format := range []string{"text", "copilot", "vscode"} {
		command := exec.Command(testBinary, "--brief", "--format", format)
		command.Dir = alias
		command.Env = append(j.environment, "PWD="+alias)
		data, err := command.Output()
		if err != nil {
			t.Fatal(err, string(data))
		}
		text := string(data)
		if format != "text" {
			var wrapper map[string]any
			if json.Unmarshal(data, &wrapper) != nil {
				t.Fatal(string(data))
			}
			if format == "copilot" {
				text = wrapper["additionalContext"].(string)
			} else {
				text = wrapper["hookSpecificOutput"].(map[string]any)["additionalContext"].(string)
			}
		}
		if !strings.Contains(text, "Project alpha: 1 file") || !strings.Contains(text, "Alpha keeps the stored lease") || strings.Contains(text, "changed source") {
			t.Fatal("alias lost stored project", text)
		}
		age := regexp.MustCompile(`last scan (\d+) s ago`).FindStringSubmatch(text)
		if len(age) != 2 {
			t.Fatal(text)
		}
		seconds, _ := strconv.Atoi(age[1])
		if seconds < 120 || seconds > 130 {
			t.Fatal("alias refreshed stored age", text)
		}
		if !reflect.DeepEqual(before, treeBytes(t, j.state)) {
			t.Fatal("alias brief changed state")
		}
	}
}

func TestBriefDuringIndex(t *testing.T) {
	j := newJourney(t)
	j.put(t, "alpha/README.md", "# Keeper\n\nA keeper lease.")
	j.ready(t)
	helper := exec.Command(os.Args[0])
	helper.Env = append(os.Environ(), "BRAIN_TEST_ORPHAN_WAL="+j.state)
	if output, err := helper.CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	os.Remove(filepath.Join(j.state, "index.sqlite-shm"))
	holder, err := platform.Acquire(j.state, "index", false)
	if err != nil {
		t.Fatal(err)
	}
	before := treeBytes(t, j.state)
	brief := exec.Command(testBinary, "--brief")
	brief.Env = j.environment
	output, err := brief.Output()
	holder.Close()
	if err != nil || !strings.Contains(string(output), "another Brain process") || !reflect.DeepEqual(before, treeBytes(t, j.state)) {
		t.Fatal("held snapshot lock", err, string(output))
	}
	for n := 0; n < 400; n++ {
		j.put(t, "alpha/document"+strconv.Itoa(n)+".md", "# Unique document "+strconv.Itoa(n)+"\n\nKeeper record "+strconv.Itoa(n)+".")
	}
	writer := exec.Command(testBinary, "index")
	writer.Env = j.environment
	writer.Dir = j.base
	var writerOutput bytes.Buffer
	writer.Stdout = &writerOutput
	writer.Stderr = &writerOutput
	if err := writer.Start(); err != nil {
		t.Fatal(err)
	}
	ended := make(chan error, 1)
	go func() { ended <- writer.Wait() }()
	counts := regexp.MustCompile(`Brain index: (\d+) files?, (\d+) chunks`)
	for n := 0; n < 30; n++ {
		brief = exec.Command(testBinary, "--brief")
		brief.Env = j.environment
		brief.Dir = j.base
		output, err = brief.Output()
		if err != nil {
			t.Fatal(err, string(output))
		}
		if strings.Contains(string(output), "not ready") {
			if !strings.Contains(string(output), "another Brain process") {
				t.Fatal("inconsistent snapshot", string(output))
			}
		} else {
			found := counts.FindStringSubmatch(string(output))
			if len(found) != 3 || found[1] != found[2] || found[1] != "1" && found[1] != "401" {
				t.Fatal("mixed snapshot counts", string(output))
			}
		}
	}
	if err := <-ended; err != nil {
		t.Fatal(err, writerOutput.String())
	}
	brief = exec.Command(testBinary, "--brief")
	brief.Env = j.environment
	output, err = brief.Output()
	if err != nil || !strings.Contains(string(output), "401 files, 401 chunks") {
		t.Fatal(err, string(output))
	}
}

func TestBriefLongPaths(t *testing.T) {
	for _, depth := range []int{0, 6, 12} {
		j := newJourney(t)
		state := j.base
		for n := 0; n < depth; n++ {
			state = filepath.Join(state, strings.Repeat("s", 90))
		}
		for room := (800-len(testBinary))/2 - len("/config.json"); depth == 0 && len(state)+1 < room; {
			state = filepath.Join(state, strings.Repeat("s", min(90, room-len(state)-1)))
		}
		command := exec.Command(testBinary, "--brief", "--state-dir", state)
		command.Env = append(j.environment, "PATH=")
		output, err := command.Output()
		if err != nil {
			t.Fatal(err, string(output))
		}
		form := briefCommandForm(t, string(output), testBinary, state)
		line := strings.Split(string(output), "\n")[2]
		if depth >= 6 {
			if form != "fixed" || strings.Contains(line, state[:100]) {
				t.Fatal("partial recovery command", line)
			}
		} else {
			if form != "run" || !strings.Contains(line, state) {
				t.Fatal("recovery command was cut", line)
			}
			words := testutil.Shell(runtime.GOOS, os.Getenv("SystemRoot"), strings.TrimPrefix(line, "Run first: "))
			follow := exec.Command(words[0], words[1:]...)
			follow.Env = append(j.environment, "PATH=")
			follow.Dir = j.source
			if data, err := follow.CombinedOutput(); err != nil {
				t.Fatal(err, string(data))
			}
			if _, err := os.Stat(filepath.Join(state, "config.json")); err != nil {
				t.Fatal("retry chose wrong state", err)
			}
		}
	}
	j := newJourney(t)
	binary := filepath.Join(j.base, "Brain  tool")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	data, err := os.ReadFile(testBinary)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(binary, data, 0700); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(j.base, "state  folder")
	command := exec.Command(binary, "--brief", "--state-dir", state)
	command.Env = append(j.environment, "PATH=")
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Split(string(output), "\n")[2]
	if !strings.Contains(line, "Brain  tool") || !strings.Contains(line, "state  folder") {
		t.Fatal("quoted whitespace changed", line)
	}
	words := testutil.Shell(runtime.GOOS, os.Getenv("SystemRoot"), strings.TrimPrefix(line, "Run first: "))
	follow := exec.Command(words[0], words[1:]...)
	follow.Env = append(j.environment, "PATH=")
	follow.Dir = j.source
	if data, err := follow.CombinedOutput(); err != nil {
		t.Fatal(err, string(data))
	}
	if _, err := os.Stat(filepath.Join(state, "config.json")); err != nil {
		t.Fatal("double-space retry chose wrong state", err)
	}
	if _, err := os.Stat(filepath.Join(j.base, "state folder")); !os.IsNotExist(err) {
		t.Fatal("whitespace-normalized state was created")
	}
}

func briefCommandForm(t *testing.T, text, binary, state string) string {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(lines) != 3 || len([]rune(text)) >= 1000 {
		t.Fatal("brief must be three lines under 1000 characters", text)
	}
	head, line := len([]rune(lines[0]))+len([]rune(lines[1]))+3, lines[2]
	if strings.HasPrefix(line, "Run first: ") {
		return "run"
	}
	if strings.Contains(line, "Doctor for ") {
		return "both"
	}
	for _, label := range []string{"Search: ", "Search stored data: "} {
		for _, kind := range []string{"write", "repair"} {
			suffix := ". The doctor command cannot fit this brief. Run the binary's help command for " + kind + " guidance."
			search, found := strings.CutPrefix(line, label)
			if !found || !strings.HasSuffix(search, suffix) {
				continue
			}
			search = strings.TrimSuffix(search, suffix)
			doctor := strings.Replace(strings.Replace(search, `"search" "your words"`, `"doctor"`, 1), `'search' 'your words'`, `'doctor'`, 1)
			if doctor == search || head+len([]rune(label+search+". Doctor for "+kind+" guidance: "+doctor)) < 1000 {
				t.Fatal("brief dropped a doctor command that fits", text)
			}
			return "search"
		}
	}
	if line != "The command cannot fit this brief. Run the binary's help command for recovery guidance." || head+len("Search: ")+len(binary)+2*len(state) < 1000 {
		t.Fatal("brief dropped a command that may fit", text)
	}
	return "fixed"
}

func treeBytes(t *testing.T, folder string) map[string][32]byte {
	t.Helper()
	files := map[string][32]byte{}
	err := filepath.WalkDir(folder, func(name string, item fs.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if item.IsDir() {
			return nil
		}
		data, err := os.ReadFile(name)
		if err == nil {
			relative, _ := filepath.Rel(folder, name)
			files[relative] = sha256.Sum256(data)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestSkillMatchesHelp(t *testing.T) {
	code, printed := call(t, "help", "--skill")
	data, err := os.ReadFile(filepath.Join(repoRoot, "skills", "brain", "SKILL.md"))
	if code != 0 || err != nil || string(data) != printed {
		t.Fatal("run scripts/generate-skill", code, err, printed)
	}
	if !strings.Contains(printed, "Use two or three specific keywords") || !strings.Contains(printed, `search "lease timeout"`) {
		t.Fatal("search keyword guidance or example is missing", printed)
	}
	if len(strings.Split(strings.TrimSpace(printed), "\n")) > 60 || len(strings.Fields(printed)) > 400 {
		t.Fatal("skill exceeds size limits")
	}
	base := t.TempDir()
	source := filepath.Join(repoRoot, "internal", "cli", "help.go")
	original, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	changed := bytes.Replace(original, []byte("Find chunks with cited paths."), []byte("Find the best chunks with cited paths."), 1)
	if bytes.Equal(original, changed) {
		t.Fatal("help mutation missed its target")
	}
	overlaySource := filepath.Join(base, "help.go")
	os.WriteFile(overlaySource, changed, 0600)
	overlay := filepath.Join(base, "overlay.json")
	encoded, _ := json.Marshal(map[string]any{"Replace": map[string]string{source: overlaySource}})
	os.WriteFile(overlay, encoded, 0600)
	binary := filepath.Join(base, "brain")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command(toolchain.Binary(), "build", "-trimpath", "-buildvcs=false", "-overlay", overlay, "-o", binary, "./cmd/brain")
	build.Dir = repoRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	// The normal lane uses this exact drift gate. The mutated help must be rejected.
	if runtime.GOOS != "windows" {
		check := exec.Command(filepath.Join(repoRoot, "scripts", "check-skill"), binary, filepath.Join(repoRoot, "skills", "brain", "SKILL.md"))
		output, err := check.CombinedOutput()
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 || !strings.Contains(string(output), "skill drift") {
			t.Fatal(err, string(output))
		}
	}
	command := exec.Command(binary, "help", "--skill")
	output, err := command.Output()
	if err != nil || bytes.Equal(output, data) || !strings.Contains(string(output), "Find the best chunks") {
		t.Fatal("help drift not reflected in skill", err, string(output))
	}
}

func TestBriefThreeFormats(t *testing.T) {
	for stage := 0; stage < 3; stage++ {
		j := newJourney(t)
		j.put(t, "alpha/README.md", "# Alpha\n\nAlpha returns a lease.")
		if stage > 0 {
			if code, reply := j.run(t, "init", "--source", j.source); code != 0 {
				t.Fatal(code, reply)
			}
		}
		if stage > 1 {
			if code, reply := j.run(t, "index"); code != 0 {
				t.Fatal(code, reply)
			}
		}
		before := treeBytes(t, j.state)
		for _, format := range []string{"text", "copilot", "vscode"} {
			command := exec.Command(testBinary, "--brief", "--format", format)
			command.Dir = filepath.Join(j.source, "alpha")
			command.Env = append(j.environment, "PATH=")
			var stderr bytes.Buffer
			command.Stderr = &stderr
			output, err := command.Output()
			if err != nil || stderr.Len() != 0 {
				t.Fatal(stage, format, err, stderr.String())
			}
			text := string(output)
			if format != "text" {
				var reply map[string]any
				if json.Unmarshal(output, &reply) != nil || len(reply) != 1 {
					t.Fatal(string(output))
				}
				if format == "copilot" {
					text = reply["additionalContext"].(string)
				} else {
					inside := reply["hookSpecificOutput"].(map[string]any)
					if inside["hookEventName"] != "SessionStart" || len(inside) != 2 {
						t.Fatal(reply)
					}
					text = inside["additionalContext"].(string)
				}
			}
			if !strings.HasPrefix(text, "Brain") || strings.Count(text, "\n") != 3 || len([]rune(text)) >= 1000 || briefCommandForm(t, text, testBinary, j.state) == "fixed" {
				t.Fatal(text)
			}
			if stage == 0 && !strings.Contains(text, "init") || stage == 1 && !strings.Contains(text, "index builds") || stage == 2 && (!strings.Contains(text, "1 file, 1 chunks") || !strings.Contains(text, "Project alpha: 1 file") || !strings.Contains(text, "README: Alpha returns a lease.")) {
				t.Fatal(stage, text)
			}
		}
		if !reflect.DeepEqual(before, treeBytes(t, j.state)) {
			t.Fatal("brief changed state", stage)
		}
		if stage == 2 {
			j.put(t, "alpha/new.md", "New source stays pending.")
			testutil.Permissions(t, j.state, 0500)
			command := exec.Command(testBinary, "--brief")
			command.Env = j.environment
			output, err := command.Output()
			testutil.Permissions(t, j.state, 0700)
			if err != nil || !strings.Contains(string(output), "1 file, 1 chunks") || !strings.Contains(string(output), "read-only snapshot") || !strings.Contains(string(output), "doctor") {
				t.Fatal(err, string(output))
			}
			if form := briefCommandForm(t, string(output), testBinary, j.state); !strings.Contains(string(output), "Search stored data:") || form != "both" && form != "search" {
				t.Fatal("read-only search guidance is misleading", string(output))
			}
			testutil.Permissions(t, j.state, 0500)
			code, stored := j.run(t, "search", "lease")
			testutil.Permissions(t, j.state, 0700)
			if code != 0 || stored["total"] != float64(1) || stored["read_only"] != true {
				t.Fatal("search could not read stored data", code, stored)
			}
			if !reflect.DeepEqual(before, treeBytes(t, j.state)) {
				t.Fatal("brief refreshed or changed state")
			}
			os.WriteFile(filepath.Join(j.state, "index.sqlite"), []byte("damaged"), 0600)
			command = exec.Command(testBinary, "--brief")
			command.Env = j.environment
			output, err = command.Output()
			if err != nil || !strings.Contains(string(output), `"--full"`) {
				t.Fatal(err, string(output))
			}
		}
	}
}

func TestSetupWriteOnlyCopiesSkill(t *testing.T) {
	for _, harness := range []string{"claude", "codex", "copilot", "vscode", "all"} {
		j := newJourney(t)
		for _, name := range []string{".claude/settings.json", ".codex/hooks.json", ".copilot/hooks/existing.json", ".codex/config.toml"} {
			os.MkdirAll(filepath.Dir(filepath.Join(j.base, name)), 0700)
			os.WriteFile(filepath.Join(j.base, name), []byte("Existing settings remain byte-for-byte unchanged.\n"), 0600)
		}
		before := treeBytes(t, j.base)
		code, reply := j.run(t, "setup", harness)
		if code != 0 || !reflect.DeepEqual(before, treeBytes(t, j.base)) {
			t.Fatal("guidance changed files", code, reply)
		}
		code, reply = j.run(t, "setup", harness, "--write")
		if code != 0 {
			t.Fatal(code, reply)
		}
		items := reply["setups"].([]any)
		wanted := map[string]bool{}
		for _, item := range items {
			setup := item.(map[string]any)
			if setup["written"] != true || setup["skill_current"] != true {
				t.Fatal(setup)
			}
			relative, _ := filepath.Rel(j.base, setup["skill_file"].(string))
			wanted[relative] = true
		}
		after := treeBytes(t, j.base)
		for name, digest := range before {
			if after[name] != digest {
				t.Fatal("setup changed existing file", name)
			}
		}
		if len(after) != len(before)+len(wanted) {
			t.Fatal("setup wrote extra files", after)
		}
		for name := range wanted {
			data, err := os.ReadFile(filepath.Join(j.base, name))
			committed, _ := os.ReadFile(filepath.Join(repoRoot, "skills", "brain", "SKILL.md"))
			if err != nil || !bytes.Equal(data, committed) {
				t.Fatal(name, err)
			}
		}
	}
	if runtime.GOOS != "windows" {
		j := newJourney(t)
		outside := t.TempDir()
		if err := os.Symlink(outside, filepath.Join(j.base, ".claude")); err != nil {
			t.Fatal(err)
		}
		if code, reply := j.run(t, "setup", "claude", "--write"); code != 1 || reply["error"].(map[string]any)["code"] != "state-unwritable" {
			t.Fatal(code, reply)
		}
		if len(treeBytes(t, outside)) != 0 {
			t.Fatal("setup followed skill-folder symlink")
		}
	}
}
