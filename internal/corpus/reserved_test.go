package corpus

import (
	"brain/tests/testutil"
	"path/filepath"
	"runtime"
	"testing"
)

func testReservedPaths(t *testing.T) {
	root := t.TempDir()
	put(t, root, "alpha/README.md", "kept")
	for _, name := range []string{"COM¹", "COM²", "COM³", "LPT¹", "LPT²", "LPT³", "CONIN$", "conout$", "COM¹.md", "LPT² ", "AUX .md"} {
		folder := filepath.Join(root, name)
		if runtime.GOOS != "windows" {
			put(t, root, name+"/hidden.md", "reserved canary must remain unread")
			if err := testutil.Permissions(t, folder, 0000); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { testutil.Permissions(t, folder, 0700) })
		}
		for _, source := range []string{folder, filepath.Join(folder, "child"), filepath.Join(folder, "*")} {
			found, skipped, err := Scan(Default(root, []string{source}, nil))
			if err != nil || len(found) != 0 || len(skipped) != 0 {
				t.Fatal("reserved source accessed", name, found, skipped, err)
			}
		}
	}
	found, skipped, err := Scan(Default(root, []string{root}, nil))
	if err != nil || len(found) != 1 || len(skipped) != 0 {
		t.Fatal("reserved directory entered", found, skipped, err)
	}
}
