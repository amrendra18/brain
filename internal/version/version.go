package version

import "runtime/debug"

const Declared = "0.1.0"
const Development = Declared + "-dev"

// buildVersion is set to Declared only for a validated tagged release build.
var buildVersion = Development

type Info struct {
	Version  string `json:"version"`
	Revision string `json:"revision"`
	Modified bool   `json:"modified"`
}

func Current() Info {
	build, _ := debug.ReadBuildInfo()
	return fromBuild(build)
}

func fromBuild(build *debug.BuildInfo) Info {
	result := Info{Version: buildVersion}
	if build != nil {
		for _, setting := range build.Settings {
			switch setting.Key {
			case "vcs.revision":
				result.Revision = setting.Value
			case "vcs.modified":
				result.Modified = setting.Value == "true"
			}
		}
	}
	return result
}
