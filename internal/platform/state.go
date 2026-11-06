package platform

import (
	"errors"
	"os"
	"path/filepath"
)

var ErrStateUnsafe = errors.New("the state folder is not private")

func CheckPrivateState(state string) error {
	state = filepath.Clean(state)
	info, err := os.Lstat(state)
	if err != nil {
		return err
	}
	if !info.IsDir() || !privateOwner(info) || !privateMode(info) {
		return ErrStateUnsafe
	}
	root, err := os.OpenRoot(state)
	if err != nil {
		return err
	}
	defer root.Close()
	opened, err := root.Stat(".")
	if err != nil {
		return err
	}
	if !os.SameFile(info, opened) {
		return ErrStateUnsafe
	}
	file, err := root.Open(".")
	if err != nil {
		return err
	}
	defer file.Close()
	return privateACL(file)
}

func PrepareState(state string) error {
	err := CheckPrivateState(state)
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err = os.MkdirAll(state, 0700); err != nil {
		return err
	}
	return CheckPrivateState(state)
}

// Read-only SQLite connections can still write shared-memory files.
func CheckPrivateFile(state, name string) error {
	return privateFile(state, name, false, false)
}

func PreparePrivateFile(state, name string, create bool) error {
	return privateFile(state, name, create, true)
}

func privateFile(state, name string, create, writable bool) error {
	if err := CheckPrivateState(state); err != nil {
		return err
	}
	root, err := os.OpenRoot(state)
	if err != nil {
		return err
	}
	defer root.Close()
	for attempt := 0; attempt < 8; attempt++ {
		err = privateFileAt(root, name, create, writable)
		if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, os.ErrExist) && !errors.Is(err, ErrLocked) {
			return err
		}
	}
	return ErrLocked
}

func privateFileAt(root *os.Root, name string, create, writable bool) error {
	if err := safePath(root, name); err != nil {
		return err
	}
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		if !create {
			return nil
		}
		file, problem := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if problem != nil {
			return problem
		}
		return file.Close()
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || !privateOwner(info) {
		return ErrStateUnsafe
	}
	file, err := root.Open(name)
	if err != nil {
		return err
	}
	defer file.Close()
	return checkOpenedPrivateFile(root, name, info, file, writable)
}

func checkOpenedPrivateFile(root *os.Root, name string, before os.FileInfo, file *os.File, writable bool) error {
	opened, err := file.Stat()
	if err != nil {
		return err
	}
	current, err := root.Lstat(name)
	if err != nil {
		return err
	}
	if !current.Mode().IsRegular() || !privateOwner(current) || !privateOwner(opened) {
		return ErrStateUnsafe
	}
	// SQLite removes sidecars on close. Recheck any new pathname before chmod.
	if !os.SameFile(before, opened) || !os.SameFile(opened, current) {
		return ErrLocked
	}
	if err := privateACL(file); err != nil {
		return err
	}
	if !singleLink(file, opened) {
		return ErrStateUnsafe
	}
	if writable && opened.Mode().Perm()&0077 != 0 {
		return file.Chmod(opened.Mode().Perm() & 0700)
	}
	return nil
}
