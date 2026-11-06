package index

import (
	"brain/internal/platform"
	"errors"
	"os"
	"path/filepath"
)

var sqliteFiles = []string{"index.sqlite", "index.sqlite-wal", "index.sqlite-shm", "index.sqlite-journal"}

func checkDatabase(state string) error {
	for _, name := range sqliteFiles {
		if err := platform.CheckPrivateFile(state, name); err != nil {
			return err
		}
	}
	return nil
}

func prepareDatabase(state string, create, rebuild bool, held ...bool) (*platform.Lock, error) {
	needsLock := rebuild
	for _, name := range sqliteFiles {
		info, err := os.Lstat(filepath.Join(state, name))
		if err == nil && info.Mode().Perm()&0077 != 0 || errors.Is(err, os.ErrNotExist) && name == "index.sqlite" && create {
			needsLock = true
		}
	}
	var lock *platform.Lock
	if needsLock && (len(held) == 0 || !held[0]) {
		var err error
		lock, err = platform.Acquire(state, "index", true)
		if err != nil {
			return nil, err
		}
	}
	for _, name := range sqliteFiles {
		if err := platform.PreparePrivateFile(state, name, name == "index.sqlite" && create); err != nil {
			if lock != nil {
				lock.Close()
			}
			return nil, err
		}
	}
	return lock, nil
}
