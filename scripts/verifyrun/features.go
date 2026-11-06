package verifyrun

var featureJourneys = []struct {
	name string
	run  func(*driver, string, int) error
}{
	{"cited-search", func(d *driver, owner string, n int) error { return d.citedSearch(owner, n) }},
	{"tool-contract", func(d *driver, _ string, n int) error {
		if err := d.toolContract(n); err != nil {
			return err
		}
		return d.installTemporaryHome(n)
	}},
	{"skill-drift", func(d *driver, _ string, n int) error { return d.skillDrift(n) }},
	{"config-sources", func(d *driver, _ string, n int) error { return d.configSources(n) }},
	{"freshness", func(d *driver, _ string, n int) error { return d.freshness(n) }},
	{"privacy", func(d *driver, _ string, n int) error { return d.privacy(n) }},
	{"exclusions", func(d *driver, _ string, n int) error { return d.exclusions(n) }},
	{"settings-relocation", func(d *driver, _ string, n int) error { return d.settingsRelocation(n) }},
	{"schedule", func(d *driver, _ string, n int) error { return d.schedule(n) }},
}

func knownFeature(name string) bool {
	for _, item := range featureJourneys {
		if item.name == name {
			return true
		}
	}
	return false
}
