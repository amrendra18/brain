package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestConcurrentRecentDuringIndex(t *testing.T) {
	j := newJourney(t)
	j.put(t, "README.md", "# Keeper\n\nA lasting keeper.")
	j.ready(t)
	path := filepath.Join(j.state, "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err = json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	config["options"].(map[string]any)["inline_refresh_limit"] = 1
	data, err = json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		for round := 0; round < 12; round++ {
			for number := 0; number < 80; number++ {
				name := filepath.Join(j.source, fmt.Sprintf("changing-%03d.md", number))
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
			command := exec.Command(testBinary, "index", "--state-dir", j.state, "--format", "json")
			command.Dir = j.base
			command.Env = j.environment
			if data, err := command.CombinedOutput(); err != nil {
				done <- fmt.Errorf("index: %w: %s", err, data)
				return
			}
		}
		done <- nil
	}()
	reads := 0
	for {
		code, reply := j.run(t, "recent", "--limit", "20")
		if code != 0 {
			<-done
			t.Fatal(code, reply)
		}
		count := reply["count"].(float64)
		total := reply["total"].(float64)
		if total < count || count != float64(len(reply["documents"].([]any))) {
			<-done
			t.Fatal("inconsistent recent reply", reply)
		}
		reads++
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
			if reads < 2 {
				t.Fatal("no overlapping reads")
			}
			t.Logf("%d valid recent replies during twelve real index processes", reads)
			return
		default:
		}
	}
}
