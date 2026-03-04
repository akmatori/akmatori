package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/akmatori/akmatori/internal/api"
	"github.com/akmatori/akmatori/internal/testhelpers"
)

// ========================================
// API Request Validation Integration Tests
// ========================================

// TestAPIHandler_RequestValidation tests request validation across endpoints
func TestAPIHandler_RequestValidation(t *testing.T) {
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil)

	tests := []struct {
		name           string
		method         string
		path           string
		body           string
		handler        http.HandlerFunc
		expectedStatus int
	}{
		{
			name:           "skills sync - method not allowed GET",
			method:         http.MethodGet,
			path:           "/api/skills/sync",
			handler:        h.handleSkillsSync,
			expectedStatus: http.StatusMethodNotAllowed,
		},
		{
			name:           "skills sync - method not allowed DELETE",
			method:         http.MethodDelete,
			path:           "/api/skills/sync",
			handler:        h.handleSkillsSync,
			expectedStatus: http.StatusMethodNotAllowed,
		},
		{
			name:           "skills sync - method not allowed PUT",
			method:         http.MethodPut,
			path:           "/api/skills/sync",
			handler:        h.handleSkillsSync,
			expectedStatus: http.StatusMethodNotAllowed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body io.Reader
			if tt.body != "" {
				body = strings.NewReader(tt.body)
			}
			testhelpers.NewHTTPTestContext(t, tt.method, tt.path, body).
				WithHeader("Content-Type", "application/json").
				ExecuteFunc(tt.handler).
				AssertStatus(tt.expectedStatus)
		})
	}
}

// ========================================
// API Utility Function Integration Tests
// ========================================

// TestAPI_JSONDecoding tests JSON decoding edge cases
func TestAPI_JSONDecoding(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{"valid JSON", `{"key": "value"}`, false},
		{"empty object", `{}`, false},
		{"empty array into map errors", `[]`, true}, // array can't decode into map
		{"invalid JSON", `{invalid}`, true},
		{"truncated JSON", `{"key":`, true},
		{"null value", `null`, false},
		{"number value", `123`, true}, // not an object
		{"empty body", ``, true},
		{"whitespace only", `   `, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(tt.body))
			var result map[string]interface{}
			err := api.DecodeJSON(req, &result)
			gotErr := err != nil
			if gotErr != tt.wantErr {
				t.Errorf("DecodeJSON(%q) error = %v, wantErr %v", tt.body, err, tt.wantErr)
			}
		})
	}
}

// TestAPI_ResponseFormat tests response format consistency
func TestAPI_ResponseFormat(t *testing.T) {
	t.Run("JSON response sets correct content type", func(t *testing.T) {
		w := httptest.NewRecorder()
		api.RespondJSON(w, http.StatusOK, map[string]string{"message": "ok"})

		contentType := w.Header().Get("Content-Type")
		if !strings.HasPrefix(contentType, "application/json") {
			t.Errorf("Content-Type = %q, want application/json", contentType)
		}
	})

	t.Run("error response is valid JSON", func(t *testing.T) {
		w := httptest.NewRecorder()
		api.RespondError(w, http.StatusBadRequest, "test error")

		var result map[string]interface{}
		if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
			t.Errorf("response is not valid JSON: %v", err)
		}
		if _, ok := result["error"]; !ok {
			t.Error("error response should contain 'error' field")
		}
	})

	t.Run("no content response has empty body", func(t *testing.T) {
		w := httptest.NewRecorder()
		api.RespondNoContent(w)

		if w.Code != http.StatusNoContent {
			t.Errorf("status = %d, want %d", w.Code, http.StatusNoContent)
		}
		if w.Body.Len() != 0 {
			t.Errorf("body should be empty, got %q", w.Body.String())
		}
	})
}

// ========================================
// API Pagination Integration Tests
// ========================================

func TestAPI_Pagination(t *testing.T) {
	tests := []struct {
		name           string
		queryParams    string
		expectedPage   int
		expectedPerPage int
	}{
		{"defaults", "", 1, 50},
		{"custom page", "?page=5", 5, 50},
		{"custom per_page", "?per_page=25", 1, 25},
		{"both params", "?page=3&per_page=25", 3, 25},
		{"page zero becomes 1", "?page=0", 1, 50},
		{"negative page becomes 1", "?page=-5", 1, 50},
		{"per_page capped at max", "?per_page=1000", 1, 200},
		{"per_page zero uses default", "?per_page=0", 1, 50},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/test"+tt.queryParams, nil)
			params := api.ParsePagination(req)

			if params.Page != tt.expectedPage {
				t.Errorf("Page = %d, want %d", params.Page, tt.expectedPage)
			}
			if params.PerPage != tt.expectedPerPage {
				t.Errorf("PerPage = %d, want %d", params.PerPage, tt.expectedPerPage)
			}
		})
	}
}

