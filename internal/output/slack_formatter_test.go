package output

import (
	"testing"
)

func TestMarkdownToSlack_Bold(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"double asterisks", "This is **bold** text", "This is *bold* text"},
		{"multiple bolds", "**one** and **two**", "*one* and *two*"},
		{"already single", "This is *italic* text", "This is *italic* text"},
		{"no bold", "plain text", "plain text"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MarkdownToSlack(tt.input)
			if got != tt.want {
				t.Errorf("MarkdownToSlack(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestMarkdownToSlack_Headings(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"h1", "# Title", "*Title*"},
		{"h2", "## Section", "*Section*"},
		{"h3", "### Subsection", "*Subsection*"},
		{"h2 with content", "## My Section\nsome text", "*My Section*\nsome text"},
		{"not a heading", "This is #not a heading", "This is #not a heading"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MarkdownToSlack(tt.input)
			if got != tt.want {
				t.Errorf("MarkdownToSlack(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestMarkdownToSlack_Links(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"basic link", "[Click here](https://example.com)", "<https://example.com|Click here>"},
		{"image", "![Logo](https://example.com/logo.png)", "<https://example.com/logo.png|Logo>"},
		{"image no alt", "![](https://example.com/img.png)", "https://example.com/img.png"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MarkdownToSlack(tt.input)
			if got != tt.want {
				t.Errorf("MarkdownToSlack(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestMarkdownToSlack_HorizontalRule(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"dashes", "---", "———"},
		{"asterisks", "***", "———"},
		{"underscores", "___", "———"},
		{"with content", "above\n---\nbelow", "above\n———\nbelow"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MarkdownToSlack(tt.input)
			if got != tt.want {
				t.Errorf("MarkdownToSlack(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestMarkdownToSlack_Empty(t *testing.T) {
	if got := MarkdownToSlack(""); got != "" {
		t.Errorf("MarkdownToSlack(\"\") = %q, want empty", got)
	}
}

func TestMarkdownToSlack_Table(t *testing.T) {
	input := `| Dimension | Assessment |
|-----------|------------|
| **Customer Impact** | HIGH - Single customer |
| **Scope** | REGIONAL - Limited to rgn DC |`

	got := MarkdownToSlack(input)

	// Should NOT contain pipe-delimited table rows
	if contains(got, "|-----------|") {
		t.Errorf("separator row should be removed, got:\n%s", got)
	}

	// Should contain header-formatted values
	if !contains(got, "*Dimension:*") {
		t.Errorf("missing header label 'Dimension', got:\n%s", got)
	}
	if !contains(got, "*Assessment:*") {
		t.Errorf("missing header label 'Assessment', got:\n%s", got)
	}
	if !contains(got, "Customer Impact") {
		t.Errorf("missing cell value 'Customer Impact', got:\n%s", got)
	}
	if !contains(got, "REGIONAL") {
		t.Errorf("missing cell value 'REGIONAL', got:\n%s", got)
	}
}

func TestMarkdownToSlack_TableWithBold(t *testing.T) {
	input := `| Key | Value |
|-----|-------|
| **Name** | Test |`

	got := MarkdownToSlack(input)

	// **Name** should become *Name* (bold conversion)
	if !contains(got, "*Name*") {
		t.Errorf("bold not converted in table cell, got:\n%s", got)
	}
	// Should be bullet point format
	if !contains(got, "•") {
		t.Errorf("table rows should be bullet points, got:\n%s", got)
	}
}

func TestMarkdownToSlack_MixedContent(t *testing.T) {
	input := `## Root Cause Analysis

**Finding**: The server is overloaded.

| Metric | Value |
|--------|-------|
| CPU | 95% |
| Memory | 87% |

---

## Recommendations

- Scale horizontally
- Add caching`

	got := MarkdownToSlack(input)

	// Headings converted
	if !contains(got, "*Root Cause Analysis*") {
		t.Errorf("heading not converted, got:\n%s", got)
	}
	// Bold converted
	if !contains(got, "*Finding*") {
		t.Errorf("bold not converted, got:\n%s", got)
	}
	// Table converted
	if !contains(got, "*Metric:*") {
		t.Errorf("table header not converted, got:\n%s", got)
	}
	// HR converted
	if !contains(got, "———") {
		t.Errorf("horizontal rule not converted, got:\n%s", got)
	}
	// List items preserved
	if !contains(got, "- Scale horizontally") {
		t.Errorf("list items should be preserved, got:\n%s", got)
	}
}

func TestMarkdownToSlack_CodeBlocksPreserved(t *testing.T) {
	input := "Run this:\n```bash\nsysctl -w net.core.somaxconn=65535\n```"
	got := MarkdownToSlack(input)

	// Code blocks should pass through unchanged
	if !contains(got, "```bash") {
		t.Errorf("code block should be preserved, got:\n%s", got)
	}
	if !contains(got, "sysctl -w net.core.somaxconn=65535") {
		t.Errorf("code content should be preserved, got:\n%s", got)
	}
}

func TestFormatForSlack_NoStructuredBlocks_ConvertsMarkdown(t *testing.T) {
	// Agent output with no [FINAL_RESULT] blocks — just raw markdown
	raw := "## Summary\n\n**CPU** is at 95%.\n\n---\n\nCheck the server."
	parsed := Parse(raw)
	got := FormatForSlack(parsed)

	// Should be converted to Slack format
	if contains(got, "## Summary") {
		t.Errorf("heading should be converted, got:\n%s", got)
	}
	if contains(got, "**CPU**") {
		t.Errorf("bold should be converted, got:\n%s", got)
	}
	if !contains(got, "*Summary*") {
		t.Errorf("heading should become bold, got:\n%s", got)
	}
	if !contains(got, "*CPU*") {
		t.Errorf("bold should become single asterisk, got:\n%s", got)
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && containsSubstring(s, substr)
}

func containsSubstring(s, sub string) bool {
	return len(s) >= len(sub) && searchString(s, sub)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
