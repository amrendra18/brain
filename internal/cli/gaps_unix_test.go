//go:build !windows

package cli

import (
	"brain/tests/testutil"
	"testing"
)

func gapFolder(t *testing.T, path string) func() {
	t.Helper()
	return testutil.UnavailableSource(t, path)
}
