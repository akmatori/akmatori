package services

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// IncidentContext contains context for spawning an incident manager
type IncidentContext struct {
	Source   string         // e.g., "slack", "zabbix"
	SourceID string         // e.g., thread_ts, alert_id
	Context  database.JSONB // Event details
	Message  string         // Original message/alert text for title generation
}

// SpawnIncidentManager creates a new incident manager instance
// Creates AGENTS.md at workspace root (pi-mono reads it from cwd upward)
func (s *SkillService) SpawnIncidentManager(ctx *IncidentContext) (string, string, error) {
	// Generate UUID for this incident
	incidentUUID := uuid.New().String()

	// Create incident directory with 0777 permissions so agent worker (UID 1001) can create files
	incidentDir := filepath.Join(s.incidentsDir, incidentUUID)
	if err := os.MkdirAll(incidentDir, 0777); err != nil {
		return "", "", fmt.Errorf("failed to create incident directory: %w", err)
	}
	// Ensure directory has correct permissions even if parent existed
	if err := os.Chmod(incidentDir, 0777); err != nil {
		slog.Error("failed to chmod incident directory", "dir", incidentDir, "err", err)
	}

	// Generate AGENTS.md at workspace root (pi-mono reads agentDir from cwd)
	agentsMdPath := filepath.Join(incidentDir, "AGENTS.md")
	if err := s.generateIncidentAgentsMd(agentsMdPath); err != nil {
		return "", "", fmt.Errorf("failed to generate AGENTS.md: %w", err)
	}

	// NOTE: Tool credentials are NOT written to incident directory
	// They are fetched by MCP Gateway at execution time for security

	// Use fast fallback title immediately to avoid blocking on LLM call.
	// The LLM-generated title is updated asynchronously in the background.
	titleGen := NewTitleGenerator()
	title := titleGen.GenerateFallbackTitle(ctx.Message, ctx.Source)

	// Create incident record in database with fallback title
	incident := &database.Incident{
		UUID:       incidentUUID,
		Source:     ctx.Source,
		SourceID:   ctx.SourceID,
		Title:      title,
		Status:     database.IncidentStatusPending,
		Context:    ctx.Context,
		WorkingDir: incidentDir, // Working dir is incident root
	}

	if err := s.db.Create(incident).Error; err != nil {
		return "", "", fmt.Errorf("failed to create incident record: %w", err)
	}

	// Generate LLM title in background and update DB when ready
	if ctx.Message != "" && len(ctx.Message) >= 10 {
		go func() {
			generatedTitle, err := titleGen.GenerateTitle(ctx.Message, ctx.Source)
			if err != nil {
				slog.Warn("background title generation failed", "incident", incidentUUID, "err", err)
				return
			}
			if generatedTitle != "" && generatedTitle != title {
				if err := s.db.Model(&database.Incident{}).Where("uuid = ?", incidentUUID).
					Update("title", generatedTitle).Error; err != nil {
					slog.Warn("failed to update incident title", "incident", incidentUUID, "err", err)
				} else {
					slog.Info("updated incident title", "incident", incidentUUID, "title", generatedTitle)
				}
			}
		}()
	}

	return incidentUUID, incidentDir, nil
}

// generateIncidentAgentsMd generates the AGENTS.md file for incident manager
// pi-mono reads this file from the workspace root (agentDir parameter)
// Skills are discovered by pi-mono's DefaultResourceLoader via additionalSkillPaths,
// so only the incident manager prompt is written here.
func (s *SkillService) generateIncidentAgentsMd(path string) error {
	// Get incident manager prompt from the system skill
	prompt, err := s.GetSkillPrompt("incident-manager")
	if err != nil {
		// Fallback to default if skill file doesn't exist yet
		prompt = database.DefaultIncidentManagerPrompt
	}

	var sb strings.Builder
	sb.WriteString("# Incident Manager\n\n")
	sb.WriteString(prompt)
	sb.WriteString("\n")

	if err := os.WriteFile(path, []byte(sb.String()), 0644); err != nil {
		return fmt.Errorf("failed to write AGENTS.md: %w", err)
	}

	return nil
}

