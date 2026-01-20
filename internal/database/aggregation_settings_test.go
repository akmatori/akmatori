package database

import "testing"

func TestAggregationSettings_TableName(t *testing.T) {
	as := AggregationSettings{}
	if as.TableName() != "aggregation_settings" {
		t.Errorf("expected table name 'aggregation_settings', got '%s'", as.TableName())
	}
}

func TestAggregationSettings_Defaults(t *testing.T) {
	as := NewDefaultAggregationSettings()

	if !as.Enabled {
		t.Error("expected Enabled to be true by default")
	}
	if as.CorrelationConfidenceThreshold != 0.70 {
		t.Errorf("expected CorrelationConfidenceThreshold 0.70, got %f", as.CorrelationConfidenceThreshold)
	}
	if as.MergeConfidenceThreshold != 0.75 {
		t.Errorf("expected MergeConfidenceThreshold 0.75, got %f", as.MergeConfidenceThreshold)
	}
	if !as.RecorrelationEnabled {
		t.Error("expected RecorrelationEnabled to be true by default")
	}
	if as.RecorrelationIntervalMinutes != 3 {
		t.Errorf("expected RecorrelationIntervalMinutes 3, got %d", as.RecorrelationIntervalMinutes)
	}
	if as.MaxIncidentsToAnalyze != 20 {
		t.Errorf("expected MaxIncidentsToAnalyze 20, got %d", as.MaxIncidentsToAnalyze)
	}
	if as.ObservingDurationMinutes != 30 {
		t.Errorf("expected ObservingDurationMinutes 30, got %d", as.ObservingDurationMinutes)
	}
	if as.CorrelatorTimeoutSeconds != 5 {
		t.Errorf("expected CorrelatorTimeoutSeconds 5, got %d", as.CorrelatorTimeoutSeconds)
	}
	if as.MergeAnalyzerTimeoutSeconds != 30 {
		t.Errorf("expected MergeAnalyzerTimeoutSeconds 30, got %d", as.MergeAnalyzerTimeoutSeconds)
	}
}
