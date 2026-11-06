package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestHelpFormats(t *testing.T) {
	t.Setenv("BRAIN_HOME", "invalid-relative-state")
	if len(commandNames()) != 18 {
		t.Fatal("canonical verb count", commandNames())
	}
	for _, args := range [][]string{{"help", "--format", "json"}, {"help", "search", "--format=json"}, {"help", "--brief", "--format=json"}, {"help", "--version", "--format=json"}} {
		var output bytes.Buffer
		if Run(args, &output) != 0 {
			t.Fatal(args, output.String())
		}
		var reply struct {
			Verbs    []string      `json:"verbs"`
			Commands []commandHelp `json:"commands"`
		}
		if json.Unmarshal(output.Bytes(), &reply) != nil || !reflect.DeepEqual(reply.Verbs, commandNames()) || len(reply.Commands) == 0 {
			t.Fatal(output.String())
		}
		if len(args) > 2 && args[1] != "--format" {
			expected := args[1]
			if expected == "--brief" {
				expected = "brief"
			}
			if len(reply.Commands) != 1 || reply.Commands[0].Name != expected {
				t.Fatal(args, output.String())
			}
		}
		for _, command := range reply.Commands {
			if command.Name == "doctor" && strings.HasPrefix(command.ExitCodes, "0 ") {
				t.Fatal("doctor advertises an unreachable exit", command.ExitCodes)
			}
			if command.Effects == "" || command.Inputs == "" || command.Outputs == "" || command.Preconditions == "" || command.Destinations == "" || command.ExitCodes == "" {
				t.Fatal(command)
			}
		}
		if Run(args, closedWriter{}) != 1 {
			t.Fatal("closed JSON help output accepted")
		}
	}
	for _, args := range [][]string{{"help", "--format", "markdown"}, {"help", "search", "--format=markdown"}, {"help", "--brief", "--format=markdown"}, {"help", "--version", "--format=markdown"}} {
		var output bytes.Buffer
		if Run(args, &output) != 0 || !strings.HasPrefix(output.String(), "# Brain\n") {
			t.Fatal(output.String())
		}
		if len(args) > 2 && args[1] != "--format" {
			expected := args[1]
			if expected == "--brief" {
				expected = "brief"
			}
			if !strings.Contains(output.String(), "## "+expected+"\n") || !strings.Contains(output.String(), "Effects: reads") {
				t.Fatal(args, output.String())
			}
		}
		if Run(args, closedWriter{}) != 1 {
			t.Fatal("closed Markdown help accepted")
		}
	}
	for _, name := range append(commandNames(), "--brief", "--version") {
		var output bytes.Buffer
		if Run([]string{name, "--help"}, &output) != 0 && name != "--version" {
			t.Fatal(name, output.String())
		}
		output.Reset()
		if Run([]string{"help", name}, &output) != 0 {
			t.Fatal(name, output.String())
		}
		if name == "doctor" && strings.Contains(output.String(), "Exit codes: 0 ") {
			t.Fatal("doctor advertises an unreachable exit", output.String())
		}
		for _, label := range []string{"Inputs:", "Outputs:", "Preconditions:", "Destinations:", "Effects:", "Exit codes:"} {
			if !strings.Contains(output.String(), label) {
				t.Fatal(name, label)
			}
		}
	}
	for _, args := range [][]string{{"help", "--format"}, {"help", "--format=xml"}, {"help", "--skill=yes"}, {"help", "--help=yes"}, {"help", "--other"}, {"help", "bad"}, {"help", "search", "show"}, {"help", "--skill", "--format=json"}, {"help", "search", "--skill"}} {
		var output bytes.Buffer
		if Run(args, &output) != 2 {
			t.Fatal(args, output.String())
		}
	}
	if topicHelp("unknown", "brain") != "" {
		t.Fatal("unknown topic returned help")
	}
}

func TestBriefJSON(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	t.Setenv("BRAIN_HOME", state)
	for _, name := range []string{"brief", "--brief"} {
		var output bytes.Buffer
		if Run([]string{name, "--format=json"}, &output) != 0 {
			t.Fatal(output.String())
		}
		var reply struct {
			Text    string `json:"text"`
			Age     int    `json:"index_age_s"`
			Ready   bool   `json:"ready"`
			Problem string `json:"problem"`
		}
		if json.Unmarshal(output.Bytes(), &reply) != nil || reply.Ready || reply.Problem == "" || strings.Count(reply.Text, "\n") != 3 {
			t.Fatal(output.String())
		}
		if Run([]string{name, "--format=json"}, closedWriter{}) != 1 {
			t.Fatal("closed brief JSON accepted")
		}
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatal("brief wrote state", err)
	}
}
