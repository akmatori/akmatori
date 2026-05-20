package services

import (
	"io"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/messaging"
)

// SkillManager defines the interface for skill CRUD and lifecycle operations.
type SkillManager interface {
	CreateSkill(name, description, category, prompt string) (*database.Skill, error)
	UpdateSkill(name string, description, category string, enabled bool) (*database.Skill, error)
	DeleteSkill(name string) error
	ListSkills() ([]database.Skill, error)
	ListEnabledSkills() ([]database.Skill, error)
	GetEnabledSkillNames() []string
	GetToolAllowlist() []ToolAllowlistEntry
	GetSkill(name string) (*database.Skill, error)
	AssignTools(skillName string, toolIDs []uint) error
	GetSkillDir(skillName string) string
	GetSkillScriptsDir(skillName string) string
	GetSkillPrompt(skillName string) (string, error)
	UpdateSkillPrompt(skillName string, prompt string) error
	RegenerateSkillMd(skillName string) error
	SyncSkillsFromFilesystem() error
	ListSkillScripts(skillName string) ([]string, error)
	ClearSkillScripts(skillName string) error
	GetSkillScript(skillName, filename string) (*ScriptInfo, error)
	UpdateSkillScript(skillName, filename, content string) error
	DeleteSkillScript(skillName, filename string) error
}

// IncidentManager defines the interface for incident spawn, update, and retrieval.
type IncidentManager interface {
	SpawnIncidentManager(ctx *IncidentContext) (string, string, error)
	SpawnAgentInvocation(rootSkillName string, ctx *IncidentContext) (string, string, error)
	UpdateIncidentStatus(incidentUUID string, status database.IncidentStatus, sessionID string, fullLog string) error
	UpdateIncidentComplete(incidentUUID string, status database.IncidentStatus, sessionID string, fullLog string, response string, tokensUsed int, executionTimeMs int64) error
	UpdateIncidentLog(incidentUUID string, fullLog string) error
	GetIncident(incidentUUID string) (*database.Incident, error)
	AppendSubagentLog(incidentUUID string, skillName string, subagentLog string) error
}

// SkillIncidentManager combines SkillManager and IncidentManager for handlers
// that need both skill lifecycle and incident management (e.g., APIHandler).
type SkillIncidentManager interface {
	SkillManager
	IncidentManager
}

// ToolManager defines the interface for tool instance CRUD and SSH key management.
type ToolManager interface {
	CreateToolInstance(toolTypeID uint, name string, logicalName string, settings database.JSONB) (*database.ToolInstance, error)
	GetToolInstance(id uint) (*database.ToolInstance, error)
	UpdateToolInstance(id uint, name string, logicalName string, settings database.JSONB, enabled bool) error
	DeleteToolInstance(id uint) error
	ListToolTypes() ([]database.ToolType, error)
	ListToolInstances() ([]database.ToolInstance, error)
	EnsureToolTypes() error
	GetSSHKeys(toolInstanceID uint) ([]SSHKeyEntry, error)
	AddSSHKey(toolInstanceID uint, name string, privateKey string, setAsDefault bool) (*SSHKeyEntry, error)
	UpdateSSHKey(toolInstanceID uint, keyID string, name *string, setAsDefault *bool) (*SSHKeyEntry, error)
	DeleteSSHKey(toolInstanceID uint, keyID string) error
}

// AlertManager defines the interface for alert source operations.
type AlertManager interface {
	ListSourceTypes() ([]database.AlertSourceType, error)
	ListAlertSourceTypes() ([]database.AlertSourceType, error)
	GetAlertSourceType(id uint) (*database.AlertSourceType, error)
	GetAlertSourceTypeByName(name string) (*database.AlertSourceType, error)
	CreateAlertSourceType(name, displayName, description string, defaultMappings database.JSONB, webhookSecretHeader string) (*database.AlertSourceType, error)
	EnsureAlertSourceType(name, displayName, description string, defaultMappings database.JSONB, webhookSecretHeader string) (*database.AlertSourceType, error)
	ListInstances() ([]database.AlertSourceInstance, error)
	GetInstance(id uint) (*database.AlertSourceInstance, error)
	GetInstanceByUUID(uuid string) (*database.AlertSourceInstance, error)
	CreateInstance(sourceTypeName, name, description, webhookSecret string, fieldMappings, settings database.JSONB) (*database.AlertSourceInstance, error)
	CreateInstanceByTypeID(sourceTypeID uint, name, description, webhookSecret string, fieldMappings, settings database.JSONB) (*database.AlertSourceInstance, error)
	UpdateInstance(uuid string, updates map[string]interface{}) error
	UpdateInstanceByID(id uint, name, description, webhookSecret string, fieldMappings, settings database.JSONB, enabled bool) error
	DeleteInstance(uuid string) error
	DeleteInstanceByID(id uint) error
	InitializeDefaultSourceTypes() error
}

// RunbookManager defines the interface for runbook CRUD and file sync.
type RunbookManager interface {
	CreateRunbook(title, content string) (*database.Runbook, error)
	UpdateRunbook(id uint, title, content string) (*database.Runbook, error)
	DeleteRunbook(id uint) error
	GetRunbook(id uint) (*database.Runbook, error)
	ListRunbooks() ([]database.Runbook, error)
	SyncRunbookFiles() error
}

