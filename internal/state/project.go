package state

import (
	"errors"
	"strings"
)

var ErrProjectAmbiguous = errors.New("multiple project names match without case")

// ResolveProject returns the indexed spelling, or an empty name when none matches.
func ResolveProject(names []string, requested string) (string, error) {
	found := ""
	for _, name := range names {
		if strings.EqualFold(name, requested) {
			if found != "" && found != name {
				return "", ErrProjectAmbiguous
			}
			found = name
		}
	}
	return found, nil
}
