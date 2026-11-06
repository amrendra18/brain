package index

import (
	"brain/internal/chunk"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDocxBoundsRefreshesVersionFour(t *testing.T) {
	if chunk.Version < 5 {
		t.Fatal("DOCX correction must invalidate version 4 conversion caches")
	}
	for _, full := range []bool{false, true} {
		t.Run(fmt.Sprintf("full=%t", full), func(t *testing.T) {
			source, state, db, config := fixture(t)
			const oldText = "ghostword\n\nghostword"
			path := write(t, source, "bad.docx", string(compressedDocument(t, oldText)))
			write(t, source, "good.docx", string(compressedDocument(t, "ordinary keeper")))
			refresh(t, db, state, config, false, 0)
			cacheDir := filepath.Join(state, "converted")
			caches, err := os.ReadDir(cacheDir)
			if err != nil {
				t.Fatal(err)
			}
			for _, cache := range caches {
				oldName := strings.Replace(cache.Name(), fmt.Sprintf("v%d-", chunk.Version), "v4-", 1)
				if err := os.Rename(filepath.Join(cacheDir, cache.Name()), filepath.Join(cacheDir, oldName)); err != nil {
					t.Fatal(err)
				}
			}
			malformed := compressedDocument(t, "</t></r><p><r><t>ghostword</t></r></p><r><t>")
			write(t, source, "bad.docx", string(malformed))
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			digest := fmt.Sprintf("%x", sha256.Sum256(malformed))
			if _, err := db.Exec("UPDATE files SET size=?,mtime_ns=?,sha256=? WHERE path='bad.docx'", info.Size(), info.ModTime().UnixNano(), digest); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec("UPDATE files SET chunker_version=4"); err != nil {
				t.Fatal(err)
			}
			oldCache := filepath.Join(cacheDir, "v4-docx-"+digest+".txt")
			if err := os.WriteFile(oldCache, []byte(oldText), 0600); err != nil {
				t.Fatal(err)
			}
			stored, err := OpenRead(state, true)
			if err != nil {
				t.Fatal(err)
			}
			var text string
			err = stored.QueryRow("SELECT text FROM chunks JOIN files ON files.id=chunks.file WHERE path='bad.docx'").Scan(&text)
			stored.Close()
			if err != nil || text != oldText {
				t.Fatal("old snapshot changed before refresh", text, err)
			}
			for round := 0; round < 2; round++ {
				report := refresh(t, db, state, config, full, 0)
				if len(report.Skipped) != 1 || report.Skipped[0].Path != path || report.Skipped[0].Reason != "conversion-failed" {
					t.Fatal("malformed DOCX did not become a gap", report)
				}
				gaps, err := StoredGaps(db)
				if err != nil || gaps.Count != 1 || gaps.Items[0].Path != path || gaps.Items[0].Reason != "conversion-failed" {
					t.Fatal("conversion gap was not stored", gaps, err)
				}
				var status string
				var version int
				if err := db.QueryRow("SELECT status,chunker_version FROM files WHERE path='bad.docx'").Scan(&status, &version); err != nil || status != "conversion-failed" || version != chunk.Version {
					t.Fatal("old successful conversion survived refresh", status, version, err)
				}
				if _, err := os.Stat(filepath.Join(cacheDir, cacheName(path, digest))); !os.IsNotExist(err) {
					t.Fatal("malformed conversion created a usable cache", err)
				}
				for term, want := range map[string]int{"ghostword": 0, "ordinary": 1} {
					var count int
					if err := db.QueryRow("SELECT count(*) FROM chunk_fts WHERE chunk_fts MATCH ?", term).Scan(&count); err != nil || count != want {
						t.Fatal("refreshed search contents", term, count, err)
					}
				}
			}
		})
	}
}
