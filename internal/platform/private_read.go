package platform

import (
	"os"
	"runtime"
	"syscall"
)

// ReadPrivateFile reads a private leaf without changing its parent directory's permissions.
func ReadPrivateFile(folder, name string, limit int64) ([]byte, error) {
	if limit < 0 {
		return nil, ErrSourceOversize
	}
	root, err := os.OpenRoot(folder)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err = safePath(root, name); err != nil {
		return nil, err
	}
	before, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || !privateOwner(before) || !privateMode(before) || before.Mode().Perm()&0444 == 0 {
		return nil, ErrStateUnsafe
	}
	flags := os.O_RDONLY
	if runtime.GOOS != "windows" {
		flags |= syscall.O_NONBLOCK
	}
	file, err := root.OpenFile(name, flags, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if err = checkOpenedPrivateFile(root, name, before, file, false); err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !privateMode(info) || info.Mode().Perm()&0444 == 0 {
		return nil, ErrStateUnsafe
	}
	if info.Size() > limit {
		return nil, ErrSourceOversize
	}
	return readBounded(file, info.Size())
}