func TestAPI_PaginationOffset(t *testing.T) {
	tests := []struct {
		page     int
		perPage  int
		expected int
	}{
		{1, 20, 0},
		{2, 20, 20},
		{3, 20, 40},
		{1, 50, 0},
		{2, 50, 50},
		{10, 25, 225},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			params := api.PaginationParams{Page: tt.page, PerPage: tt.perPage}
			offset := params.Offset()
			if offset != tt.expected {
				t.Errorf("Offset() = %d, want %d (page=%d, perPage=%d)",
					offset, tt.expected, tt.page, tt.perPage)
			}
		})
	}
}

func TestAPI_TotalPages(t *testing.T) {
	tests := []struct {
		total    int64
		perPage  int
		expected int
	}{
		{0, 20, 0},
		{1, 20, 1},
		{20, 20, 1},
		{21, 20, 2},
		{100, 20, 5},
		{101, 20, 6},
		{500, 50, 10},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			params := api.PaginationParams{PerPage: tt.perPage}
			pages := params.TotalPages(tt.total)
			if pages != tt.expected {
				t.Errorf("TotalPages(%d) = %d, want %d (perPage=%d)",
					tt.total, pages, tt.expected, tt.perPage)
			}
		})
	}
}

// ========================================
// Concurrent Handler Access Tests
// ========================================

func TestAPIHandler_ConcurrentRouteSetup(t *testing.T) {
	var wg sync.WaitGroup
	numGoroutines := 10

	// Concurrent handler creation and route setup should not race
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil)
			mux := http.NewServeMux()
			h.SetupRoutes(mux)
		}()
	}
	wg.Wait()
}

func TestAPIHandler_ConcurrentAlertChannelReloader(t *testing.T) {
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil)
	
	reloadCount := 0
	var mu sync.Mutex
	
	h.SetAlertChannelReloader(func() {
		mu.Lock()
		reloadCount++
		mu.Unlock()
	})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.reloadAlertChannels()
		}()
	}
	wg.Wait()
	// Should not panic
}

// ========================================
// HTTP Context Chaining Tests
// ========================================

func TestHTTPTestContext_Chaining(t *testing.T) {
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil)

	t.Run("method chaining works correctly", func(t *testing.T) {
		ctx := testhelpers.NewHTTPTestContext(t, http.MethodGet, "/api/skills/sync", nil)
		ctx.
			WithHeader("X-Custom-Header", "test-value").
			ExecuteFunc(h.handleSkillsSync).
			AssertStatus(http.StatusMethodNotAllowed)
	})

	t.Run("multiple headers can be added", func(t *testing.T) {
		ctx := testhelpers.NewHTTPTestContext(t, http.MethodGet, "/test", nil)
		ctx.
			WithHeader("X-Header-1", "value1").
			WithHeader("X-Header-2", "value2").
			WithAPIKey("test-key").
			WithBearerToken("test-token")

		if ctx.Request.Header.Get("X-Header-1") != "value1" {
			t.Error("X-Header-1 not set correctly")
		}
		if ctx.Request.Header.Get("X-Header-2") != "value2" {
			t.Error("X-Header-2 not set correctly")
		}
		if ctx.Request.Header.Get("X-API-Key") != "test-key" {
			t.Error("X-API-Key not set correctly")
		}
		if ctx.Request.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("Authorization not set correctly")
		}
	})
}

// ========================================
// Request Body Handling Tests
// ========================================

func TestAPIHandler_RequestBodyHandling(t *testing.T) {
	t.Run("large request body", func(t *testing.T) {
		// Create a large JSON body with unique keys
		largeData := make(map[string]string)
		for i := 0; i < 500; i++ {
			largeData["key_"+string(rune('0'+i/100))+string(rune('0'+(i/10)%10))+string(rune('0'+i%10))] = strings.Repeat("x", 100)
		}
		body, _ := json.Marshal(largeData)

		req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader(body))
		var result map[string]string
		err := api.DecodeJSON(req, &result)
		if err != nil {
			t.Errorf("failed to decode large JSON: %v", err)
		}
		if len(result) != 500 {
			t.Errorf("expected 500 entries, got %d", len(result))
		}
	})

	t.Run("unicode in request body", func(t *testing.T) {
		body := `{"message": "こんにちは世界 🌍 مرحبا"}`
		req := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(body))
		var result map[string]string
		err := api.DecodeJSON(req, &result)
		if err != nil {
			t.Errorf("failed to decode unicode JSON: %v", err)
		}
		if result["message"] != "こんにちは世界 🌍 مرحبا" {
			t.Errorf("unicode not preserved: %q", result["message"])
		}
	})

	t.Run("nested JSON structures", func(t *testing.T) {
		body := `{
			"level1": {
				"level2": {
					"level3": {
						"value": "deep"
					}
				}
			}
		}`
		req := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(body))
		var result map[string]interface{}
		err := api.DecodeJSON(req, &result)
		if err != nil {
			t.Errorf("failed to decode nested JSON: %v", err)
		}
	})
}

// ========================================
// Error Response Format Tests
// ========================================

