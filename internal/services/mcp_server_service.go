package services

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/gorm"
)

// Errors the MCP server handlers map to HTTP status codes.
var (
	ErrMCPServerNotFound = errors.New("MCP server config not found")
	ErrMCPServerConflict = errors.New("already exists")
)

// MCPAuthPatch describes a change to an MCP server's auth_config.
//   - Clear: remove auth entirely (JSON null)
//   - Value: the new auth config. When its type equals the stored type, a
//     secret field that is missing or empty keeps the stored secret, so the
//     UI can edit the header name without re-entering the token. OAuth
//     tokens are kept the same way.
type MCPAuthPatch struct {
	Clear bool
	Value database.JSONB
}

// MCPServerPatch is a partial update. Nil fields stay unchanged.
type MCPServerPatch struct {
	Name            *string
	Transport       *string
	URL             *string
	NamespacePrefix *string
	Auth            *MCPAuthPatch
	DisabledTools   *[]string
	Enabled         *bool
}

// MCPMaskedSecret replaces secret values in API responses. Sending it back
// on update keeps the stored secret.
const MCPMaskedSecret = "********"

// mcpSecretFields are the auth_config keys that hold secrets. They are
// never returned by the API and are kept on update when omitted.
var mcpSecretFields = []string{"token", "value", "access_token", "refresh_token", "client_secret"}

// IsMCPSecretField reports whether an auth_config key holds a secret.
func IsMCPSecretField(key string) bool {
	for _, f := range mcpSecretFields {
		if f == key {
			return true
		}
	}
	return false
}

// MCPServerService manages remote MCP server configs and keeps, for each of
// them, a managed tool type (named as the namespace prefix, kind "mcp") and
// one tool instance. Skills, cron jobs and the per-run allowlist bind that
// tool instance, so the gateway authorizes MCP tools exactly like built-in
// tools.
type MCPServerService struct {
	db *gorm.DB
}

// NewMCPServerService creates a new MCP server service
func NewMCPServerService() *MCPServerService {
	return &MCPServerService{db: database.GetDB()}
}

// NewMCPServerServiceWithDB creates a service on an explicit DB (tests).
func NewMCPServerServiceWithDB(db *gorm.DB) *MCPServerService {
	return &MCPServerService{db: db}
}

var nonNamespaceChars = regexp.MustCompile(`[^a-z0-9]+`)

// DefaultMCPNamespace derives a namespace prefix from a display name:
// "GitHub Issues" -> "ext.github_issues".
func DefaultMCPNamespace(name string) string {
	slug := strings.Trim(nonNamespaceChars.ReplaceAllString(strings.ToLower(name), "_"), "_")
	if slug == "" {
		slug = "mcp"
	}
	return "ext." + slug
}

// checkNamespace refuses prefixes that collide with built-in tools, HTTP
// connectors, other MCP servers or any non-MCP tool type.
func (s *MCPServerService) checkNamespace(tx *gorm.DB, ns string, selfID uint) error {
	if isReservedToolNamespace(ns) {
		return fmt.Errorf("namespace_prefix %q conflicts with a built-in tool namespace: %w", ns, ErrMCPServerConflict)
	}
	var n int64
	tx.Model(&database.MCPServerConfig{}).Where("namespace_prefix = ? AND id <> ?", ns, selfID).Count(&n)
	if n > 0 {
		return fmt.Errorf("MCP server with namespace_prefix %q already exists: %w", ns, ErrMCPServerConflict)
	}
	if namespaceConflictsWithHTTPConnector(tx, ns) {
		return fmt.Errorf("namespace_prefix %q conflicts with an existing HTTP connector namespace: %w", ns, ErrMCPServerConflict)
	}
	tx.Model(&database.ToolType{}).Where("name = ? AND kind <> ?", ns, database.ToolTypeKindMCP).Count(&n)
	if n > 0 {
		return fmt.Errorf("namespace_prefix %q conflicts with an existing tool type: %w", ns, ErrMCPServerConflict)
	}
	return nil
}

// uniqueNamespace returns base, or base_2, base_3... when base is taken.
func (s *MCPServerService) uniqueNamespace(tx *gorm.DB, base string) string {
	ns := base
	for i := 2; i < 100; i++ {
		if s.checkNamespace(tx, ns, 0) == nil {
			return ns
		}
		ns = fmt.Sprintf("%s_%d", base, i)
	}
	return ns
}

