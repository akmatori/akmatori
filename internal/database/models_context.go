package database

import "time"

// ContextFile stores metadata for uploaded context files
// Files are stored in filesystem, only metadata in database
//
// ContentHash is the sha256 of the file; the summary job checks it again
// before it writes. SummaryStatus is "" (never run) or one of the
// ContextSummary* states, and SummaryError says why a job failed or skipped.
// SummaryMemoryID is not stored: API responses fill it from
// memories.source_file_id.
type ContextFile struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	Filename        string    `gorm:"type:varchar(255);uniqueIndex;not null" json:"filename"`
	OriginalName    string    `gorm:"type:varchar(255)" json:"original_name"`
	MimeType        string    `gorm:"type:varchar(100)" json:"mime_type"`
	Size            int64     `json:"size"`
	Description     string    `gorm:"type:text" json:"description"`
	ContentHash     string    `gorm:"type:varchar(64)" json:"content_hash,omitempty"`
	SummaryStatus   string    `gorm:"type:varchar(16)" json:"summary_status,omitempty"`
	SummaryError    string    `gorm:"type:text" json:"summary_error,omitempty"`
	SummaryMemoryID *uint     `gorm:"-" json:"summary_memory_id,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// Context file summary states.
const (
	ContextSummaryPending = "pending"
	ContextSummaryDone    = "done"
	ContextSummaryFailed  = "failed"
	ContextSummarySkipped = "skipped"
)

func (ContextFile) TableName() string {
	return "context_files"
}

// Runbook stores operator runbooks (SOPs) that the AI agent can reference during investigations
type Runbook struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Title     string    `gorm:"type:varchar(255);not null" json:"title"`
	Content   string    `gorm:"type:text;not null" json:"content"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (Runbook) TableName() string {
	return "runbooks"
}

// Memory stores cross-incident knowledge the AI agent and operators accumulate
// over time. Scoped per skill or "global". Agents read and write it through
// the gateway memory.* tools; a read-only copy is mirrored to disk.
//
// SourceFileID links a memory to the context file it summarizes. It is
// server managed: ingest and the update API never change it, and deleting
// the file deletes its memories.
type Memory struct {
	ID           uint   `gorm:"primaryKey" json:"id"`
	Scope        string `gorm:"type:varchar(255);not null;uniqueIndex:idx_memories_scope_name,priority:1" json:"scope"`
	Type         string `gorm:"type:varchar(32);not null;index" json:"type"`
	Name         string `gorm:"type:varchar(255);not null;uniqueIndex:idx_memories_scope_name,priority:2" json:"name"`
	Description  string `gorm:"type:varchar(500);not null" json:"description"`
	Body         string `gorm:"type:text;not null" json:"body"`
	IncidentUUID string `gorm:"type:varchar(64);index" json:"incident_uuid,omitempty"`
	CreatedBy    string `gorm:"type:varchar(32)" json:"created_by,omitempty"`
	SourceFileID *uint  `gorm:"index" json:"source_file_id,omitempty"`
	// Evidence grades how far a reader can trust the memory: confirmed (an
	// operator checked it), observed (seen directly, not a cause) or
	// unverified (an unconfirmed cause, or a legacy row). Claim is causal |
	// factual, empty = not assessed. EvidenceNote says why. See
	// services/memory_evidence.go.
	Evidence     string `gorm:"type:varchar(16);not null;default:'unverified';index" json:"evidence"`
	Claim        string `gorm:"type:varchar(16);not null;default:''" json:"claim,omitempty"`
	EvidenceNote string `gorm:"type:varchar(500);not null;default:''" json:"evidence_note,omitempty"`
	// Usage, denormalised from memory_uses: UseCount counts reads in full
	// (memory.get) and citations in final answers; LastUsedAt also moves on
	// a search hit. The manifest ranks by the later of LastUsedAt and
	// UpdatedAt, so what runs use stays in the cap.
	UseCount   int        `gorm:"not null;default:0" json:"use_count"`
	LastUsedAt *time.Time `gorm:"index" json:"last_used_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

func (Memory) TableName() string {
	return "memories"
}

// Kinds of a memory use (MemoryUse.Kind).
const (
	// MemoryUseSearch: returned as a memory.search hit (moves last_used_at
	// only, so a broad search does not inflate use_count).
	MemoryUseSearch = "search"
	// MemoryUseGet: read in full with memory.get.
	MemoryUseGet = "get"
	// MemoryUseCited: named in a final answer's "Memory used:" block (the
	// only signal for a memory read from the manifest without memory.get).
	MemoryUseCited = "cited"
)

// MemoryUse records one use of a memory by a run: the gateway inserts
// search and get rows, FinishRun inserts cited rows. Rows follow the memory
// (ON DELETE CASCADE). The gateway mirrors the model.
type MemoryUse struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	MemoryID     uint      `gorm:"not null;index" json:"memory_id"`
	Memory       Memory    `gorm:"foreignKey:MemoryID;constraint:OnDelete:CASCADE" json:"-"`
	RunID        string    `gorm:"size:36;index" json:"run_id,omitempty"`
	IncidentUUID string    `gorm:"size:36;index" json:"incident_uuid,omitempty"`
	Kind         string    `gorm:"size:8;not null" json:"kind"`
	At           time.Time `gorm:"index" json:"at"`
}

func (MemoryUse) TableName() string {
	return "memory_uses"
}
