package redact

import (
	"strings"
	"testing"
)

func secretExamples() map[string]string {
	return map[string]string{
		"aws-access-key": "AK" + "IA" + strings.Repeat("Q", 16),
		"aws-secret-key": "aws_secret_access_key = \"" + strings.Repeat("Q", 40) + "\"",
		"github-token":   "gh" + "p_" + strings.Repeat("Q", 36),
		"google-api-key": "AI" + "za" + strings.Repeat("Q", 35),
		"slack-token":    "xo" + "xb-" + strings.Repeat("7", 12) + "-" + strings.Repeat("Q", 24),
		"openai-token":   "s" + "k-proj-" + strings.Repeat("Q", 48),
		"private-key":    "-----BEGIN " + "RSA PRIVATE KEY-----\n" + "synthetic key body\n-----END RSA PRIVATE KEY-----",
	}
}

func TestRecognizedSecrets(t *testing.T) {
	policy := Default()
	for rule, value := range secretExamples() {
		t.Run(rule, func(t *testing.T) {
			input := "Allowed introduction\n" + value + "\nAllowed conclusion"
			result := policy.Apply(input)
			if !strings.Contains(result.Text, "[redacted: "+rule+"]") || !strings.Contains(result.Text, "Allowed introduction") || !strings.Contains(result.Text, "Allowed conclusion") {
				t.Fatalf("rule %s did not retain context and mark the match", rule)
			}
			if len(result.Rules) != 1 || result.Rules[0] != rule || !policy.Match(input) || policy.Match(result.Text) {
				t.Fatalf("rule %s had inconsistent reporting", rule)
			}
			if next := policy.Apply(result.Text); next.Text != result.Text || len(next.Rules) != 0 {
				t.Fatalf("rule %s changed its own marker", rule)
			}
		})
	}
}

func TestLiteralOverlapAndFingerprint(t *testing.T) {
	policy, err := newPolicy([]string{"cdef", "abc", "unicode秘密", "abc"})
	if err != nil {
		t.Fatal(err)
	}
	result := policy.Apply("before abcdef after unicode秘密")
	if result.Text != "before [redacted: private-value] after [redacted: private-value]" || len(result.Rules) != 1 || result.Rules[0] != "private-value" {
		t.Fatalf("overlap or Unicode replacement wrong: %+v", result)
	}
	same, err := newPolicy([]string{"unicode秘密", "abc", "cdef"})
	if err != nil || same.Fingerprint() != policy.Fingerprint() || len(policy.Fingerprint()) != 64 {
		t.Fatal("fingerprint is not canonical", err)
	}
	other, _ := newPolicy([]string{"different-private-value"})
	if other.Fingerprint() == policy.Fingerprint() || Default().Fingerprint() == policy.Fingerprint() {
		t.Fatal("policy changes kept the fingerprint")
	}
	if next := policy.Apply(result.Text); next.Text != result.Text || len(next.Rules) != 0 {
		t.Fatal("literal marker is not stable")
	}
}

func TestSecretNearMatches(t *testing.T) {
	allowed := []string{"ordinary text", "AK" + "IA" + strings.Repeat("Q", 15), "x" + "AK" + "IA" + strings.Repeat("Q", 16), "gh" + "p_" + strings.Repeat("Q", 37), "s" + "k-short", "-----BEGIN PUBLIC KEY-----\nallowed\n-----END PUBLIC KEY-----", "AWS_SECRET_ACCESS_KEY=" + strings.Repeat("Q", 39), "AWS_SECRET_ACCESS_KEY=" + strings.Repeat("Q", 40) + "/", "not_aws_secret_access_key=" + strings.Repeat("Q", 40)}
	for _, input := range allowed {
		result := Default().Apply(input)
		if result.Text != input || len(result.Rules) != 0 || Default().Match(input) {
			t.Fatal("allowed near-match changed")
		}
	}
}

func TestAWSSecretAssignmentSeparatorsAndCaseFolding(t *testing.T) {
	policy := Default()
	value := strings.Repeat("Q", 40)
	for _, label := range []string{"aws_secret_access_key", "AWS_SECRET_ACCESS_KEY", "aws-secret-access-key", "awsSecretAccessKey", "secret_access_key", "awſ_ſecret_acceſſ_Key", "ſecret_acceſſ_Key"} {
		for _, separator := range []string{"=", ":", "\" \t: \t\"", "' = '"} {
			input := label + separator + value
			result := policy.Apply(input)
			if result.Text != label+separator+marker("aws-secret-key") || strings.Join(result.Rules, ",") != "aws-secret-key" || !policy.Match(input) {
				t.Fatalf("assignment changed: label=%q separator=%q", label, separator)
			}
		}
		for _, separator := range []string{"", " ", "：", "＝", "\n=", "\n:"} {
			input := label + separator + value
			result := policy.Apply(input)
			if result.Text != input || len(result.Rules) != 0 || policy.Match(input) {
				t.Fatalf("near-match changed: label=%q separator=%q", label, separator)
			}
		}
	}
}

