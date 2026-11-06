package platform

import "testing"

func TestPortableReservedNames(t *testing.T) {
	for _, name := range []string{"CON", "prn", "aUx", "nul.txt", "COM1", "com9.docx", "LPT1", "lpt9.rtf", "COM¹", "COM²", "COM³", "LPT¹", "LPT²", "LPT³", "COM¹.md", "lpt² .txt", "CONIN$", "conout$", "CONIN$.md", "NUL:stream", "AUX .md", "COM1 "} {
		if !ReservedName(name) {
			t.Fatal("device name accepted", name)
		}
	}
	for _, name := range []string{"", "company.md", "COM0", "LPT0", "COM10", "LPT10", "COM⁴", "CONSOLE", "CONIN", "CONOUT", "_COM1", "harbour.md"} {
		if ReservedName(name) {
			t.Fatal("ordinary name excluded", name)
		}
	}
}
