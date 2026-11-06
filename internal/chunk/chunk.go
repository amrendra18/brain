package chunk

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

const Version = 6
const Cap = 1500

type Piece struct{ Heading, Text string }
type title struct {
	level int
	text  string
}
type section struct {
	path  string
	lines []string
	body  bool
}

var heading = regexp.MustCompile("^ {0,3}(#{1,6})[\\t ]+(.*?)(?:[\\t ]+#+)?[\\t ]*$")
var paragraphBreak = regexp.MustCompile("\\n(?:[\\t ]*\\n)+")

func Split(text string) []Piece {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	sections := []section{}
	current := section{}
	titles := []title{}
	var fence Fence
	for _, line := range strings.Split(text, "\n") {
		var match []string
		if !fence.Consume(line) {
			match = heading.FindStringSubmatch(line)
		}
		if len(match) != 0 && match[2] != "" {
			sections = append(sections, current)
			level := len(match[1])
			for len(titles) != 0 && titles[len(titles)-1].level >= level {
				titles = titles[:len(titles)-1]
			}
			titles = append(titles, title{level, BoundHeading(match[2])})
			names := make([]string, len(titles))
			for i, t := range titles {
				names[i] = t.text
			}
			current = section{path: BoundHeading(strings.Join(names, " > ")), lines: []string{line}}
		} else {
			current.lines = append(current.lines, line)
			current.body = current.body || strings.TrimSpace(line) != ""
		}
	}
	sections = append(sections, current)
	result := []Piece{}
	stub := []string{}
	stubPath := ""
	add := func(path, body string) {
		for _, text := range capped(strings.TrimSpace(body)) {
			result = append(result, Piece{path, text})
		}
	}
	for _, section := range sections {
		if !section.body {
			if section.path != "" {
				stub = append(stub, section.lines...)
				stubPath = section.path
			}
			continue
		}
		add(section.path, strings.Join(append(stub, section.lines...), "\n"))
		stub = nil
	}
	if len(stub) != 0 {
		add(stubPath, strings.Join(stub, "\n"))
	}
	return result
}

func BoundHeading(text string) string {
	count := 0
	for offset := range text {
		if count == Cap {
			return strings.Clone(text[:offset])
		}
		count++
	}
	return text
}

func Key(text string) string {
	digest := sha256.Sum256([]byte(strings.ToLower(strings.Join(strings.Fields(text), " "))))
	return hex.EncodeToString(digest[:])
}

// DocumentKey keeps repeated text together only within one source document.
func DocumentKey(namespace, relative, text string) string {
	digest := sha256.Sum256([]byte(namespace + "\x00" + relative + "\x00" + Key(text)))
	return hex.EncodeToString(digest[:])
}
func ID(key string) string { return key[:16] }

func capped(text string) []string {
	if text == "" {
		return nil
	}
	if len([]rune(text)) <= Cap {
		return []string{text}
	}
	paragraphs := []string{}
	for _, block := range paragraphBreak.Split(text, -1) {
		if len([]rune(block)) <= Cap {
			paragraphs = append(paragraphs, block)
			continue
		}
		lines := []string{}
		for _, line := range strings.Split(block, "\n") {
			runes := []rune(line)
			for start := 0; start < len(runes); start += Cap {
				end := min(start+Cap, len(runes))
				lines = append(lines, string(runes[start:end]))
			}
		}
		paragraphs = append(paragraphs, pack(lines, "\n")...)
	}
	result := []string{}
	for _, part := range pack(paragraphs, "\n\n") {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}
func pack(parts []string, joiner string) []string {
	packed := []string{}
	for _, part := range parts {
		last := len(packed) - 1
		if last >= 0 && len([]rune(packed[last]))+len(joiner)+len([]rune(part)) <= Cap {
			packed[last] += joiner + part
		} else {
			packed = append(packed, part)
		}
	}
	return packed
}
