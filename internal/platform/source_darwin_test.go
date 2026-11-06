package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSourceOpensThroughSystemAlias(t *testing.T) {
	folder, err := os.MkdirTemp("/private/tmp", "brain-alias-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(folder) })
	if err := os.WriteFile(filepath.Join(folder, "note.md"), []byte("alias"), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := openSource(filepath.Join("/tmp", filepath.Base(folder), "note.md"))
	if err != nil {
		t.Fatal("the /tmp alias was refused", err)
	}
	file.Close()
	if _, err := openSource(filepath.Join("/tmp", filepath.Base(folder), "absent.md")); err == nil {
		t.Fatal("a missing file opened")
	}
}
