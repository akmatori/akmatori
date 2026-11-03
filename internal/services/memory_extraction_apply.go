package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/gorm"
)

// ErrExtractionStale: the incident is no longer the job's claim (reopened,
// re-closed, re-claimed after the lease, or gone). Nothing was written.
var ErrExtractionStale = errors.New("memory extraction: the claim is stale; nothing written")

// ExtractionResult is what one post-incident pass stores: graded memories,
// the timeline for the incident page and a note on the job (redactions).
type ExtractionResult struct {
	Memories []database.Memory
	Timeline string
	Note     string
}

// memoryWritableWhere is the protection predicate of agent writes, the same
// one the gateway's memory.write puts in its UPDATE (writableWhere).
const memoryWritableWhere = "scope = ? AND name = ? AND (created_by IS NULL OR created_by <> 'operator') AND source_file_id IS NULL"

// ApplyExtraction stores the output of the post-incident pass for
// incidentUUID in one transaction: it re-checks the claim (status closed,
// extraction running, claim time equal to claimAt), writes the outcome on
// the incident row, then writes each memory. A memory whose scope + name
// this incident wrote before (same incident_uuid, agent-owned) is updated in
// place, with the protection predicate in the SQL; a name that belongs to
// anyone else gets a unique suffix (-2, -3 …). A failure rolls everything
// back, so a rerun never leaves half of the output behind. The outcome is
// succeeded, no_output (no memories) or skipped (the operator turned agent
// memory off since the claim). ErrExtractionStale means the claim did not
// hold; the row is left alone for the next claim.
func (s *MemoryService) ApplyExtraction(ctx context.Context, incidentUUID string, claimAt time.Time, res ExtractionResult) (written int, err error) {
	status := database.MemoryExtractionSucceeded
	memories := res.Memories
	note := res.Note
	switch {
	case !MemoryGenerationEnabled():
		status, memories = database.MemoryExtractionSkipped, nil
		note = "memory generation is off"
	case len(memories) == 0:
		status = database.MemoryExtractionNoOutput
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		upd := tx.Model(&database.Incident{}).
			Where("uuid = ? AND status = ? AND memory_extraction = ? AND memory_extraction_at = ?",
				incidentUUID, database.IncidentStatusClosed, database.MemoryExtractionRunning, claimAt).
			Updates(map[string]interface{}{
				"memory_extraction":       status,
				"memory_extraction_error": "",
				"memory_extraction_note":  note,
				"memory_timeline":         res.Timeline,
			})
		if upd.Error != nil {
			return fmt.Errorf("store extraction outcome: %w", upd.Error)
		}
		if upd.RowsAffected == 0 {
			return ErrExtractionStale
		}
		for i := range memories {
			m := memories[i]
			if err := s.validate(&m); err != nil {
				slog.Warn("memory extraction: memory skipped", "incident", incidentUUID, "name", m.Name, "err", err)
				continue
			}
			m.Scope, m.Name = strings.TrimSpace(m.Scope), strings.TrimSpace(m.Name)
			m.IncidentUUID, m.CreatedBy = incidentUUID, MemoryCreatedByAgent
			if err := applyExtractedMemory(tx, &m); err != nil {
				return err
			}
			written++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return written, nil
}

// applyExtractedMemory writes one memory: update in place when this
// incident wrote the name before (also under a -2, -3 … suffix of an
// earlier pass, so a rerun stays idempotent), else create, under a unique
// name when the name belongs to someone else.
func applyExtractedMemory(tx *gorm.DB, m *database.Memory) error {
	var own []database.Memory
	if err := tx.Where("scope = ? AND incident_uuid = ? AND (created_by IS NULL OR created_by <> 'operator') AND source_file_id IS NULL AND (name = ? OR name LIKE ?)",
		m.Scope, m.IncidentUUID, m.Name, m.Name+"-%").Order("name").Find(&own).Error; err != nil {
		return fmt.Errorf("read memory %s/%s: %w", m.Scope, m.Name, err)
	}
	for _, prior := range own {
		if prior.Name != m.Name && !isNumericSuffix(strings.TrimPrefix(prior.Name, m.Name+"-")) {
			continue
		}
		res := tx.Model(&database.Memory{}).Where(memoryWritableWhere, m.Scope, prior.Name).
			Updates(map[string]interface{}{
				"type": m.Type, "description": m.Description, "body": m.Body,
				"evidence": m.Evidence, "claim": m.Claim, "evidence_note": m.EvidenceNote,
				"incident_uuid": m.IncidentUUID, "updated_at": time.Now(),
			})
		if res.Error != nil {
			return fmt.Errorf("update memory %s/%s: %w", m.Scope, prior.Name, res.Error)
		}
		if res.RowsAffected > 0 {
			m.ID, m.Name = prior.ID, prior.Name
			return nil
		}
	}
	var taken int64
	if err := tx.Model(&database.Memory{}).Where("scope = ? AND name = ?", m.Scope, m.Name).Count(&taken).Error; err != nil {
		return fmt.Errorf("read memory %s/%s: %w", m.Scope, m.Name, err)
	}
	if taken == 0 {
		if err := tx.Create(m).Error; err != nil {
			return fmt.Errorf("create memory %s/%s: %w", m.Scope, m.Name, err)
		}
		return nil
	}
	// The name belongs to someone else (an operator, a file summary, another
	// incident's finding): keep theirs, create under a free name.
	return createMemoryWithUniqueNameTx(tx, m)
}

func isNumericSuffix(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// createMemoryWithUniqueNameTx is CreateWithUniqueName inside a
// transaction, without validation or the file sync.
func createMemoryWithUniqueNameTx(tx *gorm.DB, m *database.Memory) error {
	base := m.Name
	for i := 2; i <= memoryUniqueNameTries; i++ {
		suffix := fmt.Sprintf("-%d", i)
		name := base
		if len(name)+len(suffix) > MemoryNameMaxLen {
			name = strings.TrimRight(name[:MemoryNameMaxLen-len(suffix)], "-")
		}
		name += suffix
		var count int64
		if err := tx.Model(&database.Memory{}).Where("scope = ? AND name = ?", m.Scope, name).Count(&count).Error; err != nil {
			return fmt.Errorf("check memory name: %w", err)
		}
		if count == 0 {
			m.Name = name
			if err := tx.Create(m).Error; err != nil {
				return fmt.Errorf("create memory %s/%s: %w", m.Scope, m.Name, err)
			}
			return nil
		}
	}
	return fmt.Errorf("no free memory name for %q after %d tries", base, memoryUniqueNameTries)
}
