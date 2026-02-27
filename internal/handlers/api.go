package handlers

import (
	"net/http"
	"strings"

	"github.com/akmatori/akmatori/internal/api"
	"github.com/akmatori/akmatori/internal/executor"
	"github.com/akmatori/akmatori/internal/services"
	slackutil "github.com/akmatori/akmatori/internal/slack"
)

// APIHandler handles API endpoints for the UI and skill communication
type APIHandler struct {
	skillService         *services.SkillService
	toolService          *services.ToolService
	contextService       *services.ContextService
	alertService         *services.AlertService
	codexExecutor        *executor.Executor
	agentWSHandler       *AgentWSHandler
	codexWSHandler       *CodexWSHandler
	slackManager         *slackutil.Manager
	deviceAuthService    *services.DeviceAuthService
	alertChannelReloader func() // called after alert source create/update/delete to reload Slack channel mappings
}

// NewAPIHandler creates a new API handler
func NewAPIHandler(skillService *services.SkillService, toolService *services.ToolService, contextService *services.ContextService, alertService *services.AlertService, codexExecutor *executor.Executor, agentWSHandler *AgentWSHandler, slackManager *slackutil.Manager) *APIHandler {
	return &APIHandler{
		skillService:      skillService,
		toolService:       toolService,
		contextService:    contextService,
		alertService:      alertService,
		codexExecutor:     codexExecutor,
		agentWSHandler:    agentWSHandler,
		slackManager:      slackManager,
		deviceAuthService: services.NewDeviceAuthService(),
	}
}

// SetAlertChannelReloader sets the callback invoked after alert source create/update/delete
// to reload Slack channel mappings at runtime.
func (h *APIHandler) SetAlertChannelReloader(fn func()) {
	h.alertChannelReloader = fn
}

// reloadAlertChannels triggers the alert channel reload callback if set
func (h *APIHandler) reloadAlertChannels() {
	if h.alertChannelReloader != nil {
		go h.alertChannelReloader()
	}
}

// SetupRoutes sets up all API routes
func (h *APIHandler) SetupRoutes(mux *http.ServeMux) {
	// Skills management
	mux.HandleFunc("/api/skills", h.handleSkills)
	mux.HandleFunc("/api/skills/", h.handleSkillByName)
	mux.HandleFunc("/api/skills/sync", h.handleSkillsSync)

	// Tool types and instances
	mux.HandleFunc("/api/tool-types", h.handleToolTypes)
	mux.HandleFunc("/api/tools", h.handleTools)
	mux.HandleFunc("/api/tools/", h.handleToolByID)

	// Incidents
	mux.HandleFunc("/api/incidents", h.handleIncidents)
	mux.HandleFunc("/api/incidents/", h.handleIncidentByID)

	// Incident alerts management
	mux.HandleFunc("GET /api/incidents/{uuid}/alerts", h.handleGetIncidentAlerts)
	mux.HandleFunc("POST /api/incidents/{uuid}/alerts", h.handleAttachAlert)
	mux.HandleFunc("DELETE /api/incidents/{uuid}/alerts/{alertId}", h.handleDetachAlert)
	mux.HandleFunc("POST /api/incidents/{uuid}/merge", h.handleMergeIncident)

	// Slack settings
	mux.HandleFunc("/api/settings/slack", h.handleSlackSettings)

	// LLM settings
	mux.HandleFunc("/api/settings/llm", h.handleLLMSettings)

	// Proxy settings
	mux.HandleFunc("/api/settings/proxy", h.handleProxySettings)

	// Aggregation settings
	mux.HandleFunc("GET /api/settings/aggregation", h.handleGetAggregationSettings)
	mux.HandleFunc("PUT /api/settings/aggregation", h.handleUpdateAggregationSettings)

	// Context files
	mux.HandleFunc("/api/context", h.handleContext)
	mux.HandleFunc("/api/context/", h.handleContextByID)
	mux.HandleFunc("/api/context/validate", h.handleContextValidate)

	// Alert source types and instances
	mux.HandleFunc("/api/alert-source-types", h.handleAlertSourceTypes)
	mux.HandleFunc("/api/alert-sources", h.handleAlertSources)
	mux.HandleFunc("/api/alert-sources/", h.handleAlertSourceByUUID)

	// API documentation (public, no auth required)
	mux.HandleFunc("GET /api/docs", h.handleDocs)
	mux.HandleFunc("GET /api/openapi.yaml", h.handleOpenAPISpec)
}

// ========== Utility Functions ==========

// splitPath splits a URL path by slashes
func splitPath(path string) []string {
	result := []string{}
	current := ""
	for _, char := range path {
		if char == '/' {
			if current != "" {
				result = append(result, current)
				current = ""
			}
		} else {
			current += string(char)
		}
	}
	if current != "" {
		result = append(result, current)
	}
	return result
}

// containsString checks if a string contains a substring (helper for error matching)
func containsString(s, substr string) bool {
	return strings.Contains(s, substr)
}

// CreateIncidentRequest is kept for backward compatibility with tests.
// New code should use api.CreateIncidentRequest.
type CreateIncidentRequest = api.CreateIncidentRequest
