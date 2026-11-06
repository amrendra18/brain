package main

import (
	"brain/tests/testutil"
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestScaleTenX(t *testing.T) {
	if os.Getenv("BRAIN_CHECK_LANE") != "full" {
		t.Skip("the 10000-file scale proof runs only in the full lane")
	}
	if os.Getenv("BRAIN_SCALE_ONCE") == "completed" {
		t.Skip("the scale proof already ran once in this full lane")
	}
	j := newJourney(t)
	random := rand.New(rand.NewSource(20260105))
	vocabulary := make([]string, 30000)
	for i := range vocabulary {
		vocabulary[i] = fmt.Sprintf("term%05d", i)
	}
	zipf := rand.NewZipf(random, 1.05, 1, 29999)
	sourceBytes := 0
	for number := 0; number < 10000; number++ {
		var text strings.Builder
		fmt.Fprintf(&text, "# Document %d\n", number)
		for section := 0; section < 10; section++ {
			fmt.Fprintf(&text, "\n## Section %d of document %d\n\n", section, number)
			for word := 0; word < 140; word++ {
				text.WriteString(vocabulary[zipf.Uint64()])
				text.WriteByte(' ')
			}
		}
		body := text.String()
		if len(body) > 11000 {
			body = body[:11000]
		}
		if len(body) < 11000 {
			body += strings.Repeat(" ", 11000-len(body))
		}
		j.put(t, fmt.Sprintf("project%d/area%d/note%d.md", number%100, number%7, number), body)
		sourceBytes += len(body)
	}
	if sourceBytes != 110000000 {
		t.Fatal("scale corpus size", sourceBytes)
	}
	if code, reply := j.run(t, "init", "--source", j.source); code != 0 {
		t.Fatal(code, reply)
	}
	start := time.Now()
	code, reply := j.run(t, "index")
	indexed := time.Since(start)
	if code != 0 || reply["new"] != float64(10000) {
		t.Fatal(code, reply)
	}
	chunks := int(reply["chunks"].(float64))
	var slowest time.Duration
	for _, query := range []string{"term00000", "term00007 term00023", "term00619 term00211"} {
		start = time.Now()
		code, reply = j.run(t, "search", query)
		took := time.Since(start)
		if took > slowest {
			slowest = took
		}
		if code != 0 || reply["total"] == float64(0) {
			t.Fatal(code, reply)
		}
	}
	rescans := []time.Duration{}
	for n := 0; n < 3; n++ {
		start = time.Now()
		code, reply = j.run(t, "index")
		rescans = append(rescans, time.Since(start))
		if code != 3 || reply["unchanged"] != float64(10000) {
			t.Fatal(code, reply)
		}
	}
	sort.Slice(rescans, func(i, j int) bool { return rescans[i] < rescans[j] })
	rescan := rescans[1]
	fmt.Fprintf(os.Stderr, "ten-times corpus: 10000 files, %d bytes; full index %.6f s (bar 30 s); slowest search %.6f s (bar 2 s); rescan %.6f s (bar 1 s)\n", sourceBytes, indexed.Seconds(), slowest.Seconds(), rescan.Seconds())
	if indexed >= 30*time.Second || slowest >= 2*time.Second || rescan >= time.Second {
		t.Fatal(indexed, slowest, rescan)
	}
	helper := exec.Command(os.Args[0])
	helper.Env = append(os.Environ(), "BRAIN_TEST_ORPHAN_WAL="+j.state)
	if output, err := helper.CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	os.Remove(filepath.Join(j.state, "index.sqlite-shm"))
	var diskBytes int64
	for _, suffix := range []string{"", "-wal"} {
		info, err := os.Stat(filepath.Join(j.state, "index.sqlite"+suffix))
		if err != nil {
			t.Fatal(err)
		}
		diskBytes += info.Size()
	}
	fmt.Fprintf(os.Stderr, "ten-times orphan snapshot: database plus WAL %d bytes; stored chunks %d\n", diskBytes, chunks)
	before := treeBytes(t, j.state)
	testutil.Permissions(t, j.state, 0500)
	code, reply = j.run(t, "search", "term00000", "--no-refresh")
	testutil.Permissions(t, j.state, 0700)
	if code != 0 || reply["total"] == float64(0) || reply["read_only"] != true || reply["index_age_s"].(float64) < 1000000000 {
		t.Fatal("scale orphan read lost facts", code, reply)
	}
	for _, format := range []string{"text", "copilot", "vscode"} {
		arguments := []string{testBinary, "--brief", "--format", format, "--state-dir", j.state}
		if runtime.GOOS == "darwin" {
			profile := "(version 1) (allow default) (deny file-write* (subpath " + strconv.Quote(j.state) + "))"
			arguments = append([]string{"/usr/bin/sandbox-exec", "-p", profile}, arguments...)
		}
		command := exec.Command(arguments[0], arguments[1:]...)
		command.Env = j.environment
		command.Dir = j.base
		var stderr bytes.Buffer
		command.Stderr = &stderr
		data, err := command.Output()
		if err != nil || stderr.Len() != 0 {
			t.Fatal(err, string(data), stderr.String())
		}
		text := string(data)
		if format != "text" {
			var wrapper map[string]any
			if json.Unmarshal(data, &wrapper) != nil {
				t.Fatal(string(data))
			}
			if format == "copilot" {
				text = wrapper["additionalContext"].(string)
			} else {
				text = wrapper["hookSpecificOutput"].(map[string]any)["additionalContext"].(string)
			}
		}
		if !strings.Contains(text, fmt.Sprintf("10000 files, %d chunks", chunks)) || !strings.Contains(text, "last scan 1") || !strings.Contains(text, "doctor") {
			t.Fatal("scale orphan brief lost facts", text)
		}
		if form := briefCommandForm(t, text, testBinary, j.state); form != "both" && form != "search" {
			t.Fatal("scale orphan brief lost its commands", text)
		}
	}
	if !reflect.DeepEqual(before, treeBytes(t, j.state)) {
		t.Fatal("scale orphan reader changed state")
	}
}
