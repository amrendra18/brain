package testutil

import (
	"reflect"
	"strings"
	"testing"
)

func TestHostShellSelection(t *testing.T) {
	for _, system := range []string{"darwin", "linux"} {
		if got := Shell(system, "", "native command"); !reflect.DeepEqual(got, []string{"/bin/sh", "-c", "native command"}) {
			t.Fatal(system, got)
		}
	}
	for _, root := range []string{`C:\Windows`, `C:\Windows\`} {
		got := Shell("windows", root, "native command")
		if got[0] != `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe` || got[4] != "-Command" || !strings.Contains(got[5], "exit $LASTEXITCODE") {
			t.Fatal(got)
		}
	}
}

func TestRemoteStateFixture(t *testing.T) {
	if got := RemoteState("windows"); got != `\\brain-test-server\share\state` {
		t.Fatal(got)
	}
	for _, system := range []string{"darwin", "linux"} {
		if got := RemoteState(system); got != "//brain-test-server/share/state" {
			t.Fatal(got)
		}
	}
}
