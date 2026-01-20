package jobs

import (
	"log"
	"time"

	"gorm.io/gorm"

	"github.com/akmatori/akmatori/internal/database"
)

// ObservingMonitor checks for incidents that should transition from observing to resolved
type ObservingMonitor struct {
	db *gorm.DB
}

// NewObservingMonitor creates a new observing monitor
func NewObservingMonitor(db *gorm.DB) *ObservingMonitor {
	return &ObservingMonitor{db: db}
}

// CheckAndTransition checks observing incidents and transitions expired ones to resolved
func (m *ObservingMonitor) CheckAndTransition() (int, error) {
	// Get aggregation settings
	settings, err := database.GetOrCreateAggregationSettings(m.db)
	if err != nil {
		return 0, err
	}

	// Find incidents in observing state that have exceeded the duration
	cutoff := time.Now().Add(-time.Duration(settings.ObservingDurationMinutes) * time.Minute)

	var incidents []database.Incident
	err = m.db.Where("status = ? AND observing_started_at < ?",
		database.IncidentStatusObserving, cutoff).Find(&incidents).Error
	if err != nil {
		return 0, err
	}

	transitioned := 0
	for _, incident := range incidents {
		now := time.Now()
		err := m.db.Model(&incident).Updates(map[string]interface{}{
			"status":       database.IncidentStatusCompleted,
			"completed_at": now,
		}).Error
		if err != nil {
			log.Printf("Failed to transition incident %s to resolved: %v", incident.UUID, err)
			continue
		}
		transitioned++
		log.Printf("Transitioned incident %s from observing to resolved", incident.UUID)
	}

	return transitioned, nil
}

// Start begins the periodic monitoring
func (m *ObservingMonitor) Start(interval time.Duration, stop <-chan struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			transitioned, err := m.CheckAndTransition()
			if err != nil {
				log.Printf("Observing monitor error: %v", err)
			} else if transitioned > 0 {
				log.Printf("Observing monitor: transitioned %d incidents to resolved", transitioned)
			}
		case <-stop:
			log.Println("Observing monitor stopped")
			return
		}
	}
}
