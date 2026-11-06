package convert

import (
	"brain/internal/chunk"
	"path/filepath"
	"strings"
)

func IsConverted(path string) bool {
	suffix := strings.ToLower(filepath.Ext(path))
	return suffix == ".docx" || suffix == ".rtf"
}

func FrontMatter(text string) (string, string) {
	lines := strings.Split(text, "\n")
	if strings.TrimRight(lines[0], " \t\r") != "---" {
		return "", text
	}
	end := 0
	for i := 1; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], " \t\r")
		if line == "---" || line == "..." {
			end = i
			break
		}
	}
	if end == 0 {
		return "", text
	}
	fields := map[string]string{}
	for _, line := range lines[1:end] {
		key, value, ok := strings.Cut(line, ":")
		value = strings.TrimSpace(value)
		if len(value) > 1 && value[0] == value[len(value)-1] && (value[0] == '\'' || value[0] == '"') {
			value = value[1 : len(value)-1]
		}
		if ok && (key == "name" || key == "description") && value != "" {
			fields[key] = value
		}
	}
	prefix := []string{}
	for _, key := range []string{"name", "description"} {
		if value := fields[key]; value != "" {
			prefix = append(prefix, value)
		}
	}
	return strings.Join(prefix, ": "), strings.Join(lines[end+1:], "\n")
}

func Chunks(path, text string) []chunk.Piece {
	prefix := ""
	if !IsConverted(path) {
		prefix, text = FrontMatter(text)
		if bounded := chunk.BoundHeading(prefix); bounded != prefix {
			text = prefix + "\n\n" + text
			prefix = bounded
		}
	}
	pieces := chunk.Split(text)
	if IsConverted(path) {
		for i := range pieces {
			if pieces[i].Heading == "" {
				pieces[i].Heading = chunk.BoundHeading(filepath.Base(path))
			}
		}
	} else if prefix != "" && len(pieces) > 0 {
		if pieces[0].Heading != "" {
			prefix += " > " + pieces[0].Heading
		}
		pieces[0].Heading = chunk.BoundHeading(prefix)
	}
	return pieces
}
