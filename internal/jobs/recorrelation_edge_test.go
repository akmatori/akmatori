package jobs

import (
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// setupRecorrelationTestDB creates an in-memory SQLite database for testing
func setupRecorrelationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}

	// Run migrations
	err = db.AutoMigrate(
		&database.AggregationSettings{},
		&database.Incident{},
		&database.IncidentAlert{},
		&database.IncidentMerge{},
	)
	if err != nil {
		t.Fatalf("Failed to migrate test database: %v", err)
	}

	return db
}

// TestRecorrelationJob_NewJob tests job creation
func TestRecorrelationJob_NewJob(t *testing.T) {
	db := setupRecorrelationTestDB(t)
	aggService := services.NewAggregationService(db)

	job := NewRecorrelationJob(db, aggService, nil)

	if job == nil {
		t.Fatal("NewRecorrelationJob returned nil")
	}
	if job.db == nil {
		t.Error("db should not be nil")
	}
	if job.aggService == nil {
		t.Error("aggService should not be nil")
	}
}

// TestRecorrelationJob_NoSettings tests behavior when no settings exist
func TestRecorrelationJob_NoSettings(t *testing.T) {
	db := setupRecorrelationTestDB(t)
	aggService := services.NewAggregationService(db)
	job := NewRecorrelationJob(db, aggService, nil)

	// Should use defaults when no settings exist
	merges, err := job.Run()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if merges != 0 {
		t.Errorf("expected 0 merges with no settings/incidents, got %d", merges)
	}
}

// TestRecorrelationJob_SingleIncident tests with only one incident
func TestRecorrelationJob_SingleIncident(t *testing.T) {
	db := setupRecorrelationTestDB(t)

	// Enable recorrelation
	settings := database.NewDefaultAggregationSettings()
	settings.RecorrelationEnabled = true
	db.Create(settings)

	// Create single incident
	db.Create(&database.Incident{
		UUID:   "inc-single",
		Status: database.IncidentStatusRunning,
	})

	aggService := services.NewAggregationService(db)
	job := NewRecorrelationJob(db, aggService, nil)

	merges, err := job.Run()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if merges != 0 {
		t.Errorf("expected 0 merges with single incident, got %d", merges)
	}
}

// TestRecorrelationJob_NoOpenIncidents tests with no open incidents
func TestRecorrelationJob_NoOpenIncidents(t *testing.T) {
	db := setupRecorrelationTestDB(t)

	settings := database.NewDefaultAggregationSettings()
	settings.RecorrelationEnabled = true
	db.Create(settings)

	// Only completed incidents
	db.Create(&database.Incident{UUID: "inc-1", Status: database.IncidentStatusCompleted})
	db.Create(&database.Incident{UUID: "inc-2", Status: database.IncidentStatusCompleted})

	aggService := services.NewAggregationService(db)
	job := NewRecorrelationJob(db, aggService, nil)

	merges, err := job.Run()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if merges != 0 {
		t.Errorf("expected 0 merges with no open incidents, got %d", merges)
	}
}

// TestRecorrelationJob_ExactlyAtMaxIncidents tests at the boundary
func TestRecorrelationJob_ExactlyAtMaxIncidents(t *testing.T) {
	db := setupRecorrelationTestDB(t)

	settings := database.NewDefaultAggregationSettings()
	settings.RecorrelationEnabled = true
	settings.MaxIncidentsToAnalyze = 3
	db.Create(settings)

	// Create exactly 3 incidents (at the limit)
	db.Create(&database.Incident{UUID: "inc-1", Status: database.IncidentStatusRunning})
	db.Create(&database.Incident{UUID: "inc-2", Status: database.IncidentStatusRunning})
	db.Create(&database.Incident{UUID: "inc-3", Status: database.IncidentStatusRunning})

	aggService := services.NewAggregationService(db)
	job := NewRecorrelationJob(db, aggService, nil)

	// Should NOT skip since it's exactly at the limit, not over
	merges, err := job.Run()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// No executor, so 0 merges expected
	if merges != 0 {
		t.Errorf("expected 0 merges without executor, got %d", merges)
	}
}

