//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func windowsShell(t *testing.T, shell, line string) *exec.Cmd {
	t.Helper()
	root := os.Getenv("SystemRoot")
	if shell == "cmd" {
		path := filepath.Join(root, "System32", "cmd.exe")
		command := exec.Command(path)
		command.SysProcAttr = &syscall.SysProcAttr{CmdLine: `"` + path + `" /D /V:ON /S /C "` + line + `"`}
		return command
	}
	path := filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	if shell == "powershell7" {
		path = filepath.Join(os.Getenv("ProgramFiles"), "PowerShell", "7", "pwsh.exe")
		if _, err := os.Stat(path); err != nil {
			t.Skip("PowerShell 7 is absent; Windows PowerShell 5 remains required")
		}
	}
	return exec.Command(path, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", line)
}
