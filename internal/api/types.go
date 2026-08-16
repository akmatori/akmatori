package api

import (
	"time"

	"github.com/akmatori/akmatori/internal/database"
)

// ========== Skill Types ==========

// CreateSkillRequest is the request body for POST /api/skills.
type CreateSkillRequest struct {
	Name        string `json:"name" validate:"required,min=1,max=64"`
	Description string `json:"description" validate:"omitempty,max=1024"`
	Category    string `json:"category" validate:"omitempty,max=64"`
	Prompt      string `json:"prompt"`
}

// UpdateSkillToolsRequest is the request body for PUT /api/skills/:name/tools.
type UpdateSkillToolsRequest struct {
	ToolInstanceIDs []uint `json:"tool_instance_ids"`
}

// UpdateSkillPromptRequest is the request body for PUT /api/skills/:name/prompt.
type UpdateSkillPromptRequest struct {
	Prompt string `json:"prompt"`
}

// UpdateScriptRequest is the request body for PUT /api/skills/:name/scripts/:filename.
type UpdateScriptRequest struct {
	Content string `json:"content"`
}

// SkillResponse is a skill with its prompt included.
type SkillResponse struct {
	database.Skill
	Prompt string `json:"prompt"`
}

// ========== Tool Types ==========

// CreateToolInstanceRequest is the request body for POST /api/tools.
type CreateToolInstanceRequest struct {
	ToolTypeID  uint           `json:"tool_type_id" validate:"required"`
	Name        string         `json:"name" validate:"required,min=1"`
	LogicalName string         `json:"logical_name"` // Optional; auto-derived from Name if empty
	Settings    database.JSONB `json:"settings"`
}

// UpdateToolInstanceRequest is the request body for PUT /api/tools/:id.
type UpdateToolInstanceRequest struct {
	Name        string         `json:"name"`
	LogicalName string         `json:"logical_name"` // Optional; re-derived from Name if empty
	Settings    database.JSONB `json:"settings"`
	Enabled     bool           `json:"enabled"`
}

// CreateSSHKeyRequest is the request body for POST /api/tools/:id/ssh-keys.
type CreateSSHKeyRequest struct {
	Name       string `json:"name"`
	PrivateKey string `json:"private_key"`
	IsDefault  bool   `json:"is_default"`
}

// UpdateSSHKeyRequest is the request body for PUT /api/tools/:id/ssh-keys/:keyID.
type UpdateSSHKeyRequest struct {
	Name      *string `json:"name"`
	IsDefault *bool   `json:"is_default"`
}

// ========== Incident Types ==========

// CreateIncidentRequest is the request body for POST /api/incidents.
type CreateIncidentRequest struct {
	Task    string                 `json:"task" validate:"required"`
	Context map[string]interface{} `json:"context,omitempty"`
}

// CreateIncidentResponse is the response body for POST /api/incidents.
type CreateIncidentResponse struct {
	UUID       string `json:"uuid"`
	Status     string `json:"status"`
	WorkingDir string `json:"working_dir"`
	Message    string `json:"message"`
}

// ========== Settings Types ==========

// CreateLLMSettingsRequest is the request body for POST /api/settings/llm.
// The sampling fields are optional; omitting one leaves it unset, meaning the
// request carries no such parameter and the provider default applies.
type CreateLLMSettingsRequest struct {
	Provider      string   `json:"provider" validate:"required"`
	Name          string   `json:"name" validate:"required,min=1,max=100"`
	APIKey        string   `json:"api_key"`
	Model         string   `json:"model"`
	ThinkingLevel string   `json:"thinking_level"`
	BaseURL       string   `json:"base_url"`
	Temperature   *float64 `json:"temperature"`
	TopP          *float64 `json:"top_p"`
	TopK          *int     `json:"top_k"`
	MaxTokens     *int     `json:"max_tokens"`
}

