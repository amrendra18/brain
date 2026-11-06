package notes

import (
	"brain/internal/chunk"
	"brain/tests/testutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEntries(t *testing.T) {
	block := Entry("  Text # here\n## Sub\n~~~\n", "2026-01-06T20:00:00Z")
	if block != "## 2026-01-06T20:00:00Z\n\nText # here\n\\## Sub\n\\~~~\n" || EntryID("lantern", block) != chunk.ID(chunk.DocumentKey("notes", "lantern.md", block)) {
		t.Fatal(block)
	}
	first := Entry("Searching firstmarker "+strings.Repeat("lease ", 300), "2026-01-06T20:00:00Z")
	second := Entry("Searching secondmarker "+strings.Repeat("lease ", 300), "2026-01-06T20:00:00Z")
	if EntryID("lantern", first) == EntryID("lantern", second) || EntryID("lantern", first) != chunk.ID(chunk.DocumentKey("notes", "lantern.md", chunk.Split(first)[1].Text)) {
		t.Fatal("long notes share a heading-only identifier")
	}
	for _, project := range []string{"lantern", "A B", "éclair", "_name", "²"} {
		if !Project(project) {
			t.Fatal(project)
		}
	}
	for _, project := range []string{"", ".hidden", "../other", "a/b", `a\b`, "x\x00", "CON", "LPT1", "COM¹", "COM²", "COM³", "LPT¹", "LPT²", "LPT³", "CONIN$", "CONOUT$", "COM¹.md", "end.", "end ", strings.Repeat("x", 101)} {
		if Project(project) {
			t.Fatal(project)
		}
	}
	state := t.TempDir()
	for _, text := range []string{"First", "Second"} {
		if _, err := Append(state, "lantern", Entry(text, "stamp")); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(state, "notes", "lantern.md"))
	if err != nil || string(data) != "## stamp\n\nFirst\n\n## stamp\n\nSecond\n" {
		t.Fatal(string(data), err)
	}
	if _, err = Append(state, "../escape", "text"); err == nil {
		t.Fatal("invalid project appended")
	}
	if _, err = Append(filepath.Join(state, "absent"), "lantern", "text"); err == nil {
		t.Fatal("missing state appended")
	}
	if err = testutil.Permissions(t, filepath.Join(state, "notes"), 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testutil.Permissions(t, filepath.Join(state, "notes"), 0700) })
	if _, err = Append(state, "lantern", "text"); err == nil {
		t.Fatal("unwritable notes appended")
	}
}
