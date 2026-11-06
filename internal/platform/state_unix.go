//go:build !windows

package platform

import (
	"os"
	"syscall"
)

func privateOwner(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid())
}
func privateMode(info os.FileInfo) bool { return info.Mode().Perm()&0077 == 0 }
func singleLink(_ *os.File, info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Nlink == 1
}
