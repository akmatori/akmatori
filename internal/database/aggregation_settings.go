package database

import "time"

// AggregationSettings controls alert aggregation behavior
type AggregationSettings struct {
	ID                             uint    `gorm:"primaryKey" json:"id"`
	Enabled                        bool    `gorm:"default:true" json:"enabled"`
	CorrelationConfidenceThreshold float64 `gorm:"type:decimal(3,2);default:0.70" json:"correlation_confidence_threshold"`
	MergeConfidenceThreshold       float64 `gorm:"type:decimal(3,2);default:0.75" json:"merge_confidence_threshold"`
	RecorrelationEnabled           bool    `gorm:"default:true" json:"recorrelation_enabled"`
	RecorrelationIntervalMinutes   int     `gorm:"default:3" json:"recorrelation_interval_minutes"`
	MaxIncidentsToAnalyze          int     `gorm:"default:20" json:"max_incidents_to_analyze"`
	ObservingDurationMinutes       int     `gorm:"default:30" json:"observing_duration_minutes"`
	CorrelatorTimeoutSeconds       int     `gorm:"default:5" json:"correlator_timeout_seconds"`
	MergeAnalyzerTimeoutSeconds    int     `gorm:"default:30" json:"merge_analyzer_timeout_seconds"`
	CreatedAt                      time.Time `json:"created_at"`
	UpdatedAt                      time.Time `json:"updated_at"`
}

func (AggregationSettings) TableName() string {
	return "aggregation_settings"
}

// NewDefaultAggregationSettings returns settings with default values
func NewDefaultAggregationSettings() *AggregationSettings {
	return &AggregationSettings{
		Enabled:                        true,
		CorrelationConfidenceThreshold: 0.70,
		MergeConfidenceThreshold:       0.75,
		RecorrelationEnabled:           true,
		RecorrelationIntervalMinutes:   3,
		MaxIncidentsToAnalyze:          20,
		ObservingDurationMinutes:       30,
		CorrelatorTimeoutSeconds:       5,
		MergeAnalyzerTimeoutSeconds:    30,
	}
}
