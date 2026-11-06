package cli

import (
	"io"
	"strconv"
	"strings"
	"unicode"
)

func writeText(output io.Writer, text string) (int, error) {
	var safe strings.Builder
	for _, r := range text {
		if textControl(r) {
			escaped := strconv.QuoteRune(r)
			safe.WriteString(escaped[1 : len(escaped)-1])
		} else {
			safe.WriteRune(r)
		}
	}
	n, err := io.WriteString(output, safe.String())
	if err == nil && n != safe.Len() {
		err = io.ErrShortWrite
	}
	return n, err
}

func textControl(r rune) bool {
	return r != '\n' && r != '\t' && (unicode.IsControl(r) || r == '\u2028' || r == '\u2029' || r == '\u061c' || r == '\u200e' || r == '\u200f' || r >= '\u202a' && r <= '\u202e' || r >= '\u2066' && r <= '\u2069')
}

func hasTextControls(text string) bool {
	return strings.ContainsFunc(text, textControl)
}

func textCommand(command string) string {
	if hasTextControls(command) {
		return "Use --format json for exact next and next_argv. This command contains control characters."
	}
	return command
}
