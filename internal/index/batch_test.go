package index

import (
	"archive/zip"
	"brain/internal/corpus"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func compressedDocument(t *testing.T, text string) []byte {
	t.Helper()
	var data bytes.Buffer
	archive := zip.NewWriter(&data)
	file, err := archive.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fmt.Fprintf(file, `<document><body><p><r><t>%s</t></r></p></body></document>`, text); err != nil {
		t.Fatal(err)
	}
	if err = archive.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestConvertedBatchByteBudget(t *testing.T) {
	for _, test := range []struct {
		name                    string
		bytes, files, committed int
	}{
		{"small", 100, 4, 0},
		{"expanded", 1200000, 4, 3},
		{"one oversized document", 4500000, 2, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			source, state, db, config := fixture(t)
			data := compressedDocument(t, strings.Repeat("keeper ", test.bytes/7))
			if len(data) >= 100000 {
				t.Fatal("fixture did not compress", len(data))
			}
			for i := 0; i < test.files; i++ {
				if err := os.WriteFile(filepath.Join(source, fmt.Sprintf("%d.docx", i)), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			found, _, err := corpus.Scan(config)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range found {
				item := read(work{Entry: entry}, state, config, false)
				t.Logf("compressed bytes=%d, retained work bytes=%d", len(data), retainedBytes(item))
				break
			}
			trigger := fmt.Sprintf("CREATE TRIGGER batch_failure BEFORE INSERT ON files WHEN NEW.path='%d.docx' BEGIN SELECT RAISE(ABORT,'test input'); END", test.files-1)
			if _, err := db.Exec(trigger); err != nil {
				t.Fatal(err)
			}
			if _, err := Refresh(db, state, config, false, 0); err == nil {
				t.Fatal("test failure input accepted")
			}
			var count int
			if err := db.QueryRow("SELECT count(*) FROM files").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != test.committed {
				t.Fatalf("committed files=%d, want %d before failure", count, test.committed)
			}
		})
	}
}
