package convert

import (
	"archive/zip"
	"bytes"
	"strconv"
	"strings"
	"testing"
)

func zipDocument(t *testing.T, name, text string) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	file, err := writer.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.Write([]byte(text)); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
func TestDocxText(t *testing.T) {
	xml := `<document><body><p><pPr><pStyle other="unused" val="Heading1"/></pPr><r><t>Title</t></r></p><p><hyperlink><r><t>Link text</t><tab/><t>after tab</t><br/><t>next line</t></r></hyperlink><del><r><t>deleted</t></r></del></p><p><pPr><pStyle val="ListBullet"/></pPr><r><t>Bullet</t></r></p><p><pPr><pStyle val="HeadingBad"/></pPr><r><t>Plain</t></r></p><p/><tbl><tr><tc><p><r><t>A</t></r></p><p><r><t>B</t></r></p></tc><tc><p><r><t>C</t></r></p></tc></tr></tbl></body></document>`
	got, err := Docx(zipDocument(t, "word/document.xml", xml))
	if err != nil {
		t.Fatal(err)
	}
	if got != "# Title\n\nLink text\tafter tab\nnext line\n\n- Bullet\n\nPlain\n\nA B | C" {
		t.Fatal(got)
	}
	for _, invalid := range [][]byte{[]byte("not a zip"), zipDocument(t, "other.xml", xml), zipDocument(t, "word/document.xml", "<broken>"), zipDocument(t, "word/document.xml", "<tbl><tbl/></tbl>"), zipDocument(t, "word/document.xml", strings.Repeat("x", 16*1024*1024+1))} {
		if _, err := Docx(invalid); err == nil {
			t.Fatal("bad docx accepted")
		}
	}
	unsupported := zipDocument(t, "word/document.xml", "<document/>")
	for i := 0; i+12 < len(unsupported); i++ {
		if bytes.Equal(unsupported[i:i+4], []byte{'P', 'K', 3, 4}) {
			unsupported[i+8] = 99
		}
		if bytes.Equal(unsupported[i:i+4], []byte{'P', 'K', 1, 2}) {
			unsupported[i+10] = 99
		}
	}
	if _, err := Docx(unsupported); err == nil {
		t.Fatal("unsupported compression accepted")
	}
	if _, err := documentText([]byte("<p><pPr><pStyle val='Heading7'/></pPr><t>Plain</t></p>")); err != nil {
		t.Fatal(err)
	}
}

func TestDocxRejectsNestedBlocks(t *testing.T) {
	for i, body := range []string{
		strings.Repeat("<p>", 4096) + "<r><t>BOUNDARYMARKER" + strings.Repeat(" safe words", 400) + "</t></r>" + strings.Repeat("</p>", 4096),
		"<tbl><tr><tr><tc><p><t>repeated row</t></p></tc></tr></tr></tbl>",
		"<tbl><tr><tc><tc><p><t>repeated cell</t></p></tc></tc></tr></tbl>",
		"<p><tbl><tr><tc><p><t>nested block</t></p></tc></tr></tbl></p>",
		"<tbl><tr><p><tc><p><t>nested cell</t></p></tc></p></tr></tbl>",
	} {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			if text, err := Docx(zipDocument(t, "word/document.xml", body)); err == nil || text != "" {
				t.Fatalf("malformed blocks produced %d bytes, error %v", len(text), err)
			}
		})
	}
}

func TestDocxSiblingBlocks(t *testing.T) {
	body := `<p><t>First</t></p><p><t>Second</t></p><tbl><tr><tc><p><t>A</t></p><p/><p><t>B</t></p></tc><tc><p><t>C</t></p></tc></tr><tr><tc><p><t>D</t></p></tc><tc/></tr><tr/></tbl><p><t>Last</t></p>`
	text, err := Docx(zipDocument(t, "word/document.xml", "<document>"+body+"</document>"))
	if err != nil || text != "First\n\nSecond\n\nA B | C\n\nD | \n\nLast" {
		t.Fatalf("sibling blocks changed: %q, %v", text, err)
	}
}

func TestDocxProducedTextBound(t *testing.T) {
	const maximum = 16 * 1024 * 1024
	for _, test := range []struct{ name, body string }{
		{"paragraph", "<p><t>" + strings.Repeat("x", maximum+1) + "</t></p>"},
		{"tab", "<p><t>" + strings.Repeat("x", maximum) + "</t><tab/></p>"},
		{"break", "<p><t>" + strings.Repeat("x", maximum) + "</t><br/></p>"},
		{"table", "<tbl>" + strings.Repeat("<tr><tc><p><t>"+strings.Repeat("x", maximum/2)+"</t></p></tc></tr>", 2) + "</tbl>"},
		{"lines", strings.Repeat("<p><t>"+strings.Repeat("x", maximum/2)+"</t></p>", 2)},
		{"cell", "<tbl><tr><tc>" + strings.Repeat("<p><t>"+strings.Repeat("x", maximum/2)+"</t></p>", 2) + "</tc></tr></tbl>"},
		{"row", "<tbl><tr>" + strings.Repeat("<tc><p><t>"+strings.Repeat("x", maximum/2)+"</t></p></tc>", 2) + "</tr></tbl>"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if text, err := documentText([]byte("<document>" + test.body + "</document>")); err == nil || text != "" {
				t.Fatalf("oversized output produced %d bytes, error %v", len(text), err)
			}
		})
	}
}