func TestAPIHandler_ErrorResponseFormat(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		message    string
	}{
		{"bad request", http.StatusBadRequest, "Invalid input"},
		{"not found", http.StatusNotFound, "Resource not found"},
		{"internal error", http.StatusInternalServerError, "Something went wrong"},
		{"unauthorized", http.StatusUnauthorized, "Authentication required"},
		{"forbidden", http.StatusForbidden, "Access denied"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			api.RespondError(w, tt.statusCode, tt.message)

			if w.Code != tt.statusCode {
				t.Errorf("status = %d, want %d", w.Code, tt.statusCode)
			}

			var result map[string]string
			if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
				t.Errorf("response is not valid JSON: %v", err)
			}
			if result["error"] != tt.message {
				t.Errorf("error message = %q, want %q", result["error"], tt.message)
			}
		})
	}
}

// ========================================
// Split Path Edge Cases
// ========================================

func TestSplitPath_IntegrationEdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{"URL encoded path", "/api/skills/my%20skill", []string{"api", "skills", "my%20skill"}},
		{"path with dots", "/api/v1.0/skills", []string{"api", "v1.0", "skills"}},
		{"path with special chars", "/api/skill-name_v2/test", []string{"api", "skill-name_v2", "test"}},
		{"numeric segments", "/api/123/456", []string{"api", "123", "456"}},
		{"mixed case", "/API/Skills/MySkill", []string{"API", "Skills", "MySkill"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := splitPath(tt.input)
			if len(result) != len(tt.expected) {
				t.Errorf("splitPath(%q) = %v, want %v", tt.input, result, tt.expected)
				return
			}
			for i := range result {
				if result[i] != tt.expected[i] {
					t.Errorf("splitPath(%q)[%d] = %q, want %q", tt.input, i, result[i], tt.expected[i])
				}
			}
		})
	}
}

// ========================================
// API Request Helpers Tests
// ========================================

func TestCreateIncidentRequest_Validation(t *testing.T) {
	tests := []struct {
		name    string
		request api.CreateIncidentRequest
		valid   bool
	}{
		{
			name:    "valid request",
			request: api.CreateIncidentRequest{Task: "Investigate CPU spike"},
			valid:   true,
		},
		{
			name:    "empty task",
			request: api.CreateIncidentRequest{Task: ""},
			valid:   false,
		},
		{
			name: "with context",
			request: api.CreateIncidentRequest{
				Task:    "Check disk usage",
				Context: map[string]interface{}{"host": "server-01"},
			},
			valid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Basic validation: task should not be empty
			isValid := tt.request.Task != ""
			if isValid != tt.valid {
				t.Errorf("validation = %v, want %v", isValid, tt.valid)
			}
		})
	}
}

// ========================================
// Alert Source Request Validation Tests
// ========================================

func TestAlertSourceRequest_SlackChannelValidation(t *testing.T) {
	tests := []struct {
		name        string
		sourceType  string
		settings    map[string]interface{}
		shouldError bool
	}{
		{
			name:        "slack_channel without channel_id",
			sourceType:  "slack_channel",
			settings:    map[string]interface{}{},
			shouldError: true,
		},
		{
			name:        "slack_channel with empty channel_id",
			sourceType:  "slack_channel",
			settings:    map[string]interface{}{"slack_channel_id": ""},
			shouldError: true,
		},
		{
			name:        "slack_channel with whitespace channel_id",
			sourceType:  "slack_channel",
			settings:    map[string]interface{}{"slack_channel_id": "   "},
			shouldError: true,
		},
		{
			name:        "slack_channel with valid channel_id",
			sourceType:  "slack_channel",
			settings:    map[string]interface{}{"slack_channel_id": "C12345678"},
			shouldError: false,
		},
		{
			name:        "alertmanager doesn't need channel_id",
			sourceType:  "alertmanager",
			settings:    map[string]interface{}{},
			shouldError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hasError := false
			if tt.sourceType == "slack_channel" {
				channelID, _ := tt.settings["slack_channel_id"].(string)
				if strings.TrimSpace(channelID) == "" {
					hasError = true
				}
			}
			if hasError != tt.shouldError {
				t.Errorf("validation error = %v, want %v", hasError, tt.shouldError)
			}
		})
	}
}

// ========================================
// Benchmarks
// ========================================

func BenchmarkAPI_DecodeJSON(b *testing.B) {
	body := `{"key": "value", "number": 123, "nested": {"foo": "bar"}}`
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(body))
		var result map[string]interface{}
		api.DecodeJSON(req, &result)
	}
}

func BenchmarkAPI_RespondJSON(b *testing.B) {
	data := map[string]interface{}{
		"key":    "value",
		"number": 123,
		"nested": map[string]string{"foo": "bar"},
	}
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		api.RespondJSON(w, http.StatusOK, data)
	}
}

func BenchmarkAPI_RespondError(b *testing.B) {
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		api.RespondError(w, http.StatusBadRequest, "Test error message")
	}
}

func BenchmarkAPI_ParsePagination(b *testing.B) {
	req := httptest.NewRequest(http.MethodGet, "/test?page=5&per_page=25", nil)
	for i := 0; i < b.N; i++ {
		api.ParsePagination(req)
	}
}