// UpdateIncidentStatus updates the status of an incident.
// Only sets session_id and full_log when non-empty to avoid overwriting existing values.
func (s *SkillService) UpdateIncidentStatus(incidentUUID string, status database.IncidentStatus, sessionID string, fullLog string) error {
	updates := map[string]interface{}{
		"status": status,
	}
	if sessionID != "" {
		updates["session_id"] = sessionID
	}
	if fullLog != "" {
		updates["full_log"] = fullLog
	}

	// Set completed_at timestamp when incident is completed or failed
	if status == database.IncidentStatusCompleted || status == database.IncidentStatusFailed {
		now := time.Now()
		updates["completed_at"] = &now
	}

	if err := s.db.Model(&database.Incident{}).Where("uuid = ?", incidentUUID).Updates(updates).Error; err != nil {
		return fmt.Errorf("failed to update incident status: %w", err)
	}

	return nil
}

// UpdateIncidentComplete updates the incident with final status, log, and response
func (s *SkillService) UpdateIncidentComplete(incidentUUID string, status database.IncidentStatus, sessionID string, fullLog string, response string, tokensUsed int, executionTimeMs int64) error {
	now := time.Now()
	updates := map[string]interface{}{
		"status":            status,
		"session_id":        sessionID,
		"full_log":          fullLog,
		"response":          response,
		"tokens_used":       tokensUsed,
		"execution_time_ms": executionTimeMs,
		"completed_at":      &now,
	}

	if err := s.db.Model(&database.Incident{}).Where("uuid = ?", incidentUUID).Updates(updates).Error; err != nil {
		return fmt.Errorf("failed to update incident: %w", err)
	}

	return nil
}

// UpdateIncidentLog updates only the full_log field of an incident (for progress tracking)
func (s *SkillService) UpdateIncidentLog(incidentUUID string, fullLog string) error {
	if err := s.db.Model(&database.Incident{}).Where("uuid = ?", incidentUUID).Update("full_log", fullLog).Error; err != nil {
		return fmt.Errorf("failed to update incident log: %w", err)
	}
	return nil
}

// GetIncident retrieves an incident by UUID
func (s *SkillService) GetIncident(incidentUUID string) (*database.Incident, error) {
	var incident database.Incident
	if err := s.db.Where("uuid = ?", incidentUUID).First(&incident).Error; err != nil {
		return nil, fmt.Errorf("incident not found: %w", err)
	}
	return &incident, nil
}

// SubagentSummaryInput contains the outcome of a subagent execution for context management
type SubagentSummaryInput struct {
	SkillName     string
	Success       bool
	Output        string   // Final output from the subagent
	FullLog       string   // Complete reasoning log (for database storage)
	ErrorMessages []string // Error messages if failed
	TokensUsed    int
}

// SummarizeSubagentForContext creates a concise summary for the incident manager's context
// This implements failure isolation - failed attempts don't pollute the main context
func SummarizeSubagentForContext(result *SubagentSummaryInput) string {
	if result.Success {
		// For successful runs, include just the final output (not full reasoning)
		return fmt.Sprintf(`
=== Subagent [%s] Result ===
Status: SUCCESS
Output:
%s
=== End [%s] ===
`, result.SkillName, result.Output, result.SkillName)
	}

	// For failed runs, provide minimal context to avoid polluting the LLM's context
	// The incident manager should try a different approach, not retry the same thing
	errorSummary := "Unknown error"
	if len(result.ErrorMessages) > 0 {
		// Take just the first error message, truncated
		errorSummary = result.ErrorMessages[0]
		runes := []rune(errorSummary)
		if len(runes) > 200 {
			errorSummary = string(runes[:200]) + "..."
		}
	}

	return fmt.Sprintf(`
=== Subagent [%s] Result ===
Status: FAILED
Error: %s
Note: The full reasoning log is stored but not shown here to keep context clean.
      Consider trying a different approach or skill.
=== End [%s] ===
`, result.SkillName, errorSummary, result.SkillName)
}

// AppendSubagentLog appends a subagent's reasoning log to the incident's full_log
// This stores the FULL log in the database for debugging/review purposes
// Uses SQL concatenation to avoid race conditions when multiple subagents complete concurrently
func (s *SkillService) AppendSubagentLog(incidentUUID string, skillName string, subagentLog string) error {
	// Format subagent log with markers
	formattedLog := fmt.Sprintf("\n\n--- Subagent [%s] Reasoning Log ---\n%s\n--- End Subagent [%s] Reasoning Log ---\n",
		skillName, subagentLog, skillName)

	// Use SQL concatenation to atomically append without read-modify-write race
	if err := s.db.Model(&database.Incident{}).Where("uuid = ?", incidentUUID).
		Update("full_log", gorm.Expr("COALESCE(full_log, '') || ?", formattedLog)).Error; err != nil {
		return fmt.Errorf("failed to append subagent log: %w", err)
	}

	return nil
}
