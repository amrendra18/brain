package chunk

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestHeadingMetadataBounded(t *testing.T) {
	for _, text := range []string{
		"# " + strings.Repeat("界", Cap*3) + "endmarker\n\nbodymarker",
		"# " + strings.Repeat("a", Cap-1) + "\n\nparentbody\n\n" + strings.Repeat("## childmarker\n\nchildbody\n\n", 30),
		"# " + strings.Repeat("α", Cap) + "\n## " + strings.Repeat("β", Cap) + "\n### finalmarker",
		"# " + strings.Repeat("z", Cap) + "\n\n" + strings.Repeat("ordinarybody ", 4000),
	} {
		pieces := Split(text)
		var retained strings.Builder
		if len(pieces) < 2 {
			t.Fatal("long source did not produce multiple chunks")
		}
		for _, piece := range pieces {
			if !utf8.ValidString(piece.Heading) || utf8.RuneCountInString(piece.Heading) > Cap {
				t.Fatalf("heading has %d runes", utf8.RuneCountInString(piece.Heading))
			}
			if utf8.RuneCountInString(piece.Text) > Cap {
				t.Fatal("body exceeds chunk limit")
			}
			retained.WriteString(strings.Join(strings.Fields(piece.Text), ""))
		}
		if got, want := retained.String(), strings.Join(strings.Fields(text), ""); got != want {
			t.Fatal("source heading or body text was lost")
		}
	}
}

func TestOrdinaryHeadingsUnchanged(t *testing.T) {
	text := "# Root\n\nA body.\n\n## Child\n\nAnother body."
	want := []Piece{{Heading: "Root", Text: "# Root\n\nA body."}, {Heading: "Root > Child", Text: "## Child\n\nAnother body."}}
	if got := Split(text); !reflect.DeepEqual(got, want) {
		t.Fatalf("%#v", got)
	}
}
