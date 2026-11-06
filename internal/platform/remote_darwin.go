package platform

import "syscall"

func remoteFilesystem(folder string) (bool, bool) {
	var info syscall.Statfs_t
	if syscall.Statfs(folder, &info) != nil {
		return false, false
	}
	name := make([]byte, 0, len(info.Fstypename))
	for _, value := range info.Fstypename {
		if value == 0 {
			break
		}
		name = append(name, byte(value))
	}
	switch string(name) {
	case "smbfs", "nfs", "afpfs", "webdav":
		return true, true
	case "apfs", "hfs", "msdos", "exfat", "cd9660", "udf":
		return false, true
	default:
		return false, false
	}
}
