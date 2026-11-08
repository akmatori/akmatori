package knowledge

import (
	"fmt"
	"regexp"
	"strings"
)

// Memory field rules. The shared group is the same as the main API's
// MemoryService.validate (internal/services/memory_service.go); both modules
// run tests/fixtures/memory_validation_cases.json so a drift fails both.
const (
	memoryNameMaxLen        = 200
	memoryDescriptionMaxLen = 500 // bytes
	memoryBodyMaxBytes      = 8 * 1024
	memoryScopeGlobal       = "global"
	typeFact                = "fact"
	typeFeedback            = "feedback"
	createdByAgent          = "agent"
	createdByOperator       = "operator"
	// reservedNamePrefix marks context-file summary memories.
	reservedNamePrefix = "file-"
)

var memorySlug = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// legacyFactTypes fold to fact, as in the API's NormalizeMemoryType.
var legacyFactTypes = map[string]bool{"host": true, "incident_pattern": true, "tool_quirk": true}

func validSlug(s string) bool {
	return s != "" && len(s) <= memoryNameMaxLen && memorySlug.MatchString(s)
}

// memoryInput is one memory.write call.
type memoryInput struct {
	Scope       string
	Name        string
	Type        string
	Description string
	Body        string
	// Evidence fields (optional in the call); applyAgentEvidence fills them.
	Evidence     string
	Claim        string
	EvidenceNote string
}

// normalize trims the fields and folds legacy types to fact.
func (in *memoryInput) normalize() {
	in.Scope = strings.TrimSpace(in.Scope)
	in.Name = strings.TrimSpace(in.Name)
	in.Type = strings.TrimSpace(in.Type)
	if legacyFactTypes[in.Type] {
		in.Type = typeFact
	}
}

// validateShared applies the rules shared with the API.
func validateShared(in *memoryInput) error {
	if in.Scope == "" {
		return fmt.Errorf("scope cannot be empty")
	}
	if !validSlug(in.Scope) {
		return fmt.Errorf("scope must be slug-safe (lowercase a-z, 0-9, hyphen) and ≤%d chars", memoryNameMaxLen)
	}
	if in.Type != typeFact && in.Type != typeFeedback {
		return fmt.Errorf("invalid memory type %q (allowed: fact, feedback)", in.Type)
	}
	if in.Name == "" {
		return fmt.Errorf("name cannot be empty")
	}
	if !validSlug(in.Name) {
		return fmt.Errorf("name must be slug-safe (lowercase a-z, 0-9, hyphen) and ≤%d chars", memoryNameMaxLen)
	}
	if len(in.Description) > memoryDescriptionMaxLen {
		return fmt.Errorf("description exceeds %d chars", memoryDescriptionMaxLen)
	}
	if strings.TrimSpace(in.Description) == "" {
		return fmt.Errorf("description cannot be empty")
	}
	if len(in.Body) > memoryBodyMaxBytes {
		return fmt.Errorf("body exceeds %d bytes", memoryBodyMaxBytes)
	}
	return nil
}

// applyAgentPolicy applies the agent-only rules that need no database: a
// long description is cut at a UTF-8 boundary (truncated=true), and names
// with the context-summary prefix are refused.
func applyAgentPolicy(in *memoryInput) (truncated bool, err error) {
	if strings.HasPrefix(in.Name, reservedNamePrefix) {
		return false, fmt.Errorf("name must not start with %q (reserved for context file summaries)", reservedNamePrefix)
	}
	in.Description, truncated = cutUTF8(in.Description, memoryDescriptionMaxLen)
	return truncated, nil
}

// cutUTF8 cuts s to max bytes without splitting a UTF-8 rune and trims the
// result (truncated = true when it cut).
func cutUTF8(s string, max int) (string, bool) {
	if len(s) <= max {
		return s, false
	}
	cut := max
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return strings.TrimSpace(s[:cut]), true
}
