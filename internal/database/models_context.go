package database

import "time"

// ContextFile stores metadata for uploaded context files
// Files are stored in filesystem, only metadata in database
type ContextFile struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	Filename     string    `gorm:"type:varchar(255);uniqueIndex;not null" json:"filename"`
	OriginalName string    `gorm:"type:varchar(255)" json:"original_name"`
	MimeType     string    `gorm:"type:varchar(100)" json:"mime_type"`
	Size         int64     `json:"size"`
	Description  string    `gorm:"type:text" json:"description"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

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
