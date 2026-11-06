//go:build !windows

package verifyrun

import (
	"os/exec"
	"syscall"
)

func setContractProcessGroup(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateContractProcessGroup(command *exec.Cmd) error {
	if command == nil || command.Process == nil {
		return nil
	}
	err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	if err == syscall.ESRCH {
		return nil
	}
	return err
}

func contractProcessGroupAlive(command *exec.Cmd) bool {
	if command == nil || command.Process == nil {
		return false
	}
	return syscall.Kill(-command.Process.Pid, 0) == nil
}
