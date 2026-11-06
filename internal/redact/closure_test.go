package redact

import (
	"runtime"
	"strings"
	"testing"
)

func TestReplacementBoundaryClosure(t *testing.T) {
	p, err := newPolicy([]string{"PRIVATEZZ"})
	if err != nil {
		t.Fatal(err)
	}
	token := "gh" + "p_" + strings.Repeat("Q", 36)
	for _, input := range []string{"allowed PRIVATEZZ" + token + " allowed", "allowed " + token + "PRIVATEZZ allowed", "allowed PRIVATEZZ" + token + "PRIVATEZZ allowed"} {
		first := p.Apply(input)
		second := p.Apply(first.Text)
		if first.Text != second.Text || p.Match(first.Text) || !strings.HasPrefix(first.Text, "allowed ") || !strings.HasSuffix(first.Text, " allowed") {
			t.Fatalf("replacement exposed a remaining detector match: first=%q second=%q", first.Text, second.Text)
		}
		if strings.Join(first.Rules, ",") != "github-token,private-value" {
			t.Fatal("rule reports lost", first.Rules)
		}
	}

}

func TestAllDetectorReplacementBoundaries(t *testing.T) {
	policy, err := newPolicy([]string{"PRIVATEZZ"})
	if err != nil {
		t.Fatal(err)
	}
	for rule, secret := range secretExamples() {
		for _, input := range []string{"PRIVATEZZ" + secret, secret + "PRIVATEZZ", "PRIVATEZZ" + secret + "PRIVATEZZ" + secret + "PRIVATEZZ"} {
			result := policy.Apply("allowed before " + input + " allowed after")
			if policy.Match(result.Text) || policy.Apply(result.Text).Text != result.Text || !strings.HasPrefix(result.Text, "allowed before ") || !strings.HasSuffix(result.Text, " allowed after") {
				t.Fatal("unstable boundary", rule)
			}
		}
	}
}

func BenchmarkOverlappingLiterals(b *testing.B) {
	values := make([]string, 128)
	for i := range values {
		values[i] = strings.Repeat("X", i+1)
	}
	p, err := newPolicy(values)
	if err != nil {
		b.Fatal(err)
	}
	input := strings.Repeat("X", 8192)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if p.Apply(input).Text != marker(privateValueRule) {
			b.Fatal("wrong replacement")
		}
	}
}

func TestOverlapAllocationBound(t *testing.T) {
	values := make([]string, 128)
	for i := range values {
		values[i] = strings.Repeat("X", i+1)
	}
	policy, err := newPolicy(values)
	if err != nil {
		t.Fatal(err)
	}
	input := strings.Repeat("X", 8192)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	result := policy.Apply(input)
	runtime.ReadMemStats(&after)
	if result.Text != marker(privateValueRule) || policy.Match(result.Text) {
		t.Fatal("overlap replacement failed")
	}
	allocated := after.TotalAlloc - before.TotalAlloc
	if allocated > 1<<20 {
		t.Fatalf("8 KiB overlap input allocated %d bytes, above measured 1 MiB regression limit", allocated)
	}
	t.Logf("8 KiB overlap input allocated %d bytes", allocated)
}

func TestMatchStopsAtFirstLiteral(t *testing.T) {
	policy, err := newPolicy([]string{"X", strings.Repeat("X", 128)})
	if err != nil {
		t.Fatal(err)
	}
	input := strings.Repeat("X", 8192)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	matched := policy.Match(input)
	runtime.ReadMemStats(&after)
	if !matched {
		t.Fatal("positive match missing")
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 1<<20 {
		t.Fatalf("boolean literal match allocated %d bytes", allocated)
	}
}
