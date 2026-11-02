package services

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
)

// ConnectedToolsSkillName is the generated system skill that lists every tool
// the agent may call through gateway_call. It replaces the per-skill
// "Assigned Tools" sections: tools are not mapped to skills any more.
const ConnectedToolsSkillName = "connected-tools"

const connectedToolsDescription = "Every tool the agent may call through gateway_call, with parameter schemas and examples. Read before the first infrastructure call."

// ErrGeneratedSkill is returned when an operator tries to edit or disable the
// generated connected-tools skill.
var ErrGeneratedSkill = errors.New("connected-tools is generated from the enabled connectors and cannot be edited")

// GatewayToolLister lists the tools the gateway registered for a namespace.
// Satisfied by *MCPGatewayClient. Optional: without it, MCP servers and HTTP
// connectors render a discovery hint instead of their functions.
type GatewayToolLister interface {
	NamespaceTools(ctx context.Context, namespace string) ([]GatewayTool, error)
}

// SetGatewayToolLister wires the gateway client used to render MCP server and
// HTTP connector functions into connected-tools/SKILL.md.
func (s *SkillService) SetGatewayToolLister(l GatewayToolLister) {
	s.gatewayTools = l
}

// IsGeneratedSkill reports whether name is a skill whose body is generated.
func IsGeneratedSkill(name string) bool {
	return name == ConnectedToolsSkillName
}

// EnsureConnectedToolsSkill seeds the connected-tools system skill row. An
// operator skill that already uses the name is renamed to the first free
// "connected-tools-N" (row and directory) with a warning.
func (s *SkillService) EnsureConnectedToolsSkill() error {
	var existing database.Skill
	err := s.db.Where("name = ?", ConnectedToolsSkillName).First(&existing).Error
	switch {
	case err == nil && existing.IsSystem:
		if !existing.Enabled {
			return s.db.Model(&existing).Update("enabled", true).Error
		}
		return nil
	case err == nil:
		if err := s.renameCollidingSkill(existing); err != nil {
			return err
		}
	case !errors.Is(err, gorm.ErrRecordNotFound):
		return fmt.Errorf("lookup %s skill: %w", ConnectedToolsSkillName, err)
	}
	row := &database.Skill{
		Name:        ConnectedToolsSkillName,
		Description: connectedToolsDescription,
		Category:    "system",
		IsSystem:    true,
		Enabled:     true,
	}
	if err := s.db.Create(row).Error; err != nil {
		return fmt.Errorf("seed %s skill: %w", ConnectedToolsSkillName, err)
	}
	slog.Info("seeded system skill", "skill", ConnectedToolsSkillName)
	return nil
}

func (s *SkillService) renameCollidingSkill(sk database.Skill) error {
	oldName := sk.Name
	newName := ""
	for i := 1; i < 100; i++ {
		c := fmt.Sprintf("%s-%d", ConnectedToolsSkillName, i)
		var n int64
		if err := s.db.Model(&database.Skill{}).Where("name = ?", c).Count(&n).Error; err != nil {
			return err
		}
		if _, statErr := os.Stat(s.GetSkillDir(c)); n == 0 && os.IsNotExist(statErr) {
			newName = c
			break
		}
	}
	if newName == "" {
		return fmt.Errorf("no free name to rename the operator skill %q", oldName)
	}
	if err := s.db.Model(&database.Skill{}).Where("id = ?", sk.ID).Update("name", newName).Error; err != nil {
		return fmt.Errorf("rename operator skill %s: %w", oldName, err)
	}
	oldDir := s.GetSkillDir(oldName)
	if _, err := os.Stat(oldDir); err == nil {
		if err := os.Rename(oldDir, s.GetSkillDir(newName)); err != nil {
			slog.Warn("failed to move renamed skill directory", "from", oldDir, "err", err)
		}
	}
	slog.Warn("renamed operator skill: the name is reserved for the generated system skill",
		"old", oldName, "new", newName)
	return nil
}

// RegenerateConnectedToolsSkill renders connected-tools/SKILL.md from every
// enabled tool instance. The file is written only when its content changed.
func (s *SkillService) RegenerateConnectedToolsSkill() error {
	var instances []database.ToolInstance
	if err := s.db.Preload("ToolType").Where("enabled = ?", true).Order("id ASC").Find(&instances).Error; err != nil {
		return fmt.Errorf("list tool instances: %w", err)
	}
	gatewayTools := map[string][]GatewayTool{}
	if s.gatewayTools != nil {
		for _, ti := range instances {
			ns := ti.ToolType.Name
			if _, done := gatewayTools[ns]; done || ns == "" || IsBuiltinToolNamespace(ns) || hasHandWrittenToolExample(ns) {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			tools, err := s.gatewayTools.NamespaceTools(ctx, ns)
			cancel()
			if err != nil {
				slog.Warn("connected-tools: gateway tool list failed, using the discovery hint", "namespace", ns, "err", err)
				continue
			}
			gatewayTools[ns] = tools
		}
	}
	content := generateConnectedToolsSkillMd(instances, gatewayTools)

	dir := s.GetSkillDir(ConnectedToolsSkillName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create %s dir: %w", ConnectedToolsSkillName, err)
	}
	path := filepath.Join(dir, "SKILL.md")
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, []byte(content)) {
		return nil
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// StartConnectedToolsRefresh re-renders connected-tools/SKILL.md every
// interval, so MCP server functions discovered after a reload reach the file.
func (s *SkillService) StartConnectedToolsRefresh(ctx context.Context, interval time.Duration) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := s.RegenerateConnectedToolsSkill(); err != nil {
					slog.Warn("connected-tools refresh failed", "err", err)
				}
			}
		}
	}()
}

