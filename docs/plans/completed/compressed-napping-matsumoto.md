# Plan: Ad-hoc SSH Connections

## Context

Currently, the SSH tool can only connect to servers pre-configured in `ssh_hosts` array within tool instance settings. During incidents, the agent often needs to SSH into servers not in that list (e.g., a Zabbix alert mentions `kesa-hw-edge-gc30.fe.gc.onl` but only `lux-hw-edge-preprod-gc26` is configured). The agent gets `"Server not configured: ..."` and is stuck.

**Goal**: Allow SSH tool instances to optionally connect to any server using default credentials (default SSH key, configurable user/port), so the agent can investigate incidents on servers that weren't pre-configured.

## Changes

### Phase 1: MCP Gateway - Core SSH Logic

**File: `mcp-gateway/internal/tools/ssh/ssh.go`**

1. **Add fields to `SSHConfig` struct** (line 50):
   ```go
   AllowAdhocConnections   bool
   AdhocDefaultUser        string  // default: "root"
   AdhocDefaultPort        int     // default: 22
   AdhocAllowWriteCommands bool    // default: false
   ```

2. **Update `getConfig()`** (after line 176, before ssh_hosts parsing):
   - Parse new settings: `allow_adhoc_connections`, `adhoc_default_user`, `adhoc_default_port`, `adhoc_allow_write_commands`
   - **Relax the ssh_hosts requirement** (line 180): Only error when `ssh_hosts` is empty AND `AllowAdhocConnections` is false. Guard the host parsing loop.

3. **Extract `resolveTargetHosts()` method** from inline logic in `ExecuteCommand` (lines 586-606):
   - When a requested server is found in `config.Hosts` → use its config (existing behavior)
   - When NOT found AND `AllowAdhocConnections=true` → create temporary `SSHHostConfig{Hostname: s, Address: s, User: AdhocDefaultUser, Port: AdhocDefaultPort, AllowWriteCommands: AdhocAllowWriteCommands}` (no KeyID → falls through to default key in `getKeyForHost()`)
   - When NOT found AND `AllowAdhocConnections=false` → error as before

4. **Update `ExecuteCommand()`** (line 578): Relax `len(config.Hosts) == 0` check when ad-hoc is enabled. Use `resolveTargetHosts()`.

5. **Update `TestConnectivity()`** (line 638): Add `servers []string` parameter. Use `resolveTargetHosts()` to resolve which hosts to test (allows testing ad-hoc servers). When `servers` is empty, test all configured hosts (existing behavior).

### Phase 2: MCP Gateway - Schema

**File: `mcp-gateway/internal/tools/schemas.go`**

1. **Remove `"ssh_hosts"` from `Required`** (line 78) — hosts are no longer required when ad-hoc is enabled
2. **Remove `MinItems` from `ssh_hosts` property** (line 116)
3. **Add 4 new properties** to SSH schema:
   - `allow_adhoc_connections` (boolean, default false, description: "Allow connecting to servers not in SSH Hosts using default credentials")
   - `adhoc_default_user` (string, default "root", advanced)
   - `adhoc_default_port` (integer, default 22, advanced, min 1, max 65535)
   - `adhoc_allow_write_commands` (boolean, default false, advanced, with warning)
4. **Update function descriptions** for `test_connectivity` to mention servers parameter

### Phase 3: MCP Gateway - Tool Registration

**File: `mcp-gateway/internal/tools/registry.go`**

1. **Update `ssh.test_connectivity` registration** (lines 117-132): Add `servers` parameter to InputSchema and handler, pass to `sshTool.TestConnectivity(ctx, incidentID, servers, instanceID)`

### Phase 4: Python Wrapper

**File: `agent-worker/tools/ssh/__init__.py`**

1. **Add `servers` parameter** to `test_connectivity()` function

### Phase 5: SKILL.md Generation

**File: `internal/services/skill_prompt_service.go`**

1. **Update `extractToolDetails()`** (line 229): When `allow_adhoc_connections` is true, append note: "Ad-hoc connections enabled: You can SSH into ANY server by hostname/IP, not just the configured hosts."
2. **Update `generateToolUsageExample()`** (line 181): When ad-hoc enabled, add example: `execute_command("uptime", servers=["any-server.example.com"], tool_instance_id=N)`

### Phase 6: Tests

**File: `mcp-gateway/internal/tools/ssh/ssh_test.go`**

Add tests for `resolveTargetHosts()`:
- Ad-hoc enabled + unconfigured server → returns valid HostConfig with defaults
- Ad-hoc disabled + unconfigured server → returns error
- Configured host takes precedence over ad-hoc defaults
- Mixed configured + unconfigured servers
- Ad-hoc write commands setting applied correctly
- Empty servers list with ad-hoc enabled + no configured hosts → error (must specify servers)

**File: `internal/services/skill_prompt_service_test.go`** (if exists)

- `extractToolDetails` includes ad-hoc note when enabled
- `generateToolUsageExample` includes ad-hoc example when enabled

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
