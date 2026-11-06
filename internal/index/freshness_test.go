package index

import (
	"brain/internal/notes"
	"brain/tests/testutil"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNoteIndexRetainsFullScan(t *testing.T) {
	source, folder, db, config := fixture(t)
	write(t, source, "README.md", "# Prior\n\nRetained skylark.")
	refresh(t, db, folder, config, false, 0)
	before, err := ScannedAt(db)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(source, source+"-offline"); err != nil {
		t.Fatal(err)
	}
	block := notes.Entry("A violet memory.", time.Now().UTC().Format(time.RFC3339))
	if _, err = notes.Append(folder, "alpha", block); err != nil {
		t.Fatal(err)
	}
	if err = Note(db, folder, config, "alpha"); err != nil {
		t.Fatal(err)
	}
	after, complete, err := ReadTime(db)
	if err != nil || !complete || after != before {
		t.Fatal(after, complete, err)
	}
	var files int
	if err = db.QueryRow("SELECT count(*) FROM files WHERE deleted_at IS NULL").Scan(&files); err != nil || files != 2 {
		t.Fatal(files, err)
	}
	if _, err = db.Exec("DELETE FROM meta WHERE key='scanned_at'"); err != nil {
		t.Fatal(err)
	}
	stamp, complete, err := ReadTime(db)
	if err != nil || complete || stamp == 0 {
		t.Fatal(stamp, complete, err)
	}
	if err = Note(db, folder, config, "missing"); err == nil {
		t.Fatal("missing note accepted")
	}
	if err = Note(db, folder, config, "../outside"); err == nil {
		t.Fatal("unowned note accepted")
	}
	if err = os.Mkdir(filepath.Join(folder, "notes", "directory.md"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = Note(db, folder, config, "directory"); err == nil {
		t.Fatal("directory accepted")
	}
	db.Close()
	if _, _, err = ReadTime(db); err == nil {
		t.Fatal("closed database accepted")
	}
	if err = Note(db, folder, config, "alpha"); err == nil {
		t.Fatal("closed database accepted")
	}
}

func TestIndexRunReceipt(t *testing.T) {
	folder := t.TempDir()
	if run, present, err := LastRun(folder); err != nil || present || run.Outcome != "" {
		t.Fatal(run, present, err)
	}
	run := Run{StartedAt: time.Now().UTC(), Outcome: "running"}
	if err := SaveRun(folder, run); err != nil {
		t.Fatal(err)
	}
	if got, present, err := LastRun(folder); err != nil || !present || got.Outcome != "running" {
		t.Fatal(got, present, err)
	}
	run.Outcome = "failed"
	run.FinishedAt = time.Now().UTC()
	if err := SaveRun(folder, run); err != nil {
		t.Fatal(err)
	}
	if got, present, err := LastRun(folder); err != nil || !present || got.Outcome != "failed" {
		t.Fatal(got, present, err)
	}
	for _, data := range []string{"bad JSON", `{"outcome":"complete"}`, `{"started_at":"2026-01-01T00:00:00Z","outcome":"unknown"}`} {
		if err := os.WriteFile(filepath.Join(folder, "index-run.json"), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := LastRun(folder); err == nil {
			t.Fatal("invalid receipt accepted", data)
		}
	}
	if err := testutil.Permissions(t, filepath.Join(folder, "index-run.json"), 0000); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LastRun(folder); err == nil {
		t.Fatal("unreadable receipt accepted")
	}
	if err := testutil.Permissions(t, filepath.Join(folder, "index-run.json"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := testutil.Permissions(t, folder, 0500); err != nil {
		t.Fatal(err)
	}
	defer testutil.Permissions(t, folder, 0700)
	if err := SaveRun(folder, run); err == nil {
		t.Fatal("unwritable receipt accepted")
	}
}
