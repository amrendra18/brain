package installrun

import (
	"os"
	"syscall"
	"unsafe"
)

func owned(os.FileInfo) bool { return true }
func singleLink(file *os.File, _ os.FileInfo) bool {
	var info syscall.ByHandleFileInformation
	return syscall.GetFileInformationByHandle(syscall.Handle(file.Fd()), &info) == nil && info.NumberOfLinks == 1
}
func replaceFile(old, new string) error {
	from, err := syscall.UTF16PtrFromString(old)
	if err != nil {
		return err
	}
	to, err := syscall.UTF16PtrFromString(new)
	if err != nil {
		return err
	}
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileExW")
	result, _, err := proc.Call(uintptr(unsafe.Pointer(from)), uintptr(unsafe.Pointer(to)), 0x1|0x8)
	if result == 0 {
		return err
	}
	return nil
}

func syncDirectory(string) error { return nil }

func openExisting(path string) (*os.File, error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE, nil, syscall.OPEN_EXISTING, 0x00200000, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(handle), path), nil
}
