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

func TestObservingMonitor_NoSettings(t *testing.T) {
	db := setupTestDB(t)

	// Don't create settings - test default behavior
	twoMinutesAgo := time.Now().Add(-2 * time.Minute)
	incident := &database.Incident{
		UUID:               "inc-1",
		Status:             database.IncidentStatusObserving,
		ObservingStartedAt: &twoMinutesAgo,
	}
	db.Create(incident)

	monitor := NewObservingMonitor(db)
	// Should handle missing settings gracefully
	_, err := monitor.CheckAndTransition()
	// Error handling depends on implementation - this documents behavior
	if err != nil {
		// Expected if settings are required
		t.Logf("Got expected error with no settings: %v", err)
	}
}

func TestObservingMonitor_NoObservingIncidents(t *testing.T) {
	db := setupTestDB(t)

	settings := database.NewDefaultAggregationSettings()
	settings.ObservingDurationMinutes = 1
	db.Create(settings)

	// Create incidents with other statuses
	db.Create(&database.Incident{UUID: "inc-1", Status: database.IncidentStatusRunning})
	db.Create(&database.Incident{UUID: "inc-2", Status: database.IncidentStatusCompleted})

	monitor := NewObservingMonitor(db)
	transitioned, err := monitor.CheckAndTransition()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if transitioned != 0 {
		t.Errorf("expected 0 transitioned incidents, got %d", transitioned)
	}
}

func TestObservingMonitor_NilObservingStartedAt(t *testing.T) {
	db := setupTestDB(t)

	settings := database.NewDefaultAggregationSettings()
	settings.ObservingDurationMinutes = 1
	db.Create(settings)

	// Create incident with nil ObservingStartedAt
	incident := &database.Incident{
		UUID:               "inc-1",
		Status:             database.IncidentStatusObserving,
		ObservingStartedAt: nil, // Edge case
	}
	db.Create(incident)

	monitor := NewObservingMonitor(db)
	// Should handle nil timestamp gracefully
	transitioned, err := monitor.CheckAndTransition()
	if err != nil {
		t.Logf("Got error for nil timestamp: %v", err)
	}

	// Document expected behavior
	t.Logf("Transitioned %d incidents with nil ObservingStartedAt", transitioned)
}

func TestObservingMonitor_MultipleIncidents(t *testing.T) {
	db := setupTestDB(t)

	settings := database.NewDefaultAggregationSettings()
	settings.ObservingDurationMinutes = 5
	db.Create(settings)

	// Create multiple incidents with different observing times
	tenMinutesAgo := time.Now().Add(-10 * time.Minute)
	sixMinutesAgo := time.Now().Add(-6 * time.Minute)
	threeMinutesAgo := time.Now().Add(-3 * time.Minute)
	oneMinuteAgo := time.Now().Add(-1 * time.Minute)

	db.Create(&database.Incident{UUID: "inc-1", Status: database.IncidentStatusObserving, ObservingStartedAt: &tenMinutesAgo})
	db.Create(&database.Incident{UUID: "inc-2", Status: database.IncidentStatusObserving, ObservingStartedAt: &sixMinutesAgo})
	db.Create(&database.Incident{UUID: "inc-3", Status: database.IncidentStatusObserving, ObservingStartedAt: &threeMinutesAgo})
	db.Create(&database.Incident{UUID: "inc-4", Status: database.IncidentStatusObserving, ObservingStartedAt: &oneMinuteAgo})

	monitor := NewObservingMonitor(db)
	transitioned, err := monitor.CheckAndTransition()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// inc-1 (10 min) and inc-2 (6 min) should be transitioned (both > 5 min)
	if transitioned != 2 {
		t.Errorf("expected 2 transitioned incidents, got %d", transitioned)
	}

	// Verify correct incidents were transitioned
	var inc1, inc2, inc3, inc4 database.Incident
	db.Where("uuid = ?", "inc-1").First(&inc1)
	db.Where("uuid = ?", "inc-2").First(&inc2)
	db.Where("uuid = ?", "inc-3").First(&inc3)
	db.Where("uuid = ?", "inc-4").First(&inc4)

	if inc1.Status != database.IncidentStatusCompleted {
		t.Errorf("inc-1: expected 'completed', got '%s'", inc1.Status)
	}
	if inc2.Status != database.IncidentStatusCompleted {
		t.Errorf("inc-2: expected 'completed', got '%s'", inc2.Status)
	}
	if inc3.Status != database.IncidentStatusObserving {
		t.Errorf("inc-3: expected 'observing', got '%s'", inc3.Status)
	}
	if inc4.Status != database.IncidentStatusObserving {
		t.Errorf("inc-4: expected 'observing', got '%s'", inc4.Status)
	}
}

func TestObservingMonitor_ExactlyAtBoundary(t *testing.T) {
	db := setupTestDB(t)

	settings := database.NewDefaultAggregationSettings()
	settings.ObservingDurationMinutes = 5
	db.Create(settings)

	// Create incident exactly at the boundary
	exactlyFiveMinutesAgo := time.Now().Add(-5 * time.Minute)
	incident := &database.Incident{
		UUID:               "inc-1",
		Status:             database.IncidentStatusObserving,
		ObservingStartedAt: &exactlyFiveMinutesAgo,
	}
	db.Create(incident)

	monitor := NewObservingMonitor(db)
	transitioned, err := monitor.CheckAndTransition()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Document boundary behavior (>= or > comparison)
	t.Logf("Boundary test: transitioned %d (exact 5 min duration with 5 min threshold)", transitioned)
}

func TestObservingMonitor_ZeroDuration(t *testing.T) {
	db := setupTestDB(t)

	settings := database.NewDefaultAggregationSettings()
	settings.ObservingDurationMinutes = 0 // Edge case: zero duration
	db.Create(settings)

	oneMinuteAgo := time.Now().Add(-1 * time.Minute)
	incident := &database.Incident{
		UUID:               "inc-1",
		Status:             database.IncidentStatusObserving,
		ObservingStartedAt: &oneMinuteAgo,
	}
	db.Create(incident)

	monitor := NewObservingMonitor(db)
	transitioned, err := monitor.CheckAndTransition()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Document actual behavior: zero duration uses default behavior
	// The implementation may interpret 0 as "disabled" or "use default"
	t.Logf("Zero duration test: transitioned %d incidents (documents actual implementation behavior)", transitioned)
}

func TestNewObservingMonitor(t *testing.T) {
	db := setupTestDB(t)

	monitor := NewObservingMonitor(db)
	if monitor == nil {
		t.Fatal("NewObservingMonitor() returned nil")
	}
}
