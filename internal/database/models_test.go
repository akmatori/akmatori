package database

import (
	"encoding/json"
	"testing"
	"time"
)

func TestJSONB_Scan(t *testing.T) {
	tests := []struct {
		name    string
		input   interface{}
		wantErr bool
	}{
		{
			name:    "nil value",
			input:   nil,
			wantErr: false,
		},
		{
			name:    "valid JSON",
			input:   []byte(`{"key": "value"}`),
			wantErr: false,
		},
		{
			name:    "invalid JSON",
			input:   []byte(`not json`),
			wantErr: true,
		},
		{
			name:    "wrong type",
			input:   "string",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var j JSONB
			err := j.Scan(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("Scan() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestJSONB_Value(t *testing.T) {
	tests := []struct {
		name    string
		jsonb   JSONB
		wantNil bool
	}{
		{
			name:    "nil JSONB",
			jsonb:   nil,
			wantNil: true,
		},
		{
			name:    "empty JSONB",
			jsonb:   JSONB{},
			wantNil: false,
		},
		{
			name:    "populated JSONB",
			jsonb:   JSONB{"key": "value"},
			wantNil: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value, err := tt.jsonb.Value()
			if err != nil {
				t.Errorf("Value() error = %v", err)
			}
			if tt.wantNil && value != nil {
				t.Errorf("Value() = %v, want nil", value)
			}
			if !tt.wantNil && value == nil {
				t.Error("Value() = nil, want non-nil")
			}
		})
	}
}

func TestSlackSettings_IsConfigured(t *testing.T) {
	tests := []struct {
		name     string
		settings SlackSettings
		expected bool
	}{
		{
			name:     "all empty",
			settings: SlackSettings{},
			expected: false,
		},
		{
			name: "only bot token",
			settings: SlackSettings{
				BotToken: "xoxb-test",
			},
			expected: false,
		},
		{
			name: "missing app token",
			settings: SlackSettings{
				BotToken:      "xoxb-test",
				SigningSecret: "secret",
			},
			expected: false,
		},
		{
			name: "all configured",
			settings: SlackSettings{
				BotToken:      "xoxb-test",
				SigningSecret: "secret",
				AppToken:      "xapp-test",
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.settings.IsConfigured()
			if result != tt.expected {
				t.Errorf("IsConfigured() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestSlackSettings_IsActive(t *testing.T) {
	tests := []struct {
		name     string
		settings SlackSettings
		expected bool
	}{
		{
			name:     "not configured, not enabled",
			settings: SlackSettings{},
			expected: false,
		},
		{
			name: "configured but not enabled",
			settings: SlackSettings{
				BotToken:      "xoxb-test",
				SigningSecret: "secret",
				AppToken:      "xapp-test",
				Enabled:       false,
			},
			expected: false,
		},
		{
			name: "enabled but not configured",
			settings: SlackSettings{
				BotToken: "xoxb-test",
				Enabled:  true,
			},
			expected: false,
		},
		{
			name: "configured and enabled",
			settings: SlackSettings{
				BotToken:      "xoxb-test",
				SigningSecret: "secret",
				AppToken:      "xapp-test",
				Enabled:       true,
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.settings.IsActive()
			if result != tt.expected {
				t.Errorf("IsActive() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestLLMSettings_IsConfigured(t *testing.T) {
	tests := []struct {
		name     string
		settings LLMSettings
		expected bool
	}{
		{
			name:     "no API key",
			settings: LLMSettings{},
			expected: false,
		},
		{
			name: "with API key, openai provider",
			settings: LLMSettings{
				Provider: LLMProviderOpenAI,
				APIKey:   "sk-test",
			},
			expected: true,
		},
		{
			name: "with API key, anthropic provider",
			settings: LLMSettings{
				Provider: LLMProviderAnthropic,
				APIKey:   "sk-ant-test",
			},
			expected: true,
		},
		{
			name: "with API key, google provider",
			settings: LLMSettings{
				Provider: LLMProviderGoogle,
				APIKey:   "AIza-test",
			},
			expected: true,
		},
		{
			name: "with API key, openrouter provider",
			settings: LLMSettings{
				Provider: LLMProviderOpenRouter,
				APIKey:   "sk-or-test",
			},
			expected: true,
		},
		{
			name: "with API key, custom provider",
			settings: LLMSettings{
				Provider: LLMProviderCustom,
				APIKey:   "custom-key",
				BaseURL:  "https://custom.llm.example.com",
			},
			expected: true,
		},
		{
			name: "empty API key",
			settings: LLMSettings{
				Provider: LLMProviderOpenAI,
				APIKey:   "",
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.settings.IsConfigured()
			if result != tt.expected {
				t.Errorf("IsConfigured() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestLLMSettings_IsActive(t *testing.T) {
	tests := []struct {
		name     string
		settings LLMSettings
		expected bool
	}{
		{
			name:     "not configured",
			settings: LLMSettings{},
			expected: false,
		},
		{
			name: "configured but not enabled",
			settings: LLMSettings{
				Provider: LLMProviderAnthropic,
				APIKey:   "sk-ant-test",
				Enabled:  false,
			},
			expected: false,
		},
		{
			name: "enabled and configured",
			settings: LLMSettings{
				Provider: LLMProviderAnthropic,
				APIKey:   "sk-ant-test",
				Enabled:  true,
			},
			expected: true,
		},
		{
			name: "enabled but not configured",
			settings: LLMSettings{
				Provider: LLMProviderAnthropic,
				Enabled:  true,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.settings.IsActive()
			if result != tt.expected {
				t.Errorf("IsActive() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestLLMProvider_Constants(t *testing.T) {
	tests := []struct {
		provider LLMProvider
		expected string
	}{
		{LLMProviderOpenAI, "openai"},
		{LLMProviderAnthropic, "anthropic"},
		{LLMProviderGoogle, "google"},
		{LLMProviderOpenRouter, "openrouter"},
		{LLMProviderCustom, "custom"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if string(tt.provider) != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, string(tt.provider))
			}
		})
	}
}

func TestIsValidLLMProvider(t *testing.T) {
	tests := []struct {
		provider string
		expected bool
	}{
		{"openai", true},
		{"anthropic", true},
		{"google", true},
		{"openrouter", true},
		{"custom", true},
		{"invalid", false},
		{"", false},
		{"OpenAI", false},
	}

	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			result := IsValidLLMProvider(tt.provider)
			if result != tt.expected {
				t.Errorf("IsValidLLMProvider(%q) = %v, want %v", tt.provider, result, tt.expected)
			}
		})
	}
}

func TestThinkingLevel_Constants(t *testing.T) {
	tests := []struct {
		level    ThinkingLevel
		expected string
	}{
		{ThinkingLevelOff, "off"},
		{ThinkingLevelMinimal, "minimal"},
		{ThinkingLevelLow, "low"},
		{ThinkingLevelMedium, "medium"},
		{ThinkingLevelHigh, "high"},
		{ThinkingLevelXHigh, "xhigh"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if string(tt.level) != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, string(tt.level))
			}
		})
	}
}

func TestIsValidThinkingLevel(t *testing.T) {
	tests := []struct {
		level    string
		expected bool
	}{
		{"off", true},
		{"minimal", true},
		{"low", true},
		{"medium", true},
		{"high", true},
		{"xhigh", true},
		{"invalid", false},
		{"", false},
		{"extra_high", false},
	}

	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			result := IsValidThinkingLevel(tt.level)
			if result != tt.expected {
				t.Errorf("IsValidThinkingLevel(%q) = %v, want %v", tt.level, result, tt.expected)
			}
		})
	}
}

func TestValidLLMProviders(t *testing.T) {
	providers := ValidLLMProviders()
	if len(providers) != 5 {
		t.Errorf("expected 5 providers, got %d", len(providers))
	}
}

func TestValidThinkingLevels(t *testing.T) {
	levels := ValidThinkingLevels()
	if len(levels) != 6 {
		t.Errorf("expected 6 thinking levels, got %d", len(levels))
	}
}

func TestLLMSettings_JSONSerialization(t *testing.T) {
	settings := LLMSettings{
		ID:            1,
		Provider:      LLMProviderAnthropic,
		APIKey:        "sk-ant-test",
		Model:         "claude-opus-4-6",
		ThinkingLevel: ThinkingLevelHigh,
		BaseURL:       "",
		Enabled:       true,
	}

	data, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("Failed to marshal LLMSettings: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if result["provider"] != "anthropic" {
		t.Errorf("expected provider 'anthropic', got %v", result["provider"])
	}
	if result["api_key"] != "sk-ant-test" {
		t.Errorf("expected api_key 'sk-ant-test', got %v", result["api_key"])
	}
	if result["model"] != "claude-opus-4-6" {
		t.Errorf("expected model 'claude-opus-4-6', got %v", result["model"])
	}
	if result["thinking_level"] != "high" {
		t.Errorf("expected thinking_level 'high', got %v", result["thinking_level"])
	}
	if result["enabled"] != true {
		t.Errorf("expected enabled true, got %v", result["enabled"])
	}
}

func TestLLMSettings_MultiProviderConfigs(t *testing.T) {
	tests := []struct {
		name     string
		settings LLMSettings
	}{
		{
			name: "openai with gpt-4o",
			settings: LLMSettings{
				Provider:      LLMProviderOpenAI,
				APIKey:        "sk-openai-key",
				Model:         "gpt-4o",
				ThinkingLevel: ThinkingLevelMedium,
				Enabled:       true,
			},
		},
		{
			name: "anthropic with claude",
			settings: LLMSettings{
				Provider:      LLMProviderAnthropic,
				APIKey:        "sk-ant-key",
				Model:         "claude-opus-4-6",
				ThinkingLevel: ThinkingLevelHigh,
				Enabled:       true,
			},
		},
		{
			name: "google with gemini",
			settings: LLMSettings{
				Provider:      LLMProviderGoogle,
				APIKey:        "AIza-key",
				Model:         "gemini-2.5-pro",
				ThinkingLevel: ThinkingLevelLow,
				Enabled:       true,
			},
		},
		{
			name: "openrouter with custom model",
			settings: LLMSettings{
				Provider:      LLMProviderOpenRouter,
				APIKey:        "sk-or-key",
				Model:         "anthropic/claude-opus-4-6",
				ThinkingLevel: ThinkingLevelMedium,
				BaseURL:       "https://openrouter.ai/api/v1",
				Enabled:       true,
			},
		},
		{
			name: "custom provider with base url",
			settings: LLMSettings{
				Provider:      LLMProviderCustom,
				APIKey:        "custom-key",
				Model:         "local-model",
				ThinkingLevel: ThinkingLevelOff,
				BaseURL:       "http://localhost:8080/v1",
				Enabled:       true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.settings.IsConfigured() {
				t.Error("expected IsConfigured() = true for valid settings")
			}
			if !tt.settings.IsActive() {
				t.Error("expected IsActive() = true for valid settings")
			}
			if !IsValidLLMProvider(string(tt.settings.Provider)) {
				t.Errorf("expected valid provider: %s", tt.settings.Provider)
			}
			if !IsValidThinkingLevel(string(tt.settings.ThinkingLevel)) {
				t.Errorf("expected valid thinking level: %s", tt.settings.ThinkingLevel)
			}
		})
	}
}

func TestAPIKeySettings_GetActiveKeys(t *testing.T) {
	tests := []struct {
		name     string
		keys     JSONB
		expected []string
	}{
		{
			name:     "nil keys",
			keys:     nil,
			expected: []string{},
		},
		{
			name:     "empty keys",
			keys:     JSONB{},
			expected: []string{},
		},
		{
			name: "single enabled key",
			keys: JSONB{
				"keys": []interface{}{
					map[string]interface{}{
						"key":     "key1",
						"enabled": true,
					},
				},
			},
			expected: []string{"key1"},
		},
		{
			name: "mixed enabled and disabled",
			keys: JSONB{
				"keys": []interface{}{
					map[string]interface{}{"key": "key1", "enabled": true},
					map[string]interface{}{"key": "key2", "enabled": false},
					map[string]interface{}{"key": "key3", "enabled": true},
				},
			},
			expected: []string{"key1", "key3"},
		},
		{
			name: "all disabled",
			keys: JSONB{
				"keys": []interface{}{
					map[string]interface{}{"key": "key1", "enabled": false},
				},
			},
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := APIKeySettings{Keys: tt.keys}
			result := settings.GetActiveKeys()

			if len(result) != len(tt.expected) {
				t.Errorf("GetActiveKeys() = %v, want %v", result, tt.expected)
				return
			}

			for i, key := range result {
				if key != tt.expected[i] {
					t.Errorf("GetActiveKeys()[%d] = %s, want %s", i, key, tt.expected[i])
				}
			}
		})
	}
}

func TestAPIKeySettings_IsActive(t *testing.T) {
	tests := []struct {
		name     string
		enabled  bool
		keys     JSONB
		expected bool
	}{
		{
			name:     "disabled with no keys",
			enabled:  false,
			keys:     nil,
			expected: false,
		},
		{
			name:    "enabled with no keys",
			enabled: true,
			keys:    nil,
			expected: false,
		},
		{
			name:    "disabled with keys",
			enabled: false,
			keys: JSONB{
				"keys": []interface{}{
					map[string]interface{}{"key": "key1", "enabled": true},
				},
			},
			expected: false,
		},
		{
			name:    "enabled with active keys",
			enabled: true,
			keys: JSONB{
				"keys": []interface{}{
					map[string]interface{}{"key": "key1", "enabled": true},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := APIKeySettings{
				Enabled: tt.enabled,
				Keys:    tt.keys,
			}
			result := settings.IsActive()
			if result != tt.expected {
				t.Errorf("IsActive() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestTableNames(t *testing.T) {
	tests := []struct {
		model     interface{ TableName() string }
		tableName string
	}{
		{Skill{}, "skills"},
		{ToolType{}, "tool_types"},
		{ToolInstance{}, "tool_instances"},
		{SkillTool{}, "skill_tools"},
		{EventSource{}, "event_sources"},
		{Incident{}, "incidents"},
		{SlackSettings{}, "slack_settings"},
		{LLMSettings{}, "llm_settings"},
		{ContextFile{}, "context_files"},
		{APIKeySettings{}, "api_key_settings"},
		{IncidentAlert{}, "incident_alerts"},
		{IncidentMerge{}, "incident_merges"},
		{AggregationSettings{}, "aggregation_settings"},
	}

	for _, tt := range tests {
		t.Run(tt.tableName, func(t *testing.T) {
			result := tt.model.TableName()
			if result != tt.tableName {
				t.Errorf("TableName() = %s, want %s", result, tt.tableName)
			}
		})
	}
}

func TestIncidentStatus_Constants(t *testing.T) {
	tests := []struct {
		status   IncidentStatus
		expected string
	}{
		{IncidentStatusPending, "pending"},
		{IncidentStatusRunning, "running"},
		{IncidentStatusDiagnosed, "diagnosed"},
		{IncidentStatusObserving, "observing"},
		{IncidentStatusCompleted, "completed"},
		{IncidentStatusFailed, "failed"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if string(tt.status) != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, string(tt.status))
			}
		})
	}
}

func TestEventSourceType_Constants(t *testing.T) {
	if EventSourceTypeSlack != "slack" {
		t.Error("EventSourceTypeSlack should be 'slack'")
	}
	if EventSourceTypeWebhook != "webhook" {
		t.Error("EventSourceTypeWebhook should be 'webhook'")
	}
}

func TestJSONB_RoundTrip(t *testing.T) {
	original := JSONB{
		"string": "value",
		"number": float64(42),
		"bool":   true,
		"nested": map[string]interface{}{
			"key": "nested_value",
		},
	}

	// Marshal to JSON
	bytes, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	// Scan back
	var result JSONB
	if err := result.Scan(bytes); err != nil {
		t.Fatalf("Failed to scan: %v", err)
	}

	// Verify
	if result["string"] != "value" {
		t.Error("string field mismatch")
	}
	if result["number"] != float64(42) {
		t.Error("number field mismatch")
	}
	if result["bool"] != true {
		t.Error("bool field mismatch")
	}
}

func TestIncident_AggregationFields(t *testing.T) {
	now := time.Now()
	incident := Incident{
		UUID:                     "test-uuid",
		AlertCount:               5,
		LastAlertAt:              &now,
		ObservingStartedAt:       &now,
		ObservingDurationMinutes: 30,
	}

	if incident.AlertCount != 5 {
		t.Errorf("expected AlertCount 5, got %d", incident.AlertCount)
	}
	if incident.LastAlertAt == nil {
		t.Error("expected LastAlertAt to be set")
	}
	if incident.ObservingStartedAt == nil {
		t.Error("expected ObservingStartedAt to be set")
	}
	if incident.ObservingDurationMinutes != 30 {
		t.Errorf("expected ObservingDurationMinutes 30, got %d", incident.ObservingDurationMinutes)
	}
}
