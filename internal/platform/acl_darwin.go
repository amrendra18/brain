package platform

import (
	"encoding/binary"
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

func privateACL(file *os.File) error {
	attributes := struct {
		Count                                 uint16
		Reserved                              uint16
		Common, Volume, Directory, File, Fork uint32
	}{Count: 5, Common: 0x00400000}
	var buffer [4096]byte
	_, _, errno := syscall.Syscall6(syscall.SYS_FGETATTRLIST, file.Fd(), uintptr(unsafe.Pointer(&attributes)), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), 0, 0)
	runtime.KeepAlive(file)
	if errno != 0 {
		return ErrStateUnsafe
	}
	return checkPrivateACL(buffer[:])
}

func checkPrivateACL(buffer []byte) error {
	if len(buffer) < 12 {
		return ErrStateUnsafe
	}
	length := int(binary.LittleEndian.Uint32(buffer))
	offset := 4 + int(int32(binary.LittleEndian.Uint32(buffer[4:])))
	size := int(binary.LittleEndian.Uint32(buffer[8:]))
	if length < 12 || length > len(buffer) || offset < 12 || size < 0 || offset > length-size {
		return ErrStateUnsafe
	}
	if size == 0 {
		return nil
	}
	data := buffer[offset : offset+size]
	if len(data) < 44 || binary.LittleEndian.Uint32(data) != 0x012cc16d {
		return ErrStateUnsafe
	}
	count := binary.LittleEndian.Uint32(data[36:])
	if count == 0xffffffff {
		return nil
	}
	if count > 128 || len(data) < 44+int(count)*24 {
		return ErrStateUnsafe
	}
	for i := 0; i < int(count); i++ {
		flags := binary.LittleEndian.Uint32(data[44+i*24+16:])
		if flags&0xf != 2 {
			return ErrStateUnsafe
		}
	}
	return nil
}
