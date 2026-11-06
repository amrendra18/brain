//go:build !windows

package main

import (
	"os/exec"
	"testing"
)

func windowsShell(t *testing.T, shell, line string) *exec.Cmd {
	t.Helper()
	t.Fatal("Windows shell requested on Unix")
	return nil
}
