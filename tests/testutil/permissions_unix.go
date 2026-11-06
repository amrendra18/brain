//go:build !windows

package testutil

import (
	"os"
	"testing"
)

// Permissions changes only an owned test fixture's access.
func Permissions(t *testing.T, path string, mode os.FileMode) error {
	t.Helper()
	return os.Chmod(path, mode)
}
