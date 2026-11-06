//go:build !windows

package index

import (
	"brain/internal/corpus"
	"os"
	"syscall"
	"testing"
)

func TestSourceReplacementPipe(t *testing.T) {
	source, state, _, config := fixture(t)
	path := write(t, source, "README.md", "Ordinary source.")
	found, _, err := corpus.Scan(config)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	item := read(work{Entry: found[path]}, state, config, false)
	if item.Status != "unreadable" || len(item.Pieces) != 0 {
		t.Fatal("named pipe replacement accepted", item.Status)
	}
}