// TestRecorrelationJob_MixedStatusIncidents tests with various incident statuses
func TestRecorrelationJob_MixedStatusIncidents(t *testing.T) {
	db := setupRecorrelationTestDB(t)

	settings := database.NewDefaultAggregationSettings()
	settings.RecorrelationEnabled = true
	settings.MaxIncidentsToAnalyze = 10
	db.Create(settings)

	// Create incidents with various statuses
	db.Create(&database.Incident{UUID: "inc-running-1", Status: database.IncidentStatusRunning})
	db.Create(&database.Incident{UUID: "inc-running-2", Status: database.IncidentStatusRunning})
	db.Create(&database.Incident{UUID: "inc-pending", Status: database.IncidentStatusPending})
	db.Create(&database.Incident{UUID: "inc-diagnosed", Status: database.IncidentStatusDiagnosed})
	db.Create(&database.Incident{UUID: "inc-observing", Status: database.IncidentStatusObserving})
	db.Create(&database.Incident{UUID: "inc-completed", Status: database.IncidentStatusCompleted})
	db.Create(&database.Incident{UUID: "inc-failed", Status: database.IncidentStatusFailed})

	aggService := services.NewAggregationService(db)
	job := NewRecorrelationJob(db, aggService, nil)

	merges, err := job.Run()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Without executor, always 0 merges
	if merges != 0 {
		t.Errorf("expected 0 merges without executor, got %d", merges)
	}
}

// TestRecorrelationJob_DisabledViaSettings tests explicit disable
func TestRecorrelationJob_DisabledViaSettings(t *testing.T) {
	db := setupRecorrelationTestDB(t)

	settings := database.NewDefaultAggregationSettings()
	settings.RecorrelationEnabled = false
	db.Create(settings)

	// Create multiple open incidents
	db.Create(&database.Incident{UUID: "inc-1", Status: database.IncidentStatusRunning})
	db.Create(&database.Incident{UUID: "inc-2", Status: database.IncidentStatusRunning})

	aggService := services.NewAggregationService(db)
	job := NewRecorrelationJob(db, aggService, nil)

	merges, err := job.Run()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if merges != 0 {
		t.Errorf("expected 0 merges when disabled, got %d", merges)
	}
}

// TestRecorrelationJob_IncidentsWithAlerts tests with alerts attached
func TestRecorrelationJob_IncidentsWithAlerts(t *testing.T) {
	db := setupRecorrelationTestDB(t)

	settings := database.NewDefaultAggregationSettings()
	settings.RecorrelationEnabled = true
	settings.MaxIncidentsToAnalyze = 10
	db.Create(settings)

	// Create incidents
	inc1 := &database.Incident{UUID: "inc-1", Status: database.IncidentStatusRunning}
	inc2 := &database.Incident{UUID: "inc-2", Status: database.IncidentStatusRunning}
	db.Create(inc1)
	db.Create(inc2)

	// Add alerts to incidents
	now := time.Now()
	db.Create(&database.IncidentAlert{
		IncidentID:  inc1.ID,
		AlertName:   "HighCPU",
		Severity:    string(database.AlertSeverityCritical),
		TargetHost:  "server-01",
		Summary:     "CPU above 90%",
		AttachedAt:  now,
	})
	db.Create(&database.IncidentAlert{
		IncidentID:  inc2.ID,
		AlertName:   "HighMemory",
		Severity:    string(database.AlertSeverityWarning),
		TargetHost:  "server-01",
		Summary:     "Memory above 80%",
		AttachedAt:  now,
	})

	aggService := services.NewAggregationService(db)
	job := NewRecorrelationJob(db, aggService, nil)

	// Should run without error
	merges, err := job.Run()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Without executor, 0 merges
	if merges != 0 {
		t.Errorf("expected 0 merges without executor, got %d", merges)
	}
}

// mockCodexExecutor implements CodexExecutor for testing
type mockCodexExecutor struct {
	output *services.MergeAnalyzerOutput
	err    error
	calls  int
}

func (m *mockCodexExecutor) RunMergeAnalyzer(input *services.MergeAnalyzerInput, timeoutSeconds int) (*services.MergeAnalyzerOutput, error) {
	m.calls++
	return m.output, m.err
}

