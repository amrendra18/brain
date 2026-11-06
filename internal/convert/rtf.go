package convert

import (
	"errors"
	"strconv"
	"strings"
	"unicode/utf16"
)

// Rtf reads TextEdit's text subset. Pictures and binary content are refused.
func Rtf(data []byte) (string, error) {
	bad := errors.New("unsupported or damaged rtf")
	if !strings.HasPrefix(string(data), `{\rtf1`) {
		return "", bad
	}
	type group struct {
		ignored  bool
		fallback int
	}
	current := group{fallback: 1}
	stack := []group{}
	output := []uint16{}
	skip := 0
	write := func(value uint16) {
		if skip > 0 {
			skip--
			return
		}
		if !current.ignored {
			output = append(output, value)
		}
	}
	for i := 0; i < len(data); {
		c := data[i]
		i++
		if len(stack) == 0 && c != '{' {
			if c == ' ' || c == '\r' || c == '\n' || c == '\t' {
				continue
			}
			return "", bad
		}
		switch c {
		case '{':
			stack = append(stack, current)
		case '}':
			current = stack[len(stack)-1]
			stack = stack[:len(stack)-1]
		case '\r', '\n':
		case '\\':
			if i == len(data) {
				return "", bad
			}
			c = data[i]
			i++
			switch c {
			case '\\', '{', '}':
				write(uint16(c))
				continue
			case '*':
				current.ignored = true
				continue
			case '\'':
				if i+2 > len(data) {
					return "", bad
				}
				number, err := strconv.ParseUint(string(data[i:i+2]), 16, 8)
				if err != nil {
					return "", bad
				}
				write(ansi(byte(number)))
				i += 2
				continue
			case '~':
				write(0x00a0)
				continue
			case '_':
				write(0x2011)
				continue
			case '-':
				continue
			case '\r':
				if i < len(data) && data[i] == '\n' {
					i++
				}
				write('\n')
				continue
			case '\n':
				write('\n')
				continue
			}
			start := i - 1
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
				return "", bad
			}
			for i < len(data) && (data[i] >= 'a' && data[i] <= 'z' || data[i] >= 'A' && data[i] <= 'Z') {
				i++
			}
			word := string(data[start:i])
			start = i
			if i < len(data) && data[i] == '-' {
				i++
			}
			for i < len(data) && data[i] >= '0' && data[i] <= '9' {
				i++
			}
			number := 0
			if i > start {
				var err error
				number, err = strconv.Atoi(string(data[start:i]))
				if err != nil {
					return "", bad
				}
			}
			if i < len(data) && data[i] == ' ' {
				i++
			}
			switch word {
			case "rtf":
				if number != 1 {
					return "", bad
				}
			case "ansicpg":
				if number != 1252 {
					return "", bad
				}
			case "fonttbl", "colortbl", "stylesheet", "info", "header", "footer":
				current.ignored = true
			case "pict", "bin", "object":
				return "", bad
			case "uc":
				if number < 0 || number > 16 {
					return "", bad
				}
				current.fallback = number
			case "u":
				if number < -32768 || number > 65535 {
					return "", bad
				}
				if !current.ignored {
					output = append(output, uint16(number))
				}
				skip = current.fallback
			case "par", "line":
				write('\n')
			case "tab":
				write('\t')
			case "emdash":
				write(0x2014)
			case "endash":
				write(0x2013)
			case "lquote":
				write(0x2018)
			case "rquote":
				write(0x2019)
			case "ldblquote":
				write(0x201c)
			case "rdblquote":
				write(0x201d)
			case "bullet":
				write(0x2022)
			}
		default:
			write(ansi(c))
		}
	}
	if len(stack) != 0 {
		return "", bad
	}
	return string(utf16.Decode(output)), nil
}

func ansi(value byte) uint16 {
	if value >= 0x80 && value <= 0x9f {
		return []uint16{0x20ac, 0x81, 0x201a, 0x192, 0x201e, 0x2026, 0x2020, 0x2021, 0x2c6, 0x2030, 0x160, 0x2039, 0x152, 0x8d, 0x17d, 0x8f, 0x90, 0x2018, 0x2019, 0x201c, 0x201d, 0x2022, 0x2013, 0x2014, 0x2dc, 0x2122, 0x161, 0x203a, 0x153, 0x9d, 0x17e, 0x178}[value-0x80]
	}
	return uint16(value)
}