// CreateMCPServer validates and stores a config, then creates its managed
// tool type and tool instance in the same transaction.
func (s *MCPServerService) CreateMCPServer(config *database.MCPServerConfig) (*database.MCPServerConfig, error) {
	config.Name = strings.TrimSpace(config.Name)
	config.URL = strings.TrimSpace(config.URL)
	if config.Transport == "" {
		config.Transport = database.MCPServerTransportHTTP
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if config.NamespacePrefix == "" {
			config.NamespacePrefix = s.uniqueNamespace(tx, DefaultMCPNamespace(config.Name))
		}
		if err := config.Validate(); err != nil {
			return fmt.Errorf("validation failed: %w", err)
		}
		if err := ValidateMCPAuth(config.AuthConfig); err != nil {
			return fmt.Errorf("validation failed: %w", err)
		}
		var n int64
		tx.Model(&database.MCPServerConfig{}).Where("name = ?", config.Name).Count(&n)
		if n > 0 {
			return fmt.Errorf("MCP server with name %q already exists: %w", config.Name, ErrMCPServerConflict)
		}
		if err := s.checkNamespace(tx, config.NamespacePrefix, 0); err != nil {
			return err
		}
		config.Enabled = true
		if err := tx.Create(config).Error; err != nil {
			return fmt.Errorf("failed to create MCP server config: %w", err)
		}
		return syncManagedTool(tx, config, "")
	})
	if err != nil {
		return nil, err
	}
	return config, nil
}

// GetMCPServer retrieves an MCP server configuration by ID
func (s *MCPServerService) GetMCPServer(id uint) (*database.MCPServerConfig, error) {
	var config database.MCPServerConfig
	if err := s.db.First(&config, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrMCPServerNotFound
		}
		return nil, fmt.Errorf("load MCP server config: %w", err)
	}
	return &config, nil
}

// mergeAuth applies an auth patch to the stored auth_config.
func mergeAuth(stored database.JSONB, patch *MCPAuthPatch) database.JSONB {
	if patch.Clear || len(patch.Value) == 0 {
		return nil
	}
	next := database.JSONB{}
	for k, v := range patch.Value {
		next[k] = v
	}
	if stored != nil && stored["type"] == next["type"] {
		for _, f := range mcpSecretFields {
			if v, ok := next[f]; !ok || v == "" || v == nil || v == MCPMaskedSecret {
				if old, ok := stored[f]; ok {
					next[f] = old
				} else {
					delete(next, f)
				}
			}
		}
		// OAuth: keep token metadata the UI never sends.
		if next["type"] == "oauth" {
			for k, v := range stored {
				if _, ok := next[k]; !ok {
					next[k] = v
				}
			}
		}
	}
	return next
}

// MergeMCPAuthForTest fills masked or omitted secrets of a test auth config
// from the stored one (same rules as an update).
func MergeMCPAuthForTest(stored, sent database.JSONB) database.JSONB {
	return mergeAuth(stored, &MCPAuthPatch{Value: sent})
}

// ValidateMCPAuth checks the shape of an auth_config.
func ValidateMCPAuth(cfg database.JSONB) error {
	if len(cfg) == 0 {
		return nil
	}
	t, _ := cfg["type"].(string)
	switch t {
	case "bearer":
		if tok, _ := cfg["token"].(string); strings.TrimSpace(tok) == "" {
			return fmt.Errorf("auth_config.token is required for bearer auth")
		}
	case "header":
		name, _ := cfg["header_name"].(string)
		if !httpHeaderName.MatchString(name) {
			return fmt.Errorf("auth_config.header_name must be a valid HTTP header name")
		}
		switch strings.ToLower(name) {
		case "host", "content-length", "content-type", "accept", "mcp-session-id", "transfer-encoding", "connection":
			return fmt.Errorf("auth_config.header_name %q is reserved", name)
		}
		if v, _ := cfg["value"].(string); strings.TrimSpace(v) == "" {
			return fmt.Errorf("auth_config.value is required for header auth")
		}
	case "oauth":
		// Tokens arrive through the OAuth flow; nothing is required up front.
	default:
		return fmt.Errorf("auth_config.type must be bearer, header or oauth")
	}
	return nil
}

var httpHeaderName = regexp.MustCompile("^[A-Za-z0-9!#$%&'*+.^_`|~-]+$")

