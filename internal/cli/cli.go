package cli

import (
	"brain/internal/platform"
	"brain/internal/redact"
	"brain/internal/version"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"unicode/utf16"
)

type Failure struct {
	Code     string     `json:"code"`
	Message  string     `json:"message"`
	Next     []string   `json:"next"`
	NextArgv [][]string `json:"next_argv"`
}

type refusal struct {
	Error Failure `json:"error"`
}

// WriteJSON keeps output readable through shells with different text encodings.
func WriteJSON(output io.Writer, value any) int {
	data, err := json.Marshal(value)
	if err != nil {
		return 1
	}
	var line strings.Builder
	for _, r := range string(data) {
		if r < 128 {
			line.WriteRune(r)
			continue
		}
		if r <= 0xffff {
			fmt.Fprintf(&line, "\\u%04x", r)
		} else {
			a, b := utf16.EncodeRune(r)
			fmt.Fprintf(&line, "\\u%04x\\u%04x", a, b)
		}
	}
	line.WriteByte('\n')
	if n, err := io.WriteString(output, line.String()); err != nil || n != line.Len() {
		return 1
	}
	return 0
}

func selfHelp() ([]string, [][]string) {
	c := call{policy: redact.Default(), command: "help", flags: map[string]string{}, shell: defaultShell()}
	// Stateless recovery must not repeat scope values without loading their privacy policy.
	text, argv := c.next("help")
	if len(argv) == 0 {
		return []string{}, [][]string{}
	}
	return []string{text}, [][]string{argv}
}

func defaultShell() string {
	if runtime.GOOS == "windows" {
		return "powershell"
	}
	return "sh"
}

func helpExecutable() string {
	executable, err := os.Executable()
	if err != nil {
		executable = "brain"
	}
	if redact.Default().Match(executable) {
		return "[move the executable to a path without protected text]"
	}
	quoted := platform.Command(defaultShell(), []string{executable})
	if hasTextControls(quoted) {
		return "[executable path contains controls; use the executable from next_argv in JSON output]"
	}
	return quoted
}

func badInput(output io.Writer, code, message string) int {
	next, argv := selfHelp()
	if WriteJSON(output, refusal{Failure{code, message, next, argv}}) != 0 {
		return 1
	}
	return 2
}

func help(output io.Writer) int {
	executable := helpExecutable()
	text := "Brain " + version.Current().Version + " indexes local Markdown, DOCX and RTF documents. No model or network.\n\nCommands:\n"
	for _, command := range append([]string{"--version"}, commandNames()...) {
		text += "\n" + topicHelp(command, executable)
	}
	text += "\nExit codes: 0 completed, 1 not completed or not clean, 2 bad input, 3 nothing changed or doctor found no problem.\n"
	if _, err := writeText(output, text); err != nil {
		return 1
	}
	return 0
}

var absoluteInputPath = regexp.MustCompile(`(^|[\s=("'(:])(?:[A-Za-z]:[\\/]|\\\\|/)[^\s"'<>|,;)]*`)

func inputName(input string) string {
	input = redact.Default().Apply(input).Text
	name, value, hasValue := strings.Cut(input, "=")
	if hasValue && strings.HasPrefix(name, "-") {
		return strconv.Quote(name) + "=" + inputName(value)
	}
	if strings.HasPrefix(input, "/") || strings.HasPrefix(input, "\\\\") ||
		(len(input) > 2 && input[1] == ':' && (input[2] == '\\' || input[2] == '/')) {
		return "an absolute path"
	}
	return strconv.Quote(absoluteInputPath.ReplaceAllString(input, "${1}an absolute path"))
}

// Run answers help and version without looking at any state folder.
func Run(args []string, output io.Writer) int {
	invalid := func(code, message string) int { return badInput(output, code, message) }
	if len(args) == 0 {
		return runState("", nil, output)
	}
	command := args[0]
	if command == "help" {
		for _, arg := range args[1:] {
			if arg == "--help" {
				if _, err := writeText(output, topicHelp("help", helpExecutable())); err != nil {
					return 1
				}
				return 0
			}
		}
		return runHelp(args[1:], output)
	}
	if command == "--help" && len(args) == 1 {
		return help(output)
	}
	if definition, ok := commandDefinition(command); ok {
		for _, arg := range args[1:] {
			if arg == "--help" {
				_, err := writeText(output, topicHelp(definition.Name, helpExecutable()))
				if err != nil {
					return 1
				}
				return 0
			}
		}
		if definition.Name == "brief" || definition.Name == "setup" {
			return runIntegration(command, args[1:], output)
		}
		return runState(command, args[1:], output)
	}
	if name, _, _ := strings.Cut(command, "="); name == "--state-dir" || name == "--config" || name == "--format" {
		return runState("", args, output)
	}
	if command != "--version" && command != "help" && command != "--help" {
		code := "unknown-command"
		if strings.HasPrefix(command, "-") {
			code = "unknown-flag"
		}
		return invalid(code, "Unknown input. Valid flags: --version, --help, --brief, --state-dir, --config, --format json. Valid verbs: "+strings.Join(commandNames(), ", ")+".")
	}
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if command == "--version" && (arg == "--format" || strings.HasPrefix(arg, "--format=")) {
			format := strings.TrimPrefix(arg, "--format=")
			if arg == "--format" {
				i++
				if i == len(args) {
					return invalid("missing-argument", "--format needs json or text.")
				}
				format = args[i]
			}
			if format != "json" && format != "text" {
				return invalid("invalid-value", "Invalid --format value. Use json or text.")
			}
			continue
		}
		code := "unexpected-argument"
		if strings.HasPrefix(arg, "-") {
			code = "unknown-flag"
		}
		return invalid(code, "Unknown input. Valid flags: --version, --help, --format json|text.")
	}
	if command == "--version" {
		return WriteJSON(output, version.Current())
	}
	return help(output)
}
