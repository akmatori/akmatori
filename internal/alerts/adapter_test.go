package alerts

import (
	"testing"

	"github.com/akmatori/akmatori/internal/database"
)

// ========================================
// ExtractNestedValue Tests
// ========================================

func TestExtractNestedValue(t *testing.T) {
	tests := []struct {
		name     string
		data     map[string]interface{}
		path     string
		expected interface{}
	}{
		{
			name:     "empty path",
			data:     map[string]interface{}{"key": "value"},
			path:     "",
			expected: nil,
		},
		{
			name:     "simple key",
			data:     map[string]interface{}{"key": "value"},
			path:     "key",
			expected: "value",
		},
		{
			name:     "missing key",
			data:     map[string]interface{}{"key": "value"},
			path:     "missing",
			expected: nil,
		},
		{
			name: "nested key",
			data: map[string]interface{}{
				"labels": map[string]interface{}{
					"alertname": "HighCPU",
				},
			},
			path:     "labels.alertname",
			expected: "HighCPU",
		},
		{
			name: "deeply nested",
			data: map[string]interface{}{
				"a": map[string]interface{}{
					"b": map[string]interface{}{
						"c": "deep",
					},
				},
			},
			path:     "a.b.c",
			expected: "deep",
		},
		{
			name: "nested with string map",
			data: map[string]interface{}{
				"labels": map[string]string{
					"severity": "critical",
				},
			},
			path:     "labels.severity",
			expected: "critical",
		},
		{
			name: "partial path missing",
			data: map[string]interface{}{
				"a": map[string]interface{}{
					"b": "value",
				},
			},
			path:     "a.missing.c",
			expected: nil,
		},
		{
			name:     "nil data",
			data:     nil,
			path:     "key",
			expected: nil,
		},
		{
			name: "non-map intermediate value",
			data: map[string]interface{}{
				"key": "not a map",
			},
			path:     "key.nested",
			expected: nil,
		},
		{
			name: "numeric value",
			data: map[string]interface{}{
				"count": 42,
			},
			path:     "count",
			expected: 42,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractNestedValue(tt.data, tt.path)
			if result != tt.expected {
				t.Errorf("ExtractNestedValue(%v, %q) = %v, want %v",
					tt.data, tt.path, result, tt.expected)
			}
		})
	}
}

// ========================================
// ExtractString Tests
// ========================================

func TestExtractString(t *testing.T) {
	tests := []struct {
		name     string
		data     map[string]interface{}
		path     string
		expected string
	}{
		{
			name:     "valid string",
			data:     map[string]interface{}{"key": "value"},
			path:     "key",
			expected: "value",
		},
		{
			name:     "missing key returns empty",
			data:     map[string]interface{}{"key": "value"},
			path:     "missing",
			expected: "",
		},
		{
			name:     "non-string value returns empty",
			data:     map[string]interface{}{"count": 42},
			path:     "count",
			expected: "",
		},
		{
			name: "nested string",
			data: map[string]interface{}{
				"labels": map[string]interface{}{
					"alertname": "HighCPU",
				},
			},
			path:     "labels.alertname",
			expected: "HighCPU",
		},
		{
			name:     "empty path returns empty",
			data:     map[string]interface{}{"key": "value"},
			path:     "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractString(tt.data, tt.path)
			if result != tt.expected {
				t.Errorf("ExtractString(%v, %q) = %q, want %q",
					tt.data, tt.path, result, tt.expected)
			}
		})
	}
}

// ========================================
// MergeMappings Tests
// ========================================

