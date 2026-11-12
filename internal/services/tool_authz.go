package services

import (
	"log/slog"
	"sort"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/gorm"
)

// Tool authorization policy (see .claude/rules/tool-authorization.md).
//
// Tool instances are of two kinds:
//   - connector tools: every tool type that is not a built-in. They appear on
//     the Connectors page and are authorized per instance.
//   - built-ins: credential-less namespaces served by the gateway itself. They
//     never appear as connectors and are authorized with a type-only entry.
//
// Every enabled connector tool is available to every incident run. There is no
// per-skill mapping: a skill does not restrict tools.

// Built-in tool namespaces.
const (
	BuiltinIncidents = "incidents"
	BuiltinProposals = "proposals"
	BuiltinEvents    = EventsToolType
	BuiltinRunbooks  = "runbooks"
	BuiltinMemory    = "memory"
)

// BuiltinToolNamespaces lists every built-in namespace, in display order.
var BuiltinToolNamespaces = []string{
	BuiltinEvents, BuiltinRunbooks, BuiltinMemory, BuiltinIncidents, BuiltinProposals,
}

// builtinToolLive tells which built-ins the gateway serves in this release.
// A name that is accepted but not live is stored on a cron job and left out
// of the allowlist.
var builtinToolLive = map[string]bool{
	BuiltinIncidents: true,
	BuiltinProposals: true,
	BuiltinEvents:    true,
	BuiltinRunbooks:  true,
	BuiltinMemory:    true,
}

// ProposalChatBuiltins are the built-ins of a proposal chat run. The gateway
// refuses memory writes from proposal runs: changes go through proposals.
var ProposalChatBuiltins = []string{BuiltinIncidents, BuiltinProposals, BuiltinRunbooks, BuiltinMemory}

// ProposalChatAllowlist is the allowlist of a proposal chat run.
func ProposalChatAllowlist() []ToolAllowlistEntry {
	return builtinEntries(ProposalChatBuiltins)
}

// IsBuiltinToolNamespace reports whether name is a built-in namespace.
func IsBuiltinToolNamespace(name string) bool {
	_, ok := builtinToolLive[name]
	return ok
}

// incidentRunBuiltins are the built-ins every incident run gets. proposals is
// never granted to incident runs.
var incidentRunBuiltins = []string{BuiltinEvents, BuiltinIncidents, BuiltinRunbooks, BuiltinMemory}

// DefaultCronBuiltinTools is the builtin_tools default for operator crons.
var DefaultCronBuiltinTools = []string{BuiltinEvents, BuiltinRunbooks, BuiltinMemory}

// NormalizeBuiltinTools validates, deduplicates and orders a builtin_tools
// list. An unknown name returns ok=false with that name.
func NormalizeBuiltinTools(names []string) ([]string, string, bool) {
	seen := make(map[string]bool, len(names))
	for _, n := range names {
		if !IsBuiltinToolNamespace(n) {
			return nil, n, false
		}
		seen[n] = true
	}
	out := make([]string, 0, len(seen))
	for _, n := range BuiltinToolNamespaces {
		if seen[n] {
			out = append(out, n)
		}
	}
	return out, "", true
}

// builtinEntries maps built-in names to type-only allowlist entries, keeping
// only the live ones.
func builtinEntries(names []string) []ToolAllowlistEntry {
	entries := make([]ToolAllowlistEntry, 0, len(names))
	seen := map[string]bool{}
	for _, n := range names {
		if !builtinToolLive[n] || seen[n] {
			continue
		}
		seen[n] = true
		entries = append(entries, ToolAllowlistEntry{ToolType: n})
	}
	return entries
}

// connectorToolEntries returns every enabled connector tool instance, ordered
// by instance ID so the gateway's "first authorized instance" fallback is
// stable. ok=false on a lookup error.
func connectorToolEntries(db *gorm.DB) ([]ToolAllowlistEntry, bool) {
	var instances []database.ToolInstance
	if err := db.Preload("ToolType").Where("enabled = ?", true).Order("id ASC").Find(&instances).Error; err != nil {
		slog.Error("tool authorization: list tool instances", "error", err)
		return nil, false
	}
	entries := make([]ToolAllowlistEntry, 0, len(instances))
	for _, ti := range instances {
		name := ti.ToolType.Name
		if name == "" || IsBuiltinToolNamespace(name) {
			continue
		}
		entries = append(entries, ToolAllowlistEntry{InstanceID: ti.ID, LogicalName: ti.LogicalName, ToolType: name})
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].InstanceID < entries[j].InstanceID })
	return entries, true
}

// MessagingToolTelegram is the gateway namespace of telegram.list_chats and
// telegram.send_message. It is a connector tool without a tool instance: the
// gateway reads the bot token and chats of the Telegram connector, so the
// allowlist entry is type-only.
const MessagingToolTelegram = "telegram"

// TelegramToolAvailable reports whether the Telegram connector can serve the
// agent's telegram.* tools: an enabled Telegram integration with a bot token
// and at least one enabled chat that can post. A lookup error counts as not
// available.
func TelegramToolAvailable(db *gorm.DB) bool {
	var integrations []database.Integration
	if err := db.Where("provider = ? AND enabled = ?", database.MessagingProviderTelegram, true).Find(&integrations).Error; err != nil {
		slog.Error("tool authorization: list telegram integrations", "error", err)
		return false
	}
	ids := make([]uint, 0, len(integrations))
	for _, in := range integrations {
		if token, _ := in.Credentials["bot_token"].(string); token != "" {
			ids = append(ids, in.ID)
		}
	}
	if len(ids) == 0 {
		return false
	}
	var n int64
	if err := db.Model(&database.Channel{}).
		Where("integration_id IN ? AND enabled = ? AND can_post = ?", ids, true, true).
		Count(&n).Error; err != nil {
		slog.Error("tool authorization: count telegram chats", "error", err)
		return false
	}
	return n > 0
}

// messagingToolEntries returns the type-only entries of the messaging tools
// the connectors can serve now.
func messagingToolEntries(db *gorm.DB) []ToolAllowlistEntry {
	if TelegramToolAvailable(db) {
		return []ToolAllowlistEntry{{ToolType: MessagingToolTelegram}}
	}
	return nil
}

// IncidentRunAllowlist is the allowlist of every incident run (alert, Slack,
// API request): all enabled connector tools, the messaging tools, plus the
// incident built-ins. A lookup error fails closed: a non-nil empty slice
// rejects every call.
func IncidentRunAllowlist(db *gorm.DB) []ToolAllowlistEntry {
	entries, ok := connectorToolEntries(db)
	if !ok {
		return []ToolAllowlistEntry{}
	}
	entries = append(entries, messagingToolEntries(db)...)
	return append(entries, builtinEntries(incidentRunBuiltins)...)
}

// CronAllowlist is the allowlist of one cron run: all enabled connector tools
// and the messaging tools when ToolsEnabled, plus the job's builtin_tools. A
// lookup error fails closed.
func CronAllowlist(db *gorm.DB, job *database.CronJob) []ToolAllowlistEntry {
	entries := make([]ToolAllowlistEntry, 0)
	if job.ToolsEnabled {
		conn, ok := connectorToolEntries(db)
		if !ok {
			return []ToolAllowlistEntry{}
		}
		entries = append(entries, conn...)
		entries = append(entries, messagingToolEntries(db)...)
	}
	return append(entries, builtinEntries(job.BuiltinTools)...)
}
