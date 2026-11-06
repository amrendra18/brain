//go:build !windows

package platform

import (
	"errors"
	"os"
	"syscall"
)

func tryLock(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}
func tryPrivacyLock(file *os.File, exclusive bool) error {
	mode := syscall.LOCK_SH
	if exclusive {
		mode = syscall.LOCK_EX
	}
	return syscall.Flock(int(file.Fd()), mode|syscall.LOCK_NB)
}
func lockBusy(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
}
func replace(root *os.Root, old, new string) error { return root.Rename(old, new) }

const readAccess = 4
const searchAccess = 1

func ReadableDir(path string) bool { return syscall.Access(path, readAccess|searchAccess) == nil }
func syncRootFolder(root *os.Root, folder string) error {
	file, err := root.Open(folder)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}
