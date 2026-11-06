package convert

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func publicGolden(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "tests", "golden", name))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(data))
}

func TestSyntheticRtfGolden(t *testing.T) {
	got, err := Rtf([]byte(publicGolden(t, "sample.rtf")))
	if err != nil || strings.TrimSpace(got) != publicGolden(t, "sample.txt") {
		t.Fatalf("synthetic RTF conversion changed: %q, %v", got, err)
	}
}

func TestSyntheticDocxGolden(t *testing.T) {
	xml := `<document><body><p><pPr><pStyle val="Heading1"/></pPr><t>Harbor checklist</t></p><p><t>Check the beacon.</t></p><tbl><tr><tc><p><t>North dock</t></p></tc><tc><p><t>Open at dawn.</t></p></tc></tr></tbl></body></document>`
	got, err := Docx(zipDocument(t, "word/document.xml", xml))
	if err != nil || strings.TrimSpace(got) != publicGolden(t, "sample-docx.txt") {
		t.Fatalf("synthetic DOCX conversion changed: %q, %v", got, err)
	}
}

func TestSyntheticLongDocxPreservesHeadingsAndText(t *testing.T) {
	var body strings.Builder
	var expected []string
	for i := 1; i <= 39; i++ {
		heading := fmt.Sprintf("Section %02d", i)
		paragraph := fmt.Sprintf("Synthetic detail %02d stays readable.", i)
		fmt.Fprintf(&body, `<p><pPr><pStyle val="Heading1"/></pPr><t>%s</t></p><p><t>%s</t></p>`, heading, paragraph)
		expected = append(expected, "# "+heading, paragraph)
	}
	got, err := Docx(zipDocument(t, "word/document.xml", "<document><body>"+body.String()+"</body></document>"))
	if err != nil || got != strings.Join(expected, "\n\n") {
		t.Fatalf("long DOCX lost a heading or paragraph: %v", err)
	}
}

func TestSyntheticLongRtfPreservesParagraphs(t *testing.T) {
	var source strings.Builder
	var expected []string
	source.WriteString(`{\rtf1 `)
	for i := 1; i <= 200; i++ {
		paragraph := fmt.Sprintf("Synthetic paragraph %03d remains searchable.", i)
		source.WriteString(paragraph)
		source.WriteString(`\par `)
		expected = append(expected, paragraph)
	}
	source.WriteByte('}')
	got, err := Rtf([]byte(source.String()))
	if err != nil || strings.TrimSpace(got) != strings.Join(expected, "\n") {
		t.Fatalf("long RTF lost a paragraph: %v", err)
	}
}
