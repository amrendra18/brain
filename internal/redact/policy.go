package redact

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

var ErrPolicyInvalid = errors.New("the privacy policy cannot be read safely or is invalid")

const Version = "2"
const privateValueRule = "private-value"
const privateKeyRule = "private-key"

var privateKeyStart = regexp.MustCompile(`-----BEGIN ((?:RSA |EC |DSA |OPENSSH |ENCRYPTED )?PRIVATE KEY)-----`)

type rule struct {
	name       string
	expression *regexp.Regexp
	group      int
}

var rules = []rule{
	{"aws-access-key", regexp.MustCompile(`(?:AKIA|ASIA)[A-Z0-9]{16}`), 0},
	{"aws-secret-key", regexp.MustCompile(`(?i:aws_secret_access_key|aws-secret-access-key|awsSecretAccessKey|secret_access_key)["']?[ \t]*(?:=|:)[ \t]*["']?([A-Za-z0-9/+=]{40})`), 1},
	{"github-token", regexp.MustCompile(`(?:gh[pousr]_[A-Za-z0-9]{36}|github_pat_[A-Za-z0-9_]{20,255})`), 0},
	{"google-api-key", regexp.MustCompile(`AIza[A-Za-z0-9_-]{35}`), 0},
	{"slack-token", regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{10,255}`), 0},
	{"openai-token", regexp.MustCompile(`sk-(?:proj-|svcacct-)?[A-Za-z0-9_-]{20,512}`), 0},
}

type Policy struct {
	literals    []string
	fingerprint string
}

type Result struct {
	Text  string
	Rules []string
}

type span struct {
	start, end int
	rule       string
}

func Default() *Policy {
	policy, _ := newPolicy(nil)
	return policy
}

func newPolicy(literals []string) (*Policy, error) {
	if len(literals) > 10000 {
		return nil, ErrPolicyInvalid
	}
	names := []string{privateValueRule, privateKeyRule}
	for _, rule := range rules {
		names = append(names, rule.name)
	}
	values := append([]string{}, literals...)
	sort.Strings(values)
	unique := values[:0]
	for _, value := range values {
		if value == "" || len(value) > 4096 || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
			return nil, ErrPolicyInvalid
		}
		for _, name := range names {
			mark := marker(name)
			if strings.Contains(mark, value) || strings.Contains(value, mark) {
				return nil, ErrPolicyInvalid
			}
			for n := 1; n < len(mark); n++ {
				if strings.HasSuffix(value, mark[:n]) || strings.HasPrefix(value, mark[len(mark)-n:]) {
					return nil, ErrPolicyInvalid
				}
			}
		}
		if len(unique) == 0 || unique[len(unique)-1] != value {
			unique = append(unique, value)
		}
	}
	catalog := []string{Version, privateKeyStart.String()}
	for _, rule := range rules {
		catalog = append(catalog, rule.name, rule.expression.String(), strconv.Itoa(rule.group))
	}
	data, _ := json.Marshal([][]string{catalog, unique})
	digest := sha256.Sum256(data)
	return &Policy{literals: unique, fingerprint: hex.EncodeToString(digest[:])}, nil
}

func (policy *Policy) Fingerprint() string { return policy.fingerprint }

func marker(rule string) string { return "[redacted: " + rule + "]" }

func tokenByte(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z' || value >= '0' && value <= '9' || value == '_' || value == '-'
}

func (policy *Policy) scan(text string, visit func(span) bool) {
	for _, rule := range rules {
		if rule.name == "aws-secret-key" && !strings.ContainsAny(text, "=:") {
			continue
		}
		for offset := 0; offset < len(text); {
			match := rule.expression.FindStringSubmatchIndex(text[offset:])
			if match == nil {
				break
			}
			start, end := offset+match[2*rule.group], offset+match[2*rule.group+1]
			beginning := offset + match[0]
			offset += match[1]
			if beginning > 0 && tokenByte(text[beginning-1]) || end < len(text) && (tokenByte(text[end]) || rule.group == 1 && strings.ContainsRune("+/=", rune(text[end]))) {
				continue
			}
			if !visit(span{start, end, rule.name}) {
				return
			}
		}
	}
	for offset := 0; offset < len(text); {
		match := privateKeyStart.FindStringSubmatchIndex(text[offset:])
		if match == nil {
			break
		}
		start, body := offset+match[0], offset+match[1]
		closing := "-----END " + text[offset+match[2]:offset+match[3]] + "-----"
		end := len(text)
		if at := strings.Index(text[body:], closing); at >= 0 {
			end = body + at + len(closing)
		}
		if !visit(span{start, end, privateKeyRule}) {
			return
		}
		offset = end
	}
	for _, value := range policy.literals {
		for offset := 0; offset < len(text); {
			at := strings.Index(text[offset:], value)
			if at < 0 {
				break
			}
			start := offset + at
			if !visit(span{start, start + len(value), privateValueRule}) {
				return
			}
			offset = start + 1
		}
	}
}

func (policy *Policy) Match(text string) bool {
	matched := false
	policy.scan(text, func(span) bool { matched = true; return false })
	return matched
}

type matchStart struct {
	end  int
	rule string
}

func covered(starts []matchStart) []span {
	found := []span{}
	for i := 0; i < len(starts); i++ {
		if starts[i].end == 0 {
			continue
		}
		current := span{i, starts[i].end, starts[i].rule}
		length := current.end - current.start
		for j := i + 1; j < current.end; j++ {
			next := starts[j]
			if next.end == 0 {
				continue
			}
			if size := next.end - j; size > length || size == length && next.rule < current.rule {
				current.rule, length = next.rule, size
			}
			current.end = max(current.end, next.end)
		}
		found = append(found, current)
		i = current.end - 1
	}
	return found
}

func (policy *Policy) Apply(text string) Result {
	result := Result{Text: text, Rules: []string{}}
	var starts []matchStart
	names := map[string]bool{}
	add := func(found span) bool {
		if starts == nil {
			starts = make([]matchStart, len(text))
		}
		names[found.rule] = true
		old := starts[found.start]
		if found.end > old.end || found.end == old.end && found.rule < old.rule {
			starts[found.start] = matchStart{found.end, found.rule}
		}
		return true
	}
	policy.scan(text, add)
	if starts == nil {
		return result
	}
	regions := covered(starts)
	for {
		changed := false
		offset := 0
		gap := func(end int) {
			policy.scan(text[offset:end], func(found span) bool {
				found.start += offset
				found.end += offset
				changed = true
				return add(found)
			})
		}
		for _, region := range regions {
			gap(region.start)
			offset = region.end
		}
		gap(len(text))
		if !changed {
			break
		}
		regions = covered(starts)
	}
	var out strings.Builder
	copied := 0
	for _, region := range regions {
		out.WriteString(text[copied:region.start])
		out.WriteString(marker(region.rule))
		copied = region.end
	}
	out.WriteString(text[copied:])
	result.Text = out.String()
	for name := range names {
		result.Rules = append(result.Rules, name)
	}
	sort.Strings(result.Rules)
	return result
}