// UpdateLLMSettingsRequest is the request body for PUT /api/settings/llm/{id}.
// Sampling fields use Nullable so an explicit null clears an override back to
// the provider default, while omitting the key leaves the stored value alone.
type UpdateLLMSettingsRequest struct {
	Name          *string           `json:"name"`
	APIKey        *string           `json:"api_key"`
	Model         *string           `json:"model"`
	ThinkingLevel *string           `json:"thinking_level"`
	BaseURL       *string           `json:"base_url"`
	Temperature   Nullable[float64] `json:"temperature"`
	TopP          Nullable[float64] `json:"top_p"`
	TopK          Nullable[int]     `json:"top_k"`
	MaxTokens     Nullable[int]     `json:"max_tokens"`
}

// UpdateProxySettingsRequest is the request body for PUT /api/settings/proxy.
type UpdateProxySettingsRequest struct {
	ProxyURL string `json:"proxy_url"`
	NoProxy  string `json:"no_proxy"`
	Services struct {
		LLM struct {
			Enabled bool `json:"enabled"`
		} `json:"llm"`
		Slack struct {
			Enabled bool `json:"enabled"`
		} `json:"slack"`
		Zabbix struct {
			Enabled bool `json:"enabled"`
		} `json:"zabbix"`
		VictoriaMetrics struct {
			Enabled bool `json:"enabled"`
		} `json:"victoria_metrics"`
		Catchpoint struct {
			Enabled bool `json:"enabled"`
		} `json:"catchpoint"`
		Grafana struct {
			Enabled bool `json:"enabled"`
		} `json:"grafana"`
		PagerDuty struct {
			Enabled bool `json:"enabled"`
		} `json:"pagerduty"`
		NetBox struct {
			Enabled bool `json:"enabled"`
		} `json:"netbox"`
		Kubernetes struct {
			Enabled bool `json:"enabled"`
		} `json:"kubernetes"`
		Jira struct {
			Enabled bool `json:"enabled"`
		} `json:"jira"`
	} `json:"services"`
}

// UpdateGeneralSettingsRequest is the request body for PUT /api/settings/general.
type UpdateGeneralSettingsRequest struct {
	BaseURL                  *string `json:"base_url"`
	AlertCorrelationEnabled  *bool   `json:"alert_correlation_enabled"`
	AlertMonitorWindowMinutes *int   `json:"alert_monitor_window_minutes"`
	IncidentMergeEnabled     *bool   `json:"incident_merge_enabled"`
	IncidentAutoCloseEnabled *bool   `json:"incident_auto_close_enabled"`
	IncidentAutoCloseMinutes *int    `json:"incident_auto_close_minutes"`
}

// UpdateRetentionSettingsRequest is the request body for PUT /api/settings/retention.
type UpdateRetentionSettingsRequest struct {
	Enabled              *bool `json:"enabled"`
	RetentionDays        *int  `json:"retention_days"`
	CleanupIntervalHours *int  `json:"cleanup_interval_hours"`
}

// CreateFormattingRuleRequest is the request body for POST /api/formatting-rules.
// Match fields are wildcards when empty; omitted enabled defaults to true and
// omitted max_tokens/temperature default to 1500/0.2.
type CreateFormattingRuleRequest struct {
	Name                string   `json:"name"`
	Enabled             *bool    `json:"enabled"`
	MatchSourceKind     string   `json:"match_source_kind"`
	MatchSourceUUID     string   `json:"match_source_uuid"`
	MatchChannelUUID    string   `json:"match_channel_uuid"`
	MatchLastSkill      string   `json:"match_last_skill"`
	MatchExpression     string   `json:"match_expression"`
	SystemPrompt        string   `json:"system_prompt"`
	OutputSchemaExample string   `json:"output_schema_example"`
	MaxTokens           *int     `json:"max_tokens"`
	Temperature         *float64 `json:"temperature"`
}

