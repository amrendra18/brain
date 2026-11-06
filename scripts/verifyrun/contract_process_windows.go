//go:build windows

package verifyrun

import "os/exec"

func setContractProcessGroup(*exec.Cmd) {}

func terminateContractProcessGroup(command *exec.Cmd) error {
	if command == nil || command.Process == nil {
		return nil
	}
	return command.Process.Kill()
}

func contractProcessGroupAlive(*exec.Cmd) bool { return false }
