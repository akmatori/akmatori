package utils

import (
	"strings"
	"testing"
)

func TestSanitizeTask(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		expectModified bool
		expectWarnings int
	}{
		{
			name:           "clean input",
			input:          "Check the server status",
			expectModified: false,
			expectWarnings: 0,
		},
		{
			name:           "empty input",
			input:          "",
			expectModified: false,
			expectWarnings: 0,
		},
		{
			name:           "shell command substitution",
			input:          "Run $(whoami) command",
			expectModified: false,
			expectWarnings: 1, // Detected but not removed
		},
		{
			name:           "backtick command",
			input:          "Execute `ls -la` here",
			expectModified: false,
			expectWarnings: 1,
		},
		{
			name:           "pipe injection",
			input:          "Search for files | rm -rf /",
			expectModified: false,
			expectWarnings: 2, // Pipe + dangerous command
		},
		{
			name:           "semicolon injection",
			input:          "List files; shutdown -h now",
			expectModified: false,
			expectWarnings: 2,
		},
		{
			name:           "path traversal",
			input:          "Read ../../../etc/passwd",
			expectModified: false,
			expectWarnings: 2,
		},
		{
			name:           "control characters",
			input:          "Hello\x00World\x07Test",
			expectModified: true,
			expectWarnings: 1,
		},
		{
			name:           "dangerous rm command",
			input:          "Please rm -rf / the directory",
			expectModified: false,
			expectWarnings: 1,
		},
		{
			name:           "fork bomb",
			input:          "Run :(){:|:&};: to test",
			expectModified: false,
			expectWarnings: 1,
		},
		{
			name:           "environment export",
			input:          "export PATH=/evil/path",
			expectModified: false,
			expectWarnings: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SanitizeTask(tt.input)

			if result.Modified != tt.expectModified {
				t.Errorf("Modified = %v; want %v", result.Modified, tt.expectModified)
			}

			if len(result.Warnings) != tt.expectWarnings {
				t.Errorf("Warnings count = %d; want %d. Warnings: %v",
					len(result.Warnings), tt.expectWarnings, result.Warnings)
			}
		})
	}
}

func TestSanitizeTask_LongInput(t *testing.T) {
	// Create input longer than 50KB
	longInput := strings.Repeat("a", 60000)
	result := SanitizeTask(longInput)

	if !result.Modified {
		t.Error("Long input should be modified (truncated)")
	}

	if len(result.Text) != 50000 {
		t.Errorf("Text length = %d; want 50000", len(result.Text))
	}

	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "truncated") {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected truncation warning")
	}
}

func TestValidateSkillName(t *testing.T) {
	tests := []struct {
		name      string
		skillName string
		wantError bool
	}{
		{"valid simple", "my_skill", false},
		{"valid with numbers", "skill123", false},
		{"valid underscore", "my_cool_skill", false},
		{"empty", "", true},
		{"starts with number", "123skill", true},
		{"starts with underscore", "_skill", true},
		{"has uppercase", "MySkill", true},
		{"has dash", "my-skill", true},
		{"has space", "my skill", true},
		{"too long", strings.Repeat("a", 101), true},
		{"reserved admin", "admin", true},
		{"reserved root", "root", true},
		{"reserved system", "system", true},
		{"single letter", "a", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSkillName(tt.skillName)
			if (err != nil) != tt.wantError {
				t.Errorf("ValidateSkillName(%q) error = %v; wantError = %v",
					tt.skillName, err, tt.wantError)
			}
		})
	}
}

func TestValidateIncidentUUID(t *testing.T) {
	tests := []struct {
		name      string
		uuid      string
		wantError bool
	}{
		{"valid uuid", "550e8400-e29b-41d4-a716-446655440000", false},
		{"valid uuid uppercase", "550E8400-E29B-41D4-A716-446655440000", false},
		{"empty", "", true},
		{"too short", "550e8400-e29b-41d4-a716", true},
		{"too long", "550e8400-e29b-41d4-a716-4466554400000", true},
		{"missing dashes", "550e8400e29b41d4a716446655440000", true},
		{"wrong format", "not-a-valid-uuid-at-all", true},
		{"invalid chars", "550g8400-e29b-41d4-a716-446655440000", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateIncidentUUID(tt.uuid)
			if (err != nil) != tt.wantError {
				t.Errorf("ValidateIncidentUUID(%q) error = %v; wantError = %v",
					tt.uuid, err, tt.wantError)
			}
		})
	}
}

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		name      string
		filename  string
		expected  string
		wantError bool
	}{
		{"normal filename", "test.txt", "test.txt", false},
		{"with path separator", "/etc/passwd", "_etc_passwd", false},
		{"with windows path", "C:\\Windows\\file.txt", "C:_Windows_file.txt", false},
		{"path traversal", "../../../etc/passwd", "______etc_passwd", false},
		{"empty", "", "", true},
		{"control chars", "test\x00file.txt", "testfile.txt", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := SanitizeFilename(tt.filename)
			if (err != nil) != tt.wantError {
				t.Errorf("SanitizeFilename(%q) error = %v; wantError = %v",
					tt.filename, err, tt.wantError)
			}
			if err == nil && result != tt.expected {
				t.Errorf("SanitizeFilename(%q) = %q; want %q",
					tt.filename, result, tt.expected)
			}
		})
	}
}

func TestSanitizeFilename_LongName(t *testing.T) {
	longName := strings.Repeat("a", 300) + ".txt"
	result, err := SanitizeFilename(longName)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	if len(result) > 255 {
		t.Errorf("Result too long: %d", len(result))
	}

	if !strings.HasSuffix(result, ".txt") {
		t.Error("Extension should be preserved")
	}
}

func TestContainsDangerousContent(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		expected bool
	}{
		{"safe text", "check server status", false},
		{"rm -rf root", "rm -rf /", true},
		{"fork bomb", ":(){:|:&};:", true},
		{"shutdown", "shutdown -h now", true},
		{"case insensitive", "RM -RF /", true},
		{"partial match", "some rm -rf / here", true},
		{"similar but safe", "remove files from directory", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ContainsDangerousContent(tt.text)
			if result != tt.expected {
				t.Errorf("ContainsDangerousContent(%q) = %v; want %v",
					tt.text, result, tt.expected)
			}
		})
	}
}

func TestEscapeForLogging(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		maxLen   int
		expected string
	}{
		{"simple", "hello", 10, "hello"},
		{"with newline", "hello\nworld", 20, "hello\\nworld"},
		{"with tabs", "hello\tworld", 20, "hello\\tworld"},
		{"truncated", "hello world this is long", 10, "hello worl..."},
		{"all escapes", "a\nb\rc\td", 20, "a\\nb\\rc\\td"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := EscapeForLogging(tt.text, tt.maxLen)
			if result != tt.expected {
				t.Errorf("EscapeForLogging(%q, %d) = %q; want %q",
					tt.text, tt.maxLen, result, tt.expected)
			}
		})
	}
}