// UpdateMCPServer applies a partial update and re-syncs the managed tool.
func (s *MCPServerService) UpdateMCPServer(id uint, patch MCPServerPatch) (*database.MCPServerConfig, error) {
	var config database.MCPServerConfig
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&config, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrMCPServerNotFound
			}
			return err
		}
		oldPrefix := config.NamespacePrefix
		if patch.Name != nil {
			name := strings.TrimSpace(*patch.Name)
			if name != config.Name {
				var n int64
				tx.Model(&database.MCPServerConfig{}).Where("name = ? AND id <> ?", name, id).Count(&n)
				if n > 0 {
					return fmt.Errorf("MCP server with name %q already exists: %w", name, ErrMCPServerConflict)
				}
			}
			config.Name = name
		}
		if patch.Transport != nil {
			config.Transport = database.MCPServerTransport(*patch.Transport)
		}
		if patch.URL != nil {
			config.URL = strings.TrimSpace(*patch.URL)
		}
		if patch.NamespacePrefix != nil && *patch.NamespacePrefix != config.NamespacePrefix {
			if err := s.checkNamespace(tx, *patch.NamespacePrefix, id); err != nil {
				return err
			}
			config.NamespacePrefix = *patch.NamespacePrefix
		}
		if patch.Auth != nil {
			config.AuthConfig = mergeAuth(config.AuthConfig, patch.Auth)
		}
		if patch.DisabledTools != nil {
			config.DisabledTools = database.StringList(dedupe(*patch.DisabledTools))
		}
		if patch.Enabled != nil {
			config.Enabled = *patch.Enabled
		}
		// A legacy stdio row can still be disabled or deleted, but not
		// re-enabled or edited into anything but a remote server.
		if config.Transport == database.MCPServerTransportStdio && !config.Enabled && patch.URL == nil && patch.Transport == nil {
			return tx.Save(&config).Error
		}
		// Moving off stdio: the old command fields must go.
		if config.Transport != database.MCPServerTransportStdio {
			config.Command, config.Args, config.EnvVars = "", nil, nil
		}
		if err := config.Validate(); err != nil {
			return fmt.Errorf("validation failed: %w", err)
		}
		if err := ValidateMCPAuth(config.AuthConfig); err != nil {
			return fmt.Errorf("validation failed: %w", err)
		}
		if err := tx.Save(&config).Error; err != nil {
			return fmt.Errorf("failed to update MCP server config: %w", err)
		}
		return syncManagedTool(tx, &config, oldPrefix)
	})
	if err != nil {
		return nil, err
	}
	return &config, nil
}

// StoreOAuthTokens replaces the auth_config of a server with the given
// OAuth state (no merge: the OAuth flow owns the whole object).
func (s *MCPServerService) StoreOAuthTokens(id uint, cfg database.JSONB) error {
	res := s.db.Model(&database.MCPServerConfig{}).Where("id = ?", id).Update("auth_config", cfg)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrMCPServerNotFound
	}
	return nil
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// DeleteMCPServer deletes the config, its managed tool instance (with its
// skill and cron bindings) and its managed tool type.
func (s *MCPServerService) DeleteMCPServer(id uint) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var config database.MCPServerConfig
		if err := tx.First(&config, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrMCPServerNotFound
			}
			return err
		}
		if err := deleteManagedTool(tx, config.NamespacePrefix); err != nil {
			return err
		}
		if err := tx.Delete(&database.MCPServerConfig{}, id).Error; err != nil {
			return fmt.Errorf("failed to delete MCP server config: %w", err)
		}
		return nil
	})
}

// ListMCPServers lists all MCP server configurations
func (s *MCPServerService) ListMCPServers() ([]database.MCPServerConfig, error) {
	var configs []database.MCPServerConfig
	if err := s.db.Order("name ASC").Find(&configs).Error; err != nil {
		return nil, fmt.Errorf("failed to list MCP server configs: %w", err)
	}
	return configs, nil
}

// SyncAllManagedTools creates or fixes the managed tool type and instance of
// every MCP server (upgrade path for servers created before they existed).
func (s *MCPServerService) SyncAllManagedTools() error {
	configs, err := s.ListMCPServers()
	if err != nil {
		return err
	}
	for i := range configs {
		cfg := configs[i]
		if err := s.db.Transaction(func(tx *gorm.DB) error { return syncManagedTool(tx, &cfg, cfg.NamespacePrefix) }); err != nil {
			return fmt.Errorf("sync managed tool for MCP server %q: %w", cfg.Name, err)
		}
	}
	return nil
}

// ManagedToolInstanceID returns the managed tool instance of an MCP server.
func (s *MCPServerService) ManagedToolInstanceID(id uint) (uint, error) {
	config, err := s.GetMCPServer(id)
	if err != nil {
		return 0, err
	}
	var inst database.ToolInstance
	err = s.db.Joins("JOIN tool_types ON tool_types.id = tool_instances.tool_type_id").
		Where("tool_types.kind = ? AND tool_types.name = ?", database.ToolTypeKindMCP, config.NamespacePrefix).
		First(&inst).Error
	if err != nil {
		return 0, err
	}
	return inst.ID, nil
}

