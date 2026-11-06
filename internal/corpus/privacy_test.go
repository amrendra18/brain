package corpus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrivateControlFileIdentity(t *testing.T) {
	for _, kind := range []string{"exact", "alias", "case"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			put(t, root, "values.md", `["ProtectedFixture"]`)
			put(t, root, "README.md", "Allowed lighthouse")
			path := filepath.Join(root, "values.md")
			control := path
			switch kind {
			case "alias":
				resolved, err := filepath.EvalSymlinks(path)
				if err != nil {
					t.Fatal(err)
				}
				if resolved == path {
					t.Skip("temporary path has no platform alias")
				}
				control = resolved
			case "case":
				control = filepath.Join(root, "VALUES.md")
				a, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				b, err := os.Stat(control)
				if err != nil || !os.SameFile(a, b) {
					t.Skip("filesystem distinguishes filename case")
				}
			}
			config := Default(root, []string{root}, nil)
			config.PrivateValuesFile = control
			scan, err := ScanWithPolicy(config)
			if err != nil {
				t.Fatal(err)
			}
			if _, found := scan.Entries[path]; found {
				t.Fatal("protected control indexed", kind)
			}
			if !scan.Excluded(path) {
				t.Fatal("protected control not excluded from stored state", kind)
			}
			allowed := 0
			for name := range scan.Entries {
				if strings.HasSuffix(name, "README.md") {
					allowed++
				}
			}
			if allowed != 1 {
				t.Fatal("allowed sibling count", allowed)
			}
		})
	}
}
