//go:build windows

package platform

import (
	"errors"
	"io"
	"os"
	"syscall"
	"time"
	"unsafe"
)

// Pinned Go marks kernel32.dll as a system DLL and loads it only from System32.
var kernel32 = syscall.NewLazyDLL("kernel32.dll")
var lockFileEx = kernel32.NewProc("LockFileEx")

const lockExclusive = 2
const lockFailImmediately = 1
const errorLockViolation = syscall.Errno(33)
const errorSharingViolation = syscall.Errno(32)

func tryLock(file *os.File) error {
	overlapped := syscall.Overlapped{}
	result, _, err := lockFileEx.Call(file.Fd(), lockExclusive|lockFailImmediately, 0, 1, 0, uintptr(unsafe.Pointer(&overlapped)))
	if result != 0 {
		return nil
	}
	return err
}
func tryPrivacyLock(file *os.File, exclusive bool) error {
	flags := uintptr(lockFailImmediately)
	if exclusive {
		flags |= lockExclusive
	}
	overlapped := syscall.Overlapped{}
	result, _, err := lockFileEx.Call(file.Fd(), flags, 0, 1, 0, uintptr(unsafe.Pointer(&overlapped)))
	if result != 0 {
		return nil
	}
	return err
}
func lockBusy(err error) bool { return errors.Is(err, errorLockViolation) }
func replace(root *os.Root, old, new string) error {
	var err error
	for attempt := 0; attempt < 20; attempt++ {
		err = root.Rename(old, new)
		if err == nil {
			return nil
		}
		if !errors.Is(err, errorSharingViolation) && !errors.Is(err, syscall.ERROR_ACCESS_DENIED) {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
	return err
}
func syncRootFolder(*os.Root, string) error { return nil }

func ReadableDir(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	_, err = file.Readdirnames(1)
	return err == nil || errors.Is(err, io.EOF)
}
