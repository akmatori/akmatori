package services

import (
	"errors"
	"fmt"
	"strings"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/gorm"
)

// FileSummaryMemoryName is the memory name for the summary of a context
// file: "file-" plus the filename as a slug ("runbook.pdf" → "file-runbook-pdf").
func FileSummaryMemoryName(filename string) string {
	return SlugifyMemoryName("file-" + filename)
}

// SaveFileSummary writes the summary memory of context file fileID. The
// memory that already links to the file is updated in place (its name stays);
// otherwise a new global fact is created under FileSummaryMemoryName, with
// -2, -3, … when an unrelated memory holds that name.
func (s *MemoryService) SaveFileSummary(fileID uint, filename, description, body string) (*database.Memory, error) {
	var existing database.Memory
	err := s.db.Where("source_file_id = ?", fileID).Order("id ASC").First(&existing).Error
	if err == nil {
		merged := existing
		merged.Type = MemoryTypeFact
		merged.Description = description
		merged.Body = body
		if err := s.validate(&merged); err != nil {
			return nil, err
		}
		if err := s.db.Model(&existing).Updates(map[string]any{
			"type":        merged.Type,
			"description": merged.Description,
			"body":        merged.Body,
		}).Error; err != nil {
			return nil, fmt.Errorf("failed to update file summary memory: %w", err)
		}
		if err := s.SyncMemoryFiles(); err != nil {
			return nil, fmt.Errorf("file summary updated but file sync failed: %w", err)
		}
		return s.GetMemory(existing.ID)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("failed to look up file summary memory: %w", err)
	}

	id := fileID
	return s.CreateWithUniqueName(&database.Memory{
		Scope:        MemoryScopeGlobal,
		Type:         MemoryTypeFact,
		Name:         FileSummaryMemoryName(filename),
		Description:  description,
		Body:         body,
		CreatedBy:    MemoryCreatedByAgent,
		SourceFileID: &id,
	})
}

// DeleteBySourceFile deletes every memory that links to context file fileID.
// It syncs files only when a row went away.
func (s *MemoryService) DeleteBySourceFile(fileID uint) (int64, error) {
	res := s.db.Where("source_file_id = ?", fileID).Delete(&database.Memory{})
	if res.Error != nil {
		return 0, fmt.Errorf("failed to delete file memories: %w", res.Error)
	}
	if res.RowsAffected > 0 {
		if err := s.SyncMemoryFiles(); err != nil {
			return res.RowsAffected, fmt.Errorf("file memories deleted but file sync failed: %w", err)
		}
	}
	return res.RowsAffected, nil
}

// SummaryMemoryIDs maps each context file ID to the ID of its oldest linked
// memory, for the file list.
func (s *MemoryService) SummaryMemoryIDs(fileIDs []uint) (map[uint]uint, error) {
	out := make(map[uint]uint, len(fileIDs))
	if len(fileIDs) == 0 {
		return out, nil
	}
	var rows []database.Memory
	if err := s.db.Select("id", "source_file_id").
		Where("source_file_id IN ?", fileIDs).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to list file memories: %w", err)
	}
	for _, m := range rows {
		if m.SourceFileID == nil {
			continue
		}
		if _, ok := out[*m.SourceFileID]; !ok {
			out[*m.SourceFileID] = m.ID
		}
	}
	return out, nil
}

// fileSummaryBody makes sure the body links the file it summarizes. The link
// goes first when it is missing, so the body cap cannot cut it off.
func fileSummaryBody(body, filename string) string {
	body = strings.TrimSpace(body)
	link := "[[" + filename + "]]"
	if !strings.Contains(body, link) {
		body = strings.TrimSpace("Source: " + link + "\n\n" + body)
	}
	return TruncateMemoryBody(body)
}
