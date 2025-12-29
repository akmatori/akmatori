package utils

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// SanitizationResult contains the sanitized text and any warnings
type SanitizationResult struct {
	Text     string
	Warnings []string
	Modified bool
}

// Patterns for detecting potentially dangerous content
var (
	// Shell injection patterns
	shellInjectionPatterns = []*regexp.Regexp{
		regexp.MustCompile(`\$\([^)]+\)`),            // $(command)
		regexp.MustCompile("`[^`]+`"),                // `command`
		regexp.MustCompile(`;\s*\w+`),                // ; command
		regexp.MustCompile(`\|\s*\w+`),               // | command (pipe)
		regexp.MustCompile(`&&\s*\w+`),               // && command
		regexp.MustCompile(`\|\|\s*\w+`),             // || command
		regexp.MustCompile(`>\s*/`),                  // > /path (redirect to root)
		regexp.MustCompile(`>>\s*/`),                 // >> /path (append to root)
		regexp.MustCompile(`<\s*/`),                  // < /path (read from root)
	}

	// Suspicious path patterns
	suspiciousPathPatterns = []*regexp.Regexp{
		regexp.MustCompile(`/etc/passwd`),
		regexp.MustCompile(`/etc/shadow`),
		regexp.MustCompile(`/etc/sudoers`),
		regexp.MustCompile(`~/.ssh/`),
		regexp.MustCompile(`\.\.(/|\\)`), // Path traversal
	}

	// Environment variable manipulation
	envManipulationPatterns = []*regexp.Regexp{
		regexp.MustCompile(`\bexport\s+\w+=`),
		regexp.MustCompile(`\benv\s+\w+=`),
		regexp.MustCompile(`\bunset\s+\w+`),
	}

	// Dangerous commands that should never be in a task
	dangerousCommands = []string{
		"rm -rf /",
		"mkfs",
		"dd if=",
		":(){:|:&};:", // Fork bomb
		"chmod -R 777 /",
		"chown -R",
		"> /dev/sda",
		"shutdown",
		"reboot",
		"halt",
		"init 0",
		"init 6",
	}

	// Control characters (except common whitespace)
	controlCharPattern = regexp.MustCompile(`[\x00-\x08\x0B\x0C\x0E-\x1F\x7F]`)
)

// SanitizeTask sanitizes user input for safe execution
// It removes or escapes potentially dangerous content while preserving the intent
func SanitizeTask(task string) *SanitizationResult {
	result := &SanitizationResult{
		Text:     task,
		Warnings: []string{},
		Modified: false,
	}

	if task == "" {
		return result
	}

	// Remove control characters (keep tabs, newlines, carriage returns)
	if controlCharPattern.MatchString(result.Text) {
		result.Text = controlCharPattern.ReplaceAllString(result.Text, "")
		result.Warnings = append(result.Warnings, "Removed control characters")
		result.Modified = true
	}

	// Check for shell injection patterns
	for _, pattern := range shellInjectionPatterns {
		if pattern.MatchString(result.Text) {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("Detected potential shell injection pattern: %s", pattern.String()))
		}
	}

	// Check for suspicious paths
	for _, pattern := range suspiciousPathPatterns {
		if pattern.MatchString(result.Text) {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("Detected reference to sensitive path: %s", pattern.String()))
		}
	}

	// Check for environment manipulation
	for _, pattern := range envManipulationPatterns {
		if pattern.MatchString(result.Text) {
			result.Warnings = append(result.Warnings,
				"Detected environment variable manipulation attempt")
		}
	}

	// Check for dangerous commands
	lowerTask := strings.ToLower(result.Text)
	for _, cmd := range dangerousCommands {
		if strings.Contains(lowerTask, strings.ToLower(cmd)) {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("Detected dangerous command: %s", cmd))
		}
	}

	// Limit task length to prevent resource exhaustion
	const maxTaskLength = 50000 // 50KB
	if len(result.Text) > maxTaskLength {
		result.Text = result.Text[:maxTaskLength]
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("Task truncated to %d characters", maxTaskLength))
		result.Modified = true
	}

	return result
}

// ValidateSkillName validates that a skill name is safe
func ValidateSkillName(name string) error {
	if name == "" {
		return fmt.Errorf("skill name is required")
	}

	if len(name) > 100 {
		return fmt.Errorf("skill name too long (max 100 characters)")
	}

	// Must be snake_case: lowercase letters, numbers, underscores
	// Must start with a letter
	validPattern := regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	if !validPattern.MatchString(name) {
		return fmt.Errorf("skill name must be snake_case (lowercase letters, numbers, and underscores only, starting with a letter)")
	}

	// Check for reserved names
	reserved := []string{
		"admin", "root", "system", "incident_manager",
		"test", "debug", "config", "settings",
	}
	for _, r := range reserved {
		if name == r {
			return fmt.Errorf("skill name '%s' is reserved", name)
		}
	}

	return nil
}

// ValidateIncidentUUID validates that a UUID is properly formatted
func ValidateIncidentUUID(uuid string) error {
	if uuid == "" {
		return fmt.Errorf("incident UUID is required")
	}

	// Standard UUID format: 8-4-4-4-12 hex characters
	uuidPattern := regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
	if !uuidPattern.MatchString(strings.ToLower(uuid)) {
		return fmt.Errorf("invalid UUID format")
	}

	return nil
}

// SanitizeFilename sanitizes a filename for safe storage
func SanitizeFilename(filename string) (string, error) {
	if filename == "" {
		return "", fmt.Errorf("filename is required")
	}

	// Remove path components
	filename = strings.ReplaceAll(filename, "/", "_")
	filename = strings.ReplaceAll(filename, "\\", "_")
	filename = strings.ReplaceAll(filename, "..", "_")

	// Remove control characters
	var sanitized strings.Builder
	for _, r := range filename {
		if unicode.IsPrint(r) && r != '\t' {
			sanitized.WriteRune(r)
		}
	}
	filename = sanitized.String()

	// Limit length
	if len(filename) > 255 {
		// Keep extension
		ext := ""
		if idx := strings.LastIndex(filename, "."); idx > 0 {
			ext = filename[idx:]
			filename = filename[:idx]
		}
		maxBase := 255 - len(ext)
		if maxBase > 0 {
			filename = filename[:maxBase] + ext
		} else {
			filename = filename[:255]
		}
	}

	if filename == "" {
		return "", fmt.Errorf("filename is empty after sanitization")
	}

	return filename, nil
}

// ContainsDangerousContent checks if text contains obviously dangerous content
// Returns true if dangerous content is detected
func ContainsDangerousContent(text string) bool {
	lowerText := strings.ToLower(text)

	for _, cmd := range dangerousCommands {
		if strings.Contains(lowerText, strings.ToLower(cmd)) {
			return true
		}
	}

	return false
}

// EscapeForLogging escapes sensitive content for safe logging
func EscapeForLogging(text string, maxLen int) string {
	// Truncate
	if len(text) > maxLen {
		text = text[:maxLen] + "..."
	}

	// Remove newlines for single-line logging
	text = strings.ReplaceAll(text, "\n", "\\n")
	text = strings.ReplaceAll(text, "\r", "\\r")
	text = strings.ReplaceAll(text, "\t", "\\t")

	return text
}
