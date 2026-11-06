package verifyrun

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSettingsJourney(t *testing.T) {
	d := stubFixtureDriver(t, "settings", "")
	d.binary = binary
	canonical, err := filepath.EvalSymlinks(d.summary.Scratch)
	if err != nil {
		t.Fatal(err)
	}
	d.summary.Scratch = canonical
	if err := d.settingsRelocation(0); err != nil {
		t.Fatal(err)
	}
	if d.summary.SideEffects["settings_relocation_1"] == "" {
		t.Fatal("missing relocation evidence")
	}
}

func TestSettingsReplyValidation(t *testing.T) {
	for _, test := range []struct {
		data  string
		valid bool
	}{
		{`{"selection":{"state_dir":"state","config":"config"},"saved":true}`, true},
		{`{"selection":{"state_dir":"other","config":"config"},"saved":true}`, false},
		{`{"selection":{"state_dir":"state","config":"other"},"saved":true}`, false},
		{`{"selection":{"state_dir":"state","config":"config"},"saved":false}`, false},
		{`{`, false},
	} {
		if err := checkSettings([]byte(test.data), "state", "config"); (err == nil) != test.valid {
			t.Fatal(test, err)
		}
	}
}

func TestSettingsJourneyRejectsFailedCommands(t *testing.T) {
	for step := 1; step <= 9; step++ {
		t.Run(fmt.Sprint(step), func(t *testing.T) {
			d := stubFixtureDriver(t, "settings", "")
			canonical, err := filepath.EvalSymlinks(d.summary.Scratch)
			if err != nil {
				t.Fatal(err)
			}
			d.summary.Scratch = canonical
			counter := filepath.Join(d.evidence, "counter")
			quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
			exit := 1
			if step == 9 {
				exit = 0
			}
			script := fmt.Sprintf("#!/bin/sh\nn=0\nif [ -f %s ]; then IFS= read -r n < %s; fi\nn=$((n+1))\nprintf '%%s\\n' \"$n\" > %s\nif [ \"$n\" = %d ]; then printf '{}\\n'; exit %d; fi\nexec %s \"$@\"\n", quote(counter), quote(counter), quote(counter), step, exit, quote(binary))
			if err = os.WriteFile(d.binary, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			if err = d.settingsRelocation(0); err == nil {
				t.Fatal("accepted deliberately broken command", step)
			}
		})
	}
}

func TestSettingsJourneyFilesystemFailures(t *testing.T) {
	for _, kind := range []string{"base", "document", "config"} {
		t.Run(kind, func(t *testing.T) {
			d := stubFixtureDriver(t, "settings", "")
			d.binary = binary
			base := filepath.Join(d.summary.Scratch, "settings-relocation-0")
			target := base
			if kind == "document" {
				target = filepath.Join(base, "sources", "harbor")
			}
			if kind == "config" {
				target = filepath.Join(base, "selected.json")
			}
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				t.Fatal(err)
			}
			if kind == "config" {
				if err := os.Mkdir(target, 0700); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(target, []byte("deliberately conflicting test input"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := d.settingsRelocation(0); err == nil {
				t.Fatal("accepted deliberately conflicting path", kind)
			}
		})
	}
}
