package state

import (
	"errors"
	"testing"
)

func TestResolveProject(t *testing.T) {
	for _, test := range []struct {
		names           []string
		requested, want string
		ambiguous       bool
	}{
		{[]string{"Alpha", "alpha"}, "Alpha", "", true},
		{[]string{"Alpha", "alpha"}, "alpha", "", true},
		{[]string{"Alpha", "alpha"}, "ALPHA", "", true},
		{[]string{"Alpha", "Beta"}, "aLpHa", "Alpha", false},
		{[]string{"Alpha", "Beta"}, "new", "", false},
		{[]string{"Alpha", "Alpha"}, "alpha", "Alpha", false},
		{nil, "new", "", false},
	} {
		got, err := ResolveProject(test.names, test.requested)
		if got != test.want || errors.Is(err, ErrProjectAmbiguous) != test.ambiguous {
			t.Fatalf("%+v: got %q, %v", test, got, err)
		}
	}
}
