package tools

import (
	"context"

	"github.com/akmatori/mcp-gateway/internal/database"
	"github.com/akmatori/mcp-gateway/internal/mcp"
	"github.com/akmatori/mcp-gateway/internal/tools/knowledge"
)

// memoryWriteDescription is the agent's whole guide to recording findings.
const memoryWriteDescription = `Record a durable finding as a memory, or update the memory that already covers the subject (same scope + name = update). Rules:
- Search first (memory.search) and update the memory that covers the subject instead of adding a new name. Correct or delete (memory.delete) a memory that new facts prove wrong.
- Save only what future investigations need and cannot get elsewhere: host quirks, recurring causes, tool behaviour, operator preferences. Never save what runbooks, alerts or context files already say, and never save one incident's timeline.
- Absolute dates only (2026-10-14, not "yesterday").
- type "fact" = knowledge about the environment; type "feedback" = an operator instruction about how to work. A feedback body is: the rule, then "**Why:**" and "**How to apply:**" lines.
- description: one line, at most 500 bytes (longer is cut). Put details in the body (at most 8 KiB).
- scope: "global", or the name of the skill the memory belongs to. name: lowercase slug (a-z, 0-9, hyphens), not starting with "file-".
- Operator-authored memories and context-file summaries are protected: the write answers action "refused_protected" and changes nothing.
- claim: "factual" (what is: config, tool behaviour, what you saw) or "causal" (X caused Y). A causal claim is stored as evidence "unverified": only an operator can confirm a cause. Put the start time of the cause and of the effect in evidence_note, and check that the cause came first; an effect that started later is not the cause. Causal wording ("caused by", "because", "due to", "root cause") marks the claim causal.
- evidence: "observed" (default for a factual claim) or "unverified". "confirmed" is for operators and is stored as unverified.
Example: {"scope": "global", "name": "nginx-edge-03-flaky-nic", "type": "fact", "claim": "factual", "description": "nginx-edge-03 eth1 logs NETDEV WATCHDOG under load", "body": "Seen 2026-09-14 and 2026-10-02: NETDEV WATCHDOG on eth1 at the start of each 502 burst on the edge. Failing traffic over to nginx-edge-04 ended both bursts.", "evidence_note": "kern.log 2026-10-02 10:02 UTC; 502s from 10:03 UTC"}
Returns {id, scope, name, action: created | updated | unchanged | refused_protected, evidence, claim}.`

