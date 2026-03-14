package services

import (
	"strings"
	"testing"
)

// --- ValidateSkillName Tests (Table-Driven) ---

func TestValidateSkillName_ValidNames(t *testing.T) {
	validNames := []struct {
		name  string
		input string
	}{
		{"simple lowercase", "zabbix"},
		{"single char start", "a"},
		{"two chars", "ab"},
		{"kebab-case simple", "zabbix-analyst"},
		{"kebab-case multiple", "ssh-server-admin"},
		{"kebab-case three parts", "my-cool-skill"},
		{"with numbers", "k8s-admin"},
		{"numbers in middle", "app123-handler"},
		{"numbers at end", "handler2"},
		{"max length 64", strings.Repeat("a", 64)},
		{"realistic names", "prometheus-alertmanager"},
		{"realistic names 2", "database-maintenance"},
		{"starts with letter ends number", "skill1"},
		{"complex realistic", "aws-ec2-instance-checker"},
	}

	for _, tt := range validNames {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSkillName(tt.input)
			if err != nil {
				t.Errorf("ValidateSkillName(%q) = %v, want nil", tt.input, err)
			}
		})
	}
}

func TestValidateSkillName_InvalidNames(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		errContains string
	}{
		// Empty/length issues
		{"empty string", "", "cannot be empty"},
		{"too long 65 chars", strings.Repeat("a", 65), "64 characters or less"},
		{"too long 100 chars", strings.Repeat("a", 100), "64 characters or less"},

		// Case issues
		{"uppercase letters", "Zabbix", "kebab-case"},
		{"all uppercase", "ZABBIX", "kebab-case"},
		{"mixed case", "zabbixAnalyst", "kebab-case"},
		{"uppercase in middle", "zabbixA", "kebab-case"},

		// Invalid start/end
		{"starts with number", "1zabbix", "kebab-case"},
		{"starts with hyphen", "-zabbix", "kebab-case"},
		{"ends with hyphen", "zabbix-", "kebab-case"},
		{"only hyphen", "-", "kebab-case"},
		{"only hyphens", "---", "kebab-case"},

		// Consecutive hyphens
		{"consecutive hyphens", "zabbix--analyst", "kebab-case"},
		{"triple hyphens", "zab---bix", "kebab-case"},

		// Invalid characters
		{"underscore", "zabbix_analyst", "kebab-case"},
		{"space", "zabbix analyst", "kebab-case"},
		{"dot", "zabbix.analyst", "kebab-case"},
		{"slash", "zabbix/analyst", "kebab-case"},
		{"backslash", "zabbix\\analyst", "kebab-case"},
		{"special chars", "zabbix@analyst", "kebab-case"},
		{"unicode", "zabbix-分析師", "kebab-case"},
		{"emoji", "zabbix-🔥", "kebab-case"},
		{"colon", "zabbix:analyst", "kebab-case"},
		{"semicolon", "zabbix;analyst", "kebab-case"},
		{"quotes", "zabbix\"analyst", "kebab-case"},

		// Path traversal attempts
		{"path traversal dot dot", "../evil", "kebab-case"},
		{"path traversal embedded", "skill/../../../etc/passwd", "kebab-case"},

		// Command injection attempts
		{"command injection semicolon", "skill;rm -rf /", "kebab-case"},
		{"command injection pipe", "skill|cat /etc/passwd", "kebab-case"},
		{"command injection backtick", "skill`whoami`", "kebab-case"},
		{"command injection dollar", "skill$(whoami)", "kebab-case"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSkillName(tt.input)
			if err == nil {
				t.Errorf("ValidateSkillName(%q) = nil, want error containing %q", tt.input, tt.errContains)
				return
			}
			if !strings.Contains(err.Error(), tt.errContains) {
				t.Errorf("ValidateSkillName(%q) = %q, want error containing %q", tt.input, err.Error(), tt.errContains)
			}
		})
	}
}

// --- ValidateScriptFilename Tests (Table-Driven) ---

