package api

import (
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/database"
)

func TestSkillToResponse(t *testing.T) {
	skill := database.Skill{
		ID:          1,
		Name:        "test-skill",
		Description: "A test skill",
		Category:    "testing",
	}

	resp := SkillToResponse(skill, "some prompt text")

	if resp.Name != "test-skill" {
		t.Errorf("Name = %q, want %q", resp.Name, "test-skill")
	}
	if resp.Prompt != "some prompt text" {
		t.Errorf("Prompt = %q, want %q", resp.Prompt, "some prompt text")
	}
	if resp.ID != 1 {
		t.Errorf("ID = %d, want 1", resp.ID)
	}
}

func TestIncidentToListItem(t *testing.T) {
	now := time.Now()
	completed := now.Add(5 * time.Minute)
	incident := database.Incident{
		ID:              42,
		UUID:            "test-uuid-123",
		Source:          "api",
		SourceID:        "api-1234",
		Title:           "Test incident",
		Status:          database.IncidentStatusCompleted,
		FullLog:         "very long log output that should be omitted...",
		Response:        "final response also omitted",
		WorkingDir:      "/akmatori/incidents/test",
		TokensUsed:      1500,
		ExecutionTimeMs: 30000,
		AlertCount:      3,
		StartedAt:       now,
		CompletedAt:     &completed,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	item := IncidentToListItem(incident)

	if item.ID != 42 {
		t.Errorf("ID = %d, want 42", item.ID)
	}
	if item.UUID != "test-uuid-123" {
		t.Errorf("UUID = %q, want %q", item.UUID, "test-uuid-123")
	}
	if item.Title != "Test incident" {
		t.Errorf("Title = %q, want %q", item.Title, "Test incident")
	}
	if item.Status != database.IncidentStatusCompleted {
		t.Errorf("Status = %q, want %q", item.Status, database.IncidentStatusCompleted)
	}
	if item.TokensUsed != 1500 {
		t.Errorf("TokensUsed = %d, want 1500", item.TokensUsed)
	}
	if item.AlertCount != 3 {
		t.Errorf("AlertCount = %d, want 3", item.AlertCount)
	}
	if item.CompletedAt == nil {
		t.Error("CompletedAt should not be nil")
	}
}

func TestIncidentsToListItems(t *testing.T) {
	incidents := []database.Incident{
		{ID: 1, UUID: "uuid-1", Status: database.IncidentStatusPending},
		{ID: 2, UUID: "uuid-2", Status: database.IncidentStatusRunning},
		{ID: 3, UUID: "uuid-3", Status: database.IncidentStatusCompleted},
	}

	items := IncidentsToListItems(incidents)

	if len(items) != 3 {
		t.Fatalf("len(items) = %d, want 3", len(items))
	}
	if items[0].UUID != "uuid-1" {
		t.Errorf("items[0].UUID = %q, want %q", items[0].UUID, "uuid-1")
	}
	if items[2].Status != database.IncidentStatusCompleted {
		t.Errorf("items[2].Status = %q, want %q", items[2].Status, database.IncidentStatusCompleted)
	}
}

func TestIncidentsToListItems_Empty(t *testing.T) {
	items := IncidentsToListItems([]database.Incident{})
	if len(items) != 0 {
		t.Errorf("len(items) = %d, want 0", len(items))
	}
}
