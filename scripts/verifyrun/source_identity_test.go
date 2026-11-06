package verifyrun

import (
	"strings"
	"testing"
)

func TestSourceTreeNamesGitCheckout(t *testing.T) {
	_, err := sourceTree(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "needs a Git checkout") {
		t.Fatal("a folder without Git gave no Git guidance", err)
	}
}
