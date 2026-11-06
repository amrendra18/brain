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

func TestConversionCacheSeparatesFormats(t *testing.T) {
	for _, sample := range []struct {
		format string
		data   []byte
	}{
		{"docx", compressedDocument(t, "keeper lantern")},
		{"rtf", []byte(`{\rtf1 keeper lantern\par}`)},
	} {
		for _, order := range [][]string{{"docx", "rtf"}, {"rtf", "docx"}} {
			t.Run(sample.format+"-"+order[0], func(t *testing.T) {
				_, state, _, _ := fixture(t)
				digest := fmt.Sprintf("%x", sha256.Sum256(sample.data))
				for round := 0; round < 2; round++ {
					for _, format := range order {
						text, err := sourceText(state, "file."+format, digest, sample.data)
						if format != sample.format {
							if err == nil {
								t.Fatalf("%s accepted as %s: %q", sample.format, format, text)
							}
						} else if err != nil || !strings.Contains(text, "keeper lantern") {
							t.Fatal(text, err)
						}
					}
				}
				lower := cacheName("file."+sample.format, digest)
				upper := cacheName("FILE."+strings.ToUpper(sample.format), digest)
				if lower != upper {
					t.Fatal("extension case changed cache identity")
				}
			})
		}
	}
	data := compressedDocument(t, "keeper lantern")
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	t.Run("old cache and failed row", func(t *testing.T) {
		source, state, db, config := fixture(t)
		if err := os.Mkdir(filepath.Join(state, "converted"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(state, "converted", digest+".txt"), []byte("old wrong text"), 0600); err != nil {
			t.Fatal(err)
		}
		name := filepath.Join(source, "good.docx")
		if err := os.WriteFile(name, data, 0600); err != nil {
			t.Fatal(err)
		}
		refresh(t, db, state, config, false, 0)
		var text string
		if err := db.QueryRow("SELECT text FROM chunks").Scan(&text); err != nil || !strings.Contains(text, "keeper lantern") {
			t.Fatalf("old cache read: %q %v", text, err)
		}
		if _, err := db.Exec("UPDATE files SET status='conversion-failed',chunker_version=?", chunk.Version-1); err != nil {
			t.Fatal(err)
		}
		result := refresh(t, db, state, config, false, 0)
		if result.New != 1 || len(result.Skipped) != 0 {
			t.Fatal("old failure did not recover", result)
		}
	})
}
