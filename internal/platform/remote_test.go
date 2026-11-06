package platform

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRemotePathForms(t *testing.T) {
	for _, test := range []struct {
		system, path string
		remote       bool
	}{
		{"darwin", "/Network/server/state", true}, {"darwin", "//server/share/state", true},
		{"linux", "/net/server/state", true}, {"linux", "nfs://server/share", true},
		{"windows", `\\server\share\state`, true}, {"windows", `C:\People\Ada\Brain`, false},
		{"darwin", "/Volumes/local/Brain", false}, {"linux", "/srv/brain", false},
	} {
		if got := NetworkPath(test.system, test.path); got != test.remote {
			t.Fatal(test, got)
		}
	}
	root := t.TempDir()
	if remote, known := RemoteFolder(filepath.Join(root, "not-created", "state")); remote || !known {
		t.Fatal(remote, known)
	}
	if remote, known := RemoteFolder("//server/share/state"); !remote || !known {
		t.Fatal(remote, known)
	}
}
func TestHarnessGuidance(t *testing.T) {
	t.Setenv("CODEX_SANDBOX", "")
	t.Setenv("CLAUDE_CODE", "")
	t.Setenv("CLAUDECODE", "")
	path := filepath.Join(t.TempDir(), "state")
	if got := WriteGuidance(path); !strings.Contains(got, "no known harness") || !strings.Contains(got, path) {
		t.Fatal(got)
	}
	t.Setenv("CODEX_SANDBOX", "seatbelt")
	if got := WriteGuidance(path); !strings.Contains(got, "writable_roots") || !strings.Contains(got, "Best effort") {
		t.Fatal(got)
	}
	t.Setenv("CODEX_SANDBOX", "")
	t.Setenv("CLAUDE_CODE", "1")
	if got := WriteGuidance(path); !strings.Contains(got, "allowWrite") {
		t.Fatal(got)
	}
	t.Setenv("CLAUDE_CODE", "")
	t.Setenv("CLAUDECODE", "1")
	if got := WriteGuidance(path); !strings.Contains(got, "Claude Code") {
		t.Fatal(got)
	}
}
