# Plan: Ad-hoc SSH Connections

## Context

Currently, the SSH tool can only connect to servers pre-configured in `ssh_hosts` array within tool instance settings. During incidents, the agent often needs to SSH into servers not in that list (e.g., a Zabbix alert mentions `kesa-hw-edge-gc30.fe.gc.onl` but only `lux-hw-edge-preprod-gc26` is configured). The agent gets `"Server not configured: ..."` and is stuck.

**Goal**: Allow SSH tool instances to optionally connect to any server using default credentials (default SSH key, configurable user/port), so the agent can investigate incidents on servers that weren't pre-configured.

## Changes

### Task 1: MCP Gateway - Core SSH Logic

**File: `mcp-gateway/internal/tools/ssh/ssh.go`**

- [x] Add ad-hoc connection fields to `SSHConfig` struct
- [x] Update `getConfig()` to parse new settings and relax ssh_hosts requirement
- [x] Extract `resolveTargetHosts()` method with ad-hoc fallback logic
- [x] Update `ExecuteCommand()` to use `resolveTargetHosts()`
- [x] Update `TestConnectivity()` to accept `servers` parameter and use `resolveTargetHosts()`

### Task 2: MCP Gateway - Schema

**File: `mcp-gateway/internal/tools/schemas.go`**

- [ ] Remove `ssh_hosts` from Required and MinItems constraint
- [ ] Add 4 new ad-hoc properties to SSH schema
- [ ] Update `test_connectivity` function description for servers parameter

### Task 3: MCP Gateway - Tool Registration

**File: `mcp-gateway/internal/tools/registry.go`**

- [ ] Add `servers` parameter to `ssh.test_connectivity` InputSchema and handler

### Task 4: Python Wrapper

**File: `agent-worker/tools/ssh/__init__.py`**

- [ ] Add `servers` parameter to `test_connectivity()` function

### Task 5: SKILL.md Generation

**File: `internal/services/skill_prompt_service.go`**

- [ ] Update `extractToolDetails()` to note ad-hoc connections when enabled
- [ ] Update `generateToolUsageExample()` to include ad-hoc example when enabled

### Task 6: Tests

**File: `mcp-gateway/internal/tools/ssh/ssh_test.go`**

- [ ] Add tests for `resolveTargetHosts()` (ad-hoc enabled/disabled, configured precedence, mixed servers, write commands, empty servers)

**File: `internal/services/skill_prompt_service_test.go`** (if exists)

- [ ] Add tests for ad-hoc note in `extractToolDetails` and ad-hoc example in `generateToolUsageExample`

## Key Design Decisions

- **Opt-in**: `allow_adhoc_connections` defaults to `false` — no behavior change for existing users
- **Default key**: Ad-hoc hosts use the default SSH key (no `KeyID` set → `getKeyForHost()` falls through naturally)
- **No jumphost for ad-hoc**: Direct connections only — if a jumphost is needed, pre-configure the host
- **Read-only by default**: `adhoc_allow_write_commands` defaults to `false`
- **Server name = address**: For ad-hoc hosts, the server string from the agent is used as both display hostname and connection address (agents pass FQDNs or IPs)
- **Frontend**: No frontend code changes needed — the schema-driven form in `ToolFormSection.tsx` will automatically render the new fields based on the updated schema

## Verification

1. `make test-mcp` — run MCP gateway tests
2. `make test` — run all Go tests
3. `make verify` — go vet + all tests
4. Docker rebuild: `docker-compose build mcp-gateway && docker-compose build akmatori-api`
