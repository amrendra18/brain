package query

import (
	"reflect"
	"testing"
)

func TestQueryHygiene(t *testing.T) {
	for _, test := range []struct{ typed, first, fallback string }{
		{"sample-project", `"sample-project"`, ""}, {"GOAL.md pattern", `"GOAL.md" pattern`, `"GOAL.md" OR pattern`},
		{"src/brain lease_id project:lantern", `"src/brain" "lease_id" "project:lantern"`, `"src/brain" OR "lease_id" OR "project:lantern"`},
		{"what's C++", `"what's" "C++"`, `"what's" OR "C++"`},
		{"lease* ^start (a) -b", `"lease*" "^start" "(a)" "-b"`, `"lease*" OR "^start" OR "(a)" OR "-b"`},
		{`say "hello there" now`, `say "hello there" now`, `say OR "hello there" OR now`},
		{`broken "quote`, "broken quote", "broken OR quote"},
		{"drawbridge OR threat", "drawbridge OR threat", ""}, {"NOT a OR OR b AND", "a OR b", ""}, {"lowercase or not", "lowercase or not", "lowercase OR or OR not"},
		{"é 日本", "é 日本", "é OR 日本"},
		{"²", "²", ""}, {"Ⅵ", "Ⅵ", ""}, {"lease\u00a0keeper", "lease keeper", "lease OR keeper"},
	} {
		got := Make(test.typed, nil)
		if got == nil || got.First != test.first || got.Fallback != test.fallback {
			t.Fatalf("%s: %#v", test.typed, got)
		}
	}
	for _, empty := range []string{"", "   ", "??? - !", `""`, "AND OR"} {
		if Make(empty, nil) != nil {
			t.Fatal(empty)
		}
	}
	got := Make("Lantern lantern? quillpad", []string{"lantern", "sample-project", "QuillPad"})
	if !reflect.DeepEqual(got.Boost, []string{"lantern", "QuillPad"}) {
		t.Fatal(got)
	}
	if got := Make(`"sample-project" files`, []string{"sample-project"}); !reflect.DeepEqual(got.Boost, []string{"sample-project"}) {
		t.Fatal(got)
	}
	if plain("") || plain("a_b") {
		t.Fatal("plain identifier test changed")
	}
}

func TestQueryMode(t *testing.T) {
	for _, test := range []struct{ typed, mode string }{
		{"lease", "and"}, {"lease keeper", "and"}, {"lease AND keeper", "and"},
		{"lease OR boats", "or"}, {"lease OR boats OR harbour", "or"},
		{"lease NOT keeper", "expression"}, {"lease OR boats AND harbour", "expression"},
		{"lease keeper OR boats", "expression"}, {"lease OR boats harbour", "expression"},
		{`lease "OR"`, "and"}, {`"OR"`, "and"}, {"NOT lease OR OR boats AND", "or"},
	} {
		if plan := Make(test.typed, nil); plan == nil || plan.Mode != test.mode {
			t.Fatalf("%q: %+v; want %s", test.typed, plan, test.mode)
		}
	}
}
