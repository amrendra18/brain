package platform

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func openSource(path string) (*os.File, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	// O_PATH holds a directory without requiring permission to list its names.
	const pathOnly = 0x200000
	parent, err := syscall.Open("/", pathOnly|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i, part := range parts {
		flags := syscall.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK | syscall.O_CLOEXEC
		if i < len(parts)-1 {
			flags = pathOnly | syscall.O_NOFOLLOW | syscall.O_DIRECTORY | syscall.O_CLOEXEC
		}
		next, problem := syscall.Openat(parent, part, flags, 0)
		syscall.Close(parent)
		if problem != nil {
			return nil, &os.PathError{Op: "open", Path: path, Err: problem}
		}
		parent = next
	}
	return os.NewFile(uintptr(parent), path), nil
}
