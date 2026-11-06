package platform

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSourceReadBounds(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "README.md")
	if err := os.WriteFile(path, []byte("ordinary"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int64{0, 8, 32} {
		data, err := ReadSource(path, limit)
		if err != nil || string(data) != "ordinary" {
			t.Fatal(string(data), err)
		}
	}
	for _, limit := range []int64{-1, 7} {
		if data, err := ReadSource(path, limit); !errors.Is(err, ErrSourceOversize) || data != nil {
			t.Fatal(data, err)
		}
	}
	if _, err := ReadSource(root, 32); err == nil {
		t.Fatal("directory accepted")
	}
	if _, err := ReadSource(filepath.Join(root, "missing"), 32); err == nil {
		t.Fatal("missing file accepted")
	}
	// A reader growing past its original size must return no partial contents.
	if data, err := readBounded(strings.NewReader("growing"), 6); data != nil || !errors.Is(err, ErrSourceOversize) {
		t.Fatal(data, err)
	}
	for _, limit := range []int64{0, 1} {
		if data, err := readBounded(sourceReadError{}, limit); data != nil || !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatal(data, err)
		}
	}
}

type sourceReadError struct{}

func (sourceReadError) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
