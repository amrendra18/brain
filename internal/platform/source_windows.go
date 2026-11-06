package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

var ntCreateFile = syscall.NewLazyDLL("ntdll.dll").NewProc("NtCreateFile")

type sourceUnicodeString struct {
	Length, MaximumLength uint16
	Buffer                *uint16
}
type sourceObjectAttributes struct {
	Length                   uint32
	RootDirectory            syscall.Handle
	ObjectName               *sourceUnicodeString
	Attributes               uint32
	SecurityDescriptor       uintptr
	SecurityQualityOfService uintptr
}

func openSource(path string) (*os.File, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	native := `\??\` + absolute
	if strings.HasPrefix(absolute, `\\`) {
		if strings.HasPrefix(absolute, `\\?\`) || strings.HasPrefix(absolute, `\\.\`) {
			return nil, os.ErrInvalid
		}
		native = `\??\UNC\` + strings.TrimPrefix(absolute, `\\`)
	}
	units, err := syscall.UTF16FromString(native)
	if err != nil || len(units) > 32767 {
		return nil, os.ErrInvalid
	}
	name := sourceUnicodeString{uint16((len(units) - 1) * 2), uint16(len(units) * 2), &units[0]}
	// OBJ_DONT_REPARSE rejects every reparse point during path resolution.
	attributes := sourceObjectAttributes{ObjectName: &name, Attributes: 0x1000 | 0x40}
	attributes.Length = uint32(unsafe.Sizeof(attributes))
	var handle syscall.Handle
	var statusBlock [2]uintptr
	// FILE_GENERIC_READ; share read/write/delete; FILE_OPEN; synchronous, non-directory.
	status, _, _ := ntCreateFile.Call(uintptr(unsafe.Pointer(&handle)), 0x120089,
		uintptr(unsafe.Pointer(&attributes)), uintptr(unsafe.Pointer(&statusBlock)), 0,
		syscall.FILE_ATTRIBUTE_NORMAL, 7, 1, 0x20|0x40, 0, 0)
	runtime.KeepAlive(units)
	if int32(status) < 0 {
		return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrPermission}
	}
	return os.NewFile(uintptr(handle), path), nil
}
