package extraction

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
)

// mockHTTPClient implements HTTPDoer for testing
type mockHTTPClient struct {
	response *http.Response
	err      error
	requests []*http.Request
}

func (m *mockHTTPClient) Do(req *http.Request) (*http.Response, error) {
	m.requests = append(m.requests, req)
	return m.response, m.err
}

// Helper to create mock OpenAI response
func createMockOpenAIResponse(content string) *http.Response {
	body := openAIResponse{
		ID: "test-id",
		Choices: []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		}{
			{
				Message: struct {
					Content string `json:"content"`
				}{Content: content},
				FinishReason: "stop",
			},
		},
	}
	jsonBody, _ := json.Marshal(body)
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(bytes.NewReader(jsonBody)),
	}
}

// Helper to create mock error response
func createMockErrorResponse(errMsg string) *http.Response {
	body := openAIResponse{
		Error: &struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		}{
			Message: errMsg,
			Type:    "api_error",
		},
	}
	jsonBody, _ := json.Marshal(body)
	return &http.Response{
		StatusCode: 400,
		Body:       io.NopCloser(bytes.NewReader(jsonBody)),
	}
}

func TestExtract_LLMSettingsError(t *testing.T) {
	mockClient := &mockHTTPClient{}
	settingsErr := errors.New("database error")

	extractor := NewAlertExtractorWithDeps(mockClient, func() (*database.LLMSettings, error) {
		return nil, settingsErr
	})

	alert, err := extractor.Extract(context.Background(), "Test alert message")

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if alert == nil {
		t.Fatal("Expected fallback alert, got nil")
	}
	if alert.AlertName != "Test alert message" {
		t.Errorf("Expected fallback alert name, got %q", alert.AlertName)
	}
	if alert.RawPayload["extraction_mode"] != "fallback" {
		t.Error("Expected extraction_mode to be fallback")
	}
}

func TestExtract_NoAPIKey(t *testing.T) {
	mockClient := &mockHTTPClient{}

	extractor := NewAlertExtractorWithDeps(mockClient, func() (*database.LLMSettings, error) {
		return &database.LLMSettings{
			APIKey:   "",
			Provider: database.LLMProviderOpenAI,
		}, nil
	})

	alert, err := extractor.Extract(context.Background(), "Test alert message")

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if alert.RawPayload["extraction_mode"] != "fallback" {
		t.Error("Expected fallback when no API key")
	}
	// HTTP client should not have been called
	if len(mockClient.requests) != 0 {
		t.Errorf("Expected no HTTP requests, got %d", len(mockClient.requests))
	}
}

func TestExtract_NonOpenAIProvider(t *testing.T) {
	mockClient := &mockHTTPClient{}

	extractor := NewAlertExtractorWithDeps(mockClient, func() (*database.LLMSettings, error) {
		return &database.LLMSettings{
			APIKey:   "test-key",
			Provider: database.LLMProviderAnthropic,
		}, nil
	})

	alert, err := extractor.Extract(context.Background(), "Test alert")

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if alert.RawPayload["extraction_mode"] != "fallback" {
		t.Error("Expected fallback for non-OpenAI provider")
	}
	if len(mockClient.requests) != 0 {
		t.Errorf("Expected no HTTP requests for non-OpenAI provider")
	}
}

func TestExtract_HTTPRequestError(t *testing.T) {
	mockClient := &mockHTTPClient{
		err: errors.New("network error"),
	}

	extractor := NewAlertExtractorWithDeps(mockClient, func() (*database.LLMSettings, error) {
		return &database.LLMSettings{
			APIKey:   "test-key",
			Provider: database.LLMProviderOpenAI,
		}, nil
	})

	alert, err := extractor.Extract(context.Background(), "Test alert")

	if err != nil {
		t.Fatalf("Expected no error (fallback), got %v", err)
	}
	if alert.RawPayload["extraction_mode"] != "fallback" {
		t.Error("Expected fallback on HTTP error")
	}
}

