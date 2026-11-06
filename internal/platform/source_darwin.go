package platform

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func openSource(path string) (*os.File, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	// These fixed macOS system aliases are trusted. Never resolve source links.
	for _, alias := range []string{"/tmp", "/var", "/etc"} {
		if strings.HasPrefix(path, alias+"/") {
			path = "/private" + path
			break
		}
	}
	// Darwin fcntl.h: O_NOFOLLOW_ANY refuses links in every path component.
	const noFollowAny = 0x20000000
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|noFollowAny, 0)
}
