package toolchain

import (
	"os"
	"path/filepath"
	"runtime"
)

// Binary returns the compiler selected by scripts/go-env. Direct Go tests use
// the compiler that built them when no wrapper supplied a selection.
func Binary() string {
	if selected := os.Getenv("BRAIN_GO_BIN"); selected != "" {
		return selected
	}
	name := "go"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(runtime.GOROOT(), "bin", name)
}
