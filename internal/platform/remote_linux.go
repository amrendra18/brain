package platform

import "syscall"

func remoteFilesystem(folder string) (bool, bool) {
	var info syscall.Statfs_t
	if syscall.Statfs(folder, &info) != nil {
		return false, false
	}
	switch uint64(info.Type) {
	case 0x6969, 0x517b, 0xff534d42, 0x1021997:
		return true, true
	case 0xef53, 0x9123683e, 0x58465342, 0x01021994, 0x794c7630, 0x2fc12fc1, 0x52654973:
		return false, true
	default:
		return false, false
	}
}