func TestMergeMappings(t *testing.T) {
	tests := []struct {
		name      string
		defaults  database.JSONB
		overrides database.JSONB
		checkKey  string
		expected  interface{}
	}{
		{
			name:      "empty both",
			defaults:  database.JSONB{},
			overrides: database.JSONB{},
			checkKey:  "key",
			expected:  nil,
		},
		{
			name:      "defaults only",
			defaults:  database.JSONB{"key": "default"},
			overrides: database.JSONB{},
			checkKey:  "key",
			expected:  "default",
		},
		{
			name:      "overrides only",
			defaults:  database.JSONB{},
			overrides: database.JSONB{"key": "override"},
			checkKey:  "key",
			expected:  "override",
		},
		{
			name:      "override takes precedence",
			defaults:  database.JSONB{"key": "default"},
			overrides: database.JSONB{"key": "override"},
			checkKey:  "key",
			expected:  "override",
		},
		{
			name:      "nil defaults",
			defaults:  nil,
			overrides: database.JSONB{"key": "value"},
			checkKey:  "key",
			expected:  "value",
		},
		{
			name:      "nil overrides",
			defaults:  database.JSONB{"key": "default"},
			overrides: nil,
			checkKey:  "key",
			expected:  "default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MergeMappings(tt.defaults, tt.overrides)
			got := result[tt.checkKey]
			if got != tt.expected {
				t.Errorf("MergeMappings result[%q] = %v, want %v",
					tt.checkKey, got, tt.expected)
			}
		})
	}
}

func TestMergeMappings_PreservesAllKeys(t *testing.T) {
	defaults := database.JSONB{
		"a": "default_a",
		"b": "default_b",
	}
	overrides := database.JSONB{
		"b": "override_b",
		"c": "override_c",
	}

	result := MergeMappings(defaults, overrides)

	if result["a"] != "default_a" {
		t.Errorf("expected a=default_a, got %v", result["a"])
	}
	if result["b"] != "override_b" {
		t.Errorf("expected b=override_b, got %v", result["b"])
	}
	if result["c"] != "override_c" {
		t.Errorf("expected c=override_c, got %v", result["c"])
	}
	if len(result) != 3 {
		t.Errorf("expected 3 keys, got %d", len(result))
	}
}

// ========================================
// NormalizeSeverity Tests
// ========================================

func TestNormalizeSeverity(t *testing.T) {
	tests := []struct {
		name     string
		severity string
		mapping  map[string][]string
		expected database.AlertSeverity
	}{
		// Direct matches (case insensitive)
		{"critical direct", "critical", nil, database.AlertSeverityCritical},
		{"CRITICAL uppercase", "CRITICAL", nil, database.AlertSeverityCritical},
		{"Critical mixed", "Critical", nil, database.AlertSeverityCritical},
		{"high direct", "high", nil, database.AlertSeverityHigh},
		{"warning direct", "warning", nil, database.AlertSeverityWarning},
		{"info direct", "info", nil, database.AlertSeverityInfo},
		{"informational direct", "informational", nil, database.AlertSeverityInfo},

		// Unknown defaults to warning
		{"unknown severity", "unknown", nil, database.AlertSeverityWarning},
		{"empty severity", "", nil, database.AlertSeverityWarning},

		// With severity mapping
		{
			name:     "mapping to critical",
			severity: "disaster",
			mapping:  map[string][]string{"critical": {"disaster", "emergency"}},
			expected: database.AlertSeverityCritical,
		},
		{
			name:     "mapping to high",
			severity: "major",
			mapping:  map[string][]string{"high": {"major", "severe"}},
			expected: database.AlertSeverityHigh,
		},
		{
			name:     "mapping case insensitive",
			severity: "DISASTER",
			mapping:  map[string][]string{"critical": {"disaster"}},
			expected: database.AlertSeverityCritical,
		},
		{
			name:     "mapping not found uses default",
			severity: "random",
			mapping:  map[string][]string{"critical": {"disaster"}},
			expected: database.AlertSeverityWarning,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NormalizeSeverity(tt.severity, tt.mapping)
			if result != tt.expected {
				t.Errorf("NormalizeSeverity(%q, %v) = %v, want %v",
					tt.severity, tt.mapping, result, tt.expected)
			}
		})
	}
}

func TestNormalizeSeverity_WithDefaultMapping(t *testing.T) {
	// Test with the default severity mapping
	tests := []struct {
		input    string
		expected database.AlertSeverity
	}{
		{"p1", database.AlertSeverityCritical},
		{"5", database.AlertSeverityCritical},
		{"emergency", database.AlertSeverityCritical},
		{"p2", database.AlertSeverityHigh},
		{"4", database.AlertSeverityHigh},
		{"error", database.AlertSeverityHigh},
		{"p3", database.AlertSeverityWarning},
		{"average", database.AlertSeverityWarning},
		{"p4", database.AlertSeverityInfo},
		{"notice", database.AlertSeverityInfo},
		{"debug", database.AlertSeverityInfo},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := NormalizeSeverity(tt.input, DefaultSeverityMapping)
			if result != tt.expected {
				t.Errorf("NormalizeSeverity(%q, DefaultSeverityMapping) = %v, want %v",
					tt.input, result, tt.expected)
			}
		})
	}
}