// hasHandWrittenToolExample reports whether generateToolUsageExample has a
// hand-written example for the tool type.
func hasHandWrittenToolExample(toolType string) bool {
	switch toolType {
	case "ssh", "zabbix", "victoria_metrics", "postgresql", "grafana", "catchpoint",
		"pagerduty", "clickhouse", "netbox", "kubernetes", "jira":
		return true
	}
	return false
}

// generateConnectedToolsSkillMd renders the connected-tools SKILL.md.
func generateConnectedToolsSkillMd(instances []database.ToolInstance, gatewayTools map[string][]GatewayTool) string {
	fm, err := yaml.Marshal(SkillFrontmatter{
		Name:        ConnectedToolsSkillName,
		Description: connectedToolsDescription,
		Metadata:    map[string]string{"short-description": "Tools available through gateway_call"},
	})
	if err != nil {
		fm = []byte("name: " + ConnectedToolsSkillName + "\n")
	}

	byType := map[string][]database.ToolInstance{}
	var types []string
	for _, ti := range instances {
		tt := ti.ToolType.Name
		if tt == "" || IsBuiltinToolNamespace(tt) {
			continue
		}
		if _, ok := byType[tt]; !ok {
			types = append(types, tt)
		}
		byType[tt] = append(byType[tt], ti)
	}
	sort.Strings(types)

	var b strings.Builder
	b.WriteString("---\n")
	b.Write(fm)
	b.WriteString("---\n\n# Connected tools\n\n")
	b.WriteString("Every tool you may call through `gateway_call`, grouped by tool type. Tools disabled in Connectors are not listed and are refused by the gateway.\n\n")
	b.WriteString("- Use the examples below directly. Call `list_tools_for_tool_type` or `get_tool_detail` only for a tool that is not listed here.\n")
	b.WriteString("- When a tool type has several instances, pass the instance's `logical_name` as the third `gateway_call` argument.\n")
	b.WriteString("- Use `execute_script` with its built-in `gateway_call()` for batch operations.\n")

	if len(types) == 0 {
		b.WriteString("\nNo connectors are enabled. Only the built-in tools below are available.\n")
	}
	for _, tt := range types {
		fmt.Fprintf(&b, "\n## %s\n", tt)
		group := byType[tt]
		if len(group) > 1 {
			names := make([]string, 0, len(group))
			for _, ti := range group {
				names = append(names, "`"+toolLogicalName(ti)+"`")
			}
			fmt.Fprintf(&b, "\n%d instances: %s. Pass the one you mean as `logical_name`.\n", len(group), strings.Join(names, ", "))
		}
		for _, ti := range group {
			fmt.Fprintf(&b, "\n### %s (logical_name: \"%s\", type: %s)\n", ti.Name, toolLogicalName(ti), tt)
			if details := extractToolDetails(ti); details != "" {
				b.WriteString(details)
			}
			if fns, ok := gatewayTools[tt]; ok && !hasHandWrittenToolExample(tt) {
				b.WriteString(renderGatewayFunctions(tt, toolLogicalName(ti), fns))
				continue
			}
			b.WriteString(generateToolUsageExample(ti))
		}
	}

	b.WriteString("\n## Built-in tools\n\nServed by Akmatori itself; no logical_name needed. A run gets only the built-ins its kind allows; the gateway refuses the others.\n")
	for _, ns := range BuiltinToolNamespaces {
		if !builtinToolLive[ns] || ns == BuiltinProposals {
			continue
		}
		fmt.Fprintf(&b, "\n### %s\n", ns)
		b.WriteString(generateToolUsageExample(database.ToolInstance{
			Name: ns, LogicalName: ns, ToolType: database.ToolType{Name: ns},
		}))
	}
	return b.String()
}

func toolLogicalName(ti database.ToolInstance) string {
	if ti.LogicalName != "" {
		return ti.LogicalName
	}
	return ti.Name
}

// maxRenderedFunctions caps the functions listed per instance; the rest are
// reachable through list_tools_for_tool_type.
const maxRenderedFunctions = 60

// renderGatewayFunctions lists the functions of an MCP server or HTTP
// connector with their parameters.
func renderGatewayFunctions(namespace, logicalName string, fns []GatewayTool) string {
	if len(fns) == 0 {
		return fmt.Sprintf("No functions are registered right now (the server may be disconnected). List them with `list_tools_for_tool_type(\"%s\")`.\n", namespace)
	}
	var b strings.Builder
	b.WriteString("\nFunctions (`*` = required):\n")
	for i, fn := range fns {
		if i == maxRenderedFunctions {
			fmt.Fprintf(&b, "- … %d more: `list_tools_for_tool_type(\"%s\")`\n", len(fns)-i, namespace)
			break
		}
		required := map[string]bool{}
		for _, r := range fn.InputSchema.Required {
			required[r] = true
		}
		params := make([]string, 0, len(fn.InputSchema.Properties))
		for name, p := range fn.InputSchema.Properties {
			if name == "logical_name" || name == "tool_instance_id" {
				continue
			}
			mark := ""
			if required[name] {
				mark = "*"
			}
			params = append(params, fmt.Sprintf("%s%s: %s", name, mark, p.Type))
		}
		sort.Strings(params)
		desc := firstLine(fn.Description, 160)
		if desc != "" {
			desc = " — " + desc
		}
		fmt.Fprintf(&b, "- `%s(%s)`%s\n", fn.Name, strings.Join(params, ", "), desc)
	}
	fmt.Fprintf(&b, "\nCall: `gateway_call(\"%s.<function>\", {<args>}, \"%s\")`. Full schema: `get_tool_detail(\"%s.<function>\")`.\n", namespace, logicalName, namespace)
	return b.String()
}

// firstLine returns the first line of s, cut to max runes.
func firstLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	r := []rune(s)
	if len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return s
}