// TestRecorrelationJob_WithMockExecutor tests with mock executor
func TestRecorrelationJob_WithMockExecutor(t *testing.T) {
	db := setupRecorrelationTestDB(t)

	settings := database.NewDefaultAggregationSettings()
	settings.RecorrelationEnabled = true
	settings.MaxIncidentsToAnalyze = 10
	settings.MergeConfidenceThreshold = 0.8
	db.Create(settings)

	// Create two open incidents
	db.Create(&database.Incident{UUID: "inc-1", Status: database.IncidentStatusRunning})
	db.Create(&database.Incident{UUID: "inc-2", Status: database.IncidentStatusRunning})

	aggService := services.NewAggregationService(db)

	// Mock executor that returns no merges
	mockExec := &mockCodexExecutor{
		output: &services.MergeAnalyzerOutput{
			ProposedMerges: []services.ProposedMerge{},
		},
	}

	job := NewRecorrelationJob(db, aggService, mockExec)

	merges, err := job.Run()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if merges != 0 {
		t.Errorf("expected 0 merges with empty proposals, got %d", merges)
	}
	if mockExec.calls != 1 {
		t.Errorf("expected 1 executor call, got %d", mockExec.calls)
	}
}

// TestRecorrelationJob_MergesBelowThreshold tests confidence threshold
func TestRecorrelationJob_MergesBelowThreshold(t *testing.T) {
	db := setupRecorrelationTestDB(t)

	settings := database.NewDefaultAggregationSettings()
	settings.RecorrelationEnabled = true
	settings.MaxIncidentsToAnalyze = 10
	settings.MergeConfidenceThreshold = 0.8
	db.Create(settings)

	db.Create(&database.Incident{UUID: "inc-1", Status: database.IncidentStatusRunning})
	db.Create(&database.Incident{UUID: "inc-2", Status: database.IncidentStatusRunning})

	aggService := services.NewAggregationService(db)

	// Mock executor that returns merge below threshold
	mockExec := &mockCodexExecutor{
		output: &services.MergeAnalyzerOutput{
			ProposedMerges: []services.ProposedMerge{
				{
					SourceIncidentUUID: "inc-1",
					TargetIncidentUUID: "inc-2",
					Confidence:         0.5, // Below 0.8 threshold
					Reason:             "Possibly related",
				},
			},
		},
	}

	job := NewRecorrelationJob(db, aggService, mockExec)

	merges, err := job.Run()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should skip merge due to low confidence
	if merges != 0 {
		t.Errorf("expected 0 merges (below threshold), got %d", merges)
	}
}

// TestRecorrelationJob_SuccessfulMerge tests a successful merge
func TestRecorrelationJob_SuccessfulMerge(t *testing.T) {
	db := setupRecorrelationTestDB(t)

	settings := database.NewDefaultAggregationSettings()
	settings.RecorrelationEnabled = true
	settings.MaxIncidentsToAnalyze = 10
	settings.MergeConfidenceThreshold = 0.8
	db.Create(settings)

	// Create incidents
	inc1 := &database.Incident{UUID: "inc-source", Status: database.IncidentStatusRunning}
	inc2 := &database.Incident{UUID: "inc-target", Status: database.IncidentStatusRunning}
	db.Create(inc1)
	db.Create(inc2)

	// Add alert to source incident
	db.Create(&database.IncidentAlert{
		IncidentID: inc1.ID,
		AlertName:  "TestAlert",
		Severity:   string(database.AlertSeverityCritical),
	})

	aggService := services.NewAggregationService(db)

	// Mock executor with high confidence merge
	mockExec := &mockCodexExecutor{
		output: &services.MergeAnalyzerOutput{
			ProposedMerges: []services.ProposedMerge{
				{
					SourceIncidentUUID: "inc-source",
					TargetIncidentUUID: "inc-target",
					Confidence:         0.95,
					Reason:             "Same root cause",
				},
			},
		},
	}

	job := NewRecorrelationJob(db, aggService, mockExec)

	merges, err := job.Run()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if merges != 1 {
		t.Errorf("expected 1 merge, got %d", merges)
	}

	// Verify source incident is now completed
	var sourceIncident database.Incident
	db.First(&sourceIncident, inc1.ID)
	if sourceIncident.Status != database.IncidentStatusCompleted {
		t.Errorf("source incident status = %s, want completed", sourceIncident.Status)
	}

	// Verify merge record was created
	var mergeRecord database.IncidentMerge
	err = db.Where("source_incident_id = ?", inc1.ID).First(&mergeRecord).Error
	if err != nil {
		t.Errorf("merge record not found: %v", err)
	}
	if mergeRecord.MergeConfidence != 0.95 {
		t.Errorf("merge confidence = %f, want 0.95", mergeRecord.MergeConfidence)
	}
}

