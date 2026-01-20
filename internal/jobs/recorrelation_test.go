package jobs

import (
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
)

func TestRecorrelationJob_SkipsWhenDisabled(t *testing.T) {
	db := setupTestDB(t)

	// Also migrate IncidentAlert for the service
	db.AutoMigrate(&database.IncidentAlert{}, &database.IncidentMerge{})

	// Disable recorrelation
	settings := database.NewDefaultAggregationSettings()
	settings.RecorrelationEnabled = false
	db.Create(settings)

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

func TestRecorrelationJob_SkipsWhenTooManyIncidents(t *testing.T) {
	db := setupTestDB(t)

	// Also migrate IncidentAlert for the service
	db.AutoMigrate(&database.IncidentAlert{}, &database.IncidentMerge{})

	// Set max to 2
	settings := database.NewDefaultAggregationSettings()
	settings.MaxIncidentsToAnalyze = 2
	db.Create(settings)

	// Create 3 open incidents
	db.Create(&database.Incident{UUID: "inc-1", Status: database.IncidentStatusRunning})
	db.Create(&database.Incident{UUID: "inc-2", Status: database.IncidentStatusRunning})
	db.Create(&database.Incident{UUID: "inc-3", Status: database.IncidentStatusRunning})

	aggService := services.NewAggregationService(db)
	job := NewRecorrelationJob(db, aggService, nil)

	merges, err := job.Run()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if merges != 0 {
		t.Errorf("expected 0 merges when too many incidents, got %d", merges)
	}
}
