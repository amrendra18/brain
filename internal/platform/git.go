package platform

import (
	"os/exec"
	"path/filepath"
)

// GitPath locates the optional Git executable and returns an absolute path.
func GitPath() string {
	found, err := exec.LookPath("git")
	if err != nil {
		return ""
	}
	found, err = filepath.Abs(found)
	if err != nil {
		return ""
	}
	return found
}
