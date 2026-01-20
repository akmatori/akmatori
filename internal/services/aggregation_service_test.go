package services

import (
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/akmatori/akmatori/internal/database"
)

func setupTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to connect to test database: %v", err)
	}

	err = db.AutoMigrate(
		&database.Incident{},
		&database.IncidentAlert{},
		&database.IncidentMerge{},
		&database.AggregationSettings{},
	)
	if err != nil {
		t.Fatalf("failed to migrate test database: %v", err)
	}

	return db
}

func TestAggregationService_GetOpenIncidents_Empty(t *testing.T) {
	db := setupTestDB(t)
	svc := NewAggregationService(db)

	incidents, err := svc.GetOpenIncidents()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(incidents) != 0 {
		t.Errorf("expected 0 incidents, got %d", len(incidents))
	}
}

func TestAggregationService_GetOpenIncidents_FiltersResolved(t *testing.T) {
	db := setupTestDB(t)
	svc := NewAggregationService(db)

	// Create incidents with different statuses
	db.Create(&database.Incident{UUID: "inc-1", Status: database.IncidentStatusPending})
	db.Create(&database.Incident{UUID: "inc-2", Status: database.IncidentStatusRunning})
	db.Create(&database.Incident{UUID: "inc-3", Status: database.IncidentStatusDiagnosed})
	db.Create(&database.Incident{UUID: "inc-4", Status: database.IncidentStatusObserving})
	db.Create(&database.Incident{UUID: "inc-5", Status: database.IncidentStatusCompleted})
	db.Create(&database.Incident{UUID: "inc-6", Status: database.IncidentStatusFailed})

	incidents, err := svc.GetOpenIncidents()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should return pending, running, diagnosed, observing (4 total)
	if len(incidents) != 4 {
		t.Errorf("expected 4 open incidents, got %d", len(incidents))
	}
}

func TestAggregationService_GetSettings(t *testing.T) {
	db := setupTestDB(t)
	svc := NewAggregationService(db)

	settings, err := svc.GetSettings()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !settings.Enabled {
		t.Error("expected settings to be enabled by default")
	}
}

func TestAggregationService_GetOpenIncidentsForCorrelation(t *testing.T) {
	db := setupTestDB(t)
	svc := NewAggregationService(db)

	// Create incidents with different statuses
	db.Create(&database.Incident{UUID: "inc-1", Status: database.IncidentStatusPending})
	db.Create(&database.Incident{UUID: "inc-2", Status: database.IncidentStatusRunning})
	db.Create(&database.Incident{UUID: "inc-3", Status: database.IncidentStatusDiagnosed})
	db.Create(&database.Incident{UUID: "inc-4", Status: database.IncidentStatusObserving})
	db.Create(&database.Incident{UUID: "inc-5", Status: database.IncidentStatusCompleted})

	incidents, err := svc.GetOpenIncidentsForCorrelation()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should return pending, running, diagnosed (3 total) - observing excluded
	if len(incidents) != 3 {
		t.Errorf("expected 3 incidents for correlation, got %d", len(incidents))
	}
}

func TestAggregationService_UpdateSettings(t *testing.T) {
	db := setupTestDB(t)
	svc := NewAggregationService(db)

	// Get default settings
	settings, err := svc.GetSettings()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Update a setting
	settings.CorrelationConfidenceThreshold = 0.85
	err = svc.UpdateSettings(settings)
	if err != nil {
		t.Fatalf("unexpected error updating settings: %v", err)
	}

	// Verify the update
	updated, err := svc.GetSettings()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated.CorrelationConfidenceThreshold != 0.85 {
		t.Errorf("expected threshold 0.85, got %f", updated.CorrelationConfidenceThreshold)
	}
}

