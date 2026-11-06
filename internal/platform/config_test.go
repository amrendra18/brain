package platform

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestConfigAtomicWrite(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "config.json")
	first := bytes.Repeat([]byte("a"), 100000)
	second := bytes.Repeat([]byte("b"), 100000)
	if err := WriteConfig(path, first); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	stop, finished := make(chan struct{}), make(chan error, 1)
	go func() {
		for {
			select {
			case <-stop:
				finished <- nil
				return
			default:
			}
			data, err := ReadConfig(path)
			if errors.Is(err, os.ErrPermission) {
				continue
			}
			if err != nil {
				finished <- err
				return
			}
			if !bytes.Equal(data, first) && !bytes.Equal(data, second) {
				finished <- errors.New("partial config visible")
				return
			}
		}
	}()
	for i := 0; i < 30; i++ {
		data := first
		if i%2 == 0 {
			data = second
		}
		if err := WriteConfig(path, data); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	parent, _ := os.Stat(root)
	if runtime.GOOS != "windows" && parent.Mode().Perm() != 0755 {
		t.Fatal("changed existing parent", parent.Mode())
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 1 {
		t.Fatal("temporary files leaked", entries)
	}
}

func TestConfigSafeFiles(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "directory", "unreadable"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "config.json")
			target := filepath.Join(root, "target")
			os.WriteFile(target, []byte("keep"), 0600)
			switch kind {
			case "symlink":
				if err := os.Symlink(target, path); err != nil {
					t.Skip(err)
				}
			case "hardlink":
				if err := os.Link(target, path); err != nil {
					t.Skip(err)
				}
			case "directory":
				os.Mkdir(path, 0700)
			case "unreadable":
				os.WriteFile(path, []byte("private"), 0200)
			}
			if _, err := ReadConfig(path); err == nil {
				t.Fatal("unsafe read accepted")
			}
			if err := WriteConfig(path, []byte("changed")); err == nil {
				t.Fatal("unsafe write accepted")
			}
			data, _ := os.ReadFile(target)
			if string(data) != "keep" {
				t.Fatal("changed target")
			}
		})
	}
	root := t.TempDir()
	missing := filepath.Join(root, "missing", "config.json")
	if err := WriteConfig(missing, []byte("x")); err == nil {
		t.Fatal("created parent")
	}
	if _, err := AcquireConfig(missing, false); err == nil {
		t.Fatal("created lock parent")
	}
	if _, err := ReadConfig(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	path := filepath.Join(root, "config.json")
	os.WriteFile(path, []byte("keep"), 0600)
	if runtime.GOOS != "windows" {
		os.Chmod(root, 0500)
		defer os.Chmod(root, 0700)
		if err := WriteConfig(path, []byte("changed")); err == nil {
			t.Fatal("unwritable parent accepted")
		}
	}
}

func TestConfigLockSafety(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.json")
	lock, err := AcquireConfig(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireConfig(path, false); !errors.Is(err, ErrLocked) {
		t.Fatal(err)
	}
	info, _ := os.Stat(path + ".lock")
	lock.Close()
	next, err := AcquireConfig(path, false)
	if err != nil {
		t.Fatal(err)
	}
	next.Close()
	after, _ := os.Stat(path + ".lock")
	if !os.SameFile(info, after) {
		t.Fatal("lock inode changed")
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(root, alias); err == nil {
		lock, err := AcquireConfig(filepath.Join(alias, "config.json"), false)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := AcquireConfig(path, false); !errors.Is(err, ErrLocked) {
			t.Fatal("alias bypass", err)
		}
		lock.Close()
	}
	os.Remove(path + ".lock")
	os.Mkdir(path+".lock", 0700)
	if _, err := AcquireConfig(path, false); err == nil {
		t.Fatal("directory lock accepted")
	}
	os.Remove(path + ".lock")
	target := filepath.Join(root, "target")
	os.WriteFile(target, []byte("private"), 0600)
	if err := os.Link(target, path+".lock"); err == nil {
		if _, err := AcquireConfig(path, false); err == nil {
			t.Fatal("hardlinked lock accepted")
		}
		os.Remove(path + ".lock")
	}
	if runtime.GOOS != "windows" {
		os.WriteFile(path+".lock", []byte(""), 0644)
		if _, err := AcquireConfig(path, false); err == nil {
			t.Fatal("public lock accepted")
		}
	}
}

func TestConfigInterruptedWriter(t *testing.T) {
	if path := os.Getenv("BRAIN_CONFIG_WRITER_TEST"); path != "" {
		for {
			if err := WriteConfig(path, bytes.Repeat([]byte("b"), 100000)); err != nil {
				t.Fatal(err)
			}
		}
	}
	root := t.TempDir()
	path := filepath.Join(root, "config.json")
	old := bytes.Repeat([]byte("a"), 100000)
	if err := WriteConfig(path, old); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestConfigInterruptedWriter$")
	cmd.Env = append(os.Environ(), "BRAIN_CONFIG_WRITER_TEST="+path)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, err := os.ReadFile(path)
		if err == nil && bytes.Equal(data, bytes.Repeat([]byte("b"), 100000)) {
			break
		}
		if time.Now().After(deadline) {
			cmd.Process.Kill()
			cmd.Wait()
			t.Fatal("writer did not run")
		}
		time.Sleep(time.Millisecond)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	cmd.Wait()
	data, err := ReadConfig(path)
	if err != nil || !bytes.Equal(data, bytes.Repeat([]byte("b"), 100000)) {
		t.Fatal("interrupted partial config", len(data), err)
	}
}
