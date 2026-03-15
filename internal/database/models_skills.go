package database

import (
	"fmt"
	"time"
)

// Skill represents a skill definition (uses SKILL.md format internally for Codex compatibility)
// Skill prompt/instructions are stored in filesystem at /akmatori/skills/{name}/SKILL.md
type Skill struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Name        string    `gorm:"uniqueIndex;size:64;not null" json:"name"` // kebab-case name (e.g., "zabbix-analyst")
	Description string    `gorm:"size:1024" json:"description"`             // Short description for skill discovery
	Category    string    `gorm:"size:64" json:"category"`                  // Optional category (e.g., "monitoring", "database")
	IsSystem    bool      `gorm:"default:false" json:"is_system"`           // System skills cannot be deleted and don't connect to tools
	Enabled     bool      `gorm:"default:true" json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`

	// Relationships - tools are symlinked to skills/{name}/scripts/ with imports embedded in SKILL.md
	Tools []ToolInstance `gorm:"many2many:skill_tools;" json:"tools,omitempty"`
}

// ToolType represents a predefined tool type (e.g., zabbix, grafana)
type ToolType struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Name        string    `gorm:"uniqueIndex;not null" json:"name"` // Snake_case tool name matching directory (e.g., "aws_cloudwatch")
	Description string    `gorm:"type:text" json:"description"`
	Schema      JSONB     `gorm:"type:jsonb" json:"schema"` // JSON schema for settings validation
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`

	// Relationships
	Instances []ToolInstance `gorm:"foreignKey:ToolTypeID" json:"instances,omitempty"`
}

// ToolInstance represents an actual configured instance of a tool type
type ToolInstance struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	ToolTypeID  uint      `gorm:"not null;index" json:"tool_type_id"`
	Name        string    `gorm:"uniqueIndex;not null" json:"name"`         // User-friendly name
	LogicalName string    `gorm:"uniqueIndex;size:128" json:"logical_name"` // Machine-friendly logical name for agent referencing (e.g., "prod-ssh")
	Settings    JSONB     `gorm:"type:jsonb" json:"settings"`               // Tool-specific settings (URLs, tokens, etc.)
	Enabled     bool      `gorm:"default:true" json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`

	// Relationships
	ToolType ToolType `gorm:"foreignKey:ToolTypeID" json:"tool_type,omitempty"`
	Skills   []Skill  `gorm:"many2many:skill_tools;" json:"skills,omitempty"`
}

// SkillTool represents the many-to-many relationship between skills and tools
// GORM auto-manages this table via the many2many:skill_tools tag
type SkillTool struct {
	SkillID        uint      `gorm:"primaryKey" json:"skill_id"`
	ToolInstanceID uint      `gorm:"primaryKey" json:"tool_instance_id"`
	CreatedAt      time.Time `json:"created_at"`
}

// EventSourceType represents the type of event source
type EventSourceType string

const (
	EventSourceTypeSlack   EventSourceType = "slack"
	EventSourceTypeWebhook EventSourceType = "webhook"
)

// EventSource represents an event source configuration
type EventSource struct {
	ID        uint            `gorm:"primaryKey" json:"id"`
	Type      EventSourceType `gorm:"type:varchar(50);not null;index" json:"type"`
	Name      string          `gorm:"uniqueIndex;not null" json:"name"`
	Settings  JSONB           `gorm:"type:jsonb" json:"settings"` // Source-specific settings
	Enabled   bool            `gorm:"default:true" json:"enabled"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// TableName overrides for explicit table naming
func (Skill) TableName() string {
	return "skills"
}

func (ToolType) TableName() string {
	return "tool_types"
}

func (ToolInstance) TableName() string {
	return "tool_instances"
}

func (SkillTool) TableName() string {
	return "skill_tools"
}

func (EventSource) TableName() string {
	return "event_sources"
}

// HTTPConnectorAuthMethod represents the authentication method for an HTTP connector
type HTTPConnectorAuthMethod string

const (
	HTTPConnectorAuthBearer HTTPConnectorAuthMethod = "bearer_token"
	HTTPConnectorAuthBasic  HTTPConnectorAuthMethod = "basic_auth"
	HTTPConnectorAuthAPIKey HTTPConnectorAuthMethod = "api_key"
)

// HTTPConnectorToolParam defines a parameter for an HTTP connector tool
type HTTPConnectorToolParam struct {
	Name     string      `json:"name"`
	Type     string      `json:"type"`               // string, integer, number, boolean
	Required bool        `json:"required"`
	In       string      `json:"in"`                  // path, query, body, header
	Default  interface{} `json:"default,omitempty"`
}