func TestPrivateKeyIncompleteAndMultiple(t *testing.T) {
	input := "before\n-----BEGIN " + "PRIVATE KEY-----\nprotected remainder"
	result := Default().Apply(input)
	if result.Text != "before\n[redacted: private-key]" {
		t.Fatal("unterminated private key escaped")
	}
	first := secretExamples()["private-key"]
	result = Default().Apply(first + " allowed " + first)
	if result.Text != "[redacted: private-key] allowed [redacted: private-key]" || len(result.Rules) != 1 {
		t.Fatal("private key blocks combined incorrectly")
	}
}

func TestLiteralValidation(t *testing.T) {
	for _, values := range [][]string{{""}, {"contains\x00zero"}, {strings.Repeat("X", 4097)}, {"redacted"}, {"["}, {"prefix [redacted: private-value] suffix"}, {string([]byte{255})}} {
		if _, err := newPolicy(values); err != ErrPolicyInvalid {
			t.Fatal("invalid literal accepted")
		}
	}
	if _, err := newPolicy(make([]string, 10001)); err != ErrPolicyInvalid {
		t.Fatal("too many values accepted")
	}
	policy, err := newPolicy(nil)
	if err != nil || policy.Fingerprint() != Default().Fingerprint() {
		t.Fatal("empty policy changed defaults")
	}
}

func TestSecretFormsAndOverlap(t *testing.T) {
	forms := map[string][]string{
		"aws-access-key": {"AS" + "IA" + strings.Repeat("7", 16)},
		"aws-secret-key": {"\"AWS_SECRET_ACCESS_KEY\": \"" + strings.Repeat("x", 40) + "\"", "awsSecretAccessKey=" + strings.Repeat("+", 40)},
		"github-token":   {"github_" + "pat_" + strings.Repeat("x", 22) + "_" + strings.Repeat("y", 59), "gh" + "s_" + strings.Repeat("x", 36)},
		"slack-token":    {"xo" + "xp-" + strings.Repeat("7", 20)},
		"openai-token":   {"s" + "k-" + strings.Repeat("x", 48), "s" + "k-svcacct-" + strings.Repeat("x", 48)},
		"private-key":    {"-----BEGIN " + "OPENSSH PRIVATE KEY-----\r\nbody\r\n-----END OPENSSH PRIVATE KEY-----"},
	}
	for rule, inputs := range forms {
		for _, input := range inputs {
			result := Default().Apply(input)
			if len(result.Rules) != 1 || result.Rules[0] != rule || !strings.Contains(result.Text, marker(rule)) {
				t.Fatalf("form for %s was not detected", rule)
			}
		}
	}
	policy, err := newPolicy([]string{strings.Repeat("Q", 20)})
	if err != nil {
		t.Fatal(err)
	}
	result := policy.Apply(secretExamples()["github-token"])
	if result.Text != marker("github-token") || strings.Join(result.Rules, ",") != "github-token,private-value" {
		t.Fatal("overlapping rules lost their identity", result.Rules)
	}
	if result := Default().Apply(""); result.Text != "" || result.Rules == nil || len(result.Rules) != 0 {
		t.Fatal("empty result has inconsistent shape")
	}
}

func TestPrivateValuesCannotCreateMarkerMatches(t *testing.T) {
	for _, value := range []string{"value]tail", "prefix[", "]suffix", "prefix[redacted: ", "private-value]suffix"} {
		if _, err := newPolicy([]string{"SECRETZZ", value}); err != ErrPolicyInvalid {
			t.Fatal("marker-boundary literal accepted")
		}
	}
	policy, err := newPolicy([]string{"internal[bracket]value"})
	if err != nil {
		t.Fatal("unrelated internal brackets refused", err)
	}
	result := policy.Apply("before internal[bracket]value after")
	if result.Text != "before [redacted: private-value] after" || policy.Match(result.Text) {
		t.Fatal("internal bracket value escaped")
	}
}

func TestRedactionOutputHasNoMatches(t *testing.T) {
	policy, err := newPolicy([]string{"PRIVATEZZ", "overlapABC", "ABCtail", "unicode秘密"})
	if err != nil {
		t.Fatal(err)
	}
	inputs := []string{"PRIVATEZZ", "overlapABCtail", "unicode秘密", "internal[bracket]value"}
	for _, value := range secretExamples() {
		inputs = append(inputs, value)
	}
	for _, left := range inputs {
		for _, right := range inputs {
			result := policy.Apply(left + " allowed " + right)
			if policy.Match(result.Text) || policy.Apply(result.Text).Text != result.Text {
				t.Fatal("replacement created or retained a protected match")
			}
		}
	}
}