func TestDocxTextAtBound(t *testing.T) {
	const maximum = 16 * 1024 * 1024
	text, err := documentText([]byte("<p><t>" + strings.Repeat("x", maximum) + "</t></p>"))
	if err != nil || len(text) != maximum || strings.Count(text, "x") != maximum {
		t.Fatalf("exact output bound changed: %d bytes, %v", len(text), err)
	}
}

func TestDocxTextboxes(t *testing.T) {
	box := func(text string) string {
		return "<pict><shape><textbox><txbxContent>" + text + "</txbxContent></textbox></shape></pict>"
	}
	paragraph := func(text string) string { return "<p><r><t>" + text + "</t></r></p>" }
	for _, test := range []struct{ name, body, want string }{
		{"body", paragraph("beforemarker") + "<p>" + box(paragraph("textboxmarker")) + "</p>" + paragraph("aftermarker"), "beforemarker\n\ntextboxmarker\n\naftermarker"},
		{"same paragraph", "<p><t>beforemarker</t>" + box(paragraph("textboxmarker")) + "<t>aftermarker</t></p>", "beforemarker\n\ntextboxmarker\n\naftermarker"},
		{"siblings", "<p><t>beforemarker</t>" + box(paragraph("firstboxmarker")) + box(paragraph("secondboxmarker")) + "<t>aftermarker</t></p>", "beforemarker\n\nfirstboxmarker\n\nsecondboxmarker\n\naftermarker"},
		{"nested", "<p><t>beforemarker</t>" + box("<p><t>outerboxmarker</t>"+box(paragraph("innerboxmarker"))+"<t>boxaftermarker</t></p>") + "<t>aftermarker</t></p>", "beforemarker\n\nouterboxmarker\n\ninnerboxmarker\n\nboxaftermarker\n\naftermarker"},
		{"cell", "<tbl><tr><tc><p><t>beforemarker</t>" + box(paragraph("textboxmarker")) + "<t>aftermarker</t></p></tc><tc>" + paragraph("siblingcellmarker") + "</tc></tr></tbl>", "beforemarker textboxmarker aftermarker | siblingcellmarker"},
		{"table in box", "<p><t>beforemarker</t>" + box("<tbl><tr><tc>"+paragraph("textboxmarker")+"</tc><tc>"+paragraph("cellmarker")+"</tc></tr></tbl>") + "<t>aftermarker</t></p>", "beforemarker\n\ntextboxmarker | cellmarker\n\naftermarker"},
		{"empty box", "<p><t>beforemarker</t>" + box("<p/>") + "<t>aftermarker</t></p>", "beforemarker\n\naftermarker"},
		{"deleted box", "<p><t>beforemarker</t><del>" + box(paragraph("deletedmarker")) + "</del><t>aftermarker</t></p>", "beforemarker\n\naftermarker"},
		{"styles and deletion", "<p><pPr><pStyle val='Heading1'/></pPr><t>beforemarker</t>" + box("<p><pPr><pStyle val='ListBullet'/></pPr><hyperlink><r><t>textboxmarker</t></r></hyperlink><del><t>deletedmarker</t></del></p>") + "<t>aftermarker</t></p>", "# beforemarker\n\n- textboxmarker\n\n# aftermarker"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := Docx(zipDocument(t, "word/document.xml", "<document><body>"+test.body+"</body></document>"))
			if err != nil || got != test.want {
				t.Fatalf("textbox content changed: %q, error %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestDocxDeepTextboxes(t *testing.T) {
	body := strings.Repeat("<p><txbxContent>", 4096) + "<p><t>singlemarker</t></p>" + strings.Repeat("</txbxContent></p>", 4096)
	got, err := Docx(zipDocument(t, "word/document.xml", body))
	if err != nil || got != "singlemarker" {
		t.Fatalf("nested boxes repeated or lost text: %q, %v", got, err)
	}
}

func TestDocxTextboxBounds(t *testing.T) {
	const maximum = 16 * 1024 * 1024
	body := "<p><t>" + strings.Repeat("x", maximum/2) + "</t></p><p><t>" + strings.Repeat("y", maximum/2) + "</t><txbxContent><p><t>box</t></p></txbxContent></p>"
	if got, err := documentText([]byte("<document>" + body + "</document>")); err == nil || got != "" {
		t.Fatalf("textbox flush exceeded output bound: %d bytes, %v", len(got), err)
	}
	for _, body := range []string{
		"<txbxContent><p><t>misplaced box</t></p></txbxContent>",
		"<p><txbxContent><p><p><t>nested paragraph</t></p></p></txbxContent></p>",
		"<tbl><tr><tc><p><txbxContent><tbl><tr><tc><p><t>nested table</t></p></tc></tr></tbl></txbxContent></p></tc></tr></tbl>",
	} {
		if got, err := Docx(zipDocument(t, "word/document.xml", body)); err == nil || got != "" {
			t.Fatalf("unsupported textbox structure accepted: %q, %v", got, err)
		}
	}
}