// HTTPConnectorToolDef defines a single tool within an HTTP connector
type HTTPConnectorToolDef struct {
	Name        string                   `json:"name"`
	Description string                   `json:"description,omitempty"`
	HTTPMethod  string                   `json:"http_method"` // GET, POST, PUT, DELETE
	Path        string                   `json:"path"`        // URL path with {{param}} templates
	Params      []HTTPConnectorToolParam `json:"params,omitempty"`
	ReadOnly    *bool                    `json:"read_only,omitempty"` // default true
}

// IsReadOnly returns whether this tool is read-only (defaults to true if not set)
func (d HTTPConnectorToolDef) IsReadOnly() bool {
	if d.ReadOnly == nil {
		return true
	}
	return *d.ReadOnly
}

// HTTPConnectorAuthConfig holds authentication configuration for an HTTP connector
type HTTPConnectorAuthConfig struct {
	Method     HTTPConnectorAuthMethod `json:"method"`                // bearer_token, basic_auth, api_key
	TokenField string                  `json:"token_field,omitempty"` // field name in instance settings holding the token/key
	HeaderName string                  `json:"header_name,omitempty"` // custom header name (for api_key method)
}

// HTTPConnector represents a declarative HTTP connector definition
// It allows users to define integrations with external HTTP APIs without writing code
type HTTPConnector struct {
	ID           uint                    `gorm:"primaryKey" json:"id"`
	ToolTypeName string                  `gorm:"uniqueIndex;size:128;not null" json:"tool_type_name"` // e.g., "internal-billing"
	Description  string                  `gorm:"size:1024" json:"description"`
	BaseURLField string                  `gorm:"size:128;not null" json:"base_url_field"` // field name in instance settings holding the base URL
	AuthConfig   JSONB                   `gorm:"type:jsonb" json:"auth_config"`           // HTTPConnectorAuthConfig serialized
	Tools        JSONB                   `gorm:"type:jsonb;not null" json:"tools"`         // []HTTPConnectorToolDef serialized
	Enabled      bool                    `gorm:"default:true" json:"enabled"`
	CreatedAt    time.Time               `json:"created_at"`
	UpdatedAt    time.Time               `json:"updated_at"`
}

func (HTTPConnector) TableName() string {
	return "http_connectors"
}

// Validate checks that the HTTPConnector has valid configuration
func (c *HTTPConnector) Validate() error {
	if c.ToolTypeName == "" {
		return fmt.Errorf("tool_type_name is required")
	}
	if c.BaseURLField == "" {
		return fmt.Errorf("base_url_field is required")
	}

	toolDefs, err := c.GetToolDefs()
	if err != nil {
		return fmt.Errorf("invalid tools definition: %w", err)
	}
	if len(toolDefs) == 0 {
		return fmt.Errorf("at least one tool definition is required")
	}

	seen := make(map[string]bool)
	validMethods := map[string]bool{"GET": true, "POST": true, "PUT": true, "DELETE": true}
	validParamIn := map[string]bool{"path": true, "query": true, "body": true, "header": true}

	for i, tool := range toolDefs {
		if tool.Name == "" {
			return fmt.Errorf("tool[%d]: name is required", i)
		}
		if seen[tool.Name] {
			return fmt.Errorf("tool[%d]: duplicate tool name %q", i, tool.Name)
		}
		seen[tool.Name] = true

		if !validMethods[tool.HTTPMethod] {
			return fmt.Errorf("tool[%d] %q: invalid http_method %q (must be GET, POST, PUT, or DELETE)", i, tool.Name, tool.HTTPMethod)
		}
		if tool.Path == "" {
			return fmt.Errorf("tool[%d] %q: path is required", i, tool.Name)
		}

		for j, param := range tool.Params {
			if param.Name == "" {
				return fmt.Errorf("tool[%d] %q param[%d]: name is required", i, tool.Name, j)
			}
			if !validParamIn[param.In] {
				return fmt.Errorf("tool[%d] %q param[%d] %q: invalid 'in' value %q (must be path, query, body, or header)", i, tool.Name, j, param.Name, param.In)
			}
		}
	}

	return nil
}

