package services

import (
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/gorm"
)

// Memory usage tracking (plan D8). The gateway records search hits and
// full reads with the run id of every call; the final answer's
// "Memory used:" block records the memories the agent relied on without a
// memory.get (read from the manifest). Both feed memory_uses and the
// denormalised use_count / last_used_at on the memory, which rank the
// manifest and give Dreaming the retirement signal.

// memoryUsedHeading opens the citation block in a final answer.
const memoryUsedHeading = "memory used:"

var memoryUsedRefPattern = regexp.MustCompile(`\[\[([^\[\]\n]{1,256})\]\]`)

// ParseMemoryUsed returns the [[scope/name]] and [[name]] references of the
// "Memory used:" block of a final answer: the refs on the heading line and
// on the lines that follow until a blank line or a line with no reference.
// "Memory used: none" yields nothing. Refs are deduplicated in order.
func ParseMemoryUsed(text string) []string {
	var refs []string
	seen := map[string]bool{}
	add := func(line string) bool {
		found := false
		for _, m := range memoryUsedRefPattern.FindAllStringSubmatch(line, -1) {
			raw := strings.TrimSpace(m[1])
			if raw == "" || seen[raw] {
				continue
			}
			seen[raw] = true
			refs = append(refs, raw)
			found = true
		}
		return found
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		clean := strings.ToLower(strings.TrimLeft(strings.TrimSpace(line), "#*-> \t"))
		if !strings.HasPrefix(clean, memoryUsedHeading) {
			continue
		}
		add(line)
		for j := i + 1; j < len(lines); j++ {
			if strings.TrimSpace(lines[j]) == "" || !add(lines[j]) {
				break
			}
		}
	}
	return refs
}

// RecordCitedMemories parses the "Memory used:" block of a final answer and
// records one cited use per named memory for the run. A name that does not
// exist is ignored; a bare [[name]] resolves to global, else to the first
// scope that has it. It returns the number of memories recorded. Best
// effort: a failure is logged, never returned to the run.
func (s *MemoryService) RecordCitedMemories(runID, incidentUUID, response string) int {
	refs := ParseMemoryUsed(response)
	if len(refs) == 0 || s.db == nil || !s.db.Migrator().HasTable(&database.MemoryUse{}) {
		return 0
	}
	ids := make([]uint, 0, len(refs))
	seen := map[uint]bool{}
	for _, raw := range refs {
		id, ok := s.resolveMemoryRef(raw)
		if !ok || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return 0
	}
	if err := RecordMemoryUses(s.db, ids, runID, incidentUUID, database.MemoryUseCited, time.Now()); err != nil {
		slog.Warn("memory usage: record cited memories", "run", runID, "err", err)
		return 0
	}
	return len(ids)
}

// resolveMemoryRef finds the memory a [[...]] reference names.
func (s *MemoryService) resolveMemoryRef(raw string) (uint, bool) {
	var m database.Memory
	if scope, name, ok := strings.Cut(raw, "/"); ok {
		if !validMemoryName(scope) || !validMemoryName(name) {
			return 0, false
		}
		if err := s.db.Select("id").Where("scope = ? AND name = ?", scope, name).Take(&m).Error; err != nil {
			return 0, false
		}
		return m.ID, true
	}
	if !validMemoryName(raw) {
		return 0, false
	}
	if err := s.db.Select("id").Where("name = ?", raw).
		Order(gorm.Expr("CASE WHEN scope = ? THEN 0 ELSE 1 END", MemoryScopeGlobal)).Order("scope ASC").
		Take(&m).Error; err != nil {
		return 0, false
	}
	return m.ID, true
}

// RecordMemoryUses inserts one use row per memory and moves the
// denormalised counters: get and cited raise use_count, every kind moves
// last_used_at. One transaction; the gateway applies the same rule.
func RecordMemoryUses(db *gorm.DB, ids []uint, runID, incidentUUID, kind string, at time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	rows := make([]database.MemoryUse, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, database.MemoryUse{MemoryID: id, RunID: runID, IncidentUUID: incidentUUID, Kind: kind, At: at})
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&rows).Error; err != nil {
			return fmt.Errorf("insert memory uses: %w", err)
		}
		values := map[string]interface{}{"last_used_at": at}
		if kind != database.MemoryUseSearch {
			values["use_count"] = gorm.Expr("use_count + 1")
		}
		if err := tx.Model(&database.Memory{}).Where("id IN ?", ids).UpdateColumns(values).Error; err != nil {
			return fmt.Errorf("update memory usage: %w", err)
		}
		return nil
	})
}

// manifestRank is the manifest order key: the later of the last use and the
// last update, so a new memory is visible at once and a used memory stays
// in the cap.
func manifestRank(m database.Memory) time.Time {
	if m.LastUsedAt != nil && m.LastUsedAt.After(m.UpdatedAt) {
		return *m.LastUsedAt
	}
	return m.UpdatedAt
}
