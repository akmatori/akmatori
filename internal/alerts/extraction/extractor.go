package extraction

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/database"
)

// AlertExtractor extracts alert information from free-form text using AI
type AlertExtractor struct {
	httpClient *http.Client
}

// ExtractedAlert represents the structured data extracted from a message
type ExtractedAlert struct {
	AlertName     string `json:"alert_name"`
	Severity      string `json:"severity"`
	Status        string `json:"status"`
	Summary       string `json:"summary"`
	Description   string `json:"description"`
	TargetHost    string `json:"target_host"`
	TargetService string `json:"target_service"`
	SourceSystem  string `json:"source_system"`
}

// NewAlertExtractor creates a new alert extractor
func NewAlertExtractor() *AlertExtractor {
	return &AlertExtractor{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// OpenAI API structures
type openAIRequest struct {
	Model       string          `json:"model"`
	Messages    []openAIMessage `json:"messages"`
	MaxTokens   int             `json:"max_tokens"`
	Temperature float64         `json:"temperature"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIResponse struct {
	ID      string `json:"id"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

const defaultExtractionPrompt = `Extract alert information from this Slack message. Return ONLY valid JSON with these fields:
- alert_name: Brief name/title of the alert (required)
- severity: One of "critical", "high", "warning", "info" (infer from context, default to "warning")
- status: "firing" or "resolved" (default to "firing")
- summary: One-line summary of the issue
- description: Full description if available
- target_host: Affected host/server/IP if mentioned
- target_service: Affected service/application if mentioned
- source_system: Originating monitoring system (e.g., "Prometheus", "Datadog", "Zabbix") if identifiable

Use null for fields that cannot be determined from the message.

Message:
%s`

// Extract extracts alert information from a message using AI
func (e *AlertExtractor) Extract(ctx context.Context, messageText string) (*alerts.NormalizedAlert, error) {
	return e.ExtractWithPrompt(ctx, messageText, "")
}

// ExtractWithPrompt extracts alert information using a custom prompt
func (e *AlertExtractor) ExtractWithPrompt(ctx context.Context, messageText, customPrompt string) (*alerts.NormalizedAlert, error) {
	// Get OpenAI settings from database
	settings, err := database.GetOpenAISettings()
	if err != nil {
		log.Printf("Failed to get OpenAI settings: %v", err)
		return e.createFallbackAlert(messageText), nil
	}

	if !settings.IsActive() {
		log.Printf("OpenAI not configured, using fallback extraction")
		return e.createFallbackAlert(messageText), nil
	}

	// Use custom prompt or default
	prompt := customPrompt
	if prompt == "" {
		prompt = defaultExtractionPrompt
	}

	// Format the prompt with the message
	userPrompt := fmt.Sprintf(prompt, truncateMessage(messageText, 3000))

	// Build request
	reqBody := openAIRequest{
		Model: "gpt-4o-mini", // Use a fast, cheap model for extraction
		Messages: []openAIMessage{
			{Role: "user", Content: userPrompt},
		},
		MaxTokens:   500,
		Temperature: 0.1, // Low temperature for consistent extraction
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		log.Printf("Failed to marshal OpenAI request: %v", err)
		return e.createFallbackAlert(messageText), nil
	}

	// Make API request
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", bytes.NewBuffer(jsonBody))
	if err != nil {
		log.Printf("Failed to create OpenAI request: %v", err)
		return e.createFallbackAlert(messageText), nil
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+settings.APIKey)

	resp, err := e.httpClient.Do(req)
	if err != nil {
		log.Printf("OpenAI API request failed: %v", err)
		return e.createFallbackAlert(messageText), nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Failed to read OpenAI response: %v", err)
		return e.createFallbackAlert(messageText), nil
	}

	var openAIResp openAIResponse
	if err := json.Unmarshal(body, &openAIResp); err != nil {
		log.Printf("Failed to parse OpenAI response: %v", err)
		return e.createFallbackAlert(messageText), nil
	}

	if openAIResp.Error != nil {
		log.Printf("OpenAI API error: %s", openAIResp.Error.Message)
		return e.createFallbackAlert(messageText), nil
	}

	if len(openAIResp.Choices) == 0 {
		log.Printf("No choices in OpenAI response")
		return e.createFallbackAlert(messageText), nil
	}

	// Parse the extracted JSON
	content := strings.TrimSpace(openAIResp.Choices[0].Message.Content)

	// Remove markdown code block if present
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var extracted ExtractedAlert
	if err := json.Unmarshal([]byte(content), &extracted); err != nil {
		log.Printf("Failed to parse extracted alert JSON: %v, content: %s", err, content)
		return e.createFallbackAlert(messageText), nil
	}

	// Convert to NormalizedAlert
	return e.toNormalizedAlert(extracted, messageText), nil
}

// toNormalizedAlert converts ExtractedAlert to NormalizedAlert
func (e *AlertExtractor) toNormalizedAlert(extracted ExtractedAlert, originalMessage string) *alerts.NormalizedAlert {
	// Default alert name if not extracted
	alertName := extracted.AlertName
	if alertName == "" {
		alertName = "Slack Alert"
	}

	// Parse severity
	severity := alerts.NormalizeSeverity(extracted.Severity, alerts.DefaultSeverityMapping)

	// Parse status
	status := database.AlertStatusFiring
	if strings.ToLower(extracted.Status) == "resolved" {
		status = database.AlertStatusResolved
	}

	// Summary fallback
	summary := extracted.Summary
	if summary == "" {
		summary = truncateMessage(originalMessage, 100)
	}

	// Description fallback
	description := extracted.Description
	if description == "" {
		description = originalMessage
	}

	return &alerts.NormalizedAlert{
		AlertName:     alertName,
		Severity:      severity,
		Status:        status,
		Summary:       summary,
		Description:   description,
		TargetHost:    extracted.TargetHost,
		TargetService: extracted.TargetService,
		TargetLabels: map[string]string{
			"source_system": extracted.SourceSystem,
		},
		RawPayload: map[string]interface{}{
			"original_message": originalMessage,
			"extracted":        extracted,
		},
	}
}

// createFallbackAlert creates a basic alert when AI extraction fails
func (e *AlertExtractor) createFallbackAlert(messageText string) *alerts.NormalizedAlert {
	// Try to extract a title from the first line
	alertName := "Slack Alert"
	lines := strings.Split(messageText, "\n")
	if len(lines) > 0 {
		firstLine := strings.TrimSpace(lines[0])
		// Remove common prefixes
		firstLine = strings.TrimPrefix(firstLine, ":alert:")
		firstLine = strings.TrimPrefix(firstLine, ":warning:")
		firstLine = strings.TrimPrefix(firstLine, ":x:")
		firstLine = strings.TrimPrefix(firstLine, ":rotating_light:")
		firstLine = strings.TrimSpace(firstLine)

		if len(firstLine) > 0 && len(firstLine) <= 100 {
			alertName = firstLine
		} else if len(firstLine) > 100 {
			alertName = firstLine[:97] + "..."
		}
	}

	return &alerts.NormalizedAlert{
		AlertName:   alertName,
		Summary:     truncateMessage(messageText, 100),
		Description: messageText,
		Severity:    database.AlertSeverityWarning,
		Status:      database.AlertStatusFiring,
		RawPayload: map[string]interface{}{
			"original_message": messageText,
			"extraction_mode":  "fallback",
		},
	}
}

// truncateMessage truncates a message to a specified length
func truncateMessage(msg string, maxLen int) string {
	if len(msg) <= maxLen {
		return msg
	}
	// Try to cut at word boundary
	truncated := msg[:maxLen-3]
	if idx := strings.LastIndex(truncated, " "); idx > maxLen/2 {
		return truncated[:idx] + "..."
	}
	return truncated + "..."
}