// TestRecorrelationJob_MergeNonexistentIncident tests merge with missing incident
func TestRecorrelationJob_MergeNonexistentIncident(t *testing.T) {
	db := setupRecorrelationTestDB(t)

	settings := database.NewDefaultAggregationSettings()
	settings.RecorrelationEnabled = true
	settings.MaxIncidentsToAnalyze = 10
	settings.MergeConfidenceThreshold = 0.8
	db.Create(settings)

	// Only create one incident
	db.Create(&database.Incident{UUID: "inc-exists", Status: database.IncidentStatusRunning})
	db.Create(&database.Incident{UUID: "inc-exists-2", Status: database.IncidentStatusRunning})

	aggService := services.NewAggregationService(db)

	// Mock executor tries to merge with nonexistent incident
	mockExec := &mockCodexExecutor{
		output: &services.MergeAnalyzerOutput{
			ProposedMerges: []services.ProposedMerge{
				{
					SourceIncidentUUID: "inc-nonexistent",
					TargetIncidentUUID: "inc-exists",
					Confidence:         0.95,
					Reason:             "Test",
				},
			},
		},
	}

	job := NewRecorrelationJob(db, aggService, mockExec)

	// Should handle error gracefully
	merges, err := job.Run()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Merge should fail but not error
	if merges != 0 {
		t.Errorf("expected 0 merges (nonexistent incident), got %d", merges)
	}
}

// TestRecorrelationJob_MultipleMerges tests multiple merge proposals
func TestRecorrelationJob_MultipleMerges(t *testing.T) {
	db := setupRecorrelationTestDB(t)

	settings := database.NewDefaultAggregationSettings()
	settings.RecorrelationEnabled = true
	settings.MaxIncidentsToAnalyze = 10
	settings.MergeConfidenceThreshold = 0.8
	db.Create(settings)

	// Create three incidents
	inc1 := &database.Incident{UUID: "inc-1", Status: database.IncidentStatusRunning}
	inc2 := &database.Incident{UUID: "inc-2", Status: database.IncidentStatusRunning}
	inc3 := &database.Incident{UUID: "inc-3", Status: database.IncidentStatusRunning}
	db.Create(inc1)
	db.Create(inc2)
	db.Create(inc3)

	aggService := services.NewAggregationService(db)

	// Two merges: one above threshold, one below
	mockExec := &mockCodexExecutor{
		output: &services.MergeAnalyzerOutput{
			ProposedMerges: []services.ProposedMerge{
				{
					SourceIncidentUUID: "inc-1",
					TargetIncidentUUID: "inc-3",
					Confidence:         0.95, // Above threshold
					Reason:             "Related issue",
				},
				{
					SourceIncidentUUID: "inc-2",
					TargetIncidentUUID: "inc-3",
					Confidence:         0.5, // Below threshold
					Reason:             "Maybe related",
				},
			},
		},
	}

	job := NewRecorrelationJob(db, aggService, mockExec)

	merges, err := job.Run()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Only one merge should succeed
	if merges != 1 {
		t.Errorf("expected 1 merge (one above threshold), got %d", merges)
	}
}

// BenchmarkRecorrelationJob_Run benchmarks the job execution
func BenchmarkRecorrelationJob_Run(b *testing.B) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		b.Fatalf("Failed to open test database: %v", err)
	}

	db.AutoMigrate(
		&database.AggregationSettings{},
		&database.Incident{},
		&database.IncidentAlert{},
		&database.IncidentMerge{},
	)

	settings := database.NewDefaultAggregationSettings()
	settings.RecorrelationEnabled = false // Disable to benchmark skip path
	db.Create(settings)

	aggService := services.NewAggregationService(db)
	job := NewRecorrelationJob(db, aggService, nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		job.Run()
	}
}
