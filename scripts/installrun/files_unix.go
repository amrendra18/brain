//go:build !windows

package installrun

import (
	"os"
	"syscall"
)

func owned(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid())
}
func singleLink(_ *os.File, info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Nlink == 1
}
func replaceFile(old, new string) error { return os.Rename(old, new) }

func syncDirectory(path string) error {
	folder, err := os.Open(path)
	if err != nil {
		return err
	}
	defer folder.Close()
	return folder.Sync()
}

func openExisting(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}
