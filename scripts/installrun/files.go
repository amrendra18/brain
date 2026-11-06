package installrun

import (
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"

	"brain/internal/platform"
)

func safeDirectory(path string, create bool) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err = safeDirectory(filepath.Dir(path), false); err != nil {
			return err
		}
		if !create {
			return nil
		}
		if err = os.MkdirAll(path, 0700); err != nil {
			return err
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || !owned(info) || info.Mode().Perm()&0022 != 0 {
		return errors.New("installation folder has unsafe type, owner or permissions")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	if filepath.Clean(resolved) != filepath.Clean(path) {
		return errors.New("installation folders cannot use symbolic links")
	}
	return nil
}

func safeTarget(path string) error {
	if err := safeDirectory(filepath.Dir(path), false); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || !owned(info) || info.Mode().Perm()&0022 != 0 {
		return errors.New("installation target has unsafe type, owner or permissions")
	}
	file, err := openExisting(path)
	if err != nil {
		return err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(info, opened) || !singleLink(file, opened) {
		return errors.New("installation target changed or has multiple links")
	}
	return nil
}

func backupFile(source, destination string) error {
	if err := safeTarget(source); err != nil {
		return err
	}
	before, err := os.Lstat(source)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	file, err := openExisting(source)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	if err = unchangedSource(source, file, before); err != nil {
		return err
	}

	target, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	digest := sha256.New()
	_, err = io.Copy(io.MultiWriter(target, digest), file)
	if err == nil {
		err = unchangedSource(source, file, before)
	}
	if err == nil {
		err = target.Sync()
	}
	closeErr := target.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	copied, err := os.ReadFile(destination)
	if err != nil {
		return err
	}
	if string(digest.Sum(nil)) != stringHash(copied) {
		return errors.New("backup verification failed")
	}
	return syncDirectory(filepath.Dir(destination))
}

func stringHash(data []byte) string { sum := sha256.Sum256(data); return string(sum[:]) }

func replaceBinary(candidate, destination string) error {
	if err := safeTarget(destination); err != nil {
		return err
	}
	data, err := os.ReadFile(candidate)
	if err != nil {
		return err
	}
	folder := filepath.Dir(destination)
	file, err := os.CreateTemp(folder, ".brain-install-")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.Write(data); err == nil {
		err = file.Chmod(0700)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = safeTarget(destination); err != nil {
		return err
	}
	if err = replaceFile(name, destination); err != nil {
		return err
	}
	return syncDirectory(folder)
}

func publicationLease(state string) (*platform.Lock, error) {
	return platform.StateLease(state, true, true)
}

func unchangedSource(path string, file *os.File, before os.FileInfo) error {
	opened, err := file.Stat()
	if err != nil {
		return err
	}
	current, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !current.Mode().IsRegular() || !opened.Mode().IsRegular() || !owned(current) || !owned(opened) || !singleLink(file, opened) || !os.SameFile(before, opened) || !os.SameFile(opened, current) || opened.Size() != before.Size() || opened.ModTime() != before.ModTime() || current.Size() != opened.Size() || current.ModTime() != opened.ModTime() {
		return errors.New("backup source changed or is unsafe")
	}
	return nil
}
