package platform

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

// ConfigPath resolves parent aliases without following the replaceable leaf.
func ConfigPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(parent)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", os.ErrPermission
	}
	return filepath.Join(parent, filepath.Base(absolute)), nil
}

// openRegularFile checks file identity and refuses special files without waiting.
func openRegularFile(root *os.Root, name string, create bool) (*os.File, error) {
	before, err := root.Lstat(name)
	if err != nil && !(create && errors.Is(err, os.ErrNotExist)) {
		return nil, err
	}
	if err == nil && (!before.Mode().IsRegular() || !privateOwner(before)) {
		return nil, os.ErrPermission
	}
	flags := os.O_RDONLY
	if create {
		flags = os.O_CREATE | os.O_RDWR
	}
	if runtime.GOOS != "windows" {
		flags |= syscall.O_NONBLOCK
	}
	file, err := root.OpenFile(name, flags, 0600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	after, later := root.Lstat(name)
	if err != nil || later != nil || !info.Mode().IsRegular() || !privateOwner(info) || !after.Mode().IsRegular() || info.Mode().Perm()&0444 == 0 {
		file.Close()
		return nil, os.ErrPermission
	}
	if !os.SameFile(info, after) || before != nil && !os.SameFile(before, info) {
		file.Close()
		return nil, ErrLocked
	}
	if !singleLink(file, info) {
		file.Close()
		return nil, os.ErrPermission
	}
	return file, nil
}

func ReadConfig(path string) ([]byte, error) {
	path, err := ConfigPath(path)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	var file *os.File
	for attempt := 0; attempt < 8; attempt++ {
		file, err = openRegularFile(root, filepath.Base(path), false)
		if !errors.Is(err, ErrLocked) {
			break
		}
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	return readBounded(file, info.Size())
}

func AcquireConfig(path string, wait bool) (*Lock, error) {
	path, err := ConfigPath(path)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := openRegularFile(root, filepath.Base(path)+".lock", true)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !privateMode(info) {
		file.Close()
		return nil, os.ErrPermission
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		err = tryLock(file)
		if err == nil {
			current, problem := root.Lstat(filepath.Base(path) + ".lock")
			if problem != nil || !current.Mode().IsRegular() || !os.SameFile(info, current) {
				file.Close()
				return nil, ErrLocked
			}
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

func WriteConfig(path string, data []byte) error {
	path, err := ConfigPath(path)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer root.Close()
	file, err := openRegularFile(root, filepath.Base(path), false)
	if err == nil {
		file.Close()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	info, err := root.Stat(".")
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0222 == 0 {
		return os.ErrPermission
	}
	return WriteState(filepath.Dir(path), filepath.Base(path), data)
}
