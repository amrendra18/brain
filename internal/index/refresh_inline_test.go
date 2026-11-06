package index

import (
	"brain/internal/platform"
	"strconv"
	"testing"
	"time"
)

func TestRefreshInlinePendingLimit(t *testing.T) {
	for _, held := range []bool{false, true} {
		for _, limit := range []int{1, 2, 3} {
			name := "available"
			if held {
				name = "held"
			}
			t.Run(name+"/"+strconv.Itoa(limit), func(t *testing.T) {
				source, state, db, config := fixture(t)
				write(t, source, "ordinary.md", "ordinarymarker")
				refresh(t, db, state, config, false, 0)
				before, err := ScannedAt(db)
				if err != nil {
					t.Fatal(err)
				}
				write(t, source, "first.md", "firstmarker")
				write(t, source, "second.md", "secondmarker")
				if held {
					lock, err := platform.Acquire(state, "index", false)
					if err != nil {
						t.Fatal(err)
					}
					defer lock.Close()
				}
				start := time.Now()
				report := refresh(t, db, state, config, false, limit)
				if held && time.Since(start) >= time.Second {
					t.Fatal("inline refresh waited on the writer lock")
				}
				pending, created, files := 2, 0, 1
				if !held && limit > 2 {
					pending, created, files = 0, 2, 3
				}
				if report.Pending != pending || report.New != created || report.Unchanged != 1 || report.Removed != 0 {
					t.Fatal(report)
				}
				got, _, _, err := Totals(db)
				if err != nil || got != files {
					t.Fatal("inline limit changed stored files", got, files, err)
				}
				after, err := ScannedAt(db)
				if err != nil || pending > 0 && after != before {
					t.Fatal("pending work changed completed scan time", before, after, err)
				}
			})
		}
	}
}

func TestRefreshInlineIdleKeepsScanTime(t *testing.T) {
	source, state, db, config := fixture(t)
	write(t, source, "ordinary.md", "ordinarymarker")
	refresh(t, db, state, config, false, 0)
	before, err := ScannedAt(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, held := range []bool{false, true} {
		if held {
			lock, err := platform.Acquire(state, "index", false)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
		}
		report := refresh(t, db, state, config, false, 2)
		if report.Pending != 0 || report.New != 0 || report.Updated != 0 || report.Removed != 0 || report.Unchanged != 1 {
			t.Fatal(report)
		}
		after, err := ScannedAt(db)
		if err != nil || after != before {
			t.Fatal("idle refresh changed completed scan time", before, after, err)
		}
	}
}