func TestValidateScriptFilename_ValidFilenames(t *testing.T) {
	validFilenames := []struct {
		name  string
		input string
	}{
		{"simple python", "script.py"},
		{"simple shell", "run.sh"},
		{"simple bash", "deploy.bash"},
		{"yaml config", "config.yaml"},
		{"json file", "data.json"},
		{"text file", "readme.txt"},
		{"markdown", "README.md"},
		{"numbers in name", "script123.py"},
		{"underscore", "my_script.py"},
		{"dash", "my-script.py"},
		{"mixed", "my_cool-script123.py"},
		{"double extension", "file.test.py"},
		{"caps in extension", "file.PY"},
		{"caps in name", "FILE.py"},
		{"all caps", "SCRIPT.PY"},
		{"single char name", "a.py"},
		{"single char extension", "script.a"},
	}

	for _, tt := range validFilenames {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateScriptFilename(tt.input)
			if err != nil {
				t.Errorf("ValidateScriptFilename(%q) = %v, want nil", tt.input, err)
			}
		})
	}
}

func TestValidateScriptFilename_InvalidFilenames(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		errContains string
	}{
		// Empty
		{"empty string", "", "cannot be empty"},

		// No extension
		{"no extension", "script", "must have a file extension"},
		{"no extension with underscore", "my_script", "must have a file extension"},

		// Path traversal - CRITICAL security checks
		{"dot dot prefix", "../script.py", "path traversal"},
		{"dot dot middle", "foo/../script.py", "path traversal"},
		{"dot dot suffix", "script.py/..", "path traversal"},
		{"forward slash", "path/script.py", "path traversal"},
		{"backslash", "path\\script.py", "path traversal"},
		{"absolute path unix", "/etc/passwd", "path traversal"},
		{"absolute path windows", "C:\\Windows\\script.py", "path traversal"},
		{"double dot only", "..", "path traversal"},
		{"hidden with slash", ".hidden/script.py", "path traversal"},

		// Invalid characters
		{"space in name", "my script.py", "alphanumeric"},
		{"colon", "script:test.py", "alphanumeric"},
		{"semicolon", "script;test.py", "alphanumeric"},
		{"ampersand", "script&test.py", "alphanumeric"},
		{"pipe", "script|test.py", "alphanumeric"},
		{"dollar sign", "script$test.py", "alphanumeric"},
		{"backtick", "script`test.py", "alphanumeric"},
		{"single quote", "script'test.py", "alphanumeric"},
		{"double quote", "script\"test.py", "alphanumeric"},
		{"less than", "script<test.py", "alphanumeric"},
		{"greater than", "script>test.py", "alphanumeric"},
		{"asterisk", "script*.py", "alphanumeric"},
		{"question mark", "script?.py", "alphanumeric"},
		{"brackets", "script[0].py", "alphanumeric"},
		{"braces", "script{0}.py", "alphanumeric"},
		{"parentheses", "script(0).py", "alphanumeric"},
		{"at sign", "script@test.py", "alphanumeric"},
		{"hash", "script#test.py", "alphanumeric"},
		{"percent", "script%test.py", "alphanumeric"},
		{"caret", "script^test.py", "alphanumeric"},
		{"tilde", "script~test.py", "alphanumeric"},
		{"equals", "script=test.py", "alphanumeric"},
		{"plus", "script+test.py", "alphanumeric"},
		{"comma", "script,test.py", "alphanumeric"},

		// Unicode/encoding attacks
		{"unicode char", "script\u0000.py", "alphanumeric"},
		{"null byte", "script\x00.py", "alphanumeric"},
		{"newline", "script\n.py", "alphanumeric"},
		{"tab", "script\t.py", "alphanumeric"},
		{"carriage return", "script\r.py", "alphanumeric"},

		// Command injection in filename (note: / triggers path traversal check first)
		{"semicolon injection", "script;rm.py", "alphanumeric"},
		{"pipe injection", "script|cat.py", "alphanumeric"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateScriptFilename(tt.input)
			if err == nil {
				t.Errorf("ValidateScriptFilename(%q) = nil, want error containing %q", tt.input, tt.errContains)
				return
			}
			if !strings.Contains(err.Error(), tt.errContains) {
				t.Errorf("ValidateScriptFilename(%q) = %q, want error containing %q", tt.input, err.Error(), tt.errContains)
			}
		})
	}
}

