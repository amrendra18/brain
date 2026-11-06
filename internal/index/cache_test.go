package index

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConversionCache(t *testing.T) {
	source, state, db, config := fixture(t)
	const rtf = `{\rtf1\ansi Caf\'e9 lantern transcript\par The keeper trims the lantern wick each dusk.\par}`
	path := write(t, source, "talk/lantern.rtf", rtf)
	if report := refresh(t, db, state, config, false, 0); report.New != 1 || len(report.Skipped) != 0 {
		t.Fatal(report)
	}
	name := cacheName(path, fmt.Sprintf("%x", sha256.Sum256([]byte(rtf))))
	cache := filepath.Join(state, "converted", name)
	data, err := os.ReadFile(cache)
	if err != nil || string(data) != "Café lantern transcript\nThe keeper trims the lantern wick each dusk.\n" {
		t.Fatal(string(data), err)
	}
	before, err := os.Stat(cache)
	if err != nil {
		t.Fatal(err)
	}
	for _, full := range []bool{false, true} {
		if report := refresh(t, db, state, config, full, 0); report.New+report.Updated != 0 {
			t.Fatal(report)
		}
	}
	now := time.Now().Add(-time.Hour)
	os.Chtimes(path, now, now)
	refresh(t, db, state, config, false, 0)
	after, _ := os.Stat(cache)
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("unchanged source converted again")
	}
	write(t, source, "talk/lantern.rtf", strings.ReplaceAll(rtf, "dusk", "dawn"))
	if report := refresh(t, db, state, config, false, 0); report.Updated != 1 {
		t.Fatal(report)
	}
	files, _ := os.ReadDir(filepath.Dir(cache))
	if len(files) != 2 {
		t.Fatal(files)
	}
	os.WriteFile(filepath.Join(filepath.Dir(cache), ".brain-killed.tmp"), []byte("partial"), 0600)
	refresh(t, db, state, config, true, 0)
	files, _ = os.ReadDir(filepath.Dir(cache))
	if len(files) != 1 {
		t.Fatal("old cache or temporary file kept", files)
	}
	write(t, source, "talk/broken.rtf", "not rtf")
	if report := refresh(t, db, state, config, false, 0); len(report.Skipped) != 1 || report.Skipped[0].Reason != "conversion-failed" {
		t.Fatal(report)
	}
	refresh(t, db, state, config, false, 0)
	refresh(t, db, state, config, true, 0)
	write(t, source, "talk/broken.rtf", `{\rtf1 repaired lantern\par}`)
	if report := refresh(t, db, state, config, false, 0); report.New != 1 || len(report.Skipped) != 0 {
		t.Fatal(report)
	}
	if text, err := sourceText(state, "body.md", "", []byte{'x', 0xff}); err != nil || text != "x�" {
		t.Fatal(text, err)
	}
	if text, err := sourceText(filepath.Join(state, "absent"), "call.rtf", "digest", []byte(rtf)); err != nil || !strings.Contains(text, "lantern") {
		t.Fatal(text, err)
	}
	if _, err := sourceText(state, "broken.docx", "digest", []byte("not zip")); err == nil {
		t.Fatal("damaged docx converted")
	}
	config.Options.MaxFileBytes = 8
	if report := refresh(t, db, state, config, true, 0); len(report.Skipped) != 2 {
		t.Fatal(report)
	}
}

func TestCacheFailureKeepsConvertedText(t *testing.T) {
	source, state, db, config := fixture(t)
	write(t, source, "talk.rtf", `{\rtf1 lighthouse keeper\par}`)
	os.WriteFile(filepath.Join(state, "converted"), []byte("folder blocked"), 0600)
	if report := refresh(t, db, state, config, false, 0); report.New != 1 {
		t.Fatal(report)
	}
	var text string
	if err := db.QueryRow("SELECT text FROM chunks").Scan(&text); err != nil || text != "lighthouse keeper" {
		t.Fatal(text, err)
	}
	if err := pruneCache(db, state); err == nil {
		t.Fatal("file treated as cache folder")
	}
	os.Remove(filepath.Join(state, "converted"))
	if err := pruneCache(db, state); err != nil {
		t.Fatal(err)
	}
	os.Mkdir(filepath.Join(state, "converted"), 0700)
	os.Mkdir(filepath.Join(state, "converted", "not-empty"), 0700)
	os.WriteFile(filepath.Join(state, "converted", "not-empty", "held"), nil, 0600)
	if err := pruneCache(db, state); err == nil {
		t.Fatal("nonempty directory silently purged")
	}
	os.RemoveAll(filepath.Join(state, "converted", "not-empty"))
	db.Close()
	if err := pruneCache(db, state); err == nil {
		t.Fatal("closed database accepted")
	}
}
