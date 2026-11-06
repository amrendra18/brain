//go:build !windows

package index

import (
	"brain/internal/corpus"
	"brain/internal/platform"
	"brain/tests/testutil"
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
)

func TestRefreshWaitingProcess(t *testing.T) {
	state := os.Getenv("BRAIN_REFRESH_WAIT_STATE")
	if state == "" {
		return
	}
	source := os.Getenv("BRAIN_REFRESH_WAIT_SOURCE")
	db, err := Open(state, false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	config := corpus.Default(source, []string{source}, nil)
	finished := make(chan struct{})
	var report Report
	go func() { report, err = Refresh(db, state, config, false, 0); close(finished) }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		stack := make([]byte, 65536)
		stack = stack[:runtime.Stack(stack, true)]
		if bytes.Contains(stack, []byte("brain/internal/platform.Acquire(")) && bytes.Contains(stack, []byte("brain/internal/index.Refresh(")) {
			break
		}
		select {
		case <-finished:
			t.Fatalf("refresh did not wait: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("refresh never reached the index lock")
		}
		time.Sleep(time.Millisecond)
	}
	fmt.Println("waiting")
	if err := syscall.Kill(os.Getpid(), syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	<-finished
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(string(encoded))
}

func stoppedRefresh(t *testing.T, source, state string) func() Report {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRefreshWaitingProcess$")
	cmd.Env = append(os.Environ(), "BRAIN_REFRESH_WAIT_STATE="+state, "BRAIN_REFRESH_WAIT_SOURCE="+source)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	})
	reader := bufio.NewReader(stdout)
	ready, err := reader.ReadString('\n')
	if err != nil || ready != "waiting\n" {
		t.Fatalf("child readiness: %q %v %s", ready, err, stderr.String())
	}
	// BSD WaitStatus.Stopped treats SIGSTOP as continued. Check the stop record directly.
	var status syscall.WaitStatus
	if _, err := syscall.Wait4(cmd.Process.Pid, &status, syscall.WUNTRACED, nil); err != nil || status != syscall.WaitStatus(int(syscall.SIGSTOP)<<8|0x7f) {
		t.Fatalf("child did not stop at the lock: %v %v", status, err)
	}
	return func() Report {
		t.Helper()
		if err := cmd.Process.Signal(syscall.SIGCONT); err != nil {
			t.Fatal(err)
		}
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		var report Report
		if err := json.Unmarshal([]byte(line), &report); err != nil {
			t.Fatal(line, err)
		}
		if err := cmd.Wait(); err != nil {
			t.Fatal(err, stderr.String())
		}
		return report
	}
}

func indexedMatches(t *testing.T, db *sql.DB, term string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow("SELECT count(*) FROM chunk_fts JOIN chunks ON chunks.row=chunk_fts.rowid JOIN files ON files.id=chunks.file WHERE chunk_fts MATCH ? AND files.deleted_at IS NULL", term).Scan(&got); err != nil || got != want {
		t.Fatalf("search %q: got %d, want %d: %v", term, got, want, err)
	}
}

func TestRefreshWaitPreservesNewerDocument(t *testing.T) {
	source, state, db, config := fixture(t)
	write(t, source, "ordinary.md", "ordinarymarker")
	refresh(t, db, state, config, false, 0)
	lock, err := platform.Acquire(state, "index", false)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	resume := stoppedRefresh(t, source, state)
	path := write(t, source, "new.md", "latearrivalmarker")
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if report := refresh(t, db, state, config, false, 0); report.New != 1 {
		t.Fatal(report)
	}
	indexedMatches(t, db, "latearrivalmarker", 1)
	report := resume()
	if report.Removed != 0 || report.New != 0 || report.Unchanged != 2 {
		t.Fatal("older refresh regressed newer index", report)
	}
	indexedMatches(t, db, "ordinarymarker", 1)
	indexedMatches(t, db, "latearrivalmarker", 1)
	if data, err := os.ReadFile(path); err != nil || string(data) != "latearrivalmarker" {
		t.Fatal("source changed", string(data), err)
	}
	var integrity string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatal(integrity, err)
	}
	if _, err := db.Exec(FTSCheck); err != nil {
		t.Fatal(err)
	}
}

func TestRefreshWaitUsesCurrentGaps(t *testing.T) {
	for _, recover := range []bool{false, true} {
		t.Run(fmt.Sprint("recover=", recover), func(t *testing.T) {
			source, state, db, config := fixture(t)
			write(t, source, "ordinary.md", "ordinarymarker")
			directory := filepath.Join(source, "moving")
			if err := os.Mkdir(directory, 0700); err != nil {
				t.Fatal(err)
			}
			write(t, source, "moving/kept.md", "gapmarker")
			refresh(t, db, state, config, false, 0)
			t.Cleanup(func() { testutil.Permissions(t, directory, 0700) })
			if recover {
				testutil.Permissions(t, directory, 0)
			}

			refresh(t, db, state, config, false, 0)
			lock, err := platform.Acquire(state, "index", false)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			resume := stoppedRefresh(t, source, state)
			mode := os.FileMode(0)
			if recover {
				mode = 0700
			}
			testutil.Permissions(t, directory, mode)
			if err := lock.Close(); err != nil {
				t.Fatal(err)
			}
			report := resume()
			want := 1
			if recover {
				want = 0
			}
			gaps, err := StoredGaps(db)
			if err != nil || len(gaps.Items) != want || len(report.Skipped) != want {
				t.Fatal("stale scan gaps", gaps, report, err)
			}
			if want == 1 && (gaps.Items[0].Path != directory || gaps.Items[0].Reason != "unreadable") {
				t.Fatal(gaps)
			}
			indexedMatches(t, db, "ordinarymarker", 1)
			indexedMatches(t, db, "gapmarker", 1)
		})
	}
}

func TestRefreshInlineGapsRespectLock(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(fmt.Sprint("pending=", pending), func(t *testing.T) {
			source, state, db, config := fixture(t)
			write(t, source, "ordinary.md", "ordinarymarker")
			directory := filepath.Join(source, "moving")
			if err := os.Mkdir(directory, 0700); err != nil {
				t.Fatal(err)
			}
			refresh(t, db, state, config, false, 0)
			before, err := ScannedAt(db)
			if err != nil {
				t.Fatal(err)
			}
			testutil.Permissions(t, directory, 0)
			t.Cleanup(func() { testutil.Permissions(t, directory, 0700) })
			wantPending := 0
			if pending {
				write(t, source, "new.md", "newmarker")
				wantPending = 1
			}
			lock, err := platform.Acquire(state, "index", false)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			report := refresh(t, db, state, config, false, 1)
			stored, err := StoredGaps(db)
			if err != nil || len(stored.Items) != 0 || len(report.Skipped) != 1 || report.Pending != wantPending {
				t.Fatal("held lock allowed a gap write", stored, report, err)
			}
			if err := lock.Close(); err != nil {
				t.Fatal(err)
			}
			report = refresh(t, db, state, config, false, 1)
			stored, err = StoredGaps(db)
			if err != nil || len(stored.Items) != 1 || len(report.Skipped) != 1 || report.Pending != wantPending || report.New != 0 {
				t.Fatal("available lock failed to store current gap", stored, report, err)
			}
			after, err := ScannedAt(db)
			if err != nil || after != before {
				t.Fatal("gap-only update changed completed scan time", before, after, err)
			}
			indexedMatches(t, db, "ordinarymarker", 1)
		})
	}
}
