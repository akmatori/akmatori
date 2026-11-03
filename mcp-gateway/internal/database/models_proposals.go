package database

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

// Mirror structs for tables owned by the main API. The gateway never runs
// migrations — the main API's AutoMigrate owns the DDL. When a table is
// missing (e.g. gateway upgraded ahead of the API), queries return errors
// that surface as tool errors, which is the intended graceful behavior.

// Proposal mirrors the main API's Proposal model (internal/database/models_proposals.go).
type Proposal struct {
	ID                  uint       `gorm:"primaryKey" json:"id"`
	UUID                string     `gorm:"uniqueIndex;size:36;not null" json:"uuid"`
	Kind                string     `gorm:"size:32;not null" json:"kind"`
	Status              string     `gorm:"size:16;not null;default:'pending'" json:"status"`
	Title               string     `gorm:"size:255;not null" json:"title"`
	Reasoning           string     `gorm:"type:text" json:"reasoning"`
	TargetRef           string     `gorm:"size:512" json:"target_ref"`
	CurrentSnapshot     string     `gorm:"type:text" json:"current_snapshot"`
	ProposedContent     string     `gorm:"type:text;not null" json:"proposed_content"`
	SourceIncidentUUIDs JSONB      `gorm:"type:jsonb" json:"source_incident_uuids"`
	EvaluationRunUUID   string     `gorm:"size:36" json:"evaluation_run_uuid"`
	ChatIncidentUUID    string     `gorm:"size:36" json:"chat_incident_uuid"`
	CreatedBy           string     `gorm:"size:32" json:"created_by"`
	ApplyError          string     `gorm:"type:text" json:"apply_error"`
	AppliedAt           *time.Time `json:"applied_at,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

func (Proposal) TableName() string {
	return "proposals"
}

// Runbook mirrors the main API's Runbook model.
type Runbook struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Title     string    `json:"title"`
	Content   string    `gorm:"type:text" json:"content"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (Runbook) TableName() string {
	return "runbooks"
}

// Memory mirrors the main API's Memory model. The knowledge tools
// (memory.*) read and write it.
type Memory struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	Scope        string    `gorm:"uniqueIndex:idx_memories_scope_name,priority:1" json:"scope"`
	Type         string    `json:"type"`
	Name         string    `gorm:"uniqueIndex:idx_memories_scope_name,priority:2" json:"name"`
	Description  string    `json:"description"`
	Body         string    `gorm:"type:text" json:"body"`
	IncidentUUID string    `json:"incident_uuid,omitempty"`
	CreatedBy    string    `json:"created_by,omitempty"`
	SourceFileID *uint     `json:"source_file_id,omitempty"`
	Evidence     string    `gorm:"default:unverified" json:"evidence"`
	Claim        string    `json:"claim,omitempty"`
	EvidenceNote string    `json:"evidence_note,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (Memory) TableName() string {
	return "memories"
}

// CronJob mirrors the main API's CronJob model (read-only in the gateway).
// ToolsEnabled + BuiltinTools are the cron's tool access (the skill and cron
// tool mappings are gone).
type CronJob struct {
	ID           uint       `gorm:"primaryKey" json:"id"`
	UUID         string     `json:"uuid"`
	Name         string     `json:"name"`
	Schedule     string     `json:"schedule"`
	Prompt       string     `gorm:"type:text" json:"prompt"`
	IsSystem     bool       `json:"is_system"`
	Enabled      bool       `json:"enabled"`
	ToolsEnabled bool       `gorm:"not null;default:true" json:"tools_enabled"`
	BuiltinTools StringList `gorm:"type:jsonb" json:"builtin_tools"`
}

func (CronJob) TableName() string {
	return "cron_jobs"
}

// StringList is a JSON array of strings stored in a jsonb column.
type StringList []string

// Scan implements sql.Scanner.
func (l *StringList) Scan(value interface{}) error {
	if value == nil {
		*l = nil
		return nil
	}
	var raw []byte
	switch v := value.(type) {
	case []byte:
		raw = v
	case string:
		raw = []byte(v)
	default:
		return fmt.Errorf("StringList: unsupported scan type %T", value)
	}
	if len(raw) == 0 || string(raw) == "null" {
		*l = nil
		return nil
	}
	return json.Unmarshal(raw, l)
}

// Value implements driver.Valuer.
func (l StringList) Value() (driver.Value, error) {
	if l == nil {
		return nil, nil
	}
	return json.Marshal([]string(l))
}

// GeneralSettings mirrors the one column of the main API's general_settings
// row that the memory tools read: the agent-memory gate (nil = on).
type GeneralSettings struct {
	ID                      uint  `gorm:"primaryKey"`
	MemoryGenerationEnabled *bool `gorm:"default:null"`
}

func (GeneralSettings) TableName() string {
	return "general_settings"
}

// ContextFile mirrors the main API's context_files table (memory.get
// resolves [[file.ext]] references against it).
type ContextFile struct {
	ID       uint   `gorm:"primaryKey"`
	Filename string `gorm:"uniqueIndex"`
}

func (ContextFile) TableName() string {
	return "context_files"
}
