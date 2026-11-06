package platform

import (
	"path/filepath"
	"syscall"
	"unsafe"
)

var getDriveType = kernel32.NewProc("GetDriveTypeW")

const driveRemovable = 2
const driveFixed = 3
const driveRemote = 4
const driveCDROM = 5
const driveRAMDisk = 6

func remoteFilesystem(folder string) (bool, bool) {
	root, err := syscall.UTF16PtrFromString(filepath.VolumeName(folder) + `\`)
	if err != nil {
		return false, false
	}
	typeOfDrive, _, _ := getDriveType.Call(uintptr(unsafe.Pointer(root)))
	switch typeOfDrive {
	case driveRemote:
		return true, true
	case driveFixed, driveRemovable, driveRAMDisk, driveCDROM:
		return false, true
	default:
		return false, false
	}
}
