package verifyrun

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestExpectedNoOpCannotHideLogWriteFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX deliberate no-op fixture")
	}
	script := filepath.Join(t.TempDir(), "no-op")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '{}\\n'\nexit 3\n"), 0700); err != nil {
		t.Fatal(err)
	}
	evidence := t.TempDir()
	if err := os.MkdirAll(filepath.Join(evidence, "logs", "01-command.json"), 0700); err != nil {
		t.Fatal(err)
	}
	d := driver{binary: script, evidence: evidence, summary: &Summary{Scratch: t.TempDir()}}
	j, err := d.newFeatureJourney("blocked-evidence", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := j.call(3, "sources", "list"); err == nil || !strings.Contains(err.Error(), "01-command.json") {
		t.Fatal("expected exit 3 hid evidence write failure", err)
	}
	if len(d.summary.Commands) != 1 || d.summary.Commands[0].Exit == nil || *d.summary.Commands[0].Exit != 3 {
		t.Fatal("test input did not exit 3")
	}
}