func TestExtract_APIError(t *testing.T) {
	mockClient := &mockHTTPClient{
		response: createMockErrorResponse("Rate limit exceeded"),
	}

	extractor := NewAlertExtractorWithDeps(mockClient, func() (*database.LLMSettings, error) {
		return &database.LLMSettings{
			APIKey:   "test-key",
			Provider: database.LLMProviderOpenAI,
		}, nil
	})

	alert, err := extractor.Extract(context.Background(), "Test alert")

	if err != nil {
		t.Fatalf("Expected no error (fallback), got %v", err)
	}
	if alert.RawPayload["extraction_mode"] != "fallback" {
		t.Error("Expected fallback on API error")
	}
}

func TestExtract_EmptyChoices(t *testing.T) {
	emptyResponse := openAIResponse{
		ID: "test-id",
		Choices: []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		}{},
	}
	jsonBody, _ := json.Marshal(emptyResponse)

	mockClient := &mockHTTPClient{
		response: &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewReader(jsonBody)),
		},
	}

	extractor := NewAlertExtractorWithDeps(mockClient, func() (*database.LLMSettings, error) {
		return &database.LLMSettings{
			APIKey:   "test-key",
			Provider: database.LLMProviderOpenAI,
		}, nil
	})

	alert, err := extractor.Extract(context.Background(), "Test alert")

	if err != nil {
		t.Fatalf("Expected no error (fallback), got %v", err)
	}
	if alert.RawPayload["extraction_mode"] != "fallback" {
		t.Error("Expected fallback on empty choices")
	}
}

func TestExtract_InvalidJSON(t *testing.T) {
	mockClient := &mockHTTPClient{
		response: createMockOpenAIResponse("this is not json"),
	}

	extractor := NewAlertExtractorWithDeps(mockClient, func() (*database.LLMSettings, error) {
		return &database.LLMSettings{
			APIKey:   "test-key",
			Provider: database.LLMProviderOpenAI,
		}, nil
	})

	alert, err := extractor.Extract(context.Background(), "Test alert")

	if err != nil {
		t.Fatalf("Expected no error (fallback), got %v", err)
	}
	if alert.RawPayload["extraction_mode"] != "fallback" {
		t.Error("Expected fallback on invalid JSON response")
	}
}

func TestExtract_SuccessfulExtraction(t *testing.T) {
	extractedJSON := `{
		"alert_name": "High CPU Usage",
		"severity": "critical",
		"status": "firing",
		"summary": "CPU at 95%",
		"description": "Production server experiencing high CPU usage",
		"target_host": "prod-web-01",
		"target_service": "web-api",
		"source_system": "Prometheus"
	}`

	mockClient := &mockHTTPClient{
		response: createMockOpenAIResponse(extractedJSON),
	}

	extractor := NewAlertExtractorWithDeps(mockClient, func() (*database.LLMSettings, error) {
		return &database.LLMSettings{
			APIKey:   "test-key",
			Provider: database.LLMProviderOpenAI,
		}, nil
	})

	alert, err := extractor.Extract(context.Background(), "CPU is at 95% on prod-web-01")

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if alert.AlertName != "High CPU Usage" {
		t.Errorf("AlertName = %q, want %q", alert.AlertName, "High CPU Usage")
	}
	if alert.Severity != database.AlertSeverityCritical {
		t.Errorf("Severity = %v, want %v", alert.Severity, database.AlertSeverityCritical)
	}
	if alert.Status != database.AlertStatusFiring {
		t.Errorf("Status = %v, want %v", alert.Status, database.AlertStatusFiring)
	}
	if alert.TargetHost != "prod-web-01" {
		t.Errorf("TargetHost = %q, want %q", alert.TargetHost, "prod-web-01")
	}
	if alert.TargetService != "web-api" {
		t.Errorf("TargetService = %q, want %q", alert.TargetService, "web-api")
	}
}

