package handlers

import (
	"net/http"
	"time"

	"github.com/akmatori/akmatori/internal/api"
	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
)

// FingerprintGroup summarises recurring alert identity (rule + host) in the
// correlation log over the last 7 days.
type FingerprintGroup struct {
	AlertName       string `json:"alert_name"`
	TargetHost      string `json:"target_host"`
	Fingerprint     string `json:"fingerprint"`
	RecurrenceCount int64  `json:"recurrence_count"`
	EstTokensSaved  int64  `json:"est_tokens_saved"`
}

// GateRate holds raw hit and total counts for one gate over one time window.
type GateRate struct {
	Hits  int64 `json:"hits"`
	Total int64 `json:"total"`
}

// GateHitRates bundles correlation and suppression gate metrics for 24h / 7d.
type GateHitRates struct {
	Correlation24h GateRate `json:"correlation_24h"`
	Correlation7d  GateRate `json:"correlation_7d"`
	Suppression24h GateRate `json:"suppression_24h"`
	Suppression7d  GateRate `json:"suppression_7d"`
}

// RecurrenceStatsResponse is returned by GET /api/stats/recurrence.
type RecurrenceStatsResponse struct {
	FingerprintGroups   []FingerprintGroup `json:"fingerprint_groups"`
	GateHitRates        GateHitRates       `json:"gate_hit_rates"`
	CandidateSignatures []database.Memory  `json:"candidate_signatures"`
	RedundancyRate24h   float64            `json:"redundancy_rate_24h"`
}

// handleRecurrenceStats handles GET /api/stats/recurrence.
func (h *APIHandler) handleRecurrenceStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.RespondError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	now := time.Now()
	ago24h := now.Add(-24 * time.Hour)
	ago7d := now.Add(-7 * 24 * time.Hour)

	// Top-10 alert identities by number of correlation-log rows in the last 7d.
	// JOIN ensures we only include rows backed by a fingerprinted incident.
	type fpRow struct {
		AlertName       string
		TargetHost      string
		Fingerprint     string
		RecurrenceCount int64
	}
	var fpRows []fpRow
	database.DB.Raw(`
		SELECT acl.alert_name, acl.target_host, i.alert_fingerprint AS fingerprint,
		       COUNT(acl.id) AS recurrence_count
		FROM alert_correlation_logs acl
		JOIN incidents i ON i.uuid = acl.matched_incident_uuid
		WHERE acl.created_at >= ?
		  AND i.alert_fingerprint != ''
		GROUP BY acl.alert_name, acl.target_host, i.alert_fingerprint
		ORDER BY recurrence_count DESC
		LIMIT 10
	`, ago7d).Scan(&fpRows)

	const tokensPerAvoidedRun = 412000
	fingerprintGroups := make([]FingerprintGroup, 0, len(fpRows))
	for _, row := range fpRows {
		fingerprintGroups = append(fingerprintGroups, FingerprintGroup{
			AlertName:       row.AlertName,
			TargetHost:      row.TargetHost,
			Fingerprint:     row.Fingerprint,
			RecurrenceCount: row.RecurrenceCount,
			EstTokensSaved:  row.RecurrenceCount * tokensPerAvoidedRun,
		})
	}

	// Gate hit counts.
	var corrHits24h, corrHits7d, suppHits24h, suppHits7d int64
	database.DB.Model(&database.AlertCorrelationLog{}).Where("created_at >= ?", ago24h).Count(&corrHits24h)
	database.DB.Model(&database.AlertCorrelationLog{}).Where("created_at >= ?", ago7d).Count(&corrHits7d)
	database.DB.Model(&database.AlertSuppressionLog{}).Where("created_at >= ?", ago24h).Count(&suppHits24h)
	database.DB.Model(&database.AlertSuppressionLog{}).Where("created_at >= ?", ago7d).Count(&suppHits7d)

	// Total alert-triggered incidents in each window.
	// This includes suppressed incidents (they create an Incident row too).
	var totalAlertInc24h, totalAlertInc7d int64
	database.DB.Model(&database.Incident{}).
		Where("started_at >= ? AND source_kind = ?", ago24h, database.IncidentSourceKindAlert).
		Count(&totalAlertInc24h)
	database.DB.Model(&database.Incident{}).
		Where("started_at >= ? AND source_kind = ?", ago7d, database.IncidentSourceKindAlert).
		Count(&totalAlertInc7d)

	// Total alert arrivals = correlated events + spawned (or suppressed) incidents.
	// Correlated alerts never create an Incident row, so there is no double-count.
	total24h := corrHits24h + totalAlertInc24h
	total7d := corrHits7d + totalAlertInc7d

	gateHitRates := GateHitRates{
		Correlation24h: GateRate{Hits: corrHits24h, Total: total24h},
		Correlation7d:  GateRate{Hits: corrHits7d, Total: total7d},
		Suppression24h: GateRate{Hits: suppHits24h, Total: totalAlertInc24h},
		Suppression7d:  GateRate{Hits: suppHits7d, Total: totalAlertInc7d},
	}

	// Redundancy rate: fraction of correlated_count events vs total incidents in
	// the last 24h. Matches the badge threshold check in GeneralSettingsSection.
	type redundancyResult struct {
		SumCorrelated int64
		TotalCount    int64
	}
	var redResult redundancyResult
	database.DB.Raw(`
		SELECT COALESCE(SUM(correlated_count), 0) AS sum_correlated, COUNT(*) AS total_count
		FROM incidents
		WHERE started_at >= ? AND source_kind = ?
	`, ago24h, database.IncidentSourceKindAlert).Scan(&redResult)

	var redundancyRate float64
	if redResult.TotalCount > 0 {
		redundancyRate = float64(redResult.SumCorrelated) / float64(redResult.TotalCount)
	}

	// Candidate suppression signatures: incident_pattern and feedback memories
	// that are not yet flagged, created in the last 7 days.
	var candidates []database.Memory
	database.DB.Where(
		"type IN ? AND suppress = ? AND created_at >= ?",
		[]string{services.MemoryTypeIncidentPattern, services.MemoryTypeFeedback},
		false,
		ago7d,
	).Order("created_at DESC").Find(&candidates)

	if candidates == nil {
		candidates = []database.Memory{}
	}
	if fingerprintGroups == nil {
		fingerprintGroups = []FingerprintGroup{}
	}

	api.RespondJSON(w, http.StatusOK, RecurrenceStatsResponse{
		FingerprintGroups:   fingerprintGroups,
		GateHitRates:        gateHitRates,
		CandidateSignatures: candidates,
		RedundancyRate24h:   redundancyRate,
	})
}
