package corpus

import (
	"brain/internal/platform"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var ErrIgnoreUnreadable = errors.New("an ignore file cannot be read safely")

const ignoreName = ".brainignore"
const ignoreLimit = 1048576

func isIgnoreName(name string) bool { return name == ignoreName }

type ScanResult struct {
	Entries     map[string]Entry
	Skipped     []Skipped
	config      Config
	scopes      map[string][]ignoreRule
	privateFile os.FileInfo
}

type ignoreRule struct {
	parts                        []string
	matchers                     []*regexp.Regexp
	raw                          string
	negated, directory, basename bool
}

func ScanWithPolicy(config Config) (ScanResult, error) {
	result := ScanResult{config: config, scopes: map[string][]ignoreRule{}}
	var err error
	result.Entries, result.Skipped, err = scan(config, &result)
	return result, err
}

func (result ScanResult) Excluded(name string) bool {
	return result.config.Privacy().Match(name) || result.privateControl(name) || isIgnoreName(filepath.Base(name)) || Excluded(result.config, name) || result.ignored(name, false)
}

func (result ScanResult) privateControl(name string) bool {
	if result.config.PrivateValuesFile == "" {
		return false
	}
	if filepath.Clean(name) == filepath.Clean(result.config.PrivateValuesFile) {
		return true
	}
	info, err := os.Lstat(name)
	return err == nil && result.privateFile != nil && os.SameFile(info, result.privateFile)
}

func (result ScanResult) ignored(name string, directory bool) bool {
	ancestors := []string{}
	for parent := filepath.Dir(name); ; parent = filepath.Dir(parent) {
		ancestors = append(ancestors, parent)
		if parent == filepath.Dir(parent) {
			break
		}
	}
	rules := []struct {
		base  string
		rules []ignoreRule
	}{}
	for i := len(ancestors) - 1; i >= 0; i-- {
		current := ancestors[i]
		if matchIgnoreScopes(rules, current, true) {
			return true
		}
		if scoped, ok := result.scopes[current]; ok {
			rules = append(rules, struct {
				base  string
				rules []ignoreRule
			}{current, scoped})
		}
	}
	return matchIgnoreScopes(rules, name, directory)
}

func matchIgnoreScopes(scopes []struct {
	base  string
	rules []ignoreRule
}, name string, directory bool) bool {
	var ignored bool
	for _, scope := range scopes {
		relative, err := filepath.Rel(scope.base, name)
		if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		parts := strings.Split(filepath.ToSlash(relative), "/")
		for _, rule := range scope.rules {
			if rule.directory && !directory {
				continue
			}
			candidate := parts
			if rule.basename {
				candidate = parts[len(parts)-1:]
			}
			if matchIgnoreParts(rule, candidate) {
				ignored = !rule.negated
			}
		}
	}
	return ignored
}

func matchIgnoreParts(rule ignoreRule, parts []string) bool {
	pattern := rule.parts
	previous := make([]bool, len(parts)+1)
	previous[0] = true
	for i, component := range pattern {
		next := make([]bool, len(previous))
		if len(component) >= 2 && strings.Trim(component, "*") == "" {
			for j := range next {
				if i == len(pattern)-1 {
					next[j] = j > 0 && (previous[j-1] || next[j-1])
				} else {
					next[j] = previous[j] || j > 0 && next[j-1]
				}
			}
		} else {
			for j := 1; j < len(next); j++ {
				next[j] = previous[j-1] && rule.matchers[i] != nil && rule.matchers[i].MatchString(ignoreByteString(parts[j-1]))
			}
		}
		previous = next
	}
	return previous[len(parts)]
}

func (result *ScanResult) loadIgnore(folder string) error {
	if _, ok := result.scopes[folder]; ok {
		return nil
	}
	name := filepath.Join(folder, ignoreName)
	if name == result.config.PrivateValuesFile {
		result.scopes[folder] = nil
		return nil
	}
	info, err := os.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		result.scopes[folder] = nil
		return nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0444 == 0 || info.Size() > ignoreLimit {
		return ErrIgnoreUnreadable
	}
	data, err := platform.ReadSource(name, ignoreLimit)
	if err != nil {
		return ErrIgnoreUnreadable
	}
	result.scopes[folder] = parseIgnore(string(data))
	return nil
}

func parseIgnore(data string) []ignoreRule {
	rules := []ignoreRule{}
	for _, line := range strings.Split(strings.TrimPrefix(data, "\ufeff"), "\n") {
		line = strings.TrimSuffix(line, "\r")
		line, _, _ = strings.Cut(line, "\x00")
		for strings.HasSuffix(line, " ") {
			backslashes := 0
			for i := len(line) - 2; i >= 0 && line[i] == '\\'; i-- {
				backslashes++
			}
			if backslashes%2 == 1 {
				break
			}
			line = strings.TrimSuffix(line, " ")
		}
		if line == "" || line[0] == '#' {
			continue
		}
		rule := ignoreRule{raw: line}
		if line[0] == '!' {
			rule.negated = true
			line = line[1:]
		}
		anchored := strings.HasPrefix(line, "/")
		rule.directory = strings.HasSuffix(line, "/")
		line = strings.TrimSuffix(line, "/")
		line = ignoreSeparators(line)
		rule.basename = !strings.Contains(line, "/")
		if anchored {
			line = strings.TrimPrefix(line, "/")
		}
		if line == "" {
			continue
		}
		rule.parts = splitIgnoreParts(line)
		for _, part := range rule.parts {
			rule.matchers = append(rule.matchers, compileIgnorePart(part))
		}
		rules = append(rules, rule)
	}
	return rules
}

func splitIgnoreParts(pattern string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '\\':
			i++
		case '[':
			if _, end, ok := ignoreClass(pattern, i+1); ok {
				i = end
			}
		case '/':
			parts = append(parts, pattern[start:i])
			start = i + 1
		}
	}
	return append(parts, pattern[start:])
}