func TestExtract_JSONWithCodeBlock(t *testing.T) {
	// OpenAI sometimes wraps JSON in markdown code blocks
	extractedJSON := "```json\n" + `{
		"alert_name": "Memory Alert",
		"severity": "warning",
		"status": "firing"
	}` + "\n```"

	mockClient := &mockHTTPClient{
		response: createMockOpenAIResponse(extractedJSON),
	}

	extractor := NewAlertExtractorWithDeps(mockClient, func() (*database.LLMSettings, error) {
		return &database.LLMSettings{
			APIKey:   "test-key",
			Provider: database.LLMProviderOpenAI,
		}, nil
	})

	alert, err := extractor.Extract(context.Background(), "Memory is high")

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if alert.AlertName != "Memory Alert" {
		t.Errorf("AlertName = %q, want %q", alert.AlertName, "Memory Alert")
	}
}

func TestExtract_ResolvedStatus(t *testing.T) {
	extractedJSON := `{
		"alert_name": "Issue Resolved",
		"severity": "info",
		"status": "resolved"
	}`

	mockClient := &mockHTTPClient{
		response: createMockOpenAIResponse(extractedJSON),
	}

	extractor := NewAlertExtractorWithDeps(mockClient, func() (*database.LLMSettings, error) {
		return &database.LLMSettings{
			APIKey:   "test-key",
			Provider: database.LLMProviderOpenAI,
		}, nil
	})

	alert, err := extractor.Extract(context.Background(), "Issue is now resolved")

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if alert.Status != database.AlertStatusResolved {
		t.Errorf("Status = %v, want %v", alert.Status, database.AlertStatusResolved)
	}
}

func TestExtractWithPrompt_CustomPrompt(t *testing.T) {
	extractedJSON := `{
		"alert_name": "Custom Alert",
		"severity": "high"
	}`

	mockClient := &mockHTTPClient{
		response: createMockOpenAIResponse(extractedJSON),
	}

	extractor := NewAlertExtractorWithDeps(mockClient, func() (*database.LLMSettings, error) {
		return &database.LLMSettings{
			APIKey:   "test-key",
			Provider: database.LLMProviderOpenAI,
		}, nil
	})

	customPrompt := "Extract with custom instructions: %s"
	alert, err := extractor.ExtractWithPrompt(context.Background(), "Test message", customPrompt)

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if alert.AlertName != "Custom Alert" {
		t.Errorf("AlertName = %q, want %q", alert.AlertName, "Custom Alert")
	}

	// Verify the custom prompt was used
	if len(mockClient.requests) != 1 {
		t.Fatalf("Expected 1 request, got %d", len(mockClient.requests))
	}
	body, _ := io.ReadAll(mockClient.requests[0].Body)
	if !strings.Contains(string(body), "custom instructions") {
		t.Error("Expected custom prompt to be used in request")
	}
}

func TestExtract_RequestHeaders(t *testing.T) {
	mockClient := &mockHTTPClient{
		response: createMockOpenAIResponse(`{"alert_name": "Test"}`),
	}

	extractor := NewAlertExtractorWithDeps(mockClient, func() (*database.LLMSettings, error) {
		return &database.LLMSettings{
			APIKey:   "sk-test-key-12345",
			Provider: database.LLMProviderOpenAI,
		}, nil
	})

	_, _ = extractor.Extract(context.Background(), "Test")

	if len(mockClient.requests) != 1 {
		t.Fatalf("Expected 1 request, got %d", len(mockClient.requests))
	}

	req := mockClient.requests[0]

	// Check Content-Type
	if req.Header.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q, want %q", req.Header.Get("Content-Type"), "application/json")
	}

	// Check Authorization header
	authHeader := req.Header.Get("Authorization")
	if authHeader != "Bearer sk-test-key-12345" {
		t.Errorf("Authorization = %q, want %q", authHeader, "Bearer sk-test-key-12345")
	}

	// Check URL
	if req.URL.String() != "https://api.openai.com/v1/chat/completions" {
		t.Errorf("URL = %q, want OpenAI chat completions endpoint", req.URL.String())
	}
}

