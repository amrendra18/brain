package chunk

import (
	"reflect"
	"strings"
	"testing"
)

func TestSplit(t *testing.T) {
	for _, test := range []struct {
		text string
		want []Piece
	}{
		{"# Brain\n\nIntro.\n\n## Milestones\n\nList.\n\n### B1\n\nIndex.\n\n## Decisions\n\nOne.\n", []Piece{{"Brain", "# Brain\n\nIntro."}, {"Brain > Milestones", "## Milestones\n\nList."}, {"Brain > Milestones > B1", "### B1\n\nIndex."}, {"Brain > Decisions", "## Decisions\n\nOne."}}},
		{"Front text.\n\n# Title\n\nBody.\n", []Piece{{"", "Front text."}, {"Title", "# Title\n\nBody."}}},
		{"A memory note.\nSecond line.\n", []Piece{{"", "A memory note.\nSecond line."}}},
		{"", []Piece{}}, {"\n  \n\n", []Piece{}},
		{"# Guide\n\n```sh\n# not a heading\n```\n\n~~~\n## also not\n~~~\n\nAfter.\n", []Piece{{"Guide", "# Guide\n\n```sh\n# not a heading\n```\n\n~~~\n## also not\n~~~\n\nAfter."}}},
		{"# Plan\n\n## Milestones\n\n### B1\n\nIndex and search.\n", []Piece{{"Plan > Milestones > B1", "# Plan\n\n## Milestones\n\n### B1\n\nIndex and search."}}},
		{"# Notes\n\nBody.\n\n## Later\n\n### Empty\n", []Piece{{"Notes", "# Notes\n\nBody."}, {"Notes > Later > Empty", "## Later\n\n### Empty"}}},
		{"#hashtag\n    # code\n## \n#\n## Closed ##\n\nBody.\n", []Piece{{"", "#hashtag\n    # code\n## \n#"}, {"Closed", "## Closed ##\n\nBody."}}},
	} {
		if got := Split(test.text); !reflect.DeepEqual(got, test.want) {
			t.Fatalf("%q: %#v, want %#v", test.text, got, test.want)
		}
	}
	if got := Split("# A\n\na\n\n### C\n\nc\n\n## B\n\nb"); got[2].Heading != "A > B" {
		t.Fatal(got)
	}
	if got := Split("# Guide\n\n```\n~~~\n# still code\n```\n\n## Next\n\nText."); len(got) != 2 {
		t.Fatal(got)
	}
}
func TestCap(t *testing.T) {
	for _, text := range []string{strings.Repeat("word ", 4000), strings.Repeat("é", 4000), strings.Repeat("a", 1500) + strings.Repeat(" ", 1500) + strings.Repeat("b", 1500), strings.Repeat("x", 800) + "\n" + strings.Repeat("y", 800) + "\n" + strings.Repeat("z", 100), strings.Repeat("a", 900) + "\n\n" + strings.Repeat("b", 900)} {
		pieces := Split(text)
		if len(pieces) < 2 {
			t.Fatal("long input was not split")
		}
		for _, piece := range pieces {
			if len([]rune(piece.Text)) > Cap || strings.TrimSpace(piece.Text) == "" {
				t.Fatal("bad capped piece")
			}
		}
	}
	if got := Split(strings.Repeat("a", 700) + "\n\n" + strings.Repeat("b", 700)); len(got) != 1 {
		t.Fatal(got)
	}
}
func TestStableChunkIds(t *testing.T) {
	key := Key("## Shared\n\nSame  text here.")
	if len(key) != 64 || ID(key) != key[:16] || key != Key("## shared same text\there.") || key == Key("other") {
		t.Fatal(key)
	}
	if Split("# Text\r\n\r\nBody.")[0].Text != Split("# Text\n\nBody.")[0].Text {
		t.Fatal("line endings changed text")
	}
}

func TestDocumentKey(t *testing.T) {
	key := DocumentKey("source:0:", "alpha/README.md", "Same text.")
	if key != DocumentKey("source:0:", "alpha/README.md", "SAME  text.") {
		t.Fatal("normalized text changed the key")
	}
	for _, other := range []string{DocumentKey("source:1:", "alpha/README.md", "Same text."), DocumentKey("source:0:", "beta/README.md", "Same text."), DocumentKey("source:0:", "alpha/readme.md", "Same text.")} {
		if key == other {
			t.Fatal("distinct source or path shared the key")
		}
	}
}

func TestSplitFenceRules(t *testing.T) {
	for _, code := range []string{
		"````md\n```\n## Example\n````",
		"~~~md\n~~~ trailing text\n## Example\n~~~",
		"```md\n~~~\n## Example\n```",
	} {
		text := "# Guide\n\n" + code + "\n\n## Next\nBody."
		want := []Piece{{"Guide", "# Guide\n\n" + code}, {"Guide > Next", "## Next\nBody."}}
		if got := Split(text); !reflect.DeepEqual(got, want) {
			t.Errorf("%q: %#v, want %#v", text, got, want)
		}
	}
}
