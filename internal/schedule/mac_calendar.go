package schedule

import (
	"strconv"
	"strings"
)

func macCalendarMatches(p Plan, text string) bool {
	depth, blocks, triggers, streams, descriptors := 0, 0, 0, 0, 0
	fields := map[string]int{}
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "event triggers = {" {
			blocks++
			if depth != 0 || blocks != 1 {
				return false
			}
			depth = 1
			continue
		}
		if depth == 0 || line == "" {
			continue
		}
		if line == "}" {
			depth--
			continue
		}
		switch depth {
		case 1:
			if !strings.HasSuffix(line, " => {") {
				return false
			}
			triggers++
			depth++
		case 2:
			if line == "descriptor = {" {
				descriptors++
				depth++
				continue
			}
			key, value, ok := strings.Cut(line, " = ")
			if !ok {
				return false
			}
			switch key {
			case "stream":
				streams++
				if value != "com.apple.launchd.calendarinterval" {
					return false
				}
			case "keepalive":
				if value != "0" {
					return false
				}
			case "service", "monitor":
			default:
				return false
			}
		case 3:
			key, value, ok := strings.Cut(line, " => ")
			if !ok || (key != `"Hour"` && key != `"Minute"`) {
				return false
			}
			if _, duplicate := fields[key]; duplicate {
				return false
			}
			number, err := strconv.Atoi(value)
			if err != nil {
				return false
			}
			fields[key] = number
		}
	}
	return depth == 0 && blocks == 1 && triggers == 1 && streams == 1 && descriptors == 1 && len(fields) == 2 && fields[`"Hour"`] == p.Spec.Hour && fields[`"Minute"`] == p.Spec.Minute
}
