package testutil

import "strings"

// Shell selects the host's shell for executable journeys with an empty PATH.
func Shell(system, systemRoot, command string) []string {
	if system == "windows" {
		root := strings.TrimRight(systemRoot, `\/`)
		return []string{root + `\System32\WindowsPowerShell\v1.0\powershell.exe`, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", command + "; if ($null -ne $LASTEXITCODE) { exit $LASTEXITCODE }"}
	}
	return []string{"/bin/sh", "-c", command}
}