// --- truncateString Tests ---

func TestTruncateString(t *testing.T) {
	tests := []struct {
		name  string
		input string
		max   int
		want  string
	}{
		{"empty string", "", 10, ""},
		{"shorter than max", "hello", 10, "hello"},
		{"exact length", "hello", 5, "hello"},
		{"longer than max", "hello world", 8, "hello..."},
		{"max 3 truncates without ellipsis", "hello", 3, "hel"},
		{"max 4", "hello", 4, "h..."},
		{"max 1", "hello", 1, "h"},
		{"unicode safe", "日本語テスト", 5, "日本..."},
		{"unicode exact", "日本", 2, "日本"},
		{"unicode truncate small max", "日本語", 2, "日本"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateString(tt.input, tt.max)
			if got != tt.want {
				t.Errorf("truncateString(%q, %d) = %q, want %q", tt.input, tt.max, got, tt.want)
			}
		})
	}
}

// --- SummarizeSubagentForContext Tests ---

func TestSummarizeSubagentForContext_Success(t *testing.T) {
	input := &SubagentSummaryInput{
		SkillName: "zabbix-analyst",
		Success:   true,
		Output:    "Found high CPU on server-01, restarted the service.",
		FullLog:   "This is the full reasoning log...",
	}

	result := SummarizeSubagentForContext(input)

	// Should contain skill name
	if !strings.Contains(result, "zabbix-analyst") {
		t.Error("summary should contain skill name")
	}
	// Should indicate success
	if !strings.Contains(result, "SUCCESS") {
		t.Error("summary should indicate SUCCESS")
	}
	// Should include output
	if !strings.Contains(result, "Found high CPU") {
		t.Error("summary should include output")
	}
	// Should NOT include full log (context efficiency)
	if strings.Contains(result, "full reasoning log") {
		t.Error("summary should NOT include full reasoning log")
	}
}

func TestSummarizeSubagentForContext_Failure(t *testing.T) {
	input := &SubagentSummaryInput{
		SkillName:     "ssh-admin",
		Success:       false,
		Output:        "",
		FullLog:       "Full log with many details...",
		ErrorMessages: []string{"Connection refused to server-01"},
	}

	result := SummarizeSubagentForContext(input)

	// Should contain skill name
	if !strings.Contains(result, "ssh-admin") {
		t.Error("summary should contain skill name")
	}
	// Should indicate failure
	if !strings.Contains(result, "FAILED") {
		t.Error("summary should indicate FAILED")
	}
	// Should include truncated error
	if !strings.Contains(result, "Connection refused") {
		t.Error("summary should include error message")
	}
	// Should mention that full log is stored but not shown
	if !strings.Contains(result, "stored but not shown") {
		t.Error("summary should explain full log is stored separately")
	}
}

func TestSummarizeSubagentForContext_LongErrorTruncated(t *testing.T) {
	longError := strings.Repeat("x", 300)
	input := &SubagentSummaryInput{
		SkillName:     "test-skill",
		Success:       false,
		ErrorMessages: []string{longError},
	}

	result := SummarizeSubagentForContext(input)

	// Error should be truncated
	if strings.Contains(result, strings.Repeat("x", 300)) {
		t.Error("long error should be truncated")
	}
	// Should contain truncation marker
	if !strings.Contains(result, "...") {
		t.Error("truncated error should end with ...")
	}
}

func TestSummarizeSubagentForContext_NoErrors(t *testing.T) {
	input := &SubagentSummaryInput{
		SkillName:     "test-skill",
		Success:       false,
		ErrorMessages: []string{},
	}

	result := SummarizeSubagentForContext(input)

	// Should have fallback error message
	if !strings.Contains(result, "Unknown error") {
		t.Error("should have 'Unknown error' fallback when no error messages")
	}
}
