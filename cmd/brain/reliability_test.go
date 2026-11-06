package main

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func buildCorpus(t *testing.T, j *journey, count, start int, version string) {
	t.Helper()
	body := strings.Repeat("The keeper logs tides, ships and lamp oil each evening. ", 80)
	for n := start; n < start+count; n++ {
		j.put(t, fmt.Sprintf("project%d/docs/file%d.md", n%10, n), fmt.Sprintf("# File %d\n\n%s\n\n## Keeper %d\n\nThe lighthouse keeper %d writes version %s.", n, body, n, n, version))
	}
}
func startIndex(t *testing.T, j *journey, flags ...string) *exec.Cmd {
	t.Helper()
	arguments := append([]string{"index"}, flags...)
	arguments = append(arguments, "--format", "json")
	cmd := exec.Command(testBinary, arguments...)
	cmd.Env = j.environment
	cmd.Dir = j.base
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
	})
	return cmd
}
func TestKilledIndexLeavesOldIndex(t *testing.T) {
	j := newJourney(t)
	buildCorpus(t, j, 1800, 0, "one")
	j.ready(t)
	db, err := sql.Open("sqlite", j.state+"/index.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var before float64
	if err = db.QueryRow("SELECT max(indexed_at) FROM files").Scan(&before); err != nil {
		t.Fatal(err)
	}
	buildCorpus(t, j, 1800, 0, "two")
	cmd := startIndex(t, j, "--full")
	deadline := time.Now().Add(30 * time.Second)
	committed := false
	for time.Now().Before(deadline) {
		var newest float64
		if err = db.QueryRow("SELECT max(indexed_at) FROM files").Scan(&newest); err != nil {
			t.Fatal(err)
		}
		if newest != before {
			committed = true
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if !committed {
		t.Fatal("index did not commit a batch within 30 seconds")
	}
	if err = cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err = cmd.Wait(); err == nil {
		t.Fatal("index completed before it was killed")
	}
	var integrity string
	if err = db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatal(integrity, err)
	}
	if _, err = db.Exec("INSERT INTO chunk_fts(chunk_fts,rank) VALUES('integrity-check',1)"); err != nil {
		t.Fatal(err)
	}
	code, reply := j.run(t, "search", "lighthouse keeper", "--no-refresh")
	if code != 0 || reply["total"] != float64(1800) {
		t.Fatal(code, reply)
	}
	code, reply = j.run(t, "search", `"writes version two"`, "--no-refresh")
	changed := reply["total"].(float64)
	fmt.Fprintf(os.Stderr, "killed index: %.0f of 1800 files committed; old/current total preserved\n", changed)
	if code != 0 || changed <= 0 || changed >= 1800 {
		t.Fatal(code, reply)
	}
	if code, reply = j.run(t, "index"); code != 0 {
		t.Fatal(code, reply)
	}
	code, reply = j.run(t, "search", `"writes version two"`)
	if code != 0 || reply["total"] != float64(1800) {
		t.Fatal(code, reply)
	}
}
func TestConcurrentSearchDuringIndex(t *testing.T) {
	j := newJourney(t)
	buildCorpus(t, j, 50, 0, "one")
	j.ready(t)
	buildCorpus(t, j, 2400, 50, "one")
	cmd := startIndex(t, j)
	completed := make(chan error, 1)
	go func() { completed <- cmd.Wait() }()
	totals := []float64{}
	for {
		select {
		case err := <-completed:
			if err != nil {
				t.Fatal(err)
			}
			goto finished
		default:
		}
		code, reply := j.run(t, "search", "lighthouse keeper")
		if code != 0 || reply["index_age_s"] == nil {
			t.Fatal(code, reply)
		}
		totals = append(totals, reply["total"].(float64))
	}
finished:
	if len(totals) < 2 {
		t.Fatal("fewer than two concurrent searches", totals)
	}
	for _, total := range totals {
		if total < 50 || total > 2450 {
			t.Fatal(totals)
		}
	}
	code, reply := j.run(t, "search", "lighthouse keeper")
	if code != 0 || reply["total"] != float64(2450) {
		t.Fatal(code, reply)
	}
	fmt.Fprintf(os.Stderr, "concurrent search: %d replies while index ran, totals %.0f through %.0f\n", len(totals), totals[0], totals[len(totals)-1])
}
func TestCorruptIndexRecovery(t *testing.T) {
	j := newJourney(t)
	j.put(t, "alpha/README.md", "# Keeper\n\nThe lighthouse keeper tends the lamp.")
	j.ready(t)
	if err := os.WriteFile(j.state+"/index.sqlite", []byte("broken index"), 0600); err != nil {
		t.Fatal(err)
	}
	code, reply := j.run(t, "search", "keeper")
	if code != 1 || reply["error"].(map[string]any)["code"] != "index-missing" {
		t.Fatal(code, reply)
	}
	if code, reply = j.run(t, "index", "--full"); code != 0 {
		t.Fatal(code, reply)
	}
	code, reply = j.run(t, "search", "keeper")
	if code != 0 || reply["total"] != float64(1) {
		t.Fatal(code, reply)
	}
}
