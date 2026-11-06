package installrun

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstallFileBoundaries(t *testing.T) {
	f := newFixture(t)
	target := filepath.Join(f.home, "target")
	if err := safeDirectory(target, true); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0777); err != nil {
		t.Fatal(err)
	}
	if err := safeDirectory(target, false); err == nil {
		t.Fatal("accepted writable parent")
	}
	if err := os.Chmod(target, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(f.home, "alias")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := safeDirectory(link, false); err == nil {
		t.Fatal("accepted directory symlink")
	}
	nested := filepath.Join(link, "nested")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	if err := safeDirectory(nested, false); err == nil {
		t.Fatal("accepted ancestor symlink")
	}
	ordinary := filepath.Join(f.home, "file")
	write(t, ordinary, "file")
	if err := safeDirectory(ordinary, false); err == nil {
		t.Fatal("accepted file as directory")
	}
	if err := safeTarget(filepath.Join(ordinary, "child")); err == nil {
		t.Fatal("accepted file ancestor")
	}
	if err := backupFile(filepath.Join(f.home, "missing"), filepath.Join(target, "backup")); err != nil {
		t.Fatal(err)
	}
	if err := backupFile(ordinary, filepath.Join(f.home, "missing", "backup")); err == nil {
		t.Fatal("backup accepted missing destination parent")
	}
	if err := backupFile(link, filepath.Join(target, "backup")); err == nil {
		t.Fatal("backup accepted directory link")
	}
	if err := backupFile(ordinary, filepath.Join(target, "backup")); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(target, "backup")) != "file" {
		t.Fatal("backup bytes changed")
	}
	if err := backupFile(ordinary, filepath.Join(target, "backup")); err == nil {
		t.Fatal("backup overwritten")
	}
	if err := replaceBinary(filepath.Join(f.home, "missing"), ordinary); err == nil {
		t.Fatal("missing candidate accepted")
	}
	if err := replaceBinary(ordinary, link); err == nil {
		t.Fatal("unsafe destination accepted")
	}
	if err := replaceBinary(ordinary, filepath.Join(f.home, "missing", "binary")); err == nil {
		t.Fatal("missing parent accepted")
	}
	if err := replaceFile(ordinary, filepath.Join(f.home, "missing", "binary")); err == nil {
		t.Fatal("invalid rename accepted")
	}
	if !inside(f.home, ordinary) || inside(f.root, ordinary) || !inside(f.home, f.home) {
		t.Fatal("containment incorrect")
	}
}

func TestInstallPreconditions(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*testing.T, *fixture)
		message string
	}{
		{"relative home", func(t *testing.T, f *fixture) { f.installer.home = "relative" }, "home must be absolute"},
		{"missing build parent", func(t *testing.T, f *fixture) { f.installer.root = filepath.Join(f.root, "missing") }, "no such file"},
		{"unsafe backup", func(t *testing.T, f *fixture) {
			p := filepath.Join(f.home, ".local", "share", "brain-install-backups")
			write(t, p, "occupied")
		}, "unsafe type"},
		{"backup in state", func(t *testing.T, f *fixture) {
			f.state = filepath.Join(f.home, ".local")
			f.config = filepath.Join(f.state, "config.json")
		}, "outside the checkout"},
		{"unsafe skill", func(t *testing.T, f *fixture) {
			p := filepath.Join(f.home, ".agents", "skills", "brain", "SKILL.md")
			if err := os.MkdirAll(p, 0700); err != nil {
				t.Fatal(err)
			}
		}, "unsafe type"},
		{"unsafe state", func(t *testing.T, f *fixture) {
			if err := os.MkdirAll(f.state, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(f.state, 0777); err != nil {
				t.Fatal(err)
			}
		}, "not private"},
		{"moved state", func(t *testing.T, f *fixture) {
			write(t, filepath.Join(f.state, "relocation.json"), `{"id":"previous","destination":false}`)
		}, "relocation"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t)
			test.prepare(t, f)
			result, code := f.install(t, "--source", f.source, "--skills", "codex", "--write")
			if code != 1 || !strings.Contains(result.Message, test.message) {
				t.Fatalf("%+v", result)
			}
		})
	}
}

func TestInstallSettingsReply(t *testing.T) {
	for _, reply := range []string{`not-json`, `{"selection":{}}`, `{"selection":{"state_dir":"/one","config":"/one/config.json"},"saved_selection":{"state_dir":"/two","config":"/two/config.json"}}`} {
		t.Run(reply, func(t *testing.T) {
			f := newFixture(t)
			run := f.installer.run
			f.installer.run = func(binary string, args ...string) ([]byte, error) {
				if args[0] == "settings" {
					return []byte(reply), nil
				}
				return run(binary, args...)
			}
			result, code := f.install(t, "--source", f.source, "--write")
			if code != 1 {
				t.Fatalf("%+v", result)
			}
			if _, err := os.Stat(f.binary); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("published before valid settings")
			}
		})
	}
}

func TestRunEntry(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", t.TempDir())
	}
	if code := Run([]string{"--help"}, os.Stdout); code != 0 {
		t.Fatal(code)
	}
}

func TestInstallBackupRefusesChangedSource(t *testing.T) {
	f := newFixture(t)
	source := filepath.Join(f.home, "source")
	write(t, source, "original")
	before, err := os.Lstat(source)
	if err != nil {
		t.Fatal(err)
	}
	file, err := openExisting(source)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	write(t, source, "changed source length")
	if unchangedSource(source, file, before) == nil {
		t.Fatal("changed bytes accepted")
	}
	before, err = os.Lstat(source)
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(f.home, "other")
	write(t, other, "other")
	if err = os.Rename(source, source+".old"); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(other, source); err != nil {
		t.Fatal(err)
	}
	if unchangedSource(source, file, before) == nil {
		t.Fatal("replacement symlink accepted")
	}
	if err = backupFile(source, filepath.Join(f.home, "backup")); err == nil {
		t.Fatal("backup followed symlink")
	}
	if opened, err := openExisting(source); err == nil {
		opened.Close()
		if runtime.GOOS != "windows" {
			t.Fatal("opened symlink")
		}
	}
	if _, err = os.Stat(filepath.Join(f.home, "backup")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unsafe backup written")
	}
}
