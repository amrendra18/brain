package platform

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
)

var ErrFileChanged = errors.New("the destination changed while preparing the write")

// WritePrivateFileIfUnchanged creates absent files atomically and updates existing files through their verified descriptors.
func WritePrivateFileIfUnchanged(folder, name string, data []byte, before os.FileInfo, previousSHA256 string) error {
	if err := SafeStatePath(folder, name); err != nil {
		return err
	}
	root, err := os.OpenRoot(folder)
	if err != nil {
		return err
	}
	defer root.Close()
	if before != nil {
		file, err := openPrivateUpdate(root, name, before, previousSHA256)
		if err != nil {
			return err
		}
		defer file.Close()
		return writePrivateInPlace(root, name, file, before, data)
	}
	temporary := filepath.Join(filepath.Dir(name), ".brain-"+rand.Text()+".tmp")
	file, err := root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(temporary)
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	closed := file.Close()
	if err != nil {
		return err
	}
	if closed != nil {
		return closed
	}
	if err = root.Link(temporary, name); errors.Is(err, os.ErrExist) {
		return ErrFileChanged
	} else if err != nil {
		return err
	}
	if err = root.Remove(temporary); err != nil {
		return err
	}
	return syncRootFolder(root, filepath.Dir(name))
}

func openPrivateUpdate(root *os.Root, name string, before os.FileInfo, previousSHA256 string) (*os.File, error) {
	if !before.Mode().IsRegular() || !privateOwner(before) || !privateMode(before) {
		return nil, ErrStateUnsafe
	}
	flags := os.O_RDWR
	if runtime.GOOS != "windows" {
		flags |= syscall.O_NONBLOCK
	}
	file, err := root.OpenFile(name, flags, 0)
	if err != nil {
		return nil, ErrFileChanged
	}
	fail := func() (*os.File, error) { file.Close(); return nil, ErrFileChanged }
	if err = checkOpenedPrivateFile(root, name, before, file, false); err != nil {
		return fail()
	}
	info, err := file.Stat()
	if err != nil || !privateMode(info) {
		return fail()
	}
	current, err := readBounded(file, before.Size())
	if err != nil {
		return fail()
	}
	hash := sha256.Sum256(current)
	if hex.EncodeToString(hash[:]) != previousSHA256 {
		return fail()
	}
	return file, nil
}

func writePrivateInPlace(root *os.Root, name string, file *os.File, before os.FileInfo, data []byte) error {
	_, writeErr := file.WriteAt(data, 0)
	if writeErr == nil {
		writeErr = file.Truncate(int64(len(data)))
	}
	if writeErr == nil {
		writeErr = file.Sync()
	}
	after, err := root.Lstat(name)
	if err != nil || !os.SameFile(before, after) {
		return ErrFileChanged
	}
	return writeErr
}