// ========================================
// NormalizeStatus Tests
// ========================================

func TestNormalizeStatus(t *testing.T) {
	tests := []struct {
		status   string
		expected database.AlertStatus
	}{
		// Firing statuses
		{"firing", database.AlertStatusFiring},
		{"FIRING", database.AlertStatusFiring},
		{"Firing", database.AlertStatusFiring},
		{"alerting", database.AlertStatusFiring},
		{"triggered", database.AlertStatusFiring},
		{"active", database.AlertStatusFiring},
		{"problem", database.AlertStatusFiring},

		// Resolved statuses
		{"resolved", database.AlertStatusResolved},
		{"RESOLVED", database.AlertStatusResolved},
		{"ok", database.AlertStatusResolved},
		{"recovery", database.AlertStatusResolved},
		{"inactive", database.AlertStatusResolved},

		// Unknown defaults to firing
		{"unknown", database.AlertStatusFiring},
		{"", database.AlertStatusFiring},
		{"random", database.AlertStatusFiring},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			result := NormalizeStatus(tt.status)
			if result != tt.expected {
				t.Errorf("NormalizeStatus(%q) = %v, want %v",
					tt.status, result, tt.expected)
			}
		})
	}
}

// ========================================
// BaseAdapter Tests
// ========================================

func TestBaseAdapter_GetSourceType(t *testing.T) {
	tests := []struct {
		sourceType string
	}{
		{"alertmanager"},
		{"prometheus"},
		{"datadog"},
		{""},
	}

	for _, tt := range tests {
		t.Run(tt.sourceType, func(t *testing.T) {
			adapter := &BaseAdapter{SourceType: tt.sourceType}
			if got := adapter.GetSourceType(); got != tt.sourceType {
				t.Errorf("GetSourceType() = %q, want %q", got, tt.sourceType)
			}
		})
	}
}

// ========================================
// Benchmarks
// ========================================

func BenchmarkExtractNestedValue_Simple(b *testing.B) {
	data := map[string]interface{}{"key": "value"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ExtractNestedValue(data, "key")
	}
}

func BenchmarkExtractNestedValue_Nested(b *testing.B) {
	data := map[string]interface{}{
		"labels": map[string]interface{}{
			"alertname": "HighCPU",
			"severity":  "critical",
		},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ExtractNestedValue(data, "labels.alertname")
	}
}

func BenchmarkExtractNestedValue_DeeplyNested(b *testing.B) {
	data := map[string]interface{}{
		"a": map[string]interface{}{
			"b": map[string]interface{}{
				"c": map[string]interface{}{
					"d": "deep",
				},
			},
		},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ExtractNestedValue(data, "a.b.c.d")
	}
}

func BenchmarkExtractString(b *testing.B) {
	data := map[string]interface{}{
		"labels": map[string]interface{}{
			"alertname": "HighCPU",
		},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ExtractString(data, "labels.alertname")
	}
}

func BenchmarkMergeMappings(b *testing.B) {
	defaults := database.JSONB{
		"severity":  "labels.severity",
		"alertname": "labels.alertname",
		"instance":  "labels.instance",
	}
	overrides := database.JSONB{
		"severity": "custom.severity",
		"host":     "custom.host",
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		MergeMappings(defaults, overrides)
	}
}

func BenchmarkNormalizeSeverity_DirectMatch(b *testing.B) {
	for i := 0; i < b.N; i++ {
		NormalizeSeverity("critical", nil)
	}
}

func BenchmarkNormalizeSeverity_WithMapping(b *testing.B) {
	for i := 0; i < b.N; i++ {
		NormalizeSeverity("disaster", DefaultSeverityMapping)
	}
}

func BenchmarkNormalizeStatus(b *testing.B) {
	statuses := []string{"firing", "resolved", "alerting", "ok", "unknown"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		NormalizeStatus(statuses[i%len(statuses)])
	}
}
