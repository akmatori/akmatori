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
	ToolTypeID uint           `json:"tool_type_id" validate:"required"`
	Name       string         `json:"name" validate:"required,min=1"`
	Settings   database.JSONB `json:"settings"`
}

// UpdateToolInstanceRequest is the request body for PUT /api/tools/:id.
type UpdateToolInstanceRequest struct {
	Name     string         `json:"name"`
	Settings database.JSONB `json:"settings"`
	Enabled  bool           `json:"enabled"`
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

// MergeIncidentRequest is the request body for POST /api/incidents/:uuid/merge.
type MergeIncidentRequest struct {
	SourceIncidentUUID string `json:"source_incident_uuid" validate:"required"`
}

// ========== Settings Types ==========

// UpdateSlackSettingsRequest is the request body for PUT /api/settings/slack.
type UpdateSlackSettingsRequest struct {
	BotToken      *string `json:"bot_token"`
	SigningSecret *string `json:"signing_secret"`
	AppToken      *string `json:"app_token"`
	AlertsChannel *string `json:"alerts_channel"`
	Enabled       *bool   `json:"enabled"`
}

// UpdateLLMSettingsRequest is the request body for PUT /api/settings/llm.
type UpdateLLMSettingsRequest struct {
	Provider      *string `json:"provider"`
	APIKey        *string `json:"api_key"`
	Model         *string `json:"model"`
	ThinkingLevel *string `json:"thinking_level"`
	BaseURL       *string `json:"base_url"`
}

// UpdateProxySettingsRequest is the request body for PUT /api/settings/proxy.
type UpdateProxySettingsRequest struct {
	ProxyURL string `json:"proxy_url"`
	NoProxy  string `json:"no_proxy"`
	Services struct {
		OpenAI struct {
			Enabled bool `json:"enabled"`
		} `json:"openai"`
		Slack struct {
			Enabled bool `json:"enabled"`
		} `json:"slack"`
		Zabbix struct {
			Enabled bool `json:"enabled"`
		} `json:"zabbix"`
	} `json:"services"`
}

// ========== Alert Source Types ==========

// CreateAlertSourceRequest is the request body for POST /api/alert-sources.
type CreateAlertSourceRequest struct {
	SourceTypeName string         `json:"source_type_name" validate:"required"`
	Name           string         `json:"name" validate:"required,min=1"`
	Description    string         `json:"description"`
	WebhookSecret  string         `json:"webhook_secret"`
	FieldMappings  database.JSONB `json:"field_mappings"`
	Settings       database.JSONB `json:"settings"`
}

// UpdateAlertSourceRequest is the request body for PUT /api/alert-sources/:uuid.
type UpdateAlertSourceRequest struct {
	Name          *string         `json:"name"`
	Description   *string         `json:"description"`
	WebhookSecret *string         `json:"webhook_secret"`
	FieldMappings *database.JSONB `json:"field_mappings"`
	Settings      *database.JSONB `json:"settings"`
	Enabled       *bool           `json:"enabled"`
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
	ID              uint                   `json:"id"`
	UUID            string                 `json:"uuid"`
	Source          string                 `json:"source"`
	SourceID        string                 `json:"source_id"`
	Title           string                 `json:"title"`
	Status          database.IncidentStatus `json:"status"`
	TokensUsed      int                    `json:"tokens_used"`
	ExecutionTimeMs int64                  `json:"execution_time_ms"`
	AlertCount      int                    `json:"alert_count"`
	StartedAt       time.Time              `json:"started_at"`
	CompletedAt     *time.Time             `json:"completed_at,omitempty"`
	CreatedAt       time.Time              `json:"created_at"`
	UpdatedAt       time.Time              `json:"updated_at"`
}
