package services

import (
	"github.com/akmatori/akmatori/internal/database"

	"gorm.io/gorm"
)

// AggregationService handles alert aggregation and incident correlation
type AggregationService struct {
	db *gorm.DB
}

// NewAggregationService creates a new aggregation service
func NewAggregationService(db *gorm.DB) *AggregationService {
	return &AggregationService{db: db}
}

// GetOpenIncidents returns all non-resolved incidents
func (s *AggregationService) GetOpenIncidents() ([]database.Incident, error) {
	var incidents []database.Incident
	err := s.db.Where("status IN ?", []database.IncidentStatus{
		database.IncidentStatusPending,
		database.IncidentStatusRunning,
		database.IncidentStatusDiagnosed,
		database.IncidentStatusObserving,
	}).Order("created_at DESC").Find(&incidents).Error
	return incidents, err
}

// GetOpenIncidentsForCorrelation returns incidents suitable for correlation
// (excludes observing since those are winding down)
func (s *AggregationService) GetOpenIncidentsForCorrelation() ([]database.Incident, error) {
	var incidents []database.Incident
	err := s.db.Where("status IN ?", []database.IncidentStatus{
		database.IncidentStatusPending,
		database.IncidentStatusRunning,
		database.IncidentStatusDiagnosed,
	}).Order("created_at DESC").Find(&incidents).Error
	return incidents, err
}

// GetSettings returns aggregation settings (creates defaults if not exists)
func (s *AggregationService) GetSettings() (*database.AggregationSettings, error) {
	return database.GetOrCreateAggregationSettings(s.db)
}

// UpdateSettings updates aggregation settings
func (s *AggregationService) UpdateSettings(settings *database.AggregationSettings) error {
	return database.UpdateAggregationSettings(s.db, settings)
}

// GetIncidentAlerts returns all alerts for an incident
func (s *AggregationService) GetIncidentAlerts(incidentID uint) ([]database.IncidentAlert, error) {
	var alerts []database.IncidentAlert
	err := s.db.Where("incident_id = ?", incidentID).Order("attached_at ASC").Find(&alerts).Error
	return alerts, err
}

// GetIncidentByUUID returns an incident by UUID
func (s *AggregationService) GetIncidentByUUID(uuid string) (*database.Incident, error) {
	var incident database.Incident
	err := s.db.Where("uuid = ?", uuid).First(&incident).Error
	return &incident, err
}

// AttachAlertToIncident adds an alert to an existing incident
func (s *AggregationService) AttachAlertToIncident(incidentID uint, alert *database.IncidentAlert) error {
	alert.IncidentID = incidentID
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(alert).Error; err != nil {
			return err
		}
		// Update incident alert count and last_alert_at
		return tx.Model(&database.Incident{}).Where("id = ?", incidentID).Updates(map[string]interface{}{
			"alert_count":   gorm.Expr("alert_count + 1"),
			"last_alert_at": alert.AttachedAt,
		}).Error
	})
}

// CreateIncidentWithAlert creates a new incident with its first alert
func (s *AggregationService) CreateIncidentWithAlert(incident *database.Incident, alert *database.IncidentAlert) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(incident).Error; err != nil {
			return err
		}
		alert.IncidentID = incident.ID
		return tx.Create(alert).Error
	})
}

// RecordMerge records an incident merge for audit purposes
func (s *AggregationService) RecordMerge(sourceID, targetID uint, confidence float64, reason, mergedBy string) error {
	merge := &database.IncidentMerge{
		SourceIncidentID: sourceID,
		TargetIncidentID: targetID,
		MergeConfidence:  confidence,
		MergeReason:      reason,
		MergedBy:         mergedBy,
	}
	return s.db.Create(merge).Error
}
