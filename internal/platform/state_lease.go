package platform

import (
	"os"
	"path/filepath"
	"time"
)

// StateLease orders relocation before command-specific locks.
func StateLease(state string, exclusive, create bool) (*Lock, error) {
	if err := CheckPrivateState(state); err != nil {
		return nil, err
	}
	name := filepath.Join("locks", "mutation.lock")
	if err := SafeStatePath(state, name); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(state)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if create {
		if err = root.MkdirAll("locks", 0700); err != nil {
			return nil, err
		}
		if err = PreparePrivateFile(state, name, true); err != nil {
			return nil, err
		}
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	before, err := root.Lstat(name)
	if err == nil {
		err = checkOpenedPrivateFile(root, name, before, file, false)
	}
	if err != nil {
		file.Close()
		return nil, err
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		err = tryPrivacyLock(file, exclusive)
		if err == nil {
			return &Lock{file}, nil
		}
		if !lockBusy(err) {
			file.Close()
			return nil, err
		}
		if !time.Now().Before(deadline) {
			file.Close()
			return nil, ErrLocked
		}
		time.Sleep(10 * time.Millisecond)
	}
}
