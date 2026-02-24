package handlers

import (
	"encoding/json"
	"testing"
)

// --- Alert Handler Unit Tests (without database) ---

// Note: HandleWebhook requires alertService to look up instances,
// so we test what we can without a database connection.

// TestAlertHandler_RegisterAdapter_Idempotent tests registering the same adapter twice
func TestAlertHandler_RegisterAdapter_Idempotent(t *testing.T) {
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil, nil)

	adapter := &mockAlertAdapter{sourceType: "prometheus"}

	h.RegisterAdapter(adapter)
	h.RegisterAdapter(adapter)

	// Should still have exactly one adapter registered
	if len(h.adapters) != 1 {
		t.Errorf("expected 1 adapter after double registration, got %d", len(h.adapters))
	}
}

// TestAlertHandler_GetAdapterCount tests adapter counting
func TestAlertHandler_GetAdapterCount(t *testing.T) {
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil, nil)

	if len(h.adapters) != 0 {
		t.Errorf("new handler should have 0 adapters, got %d", len(h.adapters))
	}

	h.RegisterAdapter(&mockAlertAdapter{sourceType: "grafana"})
	h.RegisterAdapter(&mockAlertAdapter{sourceType: "datadog"})

	if len(h.adapters) != 2 {
		t.Errorf("expected 2 adapters, got %d", len(h.adapters))
	}
}

// --- API Handler Integration Tests ---

// TestAPIHandler_Creation tests APIHandler can be created with nil dependencies
func TestAPIHandler_Creation(t *testing.T) {
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil)
	if h == nil {
		t.Fatal("NewAPIHandler returned nil")
	}
}

// --- Path Parameter Extraction Tests ---

func TestExtractPathParam(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		prefix   string
		expected string
	}{
		{
			name:     "simple extraction",
			path:     "/api/skills/my-skill",
			prefix:   "/api/skills/",
			expected: "my-skill",
		},
		{
			name:     "with nested path",
			path:     "/api/skills/my-skill/prompt",
			prefix:   "/api/skills/",
			expected: "my-skill",
		},
		{
			name:     "UUID extraction",
			path:     "/webhook/alert/abc-123-def",
			prefix:   "/webhook/alert/",
			expected: "abc-123-def",
		},
		{
			name:     "trailing slash",
			path:     "/api/skills/my-skill/",
			prefix:   "/api/skills/",
			expected: "my-skill",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractPathParam(tt.path, tt.prefix)
			if result != tt.expected {
				t.Errorf("extractPathParam(%q, %q) = %q, want %q",
					tt.path, tt.prefix, result, tt.expected)
			}
		})
	}
}

// extractPathParam extracts the first path segment after prefix
func extractPathParam(path, prefix string) string {
	if len(path) <= len(prefix) {
		return ""
	}
	rest := path[len(prefix):]
	// Find first / after prefix to get just the param
	for i, c := range rest {
		if c == '/' {
			return rest[:i]
		}
	}
	return rest
}

// --- Concurrent Adapter Registration Tests ---

func TestAlertHandler_ConcurrentAdapterRegistration(t *testing.T) {
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil, nil)

	// Simulate concurrent adapter registrations
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(n int) {
			adapter := &mockAlertAdapter{sourceType: "type-" + string(rune('0'+n))}
			h.RegisterAdapter(adapter)
			done <- true
		}(i)
	}

	// Wait for all registrations
	for i := 0; i < 10; i++ {
		<-done
	}
	// No panic = success
}

// --- Edge Cases ---

func TestAlertHandler_AdapterLookup(t *testing.T) {
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil, nil)

	// Register some adapters
	h.RegisterAdapter(&mockAlertAdapter{sourceType: "grafana"})
	h.RegisterAdapter(&mockAlertAdapter{sourceType: "prometheus"})

	// Test adapter lookup
	if _, ok := h.adapters["grafana"]; !ok {
		t.Error("grafana adapter should be registered")
	}
	if _, ok := h.adapters["prometheus"]; !ok {
		t.Error("prometheus adapter should be registered")
	}
	if _, ok := h.adapters["nonexistent"]; ok {
		t.Error("nonexistent adapter should not be found")
	}
}

func TestAlertHandler_AdapterSourceTypes(t *testing.T) {
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil, nil)

	adapters := []struct {
		sourceType string
	}{
		{"alertmanager"},
		{"grafana"},
		{"datadog"},
		{"pagerduty"},
		{"zabbix"},
	}

	for _, a := range adapters {
		h.RegisterAdapter(&mockAlertAdapter{sourceType: a.sourceType})
	}

	// Verify all adapters are registered
	for _, a := range adapters {
		adapter, ok := h.adapters[a.sourceType]
		if !ok {
			t.Errorf("adapter %q not found", a.sourceType)
			continue
		}
		if adapter.GetSourceType() != a.sourceType {
			t.Errorf("adapter source type = %q, want %q", adapter.GetSourceType(), a.sourceType)
		}
	}
}

// --- JSON Serialization Tests ---

func TestJSONSerialization_AlertResponse(t *testing.T) {
	// Test that alert-related responses can be serialized correctly
	response := map[string]interface{}{
		"status":  "received",
		"count":   5,
		"alerts":  []string{"alert1", "alert2"},
		"message": "Alerts processed successfully",
	}

	data, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("JSON marshal failed: %v", err)
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("JSON unmarshal failed: %v", err)
	}

	if decoded["status"] != "received" {
		t.Errorf("status = %v, want 'received'", decoded["status"])
	}
}

// --- Initialization Tests ---

func TestAlertHandler_InitializationState(t *testing.T) {
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil, nil)

	// New handler should have empty but initialized adapters map
	if h.adapters == nil {
		t.Error("adapters map should be initialized")
	}

	// Should be able to safely check for adapters
	if len(h.adapters) != 0 {
		t.Errorf("new handler should have 0 adapters, got %d", len(h.adapters))
	}
}

func TestAlertHandler_NilDependencies(t *testing.T) {
	// All dependencies can be nil for creation
	h := NewAlertHandler(nil, nil, nil, nil, nil, nil, nil, nil)
	
	if h == nil {
		t.Fatal("NewAlertHandler should not return nil")
	}

	// Should be able to register adapters even with nil dependencies
	h.RegisterAdapter(&mockAlertAdapter{sourceType: "test"})
	
	if len(h.adapters) != 1 {
		t.Error("should be able to register adapter with nil dependencies")
	}
}