// syncManagedTool makes the managed tool type/instance match the config.
// oldPrefix is the namespace before this change ("" on create).
func syncManagedTool(tx *gorm.DB, config *database.MCPServerConfig, oldPrefix string) error {
	lookup := config.NamespacePrefix
	if oldPrefix != "" {
		lookup = oldPrefix
	}
	var tt database.ToolType
	err := tx.Where("name = ? AND kind = ?", lookup, database.ToolTypeKindMCP).First(&tt).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	desc := fmt.Sprintf("Custom MCP server %q (%s)", config.Name, config.URL)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		tt = database.ToolType{Name: config.NamespacePrefix, Kind: database.ToolTypeKindMCP, Description: desc}
		if err := tx.Create(&tt).Error; err != nil {
			return fmt.Errorf("create managed tool type: %w", err)
		}
	} else if tt.Name != config.NamespacePrefix || tt.Description != desc {
		if err := tx.Model(&tt).Updates(map[string]interface{}{"name": config.NamespacePrefix, "description": desc}).Error; err != nil {
			return fmt.Errorf("update managed tool type: %w", err)
		}
	}

	var inst database.ToolInstance
	err = tx.Where("tool_type_id = ?", tt.ID).First(&inst).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	// Name and logical name are unique across all tool instances.
	var clash int64
	tx.Model(&database.ToolInstance{}).Where("name = ? AND tool_type_id <> ?", config.Name, tt.ID).Count(&clash)
	if clash > 0 {
		return fmt.Errorf("a tool named %q already exists; choose another name: %w", config.Name, ErrMCPServerConflict)
	}
	logical := uniqueLogicalName(tx, database.SlugifyLogicalName(config.Name), tt.ID)
	settings := database.JSONB{"mcp_server_config_id": config.ID}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		inst = database.ToolInstance{
			ToolTypeID: tt.ID, Name: config.Name, LogicalName: logical,
			Settings: settings, Enabled: config.Enabled, ConnectorID: config.ConnectorID,
		}
		if err := tx.Create(&inst).Error; err != nil {
			return fmt.Errorf("create managed tool instance: %w", err)
		}
		return nil
	}
	updates := map[string]interface{}{"name": config.Name, "settings": settings, "enabled": config.Enabled}
	if inst.Name != config.Name {
		updates["logical_name"] = logical
	}
	if config.ConnectorID != nil {
		updates["connector_id"] = *config.ConnectorID
	}
	return tx.Model(&inst).Updates(updates).Error
}

// uniqueLogicalName returns base, or base-mcp, base-mcp-2... when another
// tool instance (not of tool type selfTypeID) already uses it.
func uniqueLogicalName(tx *gorm.DB, base string, selfTypeID uint) string {
	if base == "" {
		base = "mcp"
	}
	candidates := []string{base, base + "-mcp"}
	for i := 2; i < 100; i++ {
		candidates = append(candidates, fmt.Sprintf("%s-mcp-%d", base, i))
	}
	for _, c := range candidates {
		var n int64
		tx.Model(&database.ToolInstance{}).Where("logical_name = ? AND tool_type_id <> ?", c, selfTypeID).Count(&n)
		if n == 0 {
			return c
		}
	}
	return base
}

func deleteManagedTool(tx *gorm.DB, prefix string) error {
	var tt database.ToolType
	if err := tx.Where("name = ? AND kind = ?", prefix, database.ToolTypeKindMCP).First(&tt).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	var ids []uint
	if err := tx.Model(&database.ToolInstance{}).Where("tool_type_id = ?", tt.ID).Pluck("id", &ids).Error; err != nil {
		return err
	}
	if len(ids) > 0 {
		if err := tx.Where("tool_instance_id IN ?", ids).Delete(&database.CronJobTool{}).Error; err != nil {
			return fmt.Errorf("clear cron assignments: %w", err)
		}
		if err := tx.Where("tool_instance_id IN ?", ids).Delete(&database.SkillTool{}).Error; err != nil {
			return fmt.Errorf("clear skill assignments: %w", err)
		}
		if err := tx.Where("id IN ?", ids).Delete(&database.ToolInstance{}).Error; err != nil {
			return fmt.Errorf("delete managed tool instance: %w", err)
		}
	}
	return tx.Delete(&tt).Error
}
