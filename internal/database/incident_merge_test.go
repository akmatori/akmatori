package database

import (
	"testing"
	"time"
)

func TestIncidentMerge_TableName(t *testing.T) {
	im := IncidentMerge{}
	if im.TableName() != "incident_merges" {
		t.Errorf("expected table name 'incident_merges', got '%s'", im.TableName())
	}
}

func TestIncidentMerge_Fields(t *testing.T) {
	now := time.Now()
	im := IncidentMerge{
		SourceIncidentID: 1,
		TargetIncidentID: 2,
		MergeConfidence:  0.88,
		MergeReason:      "Same root cause",
		MergedBy:         "system",
		CreatedAt:        now,
	}

	if im.SourceIncidentID != 1 {
		t.Errorf("expected SourceIncidentID 1, got %d", im.SourceIncidentID)
	}
	if im.TargetIncidentID != 2 {
		t.Errorf("expected TargetIncidentID 2, got %d", im.TargetIncidentID)
	}
	if im.MergeConfidence != 0.88 {
		t.Errorf("expected MergeConfidence 0.88, got %f", im.MergeConfidence)
	}
	if im.MergeReason != "Same root cause" {
		t.Errorf("expected MergeReason 'Same root cause', got '%s'", im.MergeReason)
	}
	if im.MergedBy != "system" {
		t.Errorf("expected MergedBy 'system', got '%s'", im.MergedBy)
	}
	if im.CreatedAt != now {
		t.Errorf("expected CreatedAt %v, got %v", now, im.CreatedAt)
	}
}

func TestIncidentMerge_DefaultValues(t *testing.T) {
	im := IncidentMerge{}

	if im.ID != 0 {
		t.Errorf("expected ID to default to 0, got %d", im.ID)
	}
	if im.SourceIncidentID != 0 {
		t.Errorf("expected SourceIncidentID to default to 0, got %d", im.SourceIncidentID)
	}
	if im.TargetIncidentID != 0 {
		t.Errorf("expected TargetIncidentID to default to 0, got %d", im.TargetIncidentID)
	}
	if im.MergeConfidence != 0 {
		t.Errorf("expected MergeConfidence to default to 0, got %f", im.MergeConfidence)
	}
	if im.MergeReason != "" {
		t.Errorf("expected MergeReason to default to empty string, got '%s'", im.MergeReason)
	}
	if im.MergedBy != "" {
		t.Errorf("expected MergedBy to default to empty string, got '%s'", im.MergedBy)
	}
}

func TestIncidentMerge_MergedByUserID(t *testing.T) {
	// Test that MergedBy can store user IDs for manual merges
	im := IncidentMerge{
		SourceIncidentID: 10,
		TargetIncidentID: 20,
		MergeConfidence:  1.0,
		MergeReason:      "Manual merge by operator",
		MergedBy:         "user-12345",
	}

	if im.MergedBy != "user-12345" {
		t.Errorf("expected MergedBy 'user-12345', got '%s'", im.MergedBy)
	}
}
