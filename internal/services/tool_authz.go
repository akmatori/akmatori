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
	BuiltinRunbooks:  false,
	BuiltinMemory:    false,
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

// IncidentRunAllowlist is the allowlist of every incident run (alert, Slack,
// API request): all enabled connector tools plus the incident built-ins. A
// lookup error fails closed: a non-nil empty slice rejects every call.
func IncidentRunAllowlist(db *gorm.DB) []ToolAllowlistEntry {
	entries, ok := connectorToolEntries(db)
	if !ok {
		return []ToolAllowlistEntry{}
	}
	return append(entries, builtinEntries(incidentRunBuiltins)...)
}

// CronAllowlist is the allowlist of one cron run: all enabled connector tools
// when ToolsEnabled, plus the job's builtin_tools. A lookup error fails closed.
func CronAllowlist(db *gorm.DB, job *database.CronJob) []ToolAllowlistEntry {
	entries := make([]ToolAllowlistEntry, 0)
	if job.ToolsEnabled {
		conn, ok := connectorToolEntries(db)
		if !ok {
			return []ToolAllowlistEntry{}
		}
		entries = append(entries, conn...)
	}
	return append(entries, builtinEntries(job.BuiltinTools)...)
}
