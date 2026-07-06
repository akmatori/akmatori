package handlers

import (
	"net/http"
	"strings"

	"github.com/akmatori/akmatori/internal/executor"
	"github.com/akmatori/akmatori/internal/services"
	slackutil "github.com/akmatori/akmatori/internal/slack"
)

// APIHandler handles API endpoints for the UI and skill communication
type APIHandler struct {
	skillService         services.SkillIncidentManager
	toolService          services.ToolManager
	contextService       services.ContextManager
	alertService         services.AlertManager
	agentExecutor        *executor.Executor
	agentWSHandler       *AgentWSHandler
	slackManager         *slackutil.Manager
	runbookService       services.RunbookManager
	memoryService        services.MemoryManager
	httpConnectorService services.HTTPConnectorManager
	mcpServerService     services.MCPServerManager
	channelService       services.ChannelManager
	providerRegistry     services.ProviderRegistry
	cronService          services.CronJobManager
	proposalService      services.ProposalManager
	responseFormatter    *services.ResponseFormatter
	alertChannelReloader func()       // called after alert source create/update/delete to reload Slack channel mappings
	gatewayReloader      func() error // called after HTTP connector CRUD to reload gateway tools
	mcpServerReloader    func() error // called after MCP server CRUD to reload gateway MCP proxy tools
}

// NewAPIHandler creates a new API handler
func NewAPIHandler(skillService services.SkillIncidentManager, toolService services.ToolManager, contextService services.ContextManager, alertService services.AlertManager, agentExecutor *executor.Executor, agentWSHandler *AgentWSHandler, slackManager *slackutil.Manager, runbookService services.RunbookManager, memoryService services.MemoryManager, httpConnectorService services.HTTPConnectorManager, mcpServerService services.MCPServerManager) *APIHandler {
	return &APIHandler{
		skillService:         skillService,
		toolService:          toolService,
		contextService:       contextService,
		alertService:         alertService,
		agentExecutor:        agentExecutor,
		agentWSHandler:       agentWSHandler,
		slackManager:         slackManager,
		runbookService:       runbookService,
		memoryService:        memoryService,
		httpConnectorService: httpConnectorService,
		mcpServerService:     mcpServerService,
	}
}

// SetAlertChannelReloader sets the callback invoked after alert source create/update/delete
// to reload Slack channel mappings at runtime.
func (h *APIHandler) SetAlertChannelReloader(fn func()) {
	h.alertChannelReloader = fn
}

// SetResponseFormatter wires the ResponseFormatter used to apply the
// configured global formatting prompt to the agent's final response before
// it is persisted. Optional — when unset (or formatting is disabled),
// the raw agent response flows through unchanged.
func (h *APIHandler) SetResponseFormatter(f *services.ResponseFormatter) {
	h.responseFormatter = f
}

// SetGatewayReloader sets the callback invoked after HTTP connector create/update/delete
// to reload MCP Gateway tool registrations.
func (h *APIHandler) SetGatewayReloader(fn func() error) {
	h.gatewayReloader = fn
}

// SetMCPServerReloader sets the callback invoked after MCP server create/update/delete
// to reload MCP Gateway proxy tool registrations.
func (h *APIHandler) SetMCPServerReloader(fn func() error) {
	h.mcpServerReloader = fn
}

// SetChannelManager wires the ChannelManager used by /api/integrations and
// /api/channels. Optional; routes return 503 when unset so the API still
// boots without the new infrastructure (graceful degradation per CLAUDE.md).
func (h *APIHandler) SetChannelManager(svc services.ChannelManager) {
	h.channelService = svc
}

// SetProviderRegistry wires the messaging provider registry used to validate
// integration provider names at create time. Optional; when unset the handler
// falls back to the database.IsValidMessagingProvider whitelist so the model
// constants remain the source of truth.
func (h *APIHandler) SetProviderRegistry(reg services.ProviderRegistry) {
	h.providerRegistry = reg
}

// SetCronJobManager wires the CronJobManager that backs /api/cron-jobs.
// Optional — when unset the cron endpoints return 503 so the rest of the API
// boots without the scheduler (per CLAUDE.md graceful-degradation rule).
func (h *APIHandler) SetCronJobManager(svc services.CronJobManager) {
	h.cronService = svc
}

