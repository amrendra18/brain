package platform

import "strings"

// ReservedName applies Windows device rules on every system, including older Windows versions.
func ReservedName(name string) bool {
	if end := strings.IndexAny(name, ".:"); end >= 0 {
		name = name[:end]
	}
	name = strings.ToUpper(strings.TrimRight(name, " "))
	switch name {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	if strings.HasPrefix(name, "COM") || strings.HasPrefix(name, "LPT") {
		switch name[3:] {
		case "1", "2", "3", "4", "5", "6", "7", "8", "9", "¹", "²", "³":
			return true
		}
	}
	return false
}
