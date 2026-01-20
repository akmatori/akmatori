package database

import "time"

// IncidentMerge tracks when incidents are merged together.
// This provides an audit trail for merge operations, whether automatic (by Codex) or manual (by users).
type IncidentMerge struct {
	ID               uint      `gorm:"primaryKey" json:"id"`
	SourceIncidentID uint      `gorm:"not null;index" json:"source_incident_id"` // The incident that was merged away (closed)
	TargetIncidentID uint      `gorm:"not null;index" json:"target_incident_id"` // The incident that absorbed the source
	MergeConfidence  float64   `gorm:"type:decimal(3,2)" json:"merge_confidence"` // Codex's confidence score for the merge decision
	MergeReason      string    `gorm:"type:text" json:"merge_reason"`             // Explanation of why the merge was performed
	MergedBy         string    `gorm:"type:varchar(50);not null" json:"merged_by"` // 'system' for automatic merges, or user ID for manual merges
	CreatedAt        time.Time `json:"created_at"`
}

func (IncidentMerge) TableName() string {
	return "incident_merges"
}
