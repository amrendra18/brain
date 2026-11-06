package convert

import (
	"reflect"
	"strings"
	"testing"
)

func TestFrontMatter(t *testing.T) {
	for _, test := range []struct{ text, prefix, body string }{
		{"---\nname: a\ndescription: b c\n---\nBody", "a: b c", "Body"},
		{"---\r\nname: 'quoted'\r\n...\r\nBody", "quoted", "Body"},
		{"---\ndescription: only\nname:\n  name: nested\n---\n", "only", ""},
		{"---\ntitle: ignored\n---\nBody", "", "Body"},
		{"---\nname: never closed\nBody", "", "---\nname: never closed\nBody"},
		{"# No block\n---\n", "", "# No block\n---\n"},
	} {
		prefix, body := FrontMatter(test.text)
		if prefix != test.prefix || body != test.body {
			t.Fatal(test, prefix, body)
		}
	}
	pieces := Chunks("SKILL.md", "---\nname: Moor\ndescription: Harbour wall\nmetadata: zebra\n---\n# Tie\n\nTwo lines.\n\n## Release\n\nUntie them.")
	if len(pieces) != 2 || pieces[0].Heading != "Moor: Harbour wall > Tie" || pieces[1].Heading != "Tie > Release" || strings.Contains(pieces[0].Text, "zebra") {
		t.Fatal(pieces)
	}
	if pieces = Chunks("SKILL.md", "---\nname: Moor\n---\n"); len(pieces) != 0 {
		t.Fatal(pieces)
	}
	pieces = Chunks("call.rtf", "Opening.\n\n# Next\n\nMore.")
	if len(pieces) != 2 || pieces[0].Heading != "call.rtf" || pieces[1].Heading != "Next" {
		t.Fatal(pieces)
	}
}

func TestRtfText(t *testing.T) {
	for _, test := range []struct{ data, want string }{
		{`{\rtf1\ansi Caf\'e9 lantern\par At dusk.\par}`, "Café lantern\nAt dusk.\n"},
		{`{\rtf1\'80\'91\'92}`, "€‘’"},
		{"{\\rtf1 body}\n ", "body"},
		{"{\\rtf1 one\\\r\ntwo}", "one\ntwo"},
		{`{\rtf1\ansi\ansicpg1252{\fonttbl{\f0 hidden;}}{\colortbl;hidden;}{\*\unknown hidden}Kept \{brace\} \\ slash\line A\tab B\~C\_D\-E}`, "Kept {brace} \\ slash\nA\tB\u00a0C\u2011DE"},
		{`{\rtf1\uc1\u233? \uc0\u945  {\uc2\u946??} \u947\par}`, "é α β γ\n"},
		{`{\rtf1\u-10179?\u-8704?\par}`, "😀\n"},
		{"{\\rtf1 first\\\nsecond\nthird\\par}", "first\nsecondthird\n"},
		{`{\rtf1\b bold\b0 {\i italic} plain\emdash\endash\lquote\rquote\ldblquote\rdblquote\bullet}`, "bolditalic plain\u2014\u2013\u2018\u2019\u201c\u201d\u2022"},
	} {
		got, err := Rtf([]byte(test.data))
		if err != nil || got != test.want {
			t.Fatalf("%q: %q (%v), want %q", test.data, got, err, test.want)
		}
	}
	for _, bad := range []string{"plain", "{\\rtf1", "{\\rtf1}}", `{\rtf1\'zz}`, `{\rtf1\'f}`, `{\rtf1\bin3 abc}`, `{\rtf1\pict image}`, `{\rtf1\ansicpg932 text}`, `{\rtf1\uc-1 text}`, `{\rtf1\u999999 text}`, "{\\rtf1\\}", "{\\rtf1}garbage", "{\\rtf1\\", `{\rtf10 wrong}`, `{\rtf1\unknown9999999999999999999999 text}`, `{\rtf1\@ text}`, `{\rtf1\'f`} {
		if _, err := Rtf([]byte(bad)); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	if !reflect.DeepEqual(IsConverted("X.DOCX"), true) || IsConverted("x.md") {
		t.Fatal("suffix detection")
	}
}