// UpdateFormattingRuleRequest is the request body for PUT
// /api/formatting-rules/{uuid}. All fields are optional; match fields accept
// "" to clear a condition back to wildcard.
type UpdateFormattingRuleRequest struct {
	Name                *string  `json:"name"`
	Enabled             *bool    `json:"enabled"`
	MatchSourceKind     *string  `json:"match_source_kind"`
	MatchSourceUUID     *string  `json:"match_source_uuid"`
	MatchChannelUUID    *string  `json:"match_channel_uuid"`
	MatchLastSkill      *string  `json:"match_last_skill"`
	MatchExpression     *string  `json:"match_expression"`
	SystemPrompt        *string  `json:"system_prompt"`
	OutputSchemaExample *string  `json:"output_schema_example"`
	MaxTokens           *int     `json:"max_tokens"`
	Temperature         *float64 `json:"temperature"`
}

// ReorderFormattingRulesRequest is the request body for PUT
// /api/formatting-rules/reorder. UUIDs must enumerate every existing rule
// exactly once, in the desired evaluation order.
type ReorderFormattingRulesRequest struct {
	UUIDs []string `json:"uuids"`
}

// ========== Alert Source Types ==========

// CreateAlertSourceRequest is the request body for POST /api/alert-sources.
// NotificationChannelUUID is optional; when set, the alert source routes
// outbound posts to the referenced Channel instead of the provider default.
type CreateAlertSourceRequest struct {
	SourceTypeName          string         `json:"source_type_name" validate:"required"`
	Name                    string         `json:"name" validate:"required,min=1"`
	Description             string         `json:"description"`
	WebhookSecret           string         `json:"webhook_secret"`
	FieldMappings           database.JSONB `json:"field_mappings"`
	Settings                database.JSONB `json:"settings"`
	NotificationChannelUUID *string        `json:"notification_channel_uuid"`
}

// UpdateAlertSourceRequest is the request body for PUT /api/alert-sources/:uuid.
// NotificationChannelUUID is a tri-state: omitted = no change, empty string or
// JSON null = clear the existing routing override (revert to default), non-empty
// = set to that Channel UUID.
type UpdateAlertSourceRequest struct {
	Name                    *string         `json:"name"`
	Description             *string         `json:"description"`
	WebhookSecret           *string         `json:"webhook_secret"`
	FieldMappings           *database.JSONB `json:"field_mappings"`
	Settings                *database.JSONB `json:"settings"`
	Enabled                 *bool           `json:"enabled"`
	NotificationChannelUUID *string         `json:"notification_channel_uuid"`
}

// ========== Context Types ==========

// ValidateReferencesRequest is the request body for POST /api/context/validate.
type ValidateReferencesRequest struct {
	Text string `json:"text"`
}

// ========== Pagination Types ==========

// PaginationMeta contains pagination metadata for list responses.
type PaginationMeta struct {
	Page       int   `json:"page"`
	PerPage    int   `json:"per_page"`
	Total      int64 `json:"total"`
	TotalPages int   `json:"total_pages"`
}

// PaginatedResponse wraps a list response with pagination metadata.
type PaginatedResponse struct {
	Data       interface{}    `json:"data"`
	Pagination PaginationMeta `json:"pagination"`
}

// ========== Mapper Output Types ==========

// IncidentListItem is a compact representation of an incident for list views.
// It omits large fields like FullLog to reduce response size.
type IncidentListItem struct {
	ID              uint                    `json:"id"`
	UUID            string                  `json:"uuid"`
	Source          string                  `json:"source"`
	SourceID        string                  `json:"source_id"`
	Title           string                  `json:"title"`
	Status          database.IncidentStatus `json:"status"`
	TokensUsed      int                     `json:"tokens_used"`
	ExecutionTimeMs int64                   `json:"execution_time_ms"`
	StartedAt       time.Time               `json:"started_at"`
	CompletedAt     *time.Time              `json:"completed_at,omitempty"`
	CreatedAt       time.Time               `json:"created_at"`
	UpdatedAt       time.Time               `json:"updated_at"`
}
