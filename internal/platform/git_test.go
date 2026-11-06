package platform

import (
	"path/filepath"
	"testing"
)

func TestOptionalGitPath(t *testing.T) {
	if git := GitPath(); git != "" && !filepath.IsAbs(git) {
		t.Fatal("Git path must be absolute", git)
	}
	t.Setenv("PATH", "")
	if GitPath() != "" {
		t.Fatal("Git was found with no search path")
	}
}
