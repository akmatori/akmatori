package output

import (
	"strings"
	"testing"
)

func TestWithinSlackBudget(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		maxBytes int
		want     bool
	}{
		{name: "empty fits zero", text: "", maxBytes: 0, want: true},
		{name: "empty fits positive", text: "", maxBytes: 100, want: true},
		{name: "short fits", text: "hello", maxBytes: 100, want: true},
		{name: "exact fits", text: "hello", maxBytes: 5, want: true},
		{name: "over fails", text: "hello", maxBytes: 4, want: false},
		{name: "multibyte counted as bytes", text: "你", maxBytes: 2, want: false},
		{name: "multibyte fits", text: "你", maxBytes: 3, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := WithinSlackBudget(tt.text, tt.maxBytes); got != tt.want {
				t.Errorf("WithinSlackBudget(%q, %d) = %v, want %v", tt.text, tt.maxBytes, got, tt.want)
			}
		})
	}
}

func TestShortenForSlackBudget_FinalResult(t *testing.T) {
	parsed := &ParsedOutput{
		FinalResult: &FinalResult{
			Status:  "resolved",
			Summary: "Database failover completed successfully and traffic restored to primary.",
			ActionsTaken: []string{
				"Promoted standby replica to primary",
				"Updated DNS records",
			},
			Recommendations: []string{
				"Add automated failover monitoring alert",
				"Schedule capacity review",
			},
		},
	}

	got := ShortenForSlackBudget(parsed, 4000)
	if !strings.Contains(got, "Resolved") {
		t.Errorf("expected status header in output, got %q", got)
	}
	if !strings.Contains(got, "Database failover completed successfully") {
		t.Error("expected summary in output")
	}
	if !strings.Contains(got, "Promoted standby replica to primary") {
		t.Error("expected first action in output")
	}
	if strings.Contains(got, "Updated DNS records") {
		t.Error("expected only first action; subsequent actions should be dropped")
	}
	if !strings.Contains(got, "Add automated failover monitoring alert") {
		t.Error("expected first recommendation in output")
	}
	if strings.Contains(got, "Schedule capacity review") {
		t.Error("expected only first recommendation; subsequent recommendations should be dropped")
	}
}

func TestShortenForSlackBudget_Escalation(t *testing.T) {
	parsed := &ParsedOutput{
		Escalation: &Escalation{
			Urgency:          "high",
			Reason:           "Database is in read-only mode and write latency is climbing.",
			SuggestedActions: []string{"Page DBA on-call", "Open vendor support ticket"},
		},
	}

	got := ShortenForSlackBudget(parsed, 4000)
	if !strings.Contains(got, "ESCALATION REQUIRED") {
		t.Errorf("expected escalation header, got %q", got)
	}
	if !strings.Contains(got, "HIGH") {
		t.Error("expected urgency level in header")
	}
	if !strings.Contains(got, "Page DBA on-call") {
		t.Error("expected first suggested action in output")
	}
	if strings.Contains(got, "Open vendor support ticket") {
		t.Error("expected only first suggested action; subsequent actions should be dropped")
	}
}

func TestShortenForSlackBudget_FreeForm(t *testing.T) {
	parsed := &ParsedOutput{
		CleanOutput: "Investigation produced these findings:\n- noisy disk\n- slow GC pauses",
		RawOutput:   "ignore raw when CleanOutput present",
	}
	got := ShortenForSlackBudget(parsed, 4000)
	if got != parsed.CleanOutput {
		t.Errorf("expected free-form output passthrough, got %q", got)
	}
}

func TestShortenForSlackBudget_RawFallback(t *testing.T) {
	parsed := &ParsedOutput{
		RawOutput: "raw only",
	}
	got := ShortenForSlackBudget(parsed, 4000)
	if got != "raw only" {
		t.Errorf("expected raw output passthrough, got %q", got)
	}
}

func TestShortenForSlackBudget_NilSafe(t *testing.T) {
	if got := ShortenForSlackBudget(nil, 4000); got != "" {
		t.Errorf("expected empty string for nil parsed output, got %q", got)
	}
}

func TestShortenForSlackBudget_ZeroBudget(t *testing.T) {
	parsed := &ParsedOutput{CleanOutput: "anything"}
	if got := ShortenForSlackBudget(parsed, 0); got != "" {
		t.Errorf("expected empty string for zero budget, got %q", got)
	}
}

func TestShortenForSlackBudget_TruncatesOverBudgetFinalResult(t *testing.T) {
	long := strings.Repeat("x", 5000)
	parsed := &ParsedOutput{
		FinalResult: &FinalResult{
			Status:          "resolved",
			Summary:         long,
			ActionsTaken:    []string{long},
			Recommendations: []string{long},
		},
	}
	got := ShortenForSlackBudget(parsed, 200)
	if len(got) > 200 {
		t.Errorf("expected len(got)<=200, got %d", len(got))
	}
	if !strings.Contains(got, "truncated") {
		t.Errorf("expected truncation notice in output, got %q", got)
	}
}

func TestShortenForSlackBudget_TruncatesOverBudgetFreeForm(t *testing.T) {
	parsed := &ParsedOutput{CleanOutput: strings.Repeat("y", 5000)}
	got := ShortenForSlackBudget(parsed, 250)
	if len(got) > 250 {
		t.Errorf("expected len(got)<=250, got %d", len(got))
	}
	if !strings.Contains(got, "truncated") {
		t.Errorf("expected truncation notice in output")
	}
}

func TestShortenForSlackBudget_TinyBudget(t *testing.T) {
	parsed := &ParsedOutput{CleanOutput: "Hello world how are you"}
	// Budget smaller than the truncation notice — the helper must still
	// return at most maxBytes bytes (no notice appended).
	got := ShortenForSlackBudget(parsed, 5)
	if len(got) > 5 {
		t.Errorf("expected len(got)<=5, got %d (%q)", len(got), got)
	}
}

func TestShortenForSlackBudget_PreservesUTF8(t *testing.T) {
	// Multi-byte runes — truncating must not split a code point.
	parsed := &ParsedOutput{CleanOutput: strings.Repeat("你好", 200)}
	got := ShortenForSlackBudget(parsed, 100)
	if len(got) > 100 {
		t.Errorf("expected len(got)<=100, got %d", len(got))
	}
	// Result must still be valid UTF-8 — ranging over it should not produce
	// the replacement rune for invalid bytes.
	for _, r := range got {
		if r == 0xFFFD {
			t.Errorf("output contains UTF-8 replacement rune (split mid-codepoint): %q", got)
			break
		}
	}
}
