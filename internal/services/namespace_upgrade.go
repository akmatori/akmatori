package services

import (
	"log/slog"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/gorm"
)

// forgeNamespaces are built-in namespaces that operators often used for a
// remote MCP server before the built-in tool existed (GitHub's own MCP server
// is usually added with the prefix "github").
var forgeNamespaces = []string{"github", "gitea"}

// RenameReservedMCPNamespaces moves MCP servers off the forge namespaces:
// "github" becomes "github_mcp" (or "github_mcp_2", ...). The server and its
// managed tool type and instance keep working under the new prefix; only its
// tool names change (github.x → github_mcp.x).
//
// HTTP connectors with such a name are disabled instead: their tool type has
// the same kind as a built-in one, so it cannot be told apart and renamed.
//
// Run it before EnsureToolTypes: an MCP server's managed tool type is named
// like its prefix, and the seed looks tool types up by name.
func RenameReservedMCPNamespaces(db *gorm.DB) []string {
	var changed []string
	var mcps []database.MCPServerConfig
	if err := db.Where("namespace_prefix IN ?", forgeNamespaces).Find(&mcps).Error; err != nil {
		slog.Warn("failed to look for MCP servers with a reserved namespace", "err", err)
	}
	svc := NewMCPServerServiceWithDB(db)
	for i := range mcps {
		cfg := mcps[i]
		oldNS := cfg.NamespacePrefix
		err := db.Transaction(func(tx *gorm.DB) error {
			cfg.NamespacePrefix = svc.uniqueNamespace(tx, oldNS+"_mcp")
			if err := svc.checkNamespace(tx, cfg.NamespacePrefix, cfg.ID); err != nil {
				return err
			}
			if err := tx.Model(&database.MCPServerConfig{}).Where("id = ?", cfg.ID).
				Update("namespace_prefix", cfg.NamespacePrefix).Error; err != nil {
				return err
			}
			return syncManagedTool(tx, &cfg, oldNS)
		})
		if err != nil {
			slog.Warn("could not rename an MCP server off a reserved namespace; the gateway skips it until you change its namespace prefix",
				"mcp_server", cfg.Name, "namespace_prefix", oldNS, "err", err)
			continue
		}
		slog.Warn("renamed an MCP server's namespace prefix: the name is now a built-in tool; update skills that name its tools",
			"mcp_server", cfg.Name, "old_prefix", oldNS, "new_prefix", cfg.NamespacePrefix)
		changed = append(changed, "mcp server "+cfg.Name+": "+oldNS+" → "+cfg.NamespacePrefix)
	}

	var https []database.HTTPConnector
	if err := db.Where("tool_type_name IN ? AND enabled = ?", forgeNamespaces, true).Find(&https).Error; err == nil {
		for _, c := range https {
			if err := db.Model(&database.HTTPConnector{}).Where("id = ?", c.ID).Update("enabled", false).Error; err == nil {
				slog.Warn("disabled an HTTP connector: its name is now a built-in tool namespace; recreate it under another name",
					"http_connector", c.ToolTypeName)
				changed = append(changed, "http connector "+c.ToolTypeName+": disabled")
			}
		}
	}
	return changed
}
