package verifyrun

import (
	"brain/internal/platform"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

func oneObject(data []byte, value any) error {
	if len(data) == 0 || len(data) > 128*1024 {
		return errors.New("contract reply is empty or too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(value); err != nil {
		return fmt.Errorf("contract reply is invalid JSON: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("contract reply must contain one object")
	}
	return nil
}

func checkVersion(data []byte) error {
	var reply struct {
		Version  string  `json:"version"`
		Revision *string `json:"revision"`
		Modified *bool   `json:"modified"`
	}
	if err := oneObject(data, &reply); err != nil {
		return err
	}
	if !versionNumber.MatchString(reply.Version) || reply.Revision == nil || reply.Modified == nil {
		return errors.New("version lacks version, revision, or modified")
	}
	return nil
}

var versionNumber = regexp.MustCompile(`^[0-9]+\.[0-9]+(\.[0-9]+)?([-+][A-Za-z0-9.-]+)?$`)

type contractHelp struct {
	Verbs    []string `json:"verbs"`
	Commands []struct {
		Name      string          `json:"name"`
		Inputs    string          `json:"inputs"`
		Outputs   string          `json:"outputs"`
		ExitCodes string          `json:"exit_codes"`
		Effects   string          `json:"effects"`
		Flags     map[string]bool `json:"flags"`
	} `json:"commands"`
}

func checkHelpIndex(data []byte) ([]string, error) {
	var reply contractHelp
	if err := oneObject(data, &reply); err != nil {
		return nil, err
	}
	if len(reply.Verbs) < 16 || len(reply.Commands) != len(reply.Verbs) {
		return nil, errors.New("help omits verbs or command definitions")
	}
	seen := map[string]bool{}
	for i, name := range reply.Verbs {
		command := reply.Commands[i]
		if name == "" || seen[name] || command.Name != name || command.Inputs == "" || command.Outputs == "" {
			return nil, fmt.Errorf("help definition %d is missing or inconsistent", i)
		}
		if err := checkHelpSemantics(command.Effects, command.ExitCodes); err != nil {
			return nil, fmt.Errorf("help definition %s: %w", name, err)
		}
		seen[name] = true
	}
	for _, name := range []string{"help", "index", "search", "show", "setup"} {
		if !seen[name] {
			return nil, fmt.Errorf("help omits %s", name)
		}
	}
	return reply.Verbs, nil
}

func checkTopicHelp(data []byte, topic string) error {
	var reply contractHelp
	if err := oneObject(data, &reply); err != nil {
		return err
	}
	if len(reply.Commands) != 1 || reply.Commands[0].Name != topic || reply.Commands[0].Inputs == "" || reply.Commands[0].Outputs == "" {
		return fmt.Errorf("help for %s is missing its definition", topic)
	}
	return checkHelpSemantics(reply.Commands[0].Effects, reply.Commands[0].ExitCodes)
}

func checkHelpSemantics(effects, exitCodes string) error {
	allowed := map[string]bool{"reads": true, "writes own state": true, "writes workspace": true, "writes host schedule": true, "writes host skill folder": true, "process": true}
	seen := map[string]bool{}
	for _, effect := range strings.Split(effects, ", ") {
		if !allowed[effect] || seen[effect] {
			return fmt.Errorf("invalid help effect %q", effect)
		}
		seen[effect] = true
	}
	if !seen["reads"] {
		return errors.New("help effects omit reads")
	}
	seen = map[string]bool{}
	for _, item := range strings.Split(exitCodes, ", ") {
		code, description, found := strings.Cut(item, " ")
		value, err := strconv.Atoi(code)
		if !found || err != nil || value < 0 || value > 5 || strings.Trim(description, " .") == "" || seen[code] {
			return fmt.Errorf("invalid help exit code %q", item)
		}
		seen[code] = true
	}
	if !seen["2"] {
		return errors.New("help exit codes omit bad input")
	}
	return nil
}

func helpFlags(data []byte) ([]string, error) {
	var reply contractHelp
	if err := oneObject(data, &reply); err != nil {
		return nil, err
	}
	if len(reply.Commands) != 1 || reply.Commands[0].Name != "help" {
		return nil, errors.New("help command flags are unavailable")
	}
	flags := []string{}
	for name := range reply.Commands[0].Flags {
		flags = append(flags, name)
	}
	if len(flags) == 0 {
		return nil, errors.New("help command has no valid flags")
	}
	return flags, nil
}

func checkPlainTopicHelp(data []byte, topic string) error {
	text := string(data)
	for _, field := range []string{"Inputs:", "Outputs:", "Effects:", "Exit codes:"} {
		if !strings.Contains(text, field) {
			return fmt.Errorf("plain help for %s omits %s", topic, field)
		}
	}
	if !strings.HasPrefix(text, "brain ") {
		return fmt.Errorf("plain help for %s has no Brain usage", topic)
	}
	return nil
}

func checkTopLevelHelp(data []byte) error {
	text := string(data)
	if !strings.HasPrefix(text, "Brain ") || !strings.Contains(text, "\nCommands:\n") || !strings.Contains(text, "\nExit codes:") {
		return errors.New("top-level help lacks Brain commands or exit guidance")
	}
	return nil
}

func checkBadInput(data []byte, expected, binary string, names []string) error {
	var reply struct {
		Error struct {
			Code     string     `json:"code"`
			Message  string     `json:"message"`
			Next     []string   `json:"next"`
			NextArgv [][]string `json:"next_argv"`
		} `json:"error"`
	}
	if err := oneObject(data, &reply); err != nil {
		return err
	}
	if reply.Error.Code != expected || reply.Error.Message == "" || len(reply.Error.Next) == 0 || len(reply.Error.Next) != len(reply.Error.NextArgv) || !filepath.IsAbs(binary) {
		return fmt.Errorf("bad input lacks structured %s recovery", expected)
	}
	shell := "sh"
	if runtime.GOOS == "windows" {
		shell = "powershell"
	}
	prefix := platform.Command(shell, []string{binary}) + " "
	for i, command := range reply.Error.NextArgv {
		if len(command) < 2 || command[0] != binary || !filepath.IsAbs(command[0]) || command[1] != "help" || !strings.HasPrefix(reply.Error.Next[i], prefix) {
			return fmt.Errorf("bad input recovery command %d does not start with the Brain executable", i)
		}
	}
	label := "Valid verbs: "
	if expected == "unknown-flag" {
		label = "Valid flags: "
	}
	_, listed, found := strings.Cut(reply.Error.Message, label)
	if !found {
		return fmt.Errorf("bad input message omits %s", label)
	}
	listed = strings.TrimSuffix(listed, ".")
	items := map[string]bool{}
	for _, item := range strings.Split(listed, ", ") {
		items[item] = true
	}
	if expected == "unknown-command" {
		for _, name := range names {
			if !items[name] {
				return fmt.Errorf("unknown verb message omits %s", name)
			}
		}
	} else {
		valid := false
		for _, name := range names {
			valid = valid || items[name]
		}
		if !valid {
			return errors.New("unknown flag message omits the verb's valid flags")
		}
	}
	return nil
}

func checkPublicText(data []byte) error {
	content := strings.ToLower(string(data))
	if !strings.Contains(content, "brain") {
		return errors.New("public help or skill omits Brain branding")
	}
	if privateReference.MatchString(content) {
		return errors.New("public help or skill contains a home path or remote address")
	}
	return nil
}

var privateReference = regexp.MustCompile(`(/(users|home)/[^/\s]+/|[a-z]:[\\/]users[\\/][^\\/\s]+[\\/]|https?://|ssh://|git@[^\s]+:)`)

func checkReadme(data []byte) error {
	lines := strings.Split(string(data), "\n")
	headings := []string{"## Install and first search", "## What install writes", "## Requirements", "## Network", "## Remove"}
	positions := map[string]int{}
	brainTitle := false
	for i, line := range lines {
		if line == "# Brain" {
			brainTitle = true
		}
		for _, heading := range headings {
			if line == heading {
				positions[heading] = i
			}
		}
	}
	if !brainTitle || !strings.Contains(string(data), "BRAIN_HOME") {
		return errors.New("README omits Brain title or state override")
	}
	previous := -1
	for _, heading := range headings {
		position, exists := positions[heading]
		if !exists || position <= previous {
			return fmt.Errorf("README omits or reorders %s", heading)
		}
		previous = position
	}
	section := lines[positions[headings[0]]+1 : positions[headings[1]]]
	inBlock := false
	commands := []string{}
	validInstall := false
	for _, line := range section {
		line = strings.TrimSpace(line)
		if line == "```sh" {
			inBlock = true
			commands = nil
			continue
		}
		if line == "```" && inBlock {
			if len(commands) >= 2 && strings.HasPrefix(commands[0], "./scripts/install --source ") && strings.Contains(commands[0], " --write") && commands[len(commands)-1] == `"$HOME/.local/bin/brain" --version` {
				validInstall = true
			}
			inBlock = false
			continue
		}
		if inBlock && line != "" {
			commands = append(commands, line)
		}
	}
	if !validInstall {
		return errors.New("README install block must end with the installed Brain version command")
	}
	return nil
}
