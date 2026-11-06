package main

import (
	"strings"
	"testing"
)

func TestFrontMatter(t *testing.T) {
	j := newJourney(t)
	j.put(t, "tools/skills/moor/SKILL.md", "---\nname: moor-skiff\ndescription: \"Moor the skiff at the harbour wall\"\nmetadata:\n  secret: zebra\n---\n\n# Moor\n\nTie two lines.\n\n## Release\n\nUntie them.")
	j.ready(t)
	code, reply := j.run(t, "search", "skiff harbour")
	if code != 0 || reply["total"] != float64(1) {
		t.Fatal(code, reply)
	}
	first := result(t, reply, 0)
	if first["heading"] != "moor-skiff: Moor the skiff at the harbour wall > Moor" || first["kind"] != "skill" || first["snippet"] != "# Moor\n\nTie two lines." {
		t.Fatal(first)
	}
	code, reply = j.run(t, "search", "untie")
	if code != 0 || result(t, reply, 0)["heading"] != "Moor > Release" {
		t.Fatal(code, reply)
	}
	for _, word := range []string{"metadata", "zebra", "secret"} {
		code, reply = j.run(t, "search", word)
		if code != 0 || reply["total"] != float64(0) {
			t.Fatal(word, code, reply)
		}
	}
	if strings.Contains(first["snippet"].(string), "description:") {
		t.Fatal("front matter indexed as body")
	}
}
