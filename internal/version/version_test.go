package version

import (
	"runtime/debug"
	"testing"
)

func TestVersionBuildInfo(t *testing.T) {
	for _, test := range []struct {
		build    *debug.BuildInfo
		revision string
		modified bool
	}{
		{nil, "", false},
		{&debug.BuildInfo{}, "", false},
		{&debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abc"}, {Key: "vcs.modified", Value: "true"}, {Key: "other", Value: "ignored"}}}, "abc", true},
		{&debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.modified", Value: "false"}}}, "", false},
	} {
		got := fromBuild(test.build)
		if got.Version != Development || got.Revision != test.revision || got.Modified != test.modified {
			t.Fatal(got)
		}
	}
	if Current().Version != Development {
		t.Fatal("development version changed")
	}
}
