# Design: Replace Custom Tools with Python Script Wrappers

## Problem

The current pi-mono integration registers 11 MCP tool definitions as `customTools` on every agent session. Every tool's full schema (name, description, parameters with TypeBox types) is serialized into the LLM context — even when a skill only needs SSH or only needs Zabbix. This wastes context tokens and makes tool selection noisier.

The original codex-worker architecture solved this differently: Python script wrappers lived in each skill's `scripts/` directory and were called on-demand via bash. Tools only appeared in context through SKILL.md instructions, not as registered tool schemas.

## Decision

Replace `customTools` with Python script wrappers that call the MCP Gateway over HTTP. The agent uses pi-mono's built-in `bashTool` to execute Python code. Tool knowledge is conveyed through SKILL.md usage examples loaded on-demand by pi-mono's skill system.

## Architecture

### Data Flow

```
Alert → API creates incident → WS new_incident + enabled_skills
  → Agent-worker creates session:
    - DefaultResourceLoader discovers enabled SKILL.md files
    - spawnHook injects MCP_GATEWAY_URL, INCIDENT_ID, PYTHONPATH=/tools
    - NO customTools registered
  → pi-mono reads SKILL.md → sees Python usage examples
  → Agent calls bash: python3 -c "from ssh import execute_command; ..."
  → Python → mcp_client.py → HTTP POST to MCP Gateway
  → MCP Gateway resolves credentials by instance ID → executes tool
  → Result returned to agent via bash stdout
```

### Python Script Wrappers

Location: `agent-worker/tools/`

```
agent-worker/tools/
├── mcp_client.py          # MCP Gateway HTTP client (JSON-RPC 2.0)
├── ssh/
│   └── __init__.py        # execute_command(), test_connectivity(), get_server_info()
└── zabbix/
    └── __init__.py        # get_hosts(), get_problems(), get_history(), get_items_batch(), acknowledge_event()
```

Each wrapper:
- Imports `mcp_client.call(tool_name, args)` to communicate with the MCP Gateway
- Provides typed Python functions matching MCP Gateway tool names
- Accepts `tool_instance_id` as an optional kwarg for instance routing
- Reads `MCP_GATEWAY_URL` and `INCIDENT_ID` from environment variables

Restored from git history (`fe6fac5:codex-tools/`) and adapted to include `tool_instance_id` support.

### SKILL.md Tool Usage Instructions

`generateSkillMd()` in `skill_service.go` writes concrete Python usage examples per tool instance:

```markdown
## Assigned Tools

### Production hosts (ID: 3, type: ssh)
Configured hosts: edu-fleu-hel1-1, litenetwork-n2

Usage (via bash tool):
\`\`\`python
from ssh import execute_command, test_connectivity, get_server_info

result = execute_command("uptime", tool_instance_id=3)
result = test_connectivity(tool_instance_id=3)
result = get_server_info(tool_instance_id=3)
\`\`\`

### Zabbix (ID: 2, type: zabbix)

Usage (via bash tool):
\`\`\`python
from zabbix import get_hosts, get_problems, get_history, get_items_batch, acknowledge_event

result = get_hosts(tool_instance_id=2)
result = get_problems(severity_min=3, tool_instance_id=2)
result = get_items_batch(searches=["cpu", "memory"], tool_instance_id=2)
\`\`\`
```

### Agent Worker Environment Setup

Use pi-mono's `spawnHook` on `createBashTool()` to inject environment variables per-session (no global `process.env` mutation):

```typescript
const bashTool = createBashTool(params.workDir, {
  spawnHook: (ctx) => ({
    ...ctx,
    env: {
      ...ctx.env,
      MCP_GATEWAY_URL: this.mcpGatewayUrl,
      INCIDENT_ID: params.incidentId,
      PYTHONPATH: `/tools:${ctx.env.PYTHONPATH || ""}`,
    },
  }),
});
```

Pass `tools: [bashTool, ...otherCodingTools]` to `createAgentSession()` instead of `tools: createCodingTools(params.workDir)`.

### Skill Symlinks

The Go API's `RegenerateAllSkillMds()` already creates symlinks:
```
skills/linux-agent/scripts/ssh → /tools/ssh
skills/linux-agent/scripts/mcp_client.py → /tools/mcp_client.py
```

`/tools/` is inside the agent-worker container, copied from `agent-worker/tools/` via Dockerfile.

## What Gets Removed

- `agent-worker/src/tools/mcp-tools.ts` — all 11 TypeScript tool definitions
- `agent-worker/src/tools/mcp-client.ts` — TypeScript MCP Gateway HTTP client
- `customTools: mcpTools` from `createAgentSession()` in `agent-runner.ts`
- `createMCPTools()` import and invocation
- Related tests in `agent-worker/tests/tools.test.ts`

## What Gets Added

- `agent-worker/tools/mcp_client.py` — Python MCP Gateway client
- `agent-worker/tools/ssh/__init__.py` — SSH wrapper functions
- `agent-worker/tools/zabbix/__init__.py` — Zabbix wrapper functions
- `COPY tools/ /tools/` in `agent-worker/Dockerfile`
- `spawnHook` on `createBashTool()` in `agent-runner.ts`
- Enriched SKILL.md template in `skill_service.go` with Python usage examples

## What Stays the Same

- MCP Gateway (Go) — no changes, still serves JSON-RPC 2.0
- `tool_instance_id` routing — passes through Python kwargs instead of TypeBox params
- `enabled_skills` filtering — unchanged
- Skill symlink creation in `RegenerateAllSkillMds()` — unchanged
- Tool type validation in `GetToolCredentialsByInstanceID()` — unchanged

## Files to Modify

| File | Change |
|------|--------|
| `agent-worker/tools/mcp_client.py` | New: Python MCP Gateway client |
| `agent-worker/tools/ssh/__init__.py` | New: SSH wrapper with tool_instance_id |
| `agent-worker/tools/zabbix/__init__.py` | New: Zabbix wrapper with tool_instance_id |
| `agent-worker/Dockerfile` | Add `COPY tools/ /tools/` |
| `agent-worker/src/agent-runner.ts` | Replace customTools with spawnHook, remove MCP imports |
| `agent-worker/src/tools/mcp-tools.ts` | Delete |
| `agent-worker/src/tools/mcp-client.ts` | Delete |
| `internal/services/skill_service.go` | Enrich generateSkillMd() with Python usage examples |
| `internal/services/skill_service_test.go` | Update tests for new SKILL.md format |
| `agent-worker/tests/tools.test.ts` | Remove MCP tool tests, add wrapper validation |

## Verification

1. `make test` — Go tests pass (SKILL.md format changes)
2. `make test-agent` — agent-worker tests pass
3. `make test-mcp` — MCP Gateway tests unchanged
4. Rebuild containers: `docker-compose build && docker-compose up -d`
5. Verify SKILL.md: `cat akmatori_data/skills/linux-agent/SKILL.md` shows Python examples
6. Trigger test incident, verify agent uses `python3 -c "from ssh import ..."` in bash
7. Verify tool results return correctly through bash stdout
