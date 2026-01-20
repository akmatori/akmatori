package jobs

import (
	"encoding/json"
	"log"
	"time"

	"gorm.io/gorm"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
)

// CodexExecutor is the interface for running Codex merge analyzer
type CodexExecutor interface {
	RunMergeAnalyzer(input *services.MergeAnalyzerInput, timeoutSeconds int) (*services.MergeAnalyzerOutput, error)
}

// RecorrelationJob periodically checks open incidents for potential merges
type RecorrelationJob struct {
	db         *gorm.DB
	aggService *services.AggregationService
	executor   CodexExecutor
}

// NewRecorrelationJob creates a new recorrelation job
func NewRecorrelationJob(db *gorm.DB, aggService *services.AggregationService, executor CodexExecutor) *RecorrelationJob {
	return &RecorrelationJob{
		db:         db,
		aggService: aggService,
		executor:   executor,
	}
}

// Run executes one iteration of the recorrelation job
// Returns the number of merges performed
func (j *RecorrelationJob) Run() (int, error) {
	// Get aggregation settings
	settings, err := j.aggService.GetSettings()
	if err != nil {
		return 0, err
	}

	// Skip if recorrelation is disabled
	if !settings.RecorrelationEnabled {
		log.Println("Recorrelation is disabled, skipping")
		return 0, nil
	}

	// Get open incidents (excludes observing since those are winding down)
	incidents, err := j.aggService.GetOpenIncidentsForCorrelation()
	if err != nil {
		return 0, err
	}

	// Skip if too many open incidents (could overwhelm the LLM)
	if len(incidents) > settings.MaxIncidentsToAnalyze {
		log.Printf("Too many open incidents (%d > %d), skipping recorrelation",
			len(incidents), settings.MaxIncidentsToAnalyze)
		return 0, nil
	}

	// Need at least 2 incidents to consider merging
	if len(incidents) < 2 {
		return 0, nil
	}

	// Build incident summaries with their alerts
	incidentSummaries := make([]services.IncidentSummary, 0, len(incidents))
	for _, inc := range incidents {
		alerts, err := j.aggService.GetIncidentAlerts(inc.ID)
		if err != nil {
			log.Printf("Failed to get alerts for incident %s: %v", inc.UUID, err)
			continue
		}

		alertSummaries := make([]services.IncidentAlertSummary, 0, len(alerts))
		for _, a := range alerts {
			labels := make(map[string]string)
			if a.TargetLabels != nil {
				for k, v := range a.TargetLabels {
					if str, ok := v.(string); ok {
						labels[k] = str
					}
				}
			}

			alertSummaries = append(alertSummaries, services.IncidentAlertSummary{
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

		incidentSummaries = append(incidentSummaries, services.IncidentSummary{
			UUID:               inc.UUID,
			Title:              inc.Title,
			Status:             string(inc.Status),
			DiagnosedRootCause: rootCause,
			CreatedAt:          inc.CreatedAt,
			AgeMinutes:         int(time.Since(inc.CreatedAt).Minutes()),
			Alerts:             alertSummaries,
		})
	}

	// Skip if we couldn't build summaries for at least 2 incidents
	if len(incidentSummaries) < 2 {
		return 0, nil
	}

	// If no executor provided, just return (useful for testing without Codex)
	if j.executor == nil {
		log.Println("No Codex executor configured, skipping merge analysis")
		return 0, nil
	}

	// Call Codex merge analyzer
	input := &services.MergeAnalyzerInput{
		OpenIncidents:       incidentSummaries,
		ConfidenceThreshold: settings.MergeConfidenceThreshold,
	}

	output, err := j.executor.RunMergeAnalyzer(input, settings.MergeAnalyzerTimeoutSeconds)
	if err != nil {
		return 0, err
	}

	// Log output for debugging
	outputJSON, _ := json.Marshal(output)
	log.Printf("Merge analyzer output: %s", string(outputJSON))

	// Execute proposed merges that meet the confidence threshold
	mergesPerformed := 0
	for _, merge := range output.ProposedMerges {
		if merge.Confidence < settings.MergeConfidenceThreshold {
			log.Printf("Skipping merge %s -> %s (confidence %.2f < threshold %.2f)",
				merge.SourceIncidentUUID, merge.TargetIncidentUUID,
				merge.Confidence, settings.MergeConfidenceThreshold)
			continue
		}

		err := j.executeMerge(merge)
		if err != nil {
			log.Printf("Failed to execute merge %s -> %s: %v",
				merge.SourceIncidentUUID, merge.TargetIncidentUUID, err)
			continue
		}
		mergesPerformed++
	}

	return mergesPerformed, nil
}

// executeMerge moves alerts from source incident to target incident and closes source
func (j *RecorrelationJob) executeMerge(merge services.ProposedMerge) error {
	// Get source and target incidents
	sourceIncident, err := j.aggService.GetIncidentByUUID(merge.SourceIncidentUUID)
	if err != nil {
		return err
	}

	targetIncident, err := j.aggService.GetIncidentByUUID(merge.TargetIncidentUUID)
	if err != nil {
		return err
	}

	// Move alerts from source to target in a transaction
	err = j.db.Transaction(func(tx *gorm.DB) error {
		// Update all alerts from source to target
		if err := tx.Model(&database.IncidentAlert{}).
			Where("incident_id = ?", sourceIncident.ID).
			Update("incident_id", targetIncident.ID).Error; err != nil {
			return err
		}

		// Update target incident alert count
		var alertCount int64
		tx.Model(&database.IncidentAlert{}).Where("incident_id = ?", targetIncident.ID).Count(&alertCount)
		tx.Model(targetIncident).Update("alert_count", alertCount)

		// Mark source incident as completed (merged)
		now := time.Now()
		if err := tx.Model(sourceIncident).Updates(map[string]interface{}{
			"status":       database.IncidentStatusCompleted,
			"completed_at": now,
		}).Error; err != nil {
			return err
		}

		// Record the merge for audit purposes
		mergeRecord := &database.IncidentMerge{
			SourceIncidentID: sourceIncident.ID,
			TargetIncidentID: targetIncident.ID,
			MergeConfidence:  merge.Confidence,
			MergeReason:      merge.Reason,
			MergedBy:         "system",
		}
		return tx.Create(mergeRecord).Error
	})

	if err != nil {
		return err
	}

	log.Printf("Merged incident %s into %s (confidence: %.2f, reason: %s)",
		merge.SourceIncidentUUID, merge.TargetIncidentUUID,
		merge.Confidence, merge.Reason)

	return nil
}

// Start begins the periodic recorrelation checks
func (j *RecorrelationJob) Start(stop <-chan struct{}) {
	// Get initial interval from settings
	settings, err := j.aggService.GetSettings()
	if err != nil {
		log.Printf("Failed to get recorrelation settings, using default interval: %v", err)
		settings = database.NewDefaultAggregationSettings()
	}

	interval := time.Duration(settings.RecorrelationIntervalMinutes) * time.Minute
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			merges, err := j.Run()
			if err != nil {
				log.Printf("Recorrelation job error: %v", err)
			} else if merges > 0 {
				log.Printf("Recorrelation job: performed %d merges", merges)
			}

			// Refresh interval from settings (in case it changed)
			newSettings, err := j.aggService.GetSettings()
			if err == nil && newSettings.RecorrelationIntervalMinutes != settings.RecorrelationIntervalMinutes {
				settings = newSettings
				interval = time.Duration(settings.RecorrelationIntervalMinutes) * time.Minute
				ticker.Reset(interval)
				log.Printf("Recorrelation interval updated to %d minutes", settings.RecorrelationIntervalMinutes)
			}

		case <-stop:
			log.Println("Recorrelation job stopped")
			return
		}
	}
}
