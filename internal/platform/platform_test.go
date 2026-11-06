package platform

import (
	"brain/tests/testutil"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStateFolderPerOS(t *testing.T) {
	for _, test := range []struct {
		system, home, override, want string
		environment                  map[string]string
	}{
		{"darwin", "/People/Ada", "", "/People/Ada/Library/Application Support/Brain", nil},
		{"linux", "/People/Ada", "", "/People/Ada/.local/share/brain", nil},
		{"linux", "/People/Ada", "", "/data/brain", map[string]string{"XDG_DATA_HOME": "/data"}},
		{"linux", "/People/Ada", "", "/People/Ada/.local/share/brain", map[string]string{"XDG_DATA_HOME": "relative"}},
		{"windows", `C:\People\Ada`, "", `C:\People\Ada\AppData\Local\Brain`, nil},
		{"windows", `C:\People\Ada`, "", `D:\Local\Brain`, map[string]string{"LOCALAPPDATA": `D:\Local`}},
		{"windows", `C:\People\Ada`, "", `C:\People\Ada\AppData\Local\Brain`, map[string]string{"LOCALAPPDATA": "relative"}},
		{"darwin", "", "/temporary/state", "/temporary/state", nil},
		{"linux", "/People/Ada", "/flag/state", "/flag/state", map[string]string{"BRAIN_HOME": "/environment/state"}},
		{"windows", `C:\People\Ada`, "", `\\server\share\state`, map[string]string{"BRAIN_HOME": `\\server\share\state`}},
	} {
		got, err := StateFolder(test.system, test.home, test.environment, test.override)
		if err != nil || got != test.want {
			t.Fatal(test, got, err)
		}
	}
	for _, system := range []string{"linux", "darwin", "windows"} {
		if _, err := StateFolder(system, "", nil, ""); err == nil {
			t.Fatal("missing home accepted")
		}
		if _, err := StateFolder(system, "anything", nil, "relative"); err == nil {
			t.Fatal("relative override accepted")
		}
	}
	t.Setenv("BRAIN_HOME", filepath.Join(t.TempDir(), "state"))
	if _, err := CurrentState(""); err != nil {
		t.Fatal(err)
	}
}

func TestReadStateAtMost(t *testing.T) {
	folder := t.TempDir()
	if err := os.WriteFile(filepath.Join(folder, "skill.md"), []byte("current"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadStateAtMost(folder, "skill.md", 6); !errors.Is(err, ErrSourceOversize) {
		t.Fatal("oversized skill was read", err)
	}
	if data, err := ReadStateAtMost(folder, "skill.md", 7); err != nil || string(data) != "current" {
		t.Fatal("matching bounded read failed", string(data), err)
	}
	if _, err := ReadStateAtMost(folder, "skill.md", -1); !errors.Is(err, ErrSourceOversize) {
		t.Fatal("negative limit accepted", err)
	}
	if err := os.Symlink("skill.md", filepath.Join(folder, "linked.md")); err == nil {
		if _, err := ReadStateAtMost(folder, "linked.md", 7); err == nil {
			t.Fatal("linked skill was read")
		}
	}
}
func TestDurableWriteAndLocks(t *testing.T) {
	folder := t.TempDir()
	destination := filepath.Join(folder, "config.json")
	if err := WriteState(folder, "config.json", []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := WriteState(folder, "config.json", []byte("second")); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(destination); err != nil || string(data) != "second" {
		t.Fatal(string(data), err)
	}
	if err := WriteState(folder, filepath.Join("absent", "config"), nil); err == nil {
		t.Fatal("missing parent accepted")
	}
	if err := WriteState(folder, ".", []byte("cannot replace a folder")); err == nil {
		t.Fatal("directory replaced")
	}
	if !Writable(folder) || Writable(filepath.Join(folder, "absent")) || Writable(destination) {
		t.Fatal("writability result")
	}
	if !ReadableDir(folder) || ReadableDir(filepath.Join(folder, "absent")) {
		t.Fatal("directory access result")
	}
	if err := testutil.Permissions(t, folder, 0500); err != nil {
		t.Fatal(err)
	}
	if Writable(folder) {
		t.Fatal("read-only folder accepted")
	}
	testutil.Permissions(t, folder, 0700)
	first, err := Acquire(folder, "test", true)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := Acquire(folder, "test", false); !errors.Is(err, ErrLocked) {
		if second != nil {
			second.Close()
		}
		t.Fatal("second lock allowed", err)
	}
	first.Close()
	second, err := Acquire(folder, "test", false)
	if err != nil {
		t.Fatal("closed lock still held", err)
	}
	second.Close()
	if _, err := Acquire(destination, "test", false); err == nil {
		t.Fatal("file used as state folder")
	}
	if lockBusy(os.ErrNotExist) {
		t.Fatal("missing file counts as lock contention")
	}
}

func TestReadLockDoesNotWrite(t *testing.T) {
	folder := t.TempDir()
	if _, err := ReadLock(folder); err == nil {
		t.Fatal("missing lock was created")
	}
	holder, err := Acquire(folder, "index", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadLock(folder); !errors.Is(err, ErrLocked) {
		t.Fatal("writer lock ignored", err)
	}
	holder.Close()
	name := filepath.Join(folder, "locks", "index.lock")
	before, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	read, err := ReadLock(folder)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(folder, "index", false); !errors.Is(err, ErrLocked) {
		t.Fatal("snapshot lock ignored", err)
	}
	read.Close()
	after, _ := os.Stat(name)
	if !before.ModTime().Equal(after.ModTime()) || before.Size() != after.Size() {
		t.Fatal("read lock changed its file")
	}
}
func TestShellCommands(t *testing.T) {
	for _, shell := range []string{"sh", "powershell", "cmd"} {
		line := Command(shell, []string{`/temporary/a $cash 'quote' "double"`, "%value%"})
		if line == "" {
			t.Fatal(line)
		}
		if shell == "powershell" && !strings.Contains(line, "''quote''") {
			t.Fatal(line)
		}
		if shell == "sh" && !strings.Contains(line, `\$cash`) {
			t.Fatal(line)
		}
		if shell == "cmd" && !strings.Contains(line, "-EncodedCommand ") {
			t.Fatal(line)
		}
	}
}

func TestStatePathsStayConfined(t *testing.T) {
	state, outside := t.TempDir(), t.TempDir()
	sentinel := filepath.Join(outside, "sentinel")
	os.WriteFile(sentinel, []byte("untouched"), 0600)
	if err := os.Mkdir(filepath.Join(state, "notes"), 0700); err != nil {
		t.Fatal(err)
	}
	if !WritableState(state, "notes") || WritableState(state, "absent") || WritableState(filepath.Join(state, "absent"), "notes") || WritableState(state, "../outside") {
		t.Fatal("confined folder access")
	}
	if err := WriteState(state, filepath.Join("notes", "entry.md"), []byte("entry")); err != nil {
		t.Fatal(err)
	}
	if data, err := ReadState(state, filepath.Join("notes", "entry.md")); err != nil || string(data) != "entry" {
		t.Fatal(string(data), err)
	}
	if SafeStatePath(state, "../outside") == nil || SafeStatePath(filepath.Join(state, "absent"), "notes") == nil {
		t.Fatal("unsafe path accepted")
	}
	if _, err := ReadState(filepath.Join(state, "absent"), "entry"); err == nil {
		t.Fatal("missing state read")
	}
	for _, fixture := range []struct{ name, target string }{{"escaped", outside}, {"entry-link", sentinel}, {"internal-link", filepath.Join(state, "notes", "entry.md")}} {
		if err := os.Symlink(fixture.target, filepath.Join(state, fixture.name)); err != nil {
			t.Skip("symlink fixture unavailable: " + err.Error())
		}
		if SafeStatePath(state, fixture.name) == nil || WritableState(state, fixture.name) {
			t.Fatal("symlink accepted")
		}
		if _, err := ReadState(state, fixture.name); err == nil {
			t.Fatal("symlink read")
		}
		if err := WriteState(state, fixture.name, []byte("changed")); err == nil {
			t.Fatal("symlink written")
		}
	}
	if err := os.Symlink(outside, filepath.Join(state, "locks")); err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(state, "notes", true); err == nil {
		t.Fatal("escaping lock opened")
	}
	data, _ := os.ReadFile(sentinel)
	if string(data) != "untouched" {
		t.Fatal("outside state changed")
	}
}
