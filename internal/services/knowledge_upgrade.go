package services

import (
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/gorm"
)

// knowledgeNamespaces are the built-in namespaces the gateway knowledge tools
// took over. A connector that used one before the upgrade is disabled.
var knowledgeNamespaces = []string{BuiltinRunbooks, BuiltinMemory}

// DisableReservedNamespaceCollisions disables HTTP connectors and MCP servers
// whose namespace is now a built-in (runbooks, memory). The gateway skips
// them anyway; disabling makes the state visible on the Connectors page.
// Returns the names it disabled.
func DisableReservedNamespaceCollisions(db *gorm.DB) []string {
	var disabled []string
	var https []database.HTTPConnector
	if err := db.Where("tool_type_name IN ? AND enabled = ?", knowledgeNamespaces, true).Find(&https).Error; err == nil {
		for _, c := range https {
			if err := db.Model(&database.HTTPConnector{}).Where("id = ?", c.ID).Update("enabled", false).Error; err == nil {
				disabled = append(disabled, "http connector "+c.ToolTypeName)
			}
		}
	}
	var mcps []database.MCPServerConfig
	if err := db.Where("namespace_prefix IN ? AND enabled = ?", knowledgeNamespaces, true).Find(&mcps).Error; err == nil {
		for _, c := range mcps {
			if err := db.Model(&database.MCPServerConfig{}).Where("id = ?", c.ID).Update("enabled", false).Error; err == nil {
				disabled = append(disabled, "mcp server "+c.Name)
			}
		}
	}
	for _, d := range disabled {
		slog.Warn("disabled a connector: its namespace is reserved for a built-in tool; rename the namespace and enable it again", "connector", d)
	}
	return disabled
}

// retiredKnowledgeMarkers are the subagent names and agent-side paths that the
// gateway knowledge tools replaced.
var retiredKnowledgeMarkers = []string{
	"subagent(", "runbook-searcher", "memory-searcher", "memory-writer", "/akmatori/runbooks", "/akmatori/memory",
}

func mentionsRetiredKnowledge(text string) bool {
	for _, m := range retiredKnowledgeMarkers {
		if strings.Contains(text, m) {
			return true
		}
	}
	return false
}

// WarnRetiredKnowledgeReferences logs one warning that lists the skills,
// runbooks, memories and cron prompts that still name the retired subagents
// or memory paths. The agent can no longer follow them; operators should
// rewrite them to use runbooks.* / memory.*.
func WarnRetiredKnowledgeReferences(db *gorm.DB, skillsDir string) []string {
	var hits []string
	var skills []database.Skill
	if err := db.Where("is_system = ?", false).Find(&skills).Error; err == nil {
		for _, sk := range skills {
			if b, err := os.ReadFile(filepath.Join(skillsDir, sk.Name, "SKILL.md")); err == nil && mentionsRetiredKnowledge(string(b)) {
				hits = append(hits, "skill "+sk.Name)
			}
		}
	}
	var runbooks []database.Runbook
	if err := db.Select("id", "title", "content").Find(&runbooks).Error; err == nil {
		for _, r := range runbooks {
			if mentionsRetiredKnowledge(r.Content) {
				hits = append(hits, "runbook "+r.Title)
			}
		}
	}
	var memories []database.Memory
	if err := db.Select("id", "scope", "name", "body").Find(&memories).Error; err == nil {
		for _, m := range memories {
			if mentionsRetiredKnowledge(m.Body) {
				hits = append(hits, "memory "+m.Scope+"/"+m.Name)
			}
		}
	}
	var crons []database.CronJob
	if err := db.Select("id", "name", "prompt").Find(&crons).Error; err == nil {
		for _, c := range crons {
			if mentionsRetiredKnowledge(c.Prompt) {
				hits = append(hits, "cron "+c.Name)
			}
		}
	}
	sort.Strings(hits)
	if len(hits) > 0 {
		slog.Warn("these texts still name the retired runbook/memory subagents or /akmatori/runbooks, /akmatori/memory; rewrite them to use gateway_call(\"runbooks.search\") and the memory.* tools",
			"count", len(hits), "items", strings.Join(hits, "; "))
	}
	return hits
}
