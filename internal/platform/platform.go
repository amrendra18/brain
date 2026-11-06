package platform

import (
	"crypto/rand"
	"errors"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

var ErrLocked = errors.New("another process holds the state lock")

func StateFolder(system, home string, environment map[string]string, override string) (string, error) {
	if override == "" {
		override = environment["BRAIN_HOME"]
	}
	absolute := func(value string) bool {
		if system == "windows" {
			return strings.HasPrefix(value, `\\`) || len(value) > 2 && value[1] == ':' && (value[2] == '\\' || value[2] == '/')
		}
		return strings.HasPrefix(value, "/")
	}
	if override != "" {
		if !absolute(override) {
			return "", errors.New("the state folder must be an absolute path")
		}
		return override, nil
	}
	join := func(parts ...string) string {
		if system == "windows" {
			return strings.ReplaceAll(path.Join(strings.ReplaceAll(parts[0], `\`, "/"), strings.Join(parts[1:], "/")), "/", `\`)
		}
		return path.Join(parts...)
	}
	if home == "" {
		return "", errors.New("the home folder is unavailable; use --state-dir")
	}
	switch system {
	case "darwin":
		return join(home, "Library", "Application Support", "Brain"), nil
	case "windows":
		local := environment["LOCALAPPDATA"]
		if local != "" && absolute(local) {
			return join(local, "Brain"), nil
		}
		return join(home, "AppData", "Local", "Brain"), nil
	default:
		data := environment["XDG_DATA_HOME"]
		if data != "" && absolute(data) {
			return join(data, "brain"), nil
		}
		return join(home, ".local", "share", "brain"), nil
	}
}

func CurrentState(override string) (string, error) {
	home, _ := os.UserHomeDir()
	environment := map[string]string{}
	for _, name := range []string{"BRAIN_HOME", "LOCALAPPDATA", "XDG_DATA_HOME"} {
		environment[name] = os.Getenv(name)
	}
	return StateFolder(runtime.GOOS, home, environment, override)
}

// SafeStatePath rejects symlinks within state before reading or changing a path.
func SafeStatePath(state, name string) error {
	root, err := os.OpenRoot(state)
	if err != nil {
		return err
	}
	defer root.Close()
	return safePath(root, name)
}

func ReadState(state, name string) ([]byte, error) {
	return readState(state, name, -1)
}

// ReadStateAtMost keeps a state read inside its root and refuses larger files.
func ReadStateAtMost(state, name string, limit int64) ([]byte, error) {
	if limit < 0 {
		return nil, ErrSourceOversize
	}
	return readState(state, name, limit)
}

func readState(state, name string, limit int64) ([]byte, error) {
	root, err := os.OpenRoot(state)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err = safePath(root, name); err != nil {
		return nil, err
	}
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0444 == 0 {
		return nil, os.ErrPermission
	}
	// Do not wait for a writer if a pipe replaces the checked file.
	flags := os.O_RDONLY
	if runtime.GOOS != "windows" {
		flags |= syscall.O_NONBLOCK
	}
	file, err := root.OpenFile(name, flags, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !opened.Mode().IsRegular() || opened.Mode().Perm()&0444 == 0 || !os.SameFile(info, opened) {
		return nil, os.ErrPermission
	}
	if limit >= 0 && opened.Size() > limit {
		return nil, ErrSourceOversize
	}
	if err = safePath(root, name); err != nil {
		return nil, err
	}
	return readBounded(file, opened.Size())
}

func WritableState(state, name string) bool {
	root, err := os.OpenRoot(state)
	if err != nil {
		return false
	}
	defer root.Close()
	if safePath(root, name) != nil {
		return false
	}
	info, err := root.Stat(name)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0222 == 0 {
		return false
	}
	probe := filepath.Join(name, ".brain-write-"+rand.Text())
	file, err := root.OpenFile(probe, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return false
	}
	file.Close()
	return root.Remove(probe) == nil
}

func safePath(root *os.Root, name string) error {
	if !filepath.IsLocal(name) {
		return os.ErrPermission
	}
	parts := strings.Split(filepath.Clean(name), string(filepath.Separator))
	for i := range parts {
		info, err := root.Lstat(filepath.Join(parts[:i+1]...))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return os.ErrPermission
		}
	}
	return nil
}

// WriteState confines the temporary file and rename to one state folder.
func WriteState(state, destination string, data []byte) error {
	root, err := os.OpenRoot(state)
	if err != nil {
		return err
	}
	defer root.Close()
	if err = safePath(root, destination); err != nil {
		return err
	}
	folder := filepath.Dir(destination)
	name := filepath.Join(folder, ".brain-"+rand.Text()+".tmp")
	temporary, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(name)
	_, err = temporary.Write(data)
	if err == nil {
		err = temporary.Sync()
	}
	closed := temporary.Close()
	if err != nil {
		return err
	}
	if closed != nil {
		return closed
	}
	if err = replace(root, name, destination); err != nil {
		return err
	}
	return syncRootFolder(root, folder)
}

type Lock struct{ file *os.File }

// ReadLock takes an existing index lock without creating or writing any file.
func ReadLock(state string) (*Lock, error) {
	name := filepath.Join("locks", "index.lock")
	if err := SafeStatePath(state, name); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(state)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := openRegularFile(root, name, false)
	if err != nil {
		return nil, err
	}
	if err = tryLock(file); err != nil {
		file.Close()
		if lockBusy(err) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return &Lock{file}, nil
}

func Acquire(state, name string, wait bool) (*Lock, error) {
	root, err := os.OpenRoot(state)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	folder := "locks"
	destination := filepath.Join(folder, name+".lock")
	if err = safePath(root, destination); err != nil {
		return nil, err
	}
	if err := root.MkdirAll(folder, 0700); err != nil {
		return nil, err
	}
	file, err := openRegularFile(root, destination, true)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		err = tryLock(file)
		if err == nil {
			return &Lock{file}, nil
		}
		if !lockBusy(err) {
			file.Close()
			return nil, err
		}
		if !wait || !time.Now().Before(deadline) {
			file.Close()
			return nil, ErrLocked
		}
		time.Sleep(10 * time.Millisecond)
	}
}
func (lock *Lock) Close() error { return lock.file.Close() }

func Writable(folder string) bool {
	info, err := os.Stat(folder)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0222 == 0 {
		return false
	}
	file, err := os.CreateTemp(folder, ".brain-write-*")
	if err != nil {
		return false
	}
	name := file.Name()
	file.Close()
	return os.Remove(name) == nil
}
