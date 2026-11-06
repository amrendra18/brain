package corpus

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigErrorCategories(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.json")
	if _, err := Load(root); !errors.Is(err, ErrConfigMissing) || !errors.Is(err, os.ErrNotExist) || errors.Is(err, ErrConfigInvalid) {
		t.Fatal(err)
	}
	for _, data := range []string{"{invalid", "null", `{"sources":[{"path":"relative"}],"options":{"max_file_bytes":1,"inline_refresh_limit":1,"purge_after_days":1}}`} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := Load(root)
		if !errors.Is(err, ErrConfigInvalid) || errors.Is(err, ErrConfigMissing) {
			t.Fatal(err)
		}
		if data == "{invalid" {
			var syntax *json.SyntaxError
			if !errors.As(err, &syntax) {
				t.Fatal("syntax error lost", err)
			}
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root); !errors.Is(err, ErrConfigInvalid) || errors.Is(err, ErrConfigMissing) {
		t.Fatal(err)
	}
}

func TestConfigSymlinksAreInvalid(t *testing.T) {
	for _, missing := range []bool{false, true} {
		root := t.TempDir()
		target := filepath.Join(root, "target")
		if !missing {
			if err := os.WriteFile(target, []byte("secret"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Symlink(target, filepath.Join(root, "config.json")); err != nil {
			t.Skip("symlinks unavailable", err)
		}
		if _, err := Load(root); !errors.Is(err, ErrConfigInvalid) || errors.Is(err, ErrConfigMissing) {
			t.Fatal(err)
		}
	}
}