func ignoreSeparators(pattern string) string {
	var result strings.Builder
	for i := 0; i < len(pattern); i++ {
		if pattern[i] == '\\' && i+1 < len(pattern) {
			if pattern[i+1] != '/' {
				result.WriteByte(pattern[i])
			}
			i++
		}
		result.WriteByte(pattern[i])
	}
	return result.String()
}

func (result ScanResult) PolicyFingerprint() string {
	digest := sha256.New()
	data, _ := json.Marshal(struct {
		Sources []Source
		Rules   []string
	}{result.config.Sources, scanRules(result.config)})
	digest.Write(data)
	folders := make([]string, 0, len(result.scopes))
	for folder, rules := range result.scopes {
		if len(rules) > 0 {
			folders = append(folders, folder)
		}
	}
	sort.Strings(folders)
	for _, folder := range folders {
		fmt.Fprintf(digest, "%q\n", folder)
		for _, rule := range result.scopes[folder] {
			fmt.Fprintf(digest, "%q %q\n", ignoreName, rule.raw)
		}
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func compileIgnorePart(pattern string) *regexp.Regexp {
	var expression strings.Builder
	expression.WriteString("(?s)^")
	chars := []rune(ignoreByteString(pattern))
	for i := 0; i < len(chars); i++ {
		switch chars[i] {
		case '*':
			expression.WriteString(".*")
		case '?':
			expression.WriteByte('.')
		case '\\':
			i++
			if i == len(chars) {
				return nil
			}
			expression.WriteString(regexp.QuoteMeta(string(chars[i])))
		case '[':
			members, end, ok := ignoreClass(pattern, i+1)
			if !ok {
				return nil
			}
			expression.WriteByte('[')
			for value, included := range members {
				if included {
					fmt.Fprintf(&expression, `\x{%x}`, value)
				}
			}
			// An empty class must never match, including when the rule is negated.
			expression.WriteString(`\x{100}]`)
			i = end
		default:
			expression.WriteString(regexp.QuoteMeta(string(chars[i])))
		}
	}
	expression.WriteByte('$')
	compiled, _ := regexp.Compile(expression.String())
	return compiled
}

// Git matches bytes. Map each byte to one rune for Go's rune-based regexp engine.
func ignoreByteString(value string) string {
	chars := make([]rune, len(value))
	for i := range len(value) {
		chars[i] = rune(value[i])
	}
	return string(chars)
}

func ignoreClass(pattern string, start int) ([256]bool, int, bool) {
	var members [256]bool
	i := start
	negated := i < len(pattern) && (pattern[i] == '!' || pattern[i] == '^')
	if negated {
		i++
	}
	first := i
	var previous byte
	for ; i < len(pattern); i++ {
		value := pattern[i]
		if value == ']' && i != first {
			if negated {
				for value := range members {
					members[value] = !members[value]
				}
			}
			return members, i, true
		}
		switch {
		case value == '\\':
			i++
			if i == len(pattern) {
				return members, i, false
			}
			value = pattern[i]
		case value == '-' && previous != 0 && i+1 < len(pattern) && pattern[i+1] != ']':
			i++
			last := pattern[i]
			if last == '\\' {
				i++
				if i == len(pattern) {
					return members, i, false
				}
				last = pattern[i]
			}
			for value := int(previous); value <= int(last); value++ {
				members[value] = true
			}
			previous = 0
			continue
		case value == '[' && i+1 < len(pattern) && pattern[i+1] == ':':
			end := strings.IndexByte(pattern[i+2:], ']')
			if end > 0 && pattern[i+1+end] == ':' {
				class := pattern[i+2 : i+1+end]
				if !ignoreNamedClass(&members, class) {
					return members, i, false
				}
				i += end + 2
				previous = 0
				continue
			}
		}
		members[value] = true
		previous = value
	}
	return members, i, false
}

func ignoreNamedClass(members *[256]bool, class string) bool {
	for value := range members {
		alpha := value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
		digit := value >= '0' && value <= '9'
		var match bool
		switch class {
		case "alnum":
			match = alpha || digit
		case "alpha":
			match = alpha
		case "blank":
			match = value == ' ' || value == '\t'
		case "cntrl":
			match = value < 32 || value == 127
		case "digit":
			match = digit
		case "graph":
			match = value > 32 && value < 127
		case "lower":
			match = value >= 'a' && value <= 'z'
		case "print":
			match = value >= 32 && value < 127
		case "punct":
			match = value > 32 && value < 127 && !alpha && !digit
		case "space":
			match = value == ' ' || value == '\t' || value == '\n' || value == '\r'
		case "upper":
			match = value >= 'A' && value <= 'Z'
		case "xdigit":
			match = digit || value >= 'a' && value <= 'f' || value >= 'A' && value <= 'F'
		default:
			return false
		}
		members[value] = members[value] || match
	}
	return true
}
