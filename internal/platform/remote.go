package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// NetworkPath recognizes remote path forms without opening a remote folder.
func NetworkPath(system, folder string) bool {
	name := strings.ToLower(strings.ReplaceAll(folder, `\`, "/"))
	if strings.HasPrefix(name, "//") || strings.HasPrefix(name, "smb:") || strings.HasPrefix(name, "nfs:") {
		return true
	}
	if system == "darwin" {
		return name == "/network" || strings.HasPrefix(name, "/network/")
	}
	if system == "linux" {
		return name == "/net" || strings.HasPrefix(name, "/net/")
	}
	return false
}

// RemoteFolder checks the nearest existing parent, including mounted drives.
func RemoteFolder(folder string) (remote, known bool) {
	if NetworkPath(runtime.GOOS, folder) {
		return true, true
	}
	for {
		if _, err := os.Stat(folder); err == nil {
			return remoteFilesystem(folder)
		} else if !os.IsNotExist(err) {
			return false, false
		}
		parent := filepath.Dir(folder)
		if parent == folder {
			return false, false
		}
		folder = parent
	}
}
