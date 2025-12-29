package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/akmatori/akmatori/internal/database"
)

// TitleGenerator generates concise titles for incidents using LLM
type TitleGenerator struct {
	httpClient *http.Client
}

// NewTitleGenerator creates a new title generator
func NewTitleGenerator() *TitleGenerator {
	return &TitleGenerator{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// OpenAI API request/response structures
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

// GenerateTitle generates a concise title for an incident based on the incoming message/alert
func (t *TitleGenerator) GenerateTitle(messageOrAlert string, source string) (string, error) {
	// For very short messages (< 10 chars), just use the message as-is or fallback
	messageOrAlert = strings.TrimSpace(messageOrAlert)
	if len(messageOrAlert) < 10 {
		return t.GenerateFallbackTitle(messageOrAlert, source), nil
	}

	// Get OpenAI settings from database
	settings, err := database.GetOpenAISettings()
	if err != nil {
		return "", fmt.Errorf("failed to get OpenAI settings: %w", err)
	}

	if !settings.IsActive() {
		// If OpenAI is not configured, generate a simple title from the message
		return t.GenerateFallbackTitle(messageOrAlert, source), nil
	}

	// Build the prompt
	systemPrompt := `You are a concise title generator. Create a short title (max 80 characters) that accurately summarizes the given message.

IMPORTANT RULES:
- ONLY use information present in the message - do NOT invent or assume details
- If the message is vague or unclear, create a generic title like "User inquiry" or "General request"
- Do NOT make up technical issues, error types, or problems that aren't mentioned
- Keep it factual and based solely on what's written
- Do not start with "Alert:" or "Incident:"
- Use sentence case

Respond with ONLY the title, nothing else.`

	userPrompt := fmt.Sprintf("Source: %s\n\nMessage:\n%s", source, truncateForPrompt(messageOrAlert, 2000))

	// Build request
	reqBody := openAIRequest{
		Model: "gpt-4o-mini", // Use a fast, cheap model for title generation
		Messages: []openAIMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		MaxTokens:   50,
		Temperature: 0.3,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	// Make API request
	req, err := http.NewRequest("POST", "https://api.openai.com/v1/chat/completions", bytes.NewBuffer(jsonBody))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+settings.APIKey)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		log.Printf("OpenAI API request failed, using fallback title: %v", err)
		return t.GenerateFallbackTitle(messageOrAlert, source), nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Failed to read OpenAI response, using fallback title: %v", err)
		return t.GenerateFallbackTitle(messageOrAlert, source), nil
	}

	var openAIResp openAIResponse
	if err := json.Unmarshal(body, &openAIResp); err != nil {
		log.Printf("Failed to parse OpenAI response, using fallback title: %v", err)
		return t.GenerateFallbackTitle(messageOrAlert, source), nil
	}

	if openAIResp.Error != nil {
		log.Printf("OpenAI API error: %s, using fallback title", openAIResp.Error.Message)
		return t.GenerateFallbackTitle(messageOrAlert, source), nil
	}

	if len(openAIResp.Choices) == 0 {
		log.Printf("No choices in OpenAI response, using fallback title")
		return t.GenerateFallbackTitle(messageOrAlert, source), nil
	}

	title := strings.TrimSpace(openAIResp.Choices[0].Message.Content)
	// Remove quotes if the LLM wrapped the title in them
	title = strings.Trim(title, "\"'")

	// Ensure title is not too long
	if len(title) > 255 {
		title = title[:252] + "..."
	}

	return title, nil
}

// GenerateFallbackTitle creates a simple title when LLM is not available
func (t *TitleGenerator) GenerateFallbackTitle(message string, source string) string {
	// Clean up the message
	message = strings.TrimSpace(message)

	// Remove common prefixes
	message = strings.TrimPrefix(message, "Alert:")
	message = strings.TrimPrefix(message, "alert:")
	message = strings.TrimPrefix(message, "Incident:")
	message = strings.TrimPrefix(message, "incident:")
	message = strings.TrimSpace(message)

	// Take first line only
	if idx := strings.Index(message, "\n"); idx > 0 {
		message = message[:idx]
	}

	// Truncate to reasonable length
	if len(message) > 80 {
		// Try to cut at word boundary
		if idx := strings.LastIndex(message[:80], " "); idx > 40 {
			message = message[:idx] + "..."
		} else {
			message = message[:77] + "..."
		}
	}

	if message == "" {
		return fmt.Sprintf("Incident from %s", source)
	}

	return message
}

// truncateForPrompt truncates a string to fit in the prompt
func truncateForPrompt(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
