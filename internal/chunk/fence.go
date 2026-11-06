package chunk

import "strings"

type Fence struct {
	marker byte
	length int
}

func (f *Fence) Consume(line string) bool {
	text := strings.TrimLeft(line, " ")
	if len(line)-len(text) > 3 || text == "" {
		return f.length > 0
	}
	marker := text[0]
	run := 0
	for run < len(text) && text[run] == marker {
		run++
	}
	if f.length > 0 {
		if marker == f.marker && run >= f.length && strings.Trim(text[run:], " \t\r") == "" {
			f.length = 0
		}
		return true
	}
	if run < 3 || marker != '`' && marker != '~' || marker == '`' && strings.ContainsRune(text[run:], '`') {
		return false
	}
	f.marker, f.length = marker, run
	return true
}