func TestExtract_LongMessageTruncation(t *testing.T) {
	longMessage := strings.Repeat("x", 5000)

	mockClient := &mockHTTPClient{
		response: createMockOpenAIResponse(`{"alert_name": "Long Test"}`),
	}

	extractor := NewAlertExtractorWithDeps(mockClient, func() (*database.LLMSettings, error) {
		return &database.LLMSettings{
			APIKey:   "test-key",
			Provider: database.LLMProviderOpenAI,
		}, nil
	})

	_, _ = extractor.Extract(context.Background(), longMessage)

	if len(mockClient.requests) != 1 {
		t.Fatalf("Expected 1 request, got %d", len(mockClient.requests))
	}

	// Message should be truncated to 3000 chars
	body, _ := io.ReadAll(mockClient.requests[0].Body)
	// The 5000 char message should be truncated, not appear in full
	if strings.Count(string(body), "x") >= 5000 {
		t.Error("Expected long message to be truncated")
	}
}

func TestExtract_EmptyProviderDefaultsToOpenAI(t *testing.T) {
	extractedJSON := `{"alert_name": "Empty Provider Test"}`

	mockClient := &mockHTTPClient{
		response: createMockOpenAIResponse(extractedJSON),
	}

	extractor := NewAlertExtractorWithDeps(mockClient, func() (*database.LLMSettings, error) {
		return &database.LLMSettings{
			APIKey:   "test-key",
			Provider: "", // Empty provider should be treated as OpenAI
		}, nil
	})

	alert, err := extractor.Extract(context.Background(), "Test message")

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if alert.AlertName != "Empty Provider Test" {
		t.Errorf("AlertName = %q, want %q", alert.AlertName, "Empty Provider Test")
	}
	// HTTP request should have been made
	if len(mockClient.requests) != 1 {
		t.Errorf("Expected HTTP request for empty provider (OpenAI default)")
	}
}

func TestExtract_MalformedHTTPResponse(t *testing.T) {
	mockClient := &mockHTTPClient{
		response: &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewReader([]byte("not json at all"))),
		},
	}

	extractor := NewAlertExtractorWithDeps(mockClient, func() (*database.LLMSettings, error) {
		return &database.LLMSettings{
			APIKey:   "test-key",
			Provider: database.LLMProviderOpenAI,
		}, nil
	})

	alert, err := extractor.Extract(context.Background(), "Test")

	if err != nil {
		t.Fatalf("Expected no error (fallback), got %v", err)
	}
	if alert.RawPayload["extraction_mode"] != "fallback" {
		t.Error("Expected fallback on malformed HTTP response")
	}
}

// TestExtract_ContextCancellation tests that context cancellation is respected
func TestExtract_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	mockClient := &mockHTTPClient{
		err: context.Canceled,
	}

	extractor := NewAlertExtractorWithDeps(mockClient, func() (*database.LLMSettings, error) {
		return &database.LLMSettings{
			APIKey:   "test-key",
			Provider: database.LLMProviderOpenAI,
		}, nil
	})

	alert, err := extractor.Extract(ctx, "Test")

	// Should return fallback, not propagate error
	if err != nil {
		t.Fatalf("Expected no error (fallback), got %v", err)
	}
	if alert.RawPayload["extraction_mode"] != "fallback" {
		t.Error("Expected fallback on context cancellation")
	}
}

// Benchmark for Extract with mocked dependencies
func BenchmarkExtract_WithMock(b *testing.B) {
	extractedJSON := `{
		"alert_name": "Benchmark Alert",
		"severity": "warning",
		"status": "firing",
		"summary": "Test summary"
	}`

	mockClient := &mockHTTPClient{
		response: createMockOpenAIResponse(extractedJSON),
	}

	extractor := NewAlertExtractorWithDeps(mockClient, func() (*database.LLMSettings, error) {
		return &database.LLMSettings{
			APIKey:   "test-key",
			Provider: database.LLMProviderOpenAI,
		}, nil
	})

	ctx := context.Background()
	msg := "Production server CPU at 95%"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Reset the mock for each iteration (recreate response body)
		mockClient.response = createMockOpenAIResponse(extractedJSON)
		mockClient.requests = nil
		_, _ = extractor.Extract(ctx, msg)
	}
}