// SetProposalService wires the ProposalManager that backs /api/proposals.
// Optional — when unset the proposal endpoints return 503 so the rest of the
// API boots without the self-improvement loop (graceful degradation).
func (h *APIHandler) SetProposalService(svc services.ProposalManager) {
	h.proposalService = svc
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

	// Incidents — exact-method prefix routes resolve before the wildcard catch-all.
	mux.HandleFunc("/api/incidents", h.handleIncidents)
	mux.HandleFunc("GET /api/incidents/{uuid}/alerts", h.handleIncidentAlerts)
	mux.HandleFunc("GET /api/incidents/{uuid}", h.handleIncidentByID)
	mux.HandleFunc("POST /api/incidents/{uuid}/close", h.handleIncidentClose)

	// Alert management: unlink spawns a fresh investigation; move reassigns the
	// alert to a chosen incident (empty target == unlink); resolve manually
	// marks a firing alert resolved.
	mux.HandleFunc("POST /api/alerts/{uuid}/unlink", h.handleAlertUnlink)
	mux.HandleFunc("POST /api/alerts/{uuid}/move", h.handleAlertMove)
	mux.HandleFunc("POST /api/alerts/{uuid}/resolve", h.handleAlertResolve)

	// Unified events feed (alerts + non-alert incidents merged by occurred_at).
	mux.HandleFunc("GET /api/events", h.handleEvents)
	mux.HandleFunc("GET /api/events/raw", h.handleEventRaw)

	// Slack settings (removed; returns 410 Gone — use /api/integrations and
	// /api/channels). Route kept so clients on the old endpoint see a clear
	// error instead of a generic 404.
	mux.HandleFunc("/api/settings/slack", h.handleSlackSettings)

	// Messaging integrations (provider configurations) and Channels
	mux.HandleFunc("/api/integrations", h.handleIntegrations)
	mux.HandleFunc("/api/integrations/", h.handleIntegrationByUUID)
	mux.HandleFunc("/api/channels", h.handleChannels)
	mux.HandleFunc("/api/channels/", h.handleChannelByUUID)

	// Cron jobs (scheduled LLM or agent runs that post to a Channel)
	mux.HandleFunc("/api/cron-jobs", h.handleCronJobs)
	mux.HandleFunc("/api/cron-jobs/", h.handleCronJobByUUID)

	// LLM settings
	mux.HandleFunc("/api/settings/llm", h.handleLLMSettings)
	mux.HandleFunc("/api/settings/llm/", h.handleLLMSettingsByID)

	// General settings
	mux.HandleFunc("/api/settings/general", h.handleGeneralSettings)

	// Proxy settings
	mux.HandleFunc("/api/settings/proxy", h.handleProxySettings)

	// Retention settings
	mux.HandleFunc("/api/settings/retention", h.handleRetentionSettings)

	// Formatting settings
	mux.HandleFunc("/api/settings/formatting", h.handleFormattingSettings)

	// Context files
	mux.HandleFunc("/api/context", h.handleContext)
	mux.HandleFunc("/api/context/", h.handleContextByID)
	mux.HandleFunc("/api/context/validate", h.handleContextValidate)

	// Runbooks
	mux.HandleFunc("/api/runbooks", h.handleRunbooks)
	mux.HandleFunc("/api/runbooks/", h.handleRunbookByID)

	// Cross-incident memory
	mux.HandleFunc("/api/memories", h.handleMemories)
	mux.HandleFunc("/api/memories/scopes", h.handleMemoryScopes)
	mux.HandleFunc("/api/memories/", h.handleMemoryByID)
	mux.HandleFunc("POST /api/incidents/{uuid}/feedback", h.handleIncidentFeedback)

	// Self-improvement proposals (generated by the improvement-evaluator cron,
	// reviewed/refined/approved by operators)
	mux.HandleFunc("/api/proposals", h.handleProposals)
	mux.HandleFunc("GET /api/proposals/count", h.handleProposalsCount)
	mux.HandleFunc("GET /api/proposals/{uuid}", h.handleProposalByUUID)
	mux.HandleFunc("POST /api/proposals/{uuid}/approve", h.handleProposalApprove)
	mux.HandleFunc("POST /api/proposals/{uuid}/reject", h.handleProposalReject)
	mux.HandleFunc("GET /api/proposals/{uuid}/chat", h.handleProposalChatGet)
	mux.HandleFunc("POST /api/proposals/{uuid}/chat", h.handleProposalChatPost)

	// HTTP connectors
	mux.HandleFunc("/api/http-connectors", h.handleHTTPConnectors)
	mux.HandleFunc("/api/http-connectors/", h.handleHTTPConnectorByID)

	// MCP servers (admin-only)
	mux.HandleFunc("/api/mcp-servers", h.handleMCPServers)
	mux.HandleFunc("/api/mcp-servers/", h.handleMCPServerByID)

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