// registerKnowledgeTools registers runbooks.* and memory.*: lexical search
// and memory reads and writes on the gateway's own DB connection.
func (r *Registry) registerKnowledgeTools() {
	r.knowledgeTool = knowledge.NewTool(database.DB, r.logger)
	kt := r.knowledgeTool

	r.server.RegisterTool(mcp.Tool{
		Name:        "runbooks.search",
		Description: "Search the operator runbooks (standard procedures) by keywords, ranked by relevance (BM25 over title and content). Pass the original alert text, or a host, service or error string. Returns id, title and an excerpt; read the full runbook with runbooks.get.",
		InputSchema: mcp.InputSchema{Type: "object", Properties: map[string]mcp.Property{
			"query": {Type: "string", Description: "Alert text, host, service or error string (required)"},
			"limit": {Type: "integer", Description: "Maximum results (default 5, max 20)"},
		}, Required: []string{"query"}},
	}, func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
		return kt.RunbooksSearch(ctx, incidentID, args)
	})

	r.server.RegisterTool(mcp.Tool{
		Name:        "runbooks.get",
		Description: "Read one runbook in full by id (from runbooks.search).",
		InputSchema: mcp.InputSchema{Type: "object", Properties: map[string]mcp.Property{
			"id": {Type: "integer", Description: "Runbook id (required)"},
		}, Required: []string{"id"}},
	}, func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
		return kt.RunbooksGet(ctx, incidentID, args)
	})

	r.server.RegisterTool(mcp.Tool{
		Name:        "memory.search",
		Description: "Search cross-incident memories (facts about the environment and operator feedback) by keywords, ranked by relevance. Name and description weigh more than the body. Returns id, scope, name, type, description and an excerpt; read one with memory.get.",
		InputSchema: mcp.InputSchema{Type: "object", Properties: map[string]mcp.Property{
			"query":  {Type: "string", Description: "Alert text, host, service, symptom or topic (required)"},
			"scopes": {Type: "array", Items: &mcp.Items{Type: "string"}, Description: "Scopes to search, e.g. [\"<skill>\", \"global\"] (default: all)"},
			"type":   {Type: "string", Description: "fact or feedback (default: both)", Enum: []string{"fact", "feedback"}},
			"limit":  {Type: "integer", Description: "Maximum results (default 5, max 20)"},
		}, Required: []string{"query"}},
	}, func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
		return kt.MemorySearch(ctx, incidentID, args)
	})

	r.server.RegisterTool(mcp.Tool{
		Name:        "memory.list",
		Description: "List memories newest first (no bodies), for maintenance: filter by scopes, type and since (updated at or after), and page with next_cursor until it is empty.",
		InputSchema: mcp.InputSchema{Type: "object", Properties: map[string]mcp.Property{
			"scopes":   {Type: "array", Items: &mcp.Items{Type: "string"}, Description: "Scopes to list (default: all)"},
			"type":     {Type: "string", Description: "fact or feedback", Enum: []string{"fact", "feedback"}},
			"evidence": {Type: "string", Description: "confirmed, observed or unverified", Enum: []string{"confirmed", "observed", "unverified"}},
			"since":    {Type: "string", Description: "Only memories updated at or after this time: ISO-8601 or Unix seconds"},
			"limit":    {Type: "integer", Description: "Page size (default 50, max 200)"},
			"cursor":   {Type: "string", Description: "next_cursor of the previous page"},
		}},
	}, func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
		return kt.MemoryList(ctx, incidentID, args)
	})

	r.server.RegisterTool(mcp.Tool{
		Name:        "memory.get",
		Description: "Read one memory in full, by id or by scope + name. references lists its [[...]] links: memories (scope, name, id, exists) and context files (path under /akmatori/context/).",
		InputSchema: mcp.InputSchema{Type: "object", Properties: map[string]mcp.Property{
			"id":    {Type: "integer", Description: "Memory id"},
			"scope": {Type: "string", Description: "Scope (with name)"},
			"name":  {Type: "string", Description: "Name (with scope)"},
		}},
	}, func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
		return kt.MemoryGet(ctx, incidentID, args)
	})

	r.server.RegisterTool(mcp.Tool{
		Name:        "memory.write",
		Description: memoryWriteDescription,
		InputSchema: mcp.InputSchema{Type: "object", Properties: map[string]mcp.Property{
			"scope":         {Type: "string", Description: "\"global\" or a skill name (required)"},
			"name":          {Type: "string", Description: "Lowercase slug, unique in the scope (required)"},
			"type":          {Type: "string", Description: "fact or feedback (required)", Enum: []string{"fact", "feedback"}},
			"description":   {Type: "string", Description: "One line, at most 500 bytes (required)"},
			"body":          {Type: "string", Description: "The details, at most 8 KiB (required)"},
			"claim":         {Type: "string", Description: "factual (default) or causal", Enum: []string{"factual", "causal"}},
			"evidence":      {Type: "string", Description: "observed (default for factual) or unverified; causal claims are always unverified", Enum: []string{"observed", "unverified"}},
			"evidence_note": {Type: "string", Description: "What supports the memory: tool output, timestamps of cause and effect, at most 500 bytes"},
		}, Required: []string{"scope", "name", "type", "description", "body"}},
	}, func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
		return kt.MemoryWrite(ctx, incidentID, args)
	})

	r.server.RegisterTool(mcp.Tool{
		Name:        "memory.delete",
		Description: "Delete a memory that new facts prove wrong or that another memory now covers. Protected memories (operator-authored, context-file summaries) are not deleted (action refused_protected).",
		InputSchema: mcp.InputSchema{Type: "object", Properties: map[string]mcp.Property{
			"scope": {Type: "string", Description: "Scope (required)"},
			"name":  {Type: "string", Description: "Name (required)"},
		}, Required: []string{"scope", "name"}},
	}, func(ctx context.Context, incidentID string, args map[string]interface{}) (interface{}, error) {
		return kt.MemoryDelete(ctx, incidentID, args)
	})
}
