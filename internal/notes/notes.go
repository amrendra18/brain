package notes

import (
	"brain/internal/chunk"
	"brain/internal/platform"
	"brain/internal/redact"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

const LimitBytes = 64 * 1024

func Project(name string) bool {
	if name == "" || utf8.RuneCountInString(name) > 100 || strings.TrimRight(name, " .") != name {
		return false
	}
	for i, r := range name {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' {
			continue
		}
		if i == 0 || r != ' ' && r != '.' && r != '-' {
			return false
		}
	}
	return !platform.ReservedName(name)
}

func Entry(text, stamp string) string {
	text = strings.NewReplacer("\r\n", "\n", "\r", "\n", "\v", "\n", "\f", "\n", "\u0085", "\n", "\u2028", "\n", "\u2029", "\n").Replace(text)
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for i, line := range lines {
		body := strings.TrimLeft(line, " ")
		if strings.HasPrefix(body, "#") || strings.HasPrefix(body, "```") || strings.HasPrefix(body, "~~~") {
			line = line[:len(line)-len(body)] + "\\" + body
		}
		lines[i] = strings.TrimRightFunc(line, unicode.IsSpace)
	}
	return "## " + stamp + "\n\n" + strings.Join(lines, "\n") + "\n"
}

func EntryID(project, block string) string {
	pieces := chunk.Split(block)
	heading, _, _ := strings.Cut(block, "\n")
	if len(pieces) > 1 && pieces[0].Text == heading {
		pieces = pieces[1:]
	}
	return chunk.ID(chunk.DocumentKey("notes", project+".md", pieces[0].Text))
}

func Append(state, project, block string, policies ...*redact.Policy) (string, error) {
	policy := redact.Default()
	if len(policies) > 0 {
		policy = policies[0]
	}
	if policy.Match(project) {
		return "", redact.ErrPolicyInvalid
	}
	block = policy.Apply(block).Text
	if !Project(project) {
		return "", errors.New("invalid note project")
	}
	name := filepath.Join("notes", project+".md")
	if err := platform.SafeStatePath(state, name); err != nil {
		return "", err
	}
	lock, err := platform.Acquire(state, "notes", true)
	if err != nil {
		return "", err
	}
	defer lock.Close()
	root, err := os.OpenRoot(state)
	if err != nil {
		return "", err
	}
	defer root.Close()
	if err = root.MkdirAll("notes", 0700); err != nil {
		return "", err
	}
	if !platform.WritableState(state, "notes") {
		return "", os.ErrPermission
	}
	old, err := platform.ReadState(state, name)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if len(old) > 0 {
		old = append(old, '\n')
	}
	if err = platform.WriteState(state, name, []byte(policy.Apply(string(old)+block).Text)); err != nil {
		return "", err
	}
	return filepath.Join(state, name), nil
}
