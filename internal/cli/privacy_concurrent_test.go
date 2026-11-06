package cli

import (
	"brain/internal/platform"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrivacyReadersBeforeIndexWAL(t *testing.T) {
	_, folder, run := freshnessFixture(t)
	if code, out := run("index"); code != 0 {
		t.Fatal(code, out)
	}
	lock, err := platform.Acquire(folder, "index", false)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	for _, args := range [][]string{{"search", "skylark"}, {"recent"}} {
		if _, err = os.Stat(filepath.Join(folder, "index.sqlite-wal")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("expected the writer to hold its lock before opening SQLite", err)
		}
		if code, out := run(args...); code != 0 || !strings.Contains(out, "README.md") {
			t.Fatal(args, code, out)
		}
	}
}
