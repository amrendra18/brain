package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScopedDoctorHelpFormats(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "absent-state")
	t.Setenv("BRAIN_HOME", "invalid-relative-state")
	for _, format := range []string{"text", "json", "markdown"} {
		options := []string{}
		if format != "text" {
			options = []string{"--format", format}
		}
		for _, ordered := range [][]string{append([]string{"doctor", "--state-dir", folder}, options...), append(append([]string{}, options...), "doctor", "--state-dir="+folder)} {
			args := append([]string{"help"}, ordered...)
			var output bytes.Buffer
			if code := Run(args, &output); code != 0 {
				t.Fatal(args, code, output.String())
			}
			if format == "json" {
				var reply struct {
					Description string        `json:"description"`
					Verbs       []string      `json:"verbs"`
					Commands    []commandHelp `json:"commands"`
					StateDir    string        `json:"state_dir"`
					Guidance    string        `json:"guidance"`
				}
				if json.Unmarshal(output.Bytes(), &reply) != nil || reply.Description == "" || len(reply.Verbs) != 18 || len(reply.Commands) != 1 || reply.Commands[0].Name != "doctor" || reply.StateDir != folder || !strings.Contains(reply.Guidance, folder) {
					t.Fatal(output.String())
				}
				c := reply.Commands[0]
				if c.Inputs == "" || c.Outputs == "" || c.Preconditions == "" || c.Destinations == "" || c.Effects == "" || c.ExitCodes == "" || !c.Flags["--state-dir"] {
					t.Fatal(c)
				}
			} else {
				for _, label := range []string{"Inputs:", "Outputs:", "Preconditions:", "Destinations:", "Effects:", "Exit codes:", "Guidance for this state folder:", folder} {
					if !strings.Contains(output.String(), label) {
						t.Fatal(args, label, output.String())
					}
				}
				if format == "markdown" && !strings.HasPrefix(output.String(), "# Brain\n") {
					t.Fatal(output.String())
				}
			}
			if Run(args, closedWriter{}) != 1 {
				t.Fatal("closed scoped help accepted", args)
			}
		}
	}
	for _, args := range [][]string{{"help", "doctor", "--format=json", "--state-dir"}, {"help", "doctor", "--format=json", "--state-dir=relative"}, {"help", "doctor", "--state-dir="}, {"help", "doctor", "--state-dir", folder, "--skill"}, {"help", "doctor", "--state-dir", folder, "--format=xml"}, {"help", "doctor", "--state-dir", folder, "--other"}} {
		var output bytes.Buffer
		if code := Run(args, &output); code != 2 {
			t.Fatal(args, code, output.String())
		}
		var reply refusal
		if json.Unmarshal(output.Bytes(), &reply) != nil || reply.Error.Code == "" || reply.Error.Next == nil {
			t.Fatal(output.String())
		}
		if strings.Contains(output.String(), "Unknown flag") && !strings.Contains(output.String(), "--state-dir") {
			t.Fatal("missing valid scoped flag", output.String())
		}
	}
	if _, err := os.Stat(folder); !os.IsNotExist(err) {
		t.Fatal("help created state", err)
	}
	var output bytes.Buffer
	if Run([]string{"help", "help", "--format=json"}, &output) != 0 || !strings.Contains(output.String(), `"--state-dir":true`) {
		t.Fatal("help metadata omitted scope flag", output.String())
	}
}
