package convert

import (
	"brain/internal/chunk"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestConvertedHeadingMetadataBounded(t *testing.T) {
	heading := strings.Repeat("界", chunk.Cap*2) + "convertedmarker"
	for _, path := range []string{"call.rtf", "call.docx", strings.Repeat("é", chunk.Cap+1) + ".rtf"} {
		pieces := Chunks(path, "Opening.\n\n# "+heading+"\n\nbodymarker")
		var body strings.Builder
		for _, piece := range pieces {
			if !utf8.ValidString(piece.Heading) || utf8.RuneCountInString(piece.Heading) > chunk.Cap {
				t.Fatalf("%s: oversized heading", path)
			}
			body.WriteString(piece.Text)
		}
		if !strings.Contains(body.String(), heading) || !strings.Contains(body.String(), "bodymarker") {
			t.Fatal("converted text was lost")
		}
	}
}

func TestFrontMatterHeadingOverflow(t *testing.T) {
	for _, body := range []string{"", "# Heading\n\nbodymarker"} {
		name := strings.Repeat("界", chunk.Cap) + "namemarker"
		description := strings.Repeat("é", chunk.Cap) + "descriptionmarker"
		pieces := Chunks("SKILL.md", "---\nname: "+name+"\ndescription: "+description+"\n---\n"+body)
		if len(pieces) == 0 {
			t.Fatal("oversized front matter disappeared")
		}
		var text strings.Builder
		for _, piece := range pieces {
			if !utf8.ValidString(piece.Heading) || utf8.RuneCountInString(piece.Heading) > chunk.Cap {
				t.Fatal("oversized front-matter label")
			}
			text.WriteString(piece.Text)
		}
		for _, want := range []string{name, description} {
			if !strings.Contains(text.String(), want) {
				t.Fatal("front-matter value lost from searchable text")
			}
		}
		if body != "" && !strings.Contains(text.String(), "bodymarker") {
			t.Fatal("body lost")
		}
	}
	ordinary := Chunks("SKILL.md", "---\nname: Keeper\ndescription: ordinary\n---\n# Start\n\nbodymarker")
	want := []chunk.Piece{{Heading: "Keeper: ordinary > Start", Text: "# Start\n\nbodymarker"}}
	if !reflect.DeepEqual(ordinary, want) {
		t.Fatalf("ordinary front matter changed: %#v", ordinary)
	}
	prefix := strings.Repeat("é", chunk.Cap)
	combined := Chunks("SKILL.md", "---\nname: "+prefix+"\n---\n# Heading\n\nbodymarker")
	if len(combined) != 1 || combined[0].Heading != prefix || combined[0].Text != "# Heading\n\nbodymarker" {
		t.Fatal("combined heading bound changed ordinary body")
	}
}