func TestAggregationService_GetIncidentAlerts(t *testing.T) {
	db := setupTestDB(t)
	svc := NewAggregationService(db)

	// Create an incident
	incident := &database.Incident{UUID: "inc-1", Status: database.IncidentStatusPending, Source: "test"}
	db.Create(incident)

	// Create alerts for the incident
	now := time.Now()
	db.Create(&database.IncidentAlert{IncidentID: incident.ID, AlertName: "Alert1", SourceType: "test", SourceFingerprint: "fp1", Status: "firing", AttachedAt: now})
	db.Create(&database.IncidentAlert{IncidentID: incident.ID, AlertName: "Alert2", SourceType: "test", SourceFingerprint: "fp2", Status: "firing", AttachedAt: now.Add(time.Minute)})

	alerts, err := svc.GetIncidentAlerts(incident.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(alerts) != 2 {
		t.Errorf("expected 2 alerts, got %d", len(alerts))
	}
}

func TestAggregationService_GetIncidentByUUID(t *testing.T) {
	db := setupTestDB(t)
	svc := NewAggregationService(db)

	// Create an incident
	db.Create(&database.Incident{UUID: "test-uuid-123", Status: database.IncidentStatusPending, Source: "test", Title: "Test Incident"})

	incident, err := svc.GetIncidentByUUID("test-uuid-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if incident.Title != "Test Incident" {
		t.Errorf("expected title 'Test Incident', got '%s'", incident.Title)
	}
}

func TestAggregationService_GetIncidentByUUID_NotFound(t *testing.T) {
	db := setupTestDB(t)
	svc := NewAggregationService(db)

	_, err := svc.GetIncidentByUUID("non-existent")
	if err == nil {
		t.Error("expected error for non-existent incident")
	}
}

func TestAggregationService_AttachAlertToIncident(t *testing.T) {
	db := setupTestDB(t)
	svc := NewAggregationService(db)

	// Create an incident
	incident := &database.Incident{UUID: "inc-1", Status: database.IncidentStatusPending, Source: "test", AlertCount: 1}
	db.Create(incident)

	// Attach an alert
	now := time.Now()
	alert := &database.IncidentAlert{
		AlertName:         "NewAlert",
		SourceType:        "test",
		SourceFingerprint: "fp-new",
		Status:            "firing",
		AttachedAt:        now,
	}
	err := svc.AttachAlertToIncident(incident.ID, alert)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the alert was attached
	if alert.IncidentID != incident.ID {
		t.Errorf("expected alert incident ID %d, got %d", incident.ID, alert.IncidentID)
	}

	// Verify incident alert count was updated
	var updated database.Incident
	db.First(&updated, incident.ID)
	if updated.AlertCount != 2 {
		t.Errorf("expected alert count 2, got %d", updated.AlertCount)
	}
}

func TestAggregationService_CreateIncidentWithAlert(t *testing.T) {
	db := setupTestDB(t)
	svc := NewAggregationService(db)

	now := time.Now()
	incident := &database.Incident{
		UUID:       "new-incident",
		Status:     database.IncidentStatusPending,
		Source:     "test",
		AlertCount: 1,
	}
	alert := &database.IncidentAlert{
		AlertName:         "FirstAlert",
		SourceType:        "test",
		SourceFingerprint: "fp-first",
		Status:            "firing",
		AttachedAt:        now,
	}

	err := svc.CreateIncidentWithAlert(incident, alert)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify incident was created with an ID
	if incident.ID == 0 {
		t.Error("expected incident to have an ID after creation")
	}

	// Verify alert was linked to incident
	if alert.IncidentID != incident.ID {
		t.Errorf("expected alert incident ID %d, got %d", incident.ID, alert.IncidentID)
	}

	// Verify we can retrieve the incident
	retrieved, err := svc.GetIncidentByUUID("new-incident")
	if err != nil {
		t.Fatalf("unexpected error retrieving incident: %v", err)
	}
	if retrieved.UUID != "new-incident" {
		t.Errorf("expected UUID 'new-incident', got '%s'", retrieved.UUID)
	}
}

func TestAggregationService_RecordMerge(t *testing.T) {
	db := setupTestDB(t)
	svc := NewAggregationService(db)

	// Create two incidents
	db.Create(&database.Incident{UUID: "source-inc", Status: database.IncidentStatusPending, Source: "test"})
	db.Create(&database.Incident{UUID: "target-inc", Status: database.IncidentStatusPending, Source: "test"})

	var source, target database.Incident
	db.Where("uuid = ?", "source-inc").First(&source)
	db.Where("uuid = ?", "target-inc").First(&target)

	// Record a merge
	err := svc.RecordMerge(source.ID, target.ID, 0.92, "Same root cause detected", "system")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the merge was recorded
	var merge database.IncidentMerge
	err = db.Where("source_incident_id = ?", source.ID).First(&merge).Error
	if err != nil {
		t.Fatalf("failed to find merge record: %v", err)
	}
	if merge.TargetIncidentID != target.ID {
		t.Errorf("expected target ID %d, got %d", target.ID, merge.TargetIncidentID)
	}
	if merge.MergeConfidence != 0.92 {
		t.Errorf("expected confidence 0.92, got %f", merge.MergeConfidence)
	}
	if merge.MergedBy != "system" {
		t.Errorf("expected merged_by 'system', got '%s'", merge.MergedBy)
	}
}

func TestAggregationService_BuildCorrelatorInput(t *testing.T) {
	db := setupTestDB(t)
	svc := NewAggregationService(db)

	// Create an incident with alerts
	incident := &database.Incident{
		UUID:   "inc-1",
		Title:  "Test Incident",
		Status: database.IncidentStatusRunning,
		Source: "test",
	}
	db.Create(incident)

	alert := &database.IncidentAlert{
		IncidentID:        incident.ID,
		SourceType:        "alertmanager",
		SourceFingerprint: "fp-1",
		AlertName:         "HighCPU",
		Severity:          "critical",
		TargetHost:        "prod-db-01",
		Status:            "firing",
		AttachedAt:        time.Now(),
	}
	db.Create(alert)

	// Build correlator input
	incomingAlert := AlertContext{
		AlertName:         "HighMemory",
		Severity:          "high",
		TargetHost:        "prod-db-01",
		SourceType:        "alertmanager",
		SourceFingerprint: "fp-2",
	}

	input, err := svc.BuildCorrelatorInput(incomingAlert)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if input.IncomingAlert.AlertName != "HighMemory" {
		t.Errorf("expected AlertName 'HighMemory', got '%s'", input.IncomingAlert.AlertName)
	}
	if len(input.OpenIncidents) != 1 {
		t.Errorf("expected 1 open incident, got %d", len(input.OpenIncidents))
	}
	if len(input.OpenIncidents[0].Alerts) != 1 {
		t.Errorf("expected 1 alert in incident, got %d", len(input.OpenIncidents[0].Alerts))
	}
}

func TestAggregationService_ParseCorrelatorOutput(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected CorrelatorOutput
		wantErr  bool
	}{
		{
			name:  "valid attach decision",
			input: `{"decision": "attach", "incident_uuid": "inc-1", "confidence": 0.89, "reason": "Same host"}`,
			expected: CorrelatorOutput{
				Decision:     "attach",
				IncidentUUID: "inc-1",
				Confidence:   0.89,
				Reason:       "Same host",
			},
		},
		{
			name:  "valid new decision",
			input: `{"decision": "new", "confidence": 0.75, "reason": "Different host"}`,
			expected: CorrelatorOutput{
				Decision:   "new",
				Confidence: 0.75,
				Reason:     "Different host",
			},
		},
		{
			name:    "invalid JSON",
			input:   `not json`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output, err := ParseCorrelatorOutput(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if output.Decision != tt.expected.Decision {
				t.Errorf("expected Decision '%s', got '%s'", tt.expected.Decision, output.Decision)
			}
			if output.IncidentUUID != tt.expected.IncidentUUID {
				t.Errorf("expected IncidentUUID '%s', got '%s'", tt.expected.IncidentUUID, output.IncidentUUID)
			}
		})
	}
}
