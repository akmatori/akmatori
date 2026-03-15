package services

import (
	"io"

	"github.com/akmatori/akmatori/internal/database"
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
	CreateToolInstance(toolTypeID uint, name string, settings database.JSONB) (*database.ToolInstance, error)
	GetToolInstance(id uint) (*database.ToolInstance, error)
	UpdateToolInstance(id uint, name string, settings database.JSONB, enabled bool) error
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

// AggregationManager defines the interface for incident aggregation/correlation.
type AggregationManager interface {
	GetOpenIncidents() ([]database.Incident, error)
	GetOpenIncidentsForCorrelation() ([]database.Incident, error)
	GetSettings() (*database.AggregationSettings, error)
	UpdateSettings(settings *database.AggregationSettings) error
	GetIncidentAlerts(incidentID uint) ([]database.IncidentAlert, error)
	GetIncidentByUUID(uuid string) (*database.Incident, error)
	AttachAlertToIncident(incidentID uint, alert *database.IncidentAlert) error
	CreateIncidentWithAlert(incident *database.Incident, alert *database.IncidentAlert) error
	RecordMerge(sourceID, targetID uint, confidence float64, reason, mergedBy string) error
	BuildCorrelatorInput(incomingAlert AlertContext) (*CorrelatorInput, error)
}
