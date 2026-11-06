package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestIndexExitContract(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		t.Run(map[bool]string{false: "failed-only", true: "mixed"}[mixed], func(t *testing.T) {
			j := newJourney(t)
			j.put(t, "bad.docx", "not a DOCX")
			if mixed {
				j.put(t, "good.md", "# Keeper\n\nLighthouse ordinary control.")
			}
			before := treeBytes(t, j.source)
			if code, reply := j.run(t, "init", "--source", j.source); code != 0 {
				t.Fatal(code, reply)
			}
			for _, args := range [][]string{{"index"}, {"index"}, {"index", "--full"}} {
				code, reply := j.run(t, args...)
				if code != 1 || reply["error"] != nil || reply["count"] != float64(1) || reply["total"] != float64(1) || reply["empty"] != false || len(reply["skipped"].([]any)) != 1 || reply["skipped"].([]any)[0].(map[string]any)["reason"] != "conversion-failed" || len(reply["next"].([]any)) != 1 || len(reply["next_argv"].([]any)) != 1 {
					t.Fatal(args, code, reply)
				}
				if !reflect.DeepEqual(before, treeBytes(t, j.source)) {
					t.Fatal("index changed sources")
				}
				if code, reply = j.run(t, "doctor"); code != 1 || reply["count"].(float64) < 1 {
					t.Fatal(code, reply)
				}
				want := float64(0)
				if mixed {
					want = 1
				}
				if code, reply = j.run(t, "search", "lighthouse", "--no-refresh"); code != 0 || reply["count"] != want || reply["gaps"].(map[string]any)["count"] != float64(1) {
					t.Fatal(code, reply)
				}
			}
			var document bytes.Buffer
			archive := zip.NewWriter(&document)
			entry, err := archive.Create("word/document.xml")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = entry.Write([]byte("<document><body><p><r><t>Repaired quartz evidence.</t></r></p></body></document>")); err != nil {
				t.Fatal(err)
			}
			if err = archive.Close(); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(j.source, "bad.docx"), document.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			if code, reply := j.run(t, "index"); code != 0 || reply["count"] != float64(0) || reply["empty"] != true {
				t.Fatal(code, reply)
			}
			if code, reply := j.run(t, "index"); code != 3 || reply["count"] != float64(0) {
				t.Fatal(code, reply)
			}
			if code, reply := j.run(t, "search", "quartz", "--no-refresh"); code != 0 || reply["count"] != float64(1) || result(t, reply, 0)["path"] != filepath.Join(j.source, "bad.docx") {
				t.Fatal(code, reply)
			}
			if code, reply := j.run(t, "doctor"); code != 3 || reply["count"] != float64(0) {
				t.Fatal(code, reply)
			}
		})
	}
}
