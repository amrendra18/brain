package platform

import (
	"os"
	"syscall"
)

// Windows access uses inherited ACLs; Go permission bits cannot prove ACL privacy.
func privateOwner(os.FileInfo) bool { return true }
func privateMode(os.FileInfo) bool  { return true }
func singleLink(file *os.File, _ os.FileInfo) bool {
	var info syscall.ByHandleFileInformation
	return syscall.GetFileInformationByHandle(syscall.Handle(file.Fd()), &info) == nil && info.NumberOfLinks == 1
}
