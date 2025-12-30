package database

import (
	"encoding/json"
	"testing"
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

func TestOpenAISettings_IsConfigured(t *testing.T) {
	tests := []struct {
		name     string
		settings OpenAISettings
		expected bool
	}{
		{
			name:     "no API key",
			settings: OpenAISettings{},
			expected: false,
		},
		{
			name: "with API key",
			settings: OpenAISettings{
				APIKey: "sk-test",
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

func TestOpenAISettings_GetValidReasoningEfforts(t *testing.T) {
	tests := []struct {
		model    string
		expected []string
	}{
		{"gpt-5.1-codex-max", []string{"low", "medium", "high", "extra_high"}},
		{"gpt-5.1-codex", []string{"low", "medium", "high"}},
		{"gpt-5.1-codex-mini", []string{"medium", "high"}},
		{"gpt-5.1", []string{"low", "medium", "high"}},
		{"unknown-model", []string{"medium"}},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			settings := OpenAISettings{Model: tt.model}
			result := settings.GetValidReasoningEfforts()

			if len(result) != len(tt.expected) {
				t.Errorf("GetValidReasoningEfforts() = %v, want %v", result, tt.expected)
				return
			}

			for i, v := range result {
				if v != tt.expected[i] {
					t.Errorf("GetValidReasoningEfforts()[%d] = %s, want %s", i, v, tt.expected[i])
				}
			}
		})
	}
}

func TestOpenAISettings_ValidateReasoningEffort(t *testing.T) {
	tests := []struct {
		name     string
		model    string
		effort   string
		expected bool
	}{
		{"valid medium for codex", "gpt-5.1-codex", "medium", true},
		{"valid high for codex", "gpt-5.1-codex", "high", true},
		{"invalid extra_high for codex", "gpt-5.1-codex", "extra_high", false},
		{"valid extra_high for max", "gpt-5.1-codex-max", "extra_high", true},
		{"invalid low for mini", "gpt-5.1-codex-mini", "low", false},
		{"valid medium for mini", "gpt-5.1-codex-mini", "medium", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := OpenAISettings{
				Model:                tt.model,
				ModelReasoningEffort: tt.effort,
			}
			result := settings.ValidateReasoningEffort()
			if result != tt.expected {
				t.Errorf("ValidateReasoningEffort() = %v, want %v", result, tt.expected)
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
		{OpenAISettings{}, "openai_settings"},
		{ContextFile{}, "context_files"},
		{APIKeySettings{}, "api_key_settings"},
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
	if IncidentStatusPending != "pending" {
		t.Error("IncidentStatusPending should be 'pending'")
	}
	if IncidentStatusRunning != "running" {
		t.Error("IncidentStatusRunning should be 'running'")
	}
	if IncidentStatusCompleted != "completed" {
		t.Error("IncidentStatusCompleted should be 'completed'")
	}
	if IncidentStatusFailed != "failed" {
		t.Error("IncidentStatusFailed should be 'failed'")
	}
}

func TestEventSourceType_Constants(t *testing.T) {
	if EventSourceTypeSlack != "slack" {
		t.Error("EventSourceTypeSlack should be 'slack'")
	}
	if EventSourceTypeZabbix != "zabbix" {
		t.Error("EventSourceTypeZabbix should be 'zabbix'")
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
