package services

// Memory type constants — single source of truth used by validation,
// prompt rendering, and the extractor's JSON schema. A fact is knowledge about
// the environment (hosts, services, tools, incident patterns, file
// summaries); feedback is an operator instruction about how the agent must
// work, which the manifest lists first.
const (
	MemoryTypeFact     = "fact"
	MemoryTypeFeedback = "feedback"
)

// legacyFactTypes are the types used before fact/feedback. They are still
// accepted from older clients, prompts and pending proposals, and
// NormalizeMemoryType maps them to fact; the startup migration rewrites
// stored rows.
var legacyFactTypes = map[string]bool{
	"host":             true,
	"incident_pattern": true,
	"tool_quirk":       true,
}

// MemoryScopeGlobal is the reserved scope name for memories visible to every
// incident (injected into the incident-manager AGENTS.md). Any other scope
// value is treated as a skill name.
const MemoryScopeGlobal = "global"

// MemoryCreatedByAgent / MemoryCreatedByOperator are the only allowed values
// for Memory.CreatedBy. Empty is also valid (legacy/manual rows).
const (
	MemoryCreatedByAgent    = "agent"
	MemoryCreatedByOperator = "operator"
)

// Body / description / name caps. These are enforced by the service layer.
//
// MemoryNameMaxLen is bounded BELOW the typical filesystem NAME_MAX of 255
// bytes. The on-disk memory file is `<name>.md`, and SyncMemoryFiles first
// writes it as `<name>.md.tmp.<pid>.<nanos>.<counter>`. A 255-char name
// would push that temp name past NAME_MAX and make the write fail with
// ENAMETOOLONG — leaving the DB row in place while every subsequent
// SyncMemoryFiles call kept failing on the same path. 200 reserves >50
// bytes of headroom for the temp suffix.
const (
	MemoryNameMaxLen        = 200
	MemoryDescriptionMaxLen = 500
	MemoryBodyMaxBytes      = 8 * 1024
)

// AllMemoryTypes returns all valid memory type values in canonical order.
// Used for prompt rendering and validation.
func AllMemoryTypes() []string {
	return []string{MemoryTypeFact, MemoryTypeFeedback}
}

// ValidMemoryType reports whether s is fact or feedback.
func ValidMemoryType(s string) bool {
	return s == MemoryTypeFact || s == MemoryTypeFeedback
}

// NormalizeMemoryType maps a legacy type to fact and returns any other value
// unchanged, so validation still rejects real typos.
func NormalizeMemoryType(s string) string {
	if legacyFactTypes[s] {
		return MemoryTypeFact
	}
	return s
}
