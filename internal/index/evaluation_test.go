package index

import (
	"brain/internal/corpus"
	"brain/tests/testutil"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestEvaluationRefreshRemovesStoredFiles(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		t.Run(map[bool]string{false: "readable", true: "unavailable"}[unavailable], func(t *testing.T) {
			if unavailable && runtime.GOOS == "windows" {
				t.Skip("this fixture needs Unix folder read/search permissions; Windows folder gaps have separate CLI tests")
			}
			source, state, db, config := fixture(t)
			config.Exclude = append(config.Exclude, "atlas/REPORT.md", "atlas/NEXT.md", "atlas/tests", "archive/measurements-*.md")
			names := []string{"atlas/REPORT.md", "atlas/NEXT.md", "atlas/tests/golden/README.md", "archive/measurements-2026-01-05.md", "atlas/PLAN.md", "other/REPORT.md"}
			// Seed the same records an older executable could have indexed.
			for i, name := range names {
				marker := "evaluationmarker"
				if i >= 4 {
					marker = "ordinarymarker"
				}
				path := write(t, source, name, marker)
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				entry := corpus.Entry{Path: path, Relative: name, Source: source, Project: "fixture", Kind: corpus.Kind(path), Namespace: "source:0:", Size: info.Size(), MtimeNS: info.ModTime().UnixNano()}
				item := read(work{Entry: entry}, state, config, false)
				if _, err = apply(db, []work{item}, nil, 1, 0); err != nil {
					t.Fatal(err)
				}
			}
			if err := setPrivacy(db, config.Privacy()); err != nil {
				t.Fatal(err)
			}
			count := func(term string, want int) {
				t.Helper()
				var got int
				if err := db.QueryRow("SELECT count(*) FROM chunk_fts JOIN chunks ON chunks.row=chunk_fts.rowid JOIN files ON files.id=chunks.file WHERE chunk_fts MATCH ? AND files.deleted_at IS NULL", term).Scan(&got); err != nil || got != want {
					t.Fatal(term, got, want, err)
				}
			}
			count("evaluationmarker", 4)
			count("ordinarymarker", 2)
			if unavailable {
				testutil.UnavailableSource(t, filepath.Join(source, "atlas"))
				testutil.UnavailableSource(t, filepath.Join(source, "archive"))
			}
			report := refresh(t, db, state, config, false, config.Options.InlineRefreshLimit)
			if report.Removed != 4 {
				t.Fatal("stored evaluation records were not removed", report)
			}
			count("evaluationmarker", 0)
			count("ordinarymarker", 2)
		})
	}
}