// MemoryManager defines the interface for cross-incident memory CRUD,
// idempotent upsert, and file sync. Consumed by handlers, the extractor,
// and the Slack feedback classifier.
type MemoryManager interface {
	CreateMemory(m *database.Memory) (*database.Memory, error)
	UpdateMemory(id uint, m *database.Memory) (*database.Memory, error)
	UpsertByName(m *database.Memory) (*database.Memory, error)
	DeleteMemory(id uint) error
	GetMemory(id uint) (*database.Memory, error)
	ListMemories(scope, memType string) ([]database.Memory, error)
	ListMemoriesByScope(scope string) ([]database.Memory, error)
	ListAllScopes() ([]string, error)
	CountByIncidentUUID(incidentUUID string, createdBy string) (int64, error)
	SyncMemoryFiles() error
}

// ContextManager defines the interface for context file management.
type ContextManager interface {
	GetContextDir() string
	ValidateFilename(filename string) error
	ValidateFileType(filename string) error
	FileExists(filename string) bool
	SaveFile(filename, originalName, mimeType, description string, size int64, content io.Reader) (*database.ContextFile, error)
	ListFiles() ([]database.ContextFile, error)
	GetFile(id uint) (*database.ContextFile, error)
	GetFileByName(filename string) (*database.ContextFile, error)
	DeleteFile(id uint) error
	GetFilePath(filename string) string
	ParseReferences(text string) []string
	ValidateReferences(text string) (valid bool, missing []string, found []string)
	ResolveReferences(text string) string
	ResolveReferencesToMarkdownLinks(text string) string
	CopyReferencedFilesToDir(text string, targetDir string) error
}

// HTTPConnectorManager defines the interface for HTTP connector CRUD operations.
type HTTPConnectorManager interface {
	CreateHTTPConnector(connector *database.HTTPConnector) (*database.HTTPConnector, error)
	GetHTTPConnector(id uint) (*database.HTTPConnector, error)
	UpdateHTTPConnector(id uint, updates map[string]interface{}) (*database.HTTPConnector, error)
	DeleteHTTPConnector(id uint) error
	ListHTTPConnectors() ([]database.HTTPConnector, error)
}

// ChannelManager defines the interface for messaging Integration and Channel
// CRUD plus resolution from alert sources / per-provider defaults. Handlers
// consume this rather than the concrete ChannelService so tests can swap in
// fakes that do not require a live database.
type ChannelManager interface {
	ListIntegrations() ([]database.Integration, error)
	GetIntegrationByUUID(uuid string) (*database.Integration, error)
	CreateIntegration(provider database.MessagingProvider, name string, credentials database.JSONB, enabled bool) (*database.Integration, error)
	UpdateIntegration(uuid string, name *string, credentials database.JSONB, enabled *bool) (*database.Integration, error)
	DeleteIntegration(uuid string) error

	ListChannels(filter ListChannelsFilter) ([]database.Channel, error)
	GetChannelByUUID(uuid string) (*database.Channel, error)
	CreateChannel(c *database.Channel) (*database.Channel, error)
	UpdateChannel(uuid string, patch ChannelUpdate) (*database.Channel, error)
	DeleteChannel(uuid string) error

	ResolveDefault(provider database.MessagingProvider) (*database.Channel, error)
	ResolveForAlertSource(asi *database.AlertSourceInstance, provider database.MessagingProvider) (*database.Channel, error)
}

// ProviderRegistry is the handler-facing view of the messaging provider
// registry. It is satisfied by *messaging.Registry; handlers depend on the
// interface so a stub registry can be wired in tests.
type ProviderRegistry interface {
	Get(name database.MessagingProvider) (messaging.Provider, error)
	List() []database.MessagingProvider
}

// CronJobManager is the handler-facing CRUD + manual-fire surface for cron
// jobs. It is satisfied by *CronRunner; handlers depend on this interface so
// tests can stub it without spinning up a scheduler.
//
// toolInstanceIDs on CreateJob is the per-cron tool allowlist (cron jobs ship
// with their own subset of the global tool catalog rather than inheriting the
// alert-driven incident-manager allowlist). Empty slice means the cron-agent
// runs with no infrastructure tools (memory + runbooks only).
type CronJobManager interface {
	ListJobs() ([]database.CronJob, error)
	GetJobByUUID(uuid string) (*database.CronJob, error)
	CreateJob(name, schedule, prompt string, channelUUID string, enabled bool, toolInstanceIDs []uint) (*database.CronJob, error)
	UpdateJob(uuid string, patch CronJobUpdate) (*database.CronJob, error)
	DeleteJob(uuid string) error
	RunNow(uuid string) error
}

// MCPServerManager defines the interface for MCP server configuration CRUD operations.
type MCPServerManager interface {
	CreateMCPServer(config *database.MCPServerConfig) (*database.MCPServerConfig, error)
	GetMCPServer(id uint) (*database.MCPServerConfig, error)
	UpdateMCPServer(id uint, updates map[string]interface{}) (*database.MCPServerConfig, error)
	DeleteMCPServer(id uint) error
	ListMCPServers() ([]database.MCPServerConfig, error)
}
