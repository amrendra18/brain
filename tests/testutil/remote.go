package testutil

func RemoteState(system string) string {
	if system == "windows" {
		return `\\brain-test-server\share\state`
	}
	return "//brain-test-server/share/state"
}
