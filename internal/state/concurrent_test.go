package state

import (
	"brain/internal/index"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestConcurrentRecentDuringIndex(t *testing.T) {
	source, folder, config, reader := fixture(t)
	writer, err := index.Open(folder, false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	done := make(chan error, 1)
	go func() {
		for round := 0; round < 12; round++ {
			for number := 0; number < 80; number++ {
				name := filepath.Join(source, fmt.Sprintf("changing-%03d.md", number))
				var err error
				if round%2 == 0 {
					err = os.WriteFile(name, []byte(fmt.Sprintf("# File %d\n\nRound %d beacon.", number, round)), 0600)
				} else {
					err = os.Remove(name)
				}
				if err != nil {
					done <- err
					return
				}
			}
			if _, err := index.Refresh(writer, folder, config, false, 0); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	reads := 0
	for {
		documents, total, err := Recent(reader, 7, 20)
		if err != nil {
			<-done
			t.Fatal(err)
		}
		if total < len(documents) {
			<-done
			t.Fatalf("recent gave %d documents with total %d", len(documents), total)
		}
		view, _, err := Snapshot(folder, config, reader)
		if err != nil {
			<-done
			t.Fatal(err)
		}
		if view.Index.Files != view.Index.Chunks || view.Index.Duplicates != 0 {
			<-done
			t.Fatal("inconsistent state snapshot", view.Index)
		}
		reads++
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
			if reads < 2 {
				t.Fatal("writer completed before concurrent reads")
			}
			t.Logf("%d recent and state snapshots during twelve index refreshes", reads)
			return
		default:
		}
	}
}
