package services

import (
	"time"

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

// BuildCorrelatorInput creates the input structure for the Codex correlator
func (s *AggregationService) BuildCorrelatorInput(incomingAlert AlertContext) (*CorrelatorInput, error) {
	// Get all open incidents (including observing for alert attachment)
	incidents, err := s.GetOpenIncidents()
	if err != nil {
		return nil, err
	}

	incidentSummaries := make([]IncidentSummary, 0, len(incidents))
	for _, inc := range incidents {
		// Get alerts for this incident
		alerts, err := s.GetIncidentAlerts(inc.ID)
		if err != nil {
			return nil, err
		}

		alertSummaries := make([]IncidentAlertSummary, 0, len(alerts))
		for _, a := range alerts {
			labels := make(map[string]string)
			if a.TargetLabels != nil {
				for k, v := range a.TargetLabels {
					if str, ok := v.(string); ok {
						labels[k] = str
					}
				}
			}

			alertSummaries = append(alertSummaries, IncidentAlertSummary{
				AlertName:             a.AlertName,
				Severity:              a.Severity,
				TargetHost:            a.TargetHost,
				TargetService:         a.TargetService,
				Summary:               a.Summary,
				Description:           a.Description,
				SourceType:            a.SourceType,
				SourceFingerprint:     a.SourceFingerprint,
				TargetLabels:          labels,
				Status:                a.Status,
				AttachedAt:            a.AttachedAt,
				CorrelationConfidence: a.CorrelationConfidence,
				CorrelationReason:     a.CorrelationReason,
			})
		}

		// Extract diagnosed root cause from context if available
		rootCause := ""
		if inc.Context != nil {
			if rc, ok := inc.Context["diagnosed_root_cause"].(string); ok {
				rootCause = rc
			}
		}

		incidentSummaries = append(incidentSummaries, IncidentSummary{
			UUID:               inc.UUID,
			Title:              inc.Title,
			Status:             string(inc.Status),
			DiagnosedRootCause: rootCause,
			CreatedAt:          inc.CreatedAt,
			AgeMinutes:         int(time.Since(inc.CreatedAt).Minutes()),
			Alerts:             alertSummaries,
		})
	}

	return &CorrelatorInput{
		IncomingAlert: incomingAlert,
		OpenIncidents: incidentSummaries,
	}, nil
}