// GetToolDefs parses the Tools JSONB field into typed tool definitions
func (c *HTTPConnector) GetToolDefs() ([]HTTPConnectorToolDef, error) {
	if c.Tools == nil {
		return nil, nil
	}

	toolsRaw, ok := c.Tools["tools"]
	if !ok {
		return nil, nil
	}

	toolsList, ok := toolsRaw.([]interface{})
	if !ok {
		return nil, fmt.Errorf("tools field must be an array")
	}

	var defs []HTTPConnectorToolDef
	for _, raw := range toolsList {
		m, ok := raw.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("each tool must be an object")
		}

		def := HTTPConnectorToolDef{}
		if v, ok := m["name"].(string); ok {
			def.Name = v
		}
		if v, ok := m["description"].(string); ok {
			def.Description = v
		}
		if v, ok := m["http_method"].(string); ok {
			def.HTTPMethod = v
		}
		if v, ok := m["path"].(string); ok {
			def.Path = v
		}
		if v, ok := m["read_only"]; ok {
			if b, ok := v.(bool); ok {
				def.ReadOnly = &b
			}
		}

		// Parse params
		if paramsRaw, ok := m["params"].([]interface{}); ok {
			for _, pRaw := range paramsRaw {
				pm, ok := pRaw.(map[string]interface{})
				if !ok {
					continue
				}
				param := HTTPConnectorToolParam{}
				if v, ok := pm["name"].(string); ok {
					param.Name = v
				}
				if v, ok := pm["type"].(string); ok {
					param.Type = v
				}
				if v, ok := pm["required"].(bool); ok {
					param.Required = v
				}
				if v, ok := pm["in"].(string); ok {
					param.In = v
				}
				if v, ok := pm["default"]; ok {
					param.Default = v
				}
				def.Params = append(def.Params, param)
			}
		}

		defs = append(defs, def)
	}

	return defs, nil
}

// GetAuthConfig parses the AuthConfig JSONB field into a typed auth config
func (c *HTTPConnector) GetAuthConfig() (*HTTPConnectorAuthConfig, error) {
	if c.AuthConfig == nil {
		return nil, nil
	}

	config := &HTTPConnectorAuthConfig{}
	if v, ok := c.AuthConfig["method"].(string); ok {
		config.Method = HTTPConnectorAuthMethod(v)
	}
	if v, ok := c.AuthConfig["token_field"].(string); ok {
		config.TokenField = v
	}
	if v, ok := c.AuthConfig["header_name"].(string); ok {
		config.HeaderName = v
	}

	return config, nil
}

// MCPServerTransport represents the transport type for an MCP server connection
type MCPServerTransport string

const (
	MCPServerTransportSSE   MCPServerTransport = "sse"
	MCPServerTransportStdio MCPServerTransport = "stdio"
)

// MCPServerConfig represents a registered external MCP server that can be proxied through the gateway.
// Each config defines how to connect to an external MCP server and how its tools are namespaced.
type MCPServerConfig struct {
	ID              uint               `gorm:"primaryKey" json:"id"`
	Name            string             `gorm:"uniqueIndex;size:128;not null" json:"name"`              // User-friendly name
	Transport       MCPServerTransport `gorm:"type:varchar(16);not null" json:"transport"`              // "sse" or "stdio"
	URL             string             `gorm:"size:512" json:"url,omitempty"`                           // For SSE transport
	Command         string             `gorm:"size:512" json:"command,omitempty"`                       // For stdio transport
	Args            JSONB              `gorm:"type:jsonb" json:"args,omitempty"`                        // For stdio transport: ["arg1", "arg2"]
	EnvVars         JSONB              `gorm:"type:jsonb" json:"env_vars,omitempty"`                    // For stdio transport: {"KEY": "value"}
	NamespacePrefix string             `gorm:"size:128;not null" json:"namespace_prefix"`               // e.g., "ext.github"
	AuthConfig      JSONB              `gorm:"type:jsonb" json:"auth_config,omitempty"`                 // Auth to inject into connections
	Enabled         bool               `gorm:"default:true" json:"enabled"`
	CreatedAt       time.Time          `json:"created_at"`
	UpdatedAt       time.Time          `json:"updated_at"`
}

func (MCPServerConfig) TableName() string {
	return "mcp_server_configs"
}

// Validate checks that the MCPServerConfig has valid configuration
func (c *MCPServerConfig) Validate() error {
	if c.Name == "" {
		return fmt.Errorf("name is required")
	}
	if c.NamespacePrefix == "" {
		return fmt.Errorf("namespace_prefix is required")
	}
	if c.Transport != MCPServerTransportSSE && c.Transport != MCPServerTransportStdio {
		return fmt.Errorf("transport must be 'sse' or 'stdio'")
	}
	if c.Transport == MCPServerTransportSSE && c.URL == "" {
		return fmt.Errorf("url is required for SSE transport")
	}
	if c.Transport == MCPServerTransportStdio && c.Command == "" {
		return fmt.Errorf("command is required for stdio transport")
	}
	return nil
}
