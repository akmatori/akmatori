package victoriametrics

import (
	"encoding/json"
	"io"
	"log"
	"net/url"
	"testing"
)

func TestParsePrometheusResponse_Success(t *testing.T) {
	body := []byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{"__name__":"up"},"value":[1234,"1"]}]}}`)

	data, err := parsePrometheusResponse(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal data: %v", err)
	}

	if parsed["resultType"] != "vector" {
		t.Errorf("expected resultType 'vector', got %v", parsed["resultType"])
	}
}

func TestParsePrometheusResponse_Error(t *testing.T) {
	body := []byte(`{"status":"error","errorType":"bad_data","error":"invalid query"}`)

	_, err := parsePrometheusResponse(body)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	expected := "VictoriaMetrics API error (bad_data): invalid query"
	if err.Error() != expected {
		t.Errorf("expected error %q, got %q", expected, err.Error())
	}
}

func TestParsePrometheusResponse_MalformedJSON(t *testing.T) {
	body := []byte(`not json at all`)

	_, err := parsePrometheusResponse(body)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestExtractInstanceID(t *testing.T) {
	tests := []struct {
		name   string
		args   map[string]interface{}
		wantID *uint
	}{
		{
			name:   "valid instance ID",
			args:   map[string]interface{}{"tool_instance_id": float64(5)},
			wantID: uintPtr(5),
		},
		{
			name:   "zero instance ID",
			args:   map[string]interface{}{"tool_instance_id": float64(0)},
			wantID: nil,
		},
		{
			name:   "negative instance ID",
			args:   map[string]interface{}{"tool_instance_id": float64(-1)},
			wantID: nil,
		},
		{
			name:   "missing instance ID",
			args:   map[string]interface{}{},
			wantID: nil,
		},
		{
			name:   "wrong type",
			args:   map[string]interface{}{"tool_instance_id": "5"},
			wantID: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractInstanceID(tt.args)
			if tt.wantID == nil {
				if got != nil {
					t.Errorf("expected nil, got %d", *got)
				}
			} else {
				if got == nil {
					t.Errorf("expected %d, got nil", *tt.wantID)
				} else if *got != *tt.wantID {
					t.Errorf("expected %d, got %d", *tt.wantID, *got)
				}
			}
		})
	}
}

func TestConfigCacheKey(t *testing.T) {
	key := configCacheKey("incident-123")
	expected := "creds:incident-123:victoria_metrics"
	if key != expected {
		t.Errorf("expected %q, got %q", expected, key)
	}
}

func TestResponseCacheKey(t *testing.T) {
	params1 := url.Values{"query": []string{"up"}}
	params2 := url.Values{"query": []string{"down"}}

	key1 := responseCacheKey("/api/v1/query", params1)
	key2 := responseCacheKey("/api/v1/query", params2)
	key3 := responseCacheKey("/api/v1/query", params1)

	if key1 == key2 {
		t.Error("different params should produce different keys")
	}
	if key1 != key3 {
		t.Error("same params should produce same keys")
	}
}

func TestNewVictoriaMetricsTool(t *testing.T) {
	logger := testLogger()
	tool := NewVictoriaMetricsTool(logger, nil)

	if tool == nil {
		t.Fatal("expected non-nil tool")
	}
	if tool.configCache == nil {
		t.Error("expected non-nil configCache")
	}
	if tool.responseCache == nil {
		t.Error("expected non-nil responseCache")
	}
	if tool.rateLimiter != nil {
		t.Error("expected nil rateLimiter when none provided")
	}

	// Cleanup
	tool.Stop()
}

func TestStop(t *testing.T) {
	logger := testLogger()
	tool := NewVictoriaMetricsTool(logger, nil)
	// Should not panic
	tool.Stop()
	// Double stop should not panic
	tool.Stop()
}

func TestClearCache(t *testing.T) {
	logger := testLogger()
	tool := NewVictoriaMetricsTool(logger, nil)
	defer tool.Stop()

	// Should not panic on empty caches
	tool.ClearCache()
}

func uintPtr(v uint) *uint {
	return &v
}

func testLogger() *log.Logger {
	return log.New(io.Discard, "", 0)
}
