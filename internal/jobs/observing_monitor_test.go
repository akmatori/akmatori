package jobs

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
		&database.AggregationSettings{},
	)
	if err != nil {
		t.Fatalf("failed to migrate test database: %v", err)
	}

	return db
}

func TestObservingMonitor_TransitionsExpiredIncidents(t *testing.T) {
	db := setupTestDB(t)

	// Create settings with 1 minute observing duration for testing
	settings := database.NewDefaultAggregationSettings()
	settings.ObservingDurationMinutes = 1
	db.Create(settings)

	// Create an incident that started observing 2 minutes ago
	twoMinutesAgo := time.Now().Add(-2 * time.Minute)
	incident := &database.Incident{
		UUID:               "inc-1",
		Status:             database.IncidentStatusObserving,
		ObservingStartedAt: &twoMinutesAgo,
	}
	db.Create(incident)

	// Run the monitor
	monitor := NewObservingMonitor(db)
	transitioned, err := monitor.CheckAndTransition()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if transitioned != 1 {
		t.Errorf("expected 1 transitioned incident, got %d", transitioned)
	}

	// Verify incident is now resolved
	var updated database.Incident
	db.First(&updated, incident.ID)
	if updated.Status != database.IncidentStatusCompleted {
		t.Errorf("expected status 'completed', got '%s'", updated.Status)
	}
}

func TestObservingMonitor_IgnoresRecentObserving(t *testing.T) {
	db := setupTestDB(t)

	settings := database.NewDefaultAggregationSettings()
	settings.ObservingDurationMinutes = 30
	db.Create(settings)

	// Create an incident that started observing 5 minutes ago
	fiveMinutesAgo := time.Now().Add(-5 * time.Minute)
	incident := &database.Incident{
		UUID:               "inc-1",
		Status:             database.IncidentStatusObserving,
		ObservingStartedAt: &fiveMinutesAgo,
	}
	db.Create(incident)

	monitor := NewObservingMonitor(db)
	transitioned, err := monitor.CheckAndTransition()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if transitioned != 0 {
		t.Errorf("expected 0 transitioned incidents, got %d", transitioned)
	}

	// Verify incident is still observing
	var updated database.Incident
	db.First(&updated, incident.ID)
	if updated.Status != database.IncidentStatusObserving {
		t.Errorf("expected status 'observing', got '%s'", updated.Status)
	}
}
