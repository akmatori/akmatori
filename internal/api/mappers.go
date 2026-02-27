package api

import "github.com/akmatori/akmatori/internal/database"

// SkillToResponse converts a database Skill and its prompt to a SkillResponse.
func SkillToResponse(skill database.Skill, prompt string) SkillResponse {
	return SkillResponse{
		Skill:  skill,
		Prompt: prompt,
	}
}

// IncidentToListItem converts a database Incident to a compact list representation.
// It omits large fields like FullLog and Response to reduce response size.
func IncidentToListItem(i database.Incident) IncidentListItem {
	return IncidentListItem{
		ID:              i.ID,
		UUID:            i.UUID,
		Source:          i.Source,
		SourceID:        i.SourceID,
		Title:           i.Title,
		Status:          i.Status,
		TokensUsed:      i.TokensUsed,
		ExecutionTimeMs: i.ExecutionTimeMs,
		AlertCount:      i.AlertCount,
		StartedAt:       i.StartedAt,
		CompletedAt:     i.CompletedAt,
		CreatedAt:       i.CreatedAt,
		UpdatedAt:       i.UpdatedAt,
	}
}

// IncidentsToListItems converts a slice of database Incidents to list items.
func IncidentsToListItems(incidents []database.Incident) []IncidentListItem {
	items := make([]IncidentListItem, len(incidents))
	for i, inc := range incidents {
		items[i] = IncidentToListItem(inc)
	}
	return items
}
