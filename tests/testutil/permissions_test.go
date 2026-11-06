package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOwnedFixturePermissions(t *testing.T) {
	folder := t.TempDir()
	if err := Permissions(t, folder, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := Permissions(t, folder, 0700); err != nil {
			t.Error(err)
		}
	})
	if err := os.WriteFile(filepath.Join(folder, "denied"), []byte("owned fixture"), 0600); err == nil {
		t.Fatal("OS allowed a denied write")
	}
	if err := Permissions(t, folder, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "restored"), []byte("owned fixture"), 0600); err != nil {
		t.Fatal(err)
	}
}
