# ClickHouse MCP Gateway Tool

## Overview

Add a ClickHouse tool to the MCP Gateway, providing read-only query execution and system diagnostics for OLAP incident investigation. Follows the established PostgreSQL tool pattern with ClickHouse-specific adaptations (HTTP protocol, ClickHouse SQL dialect, system tables for diagnostics).

## Context

- **Files involved:**
  - `mcp-gateway/internal/tools/postgresql/postgresql.go` — primary reference implementation
  - `mcp-gateway/internal/tools/registry.go` — tool registration
  - `mcp-gateway/internal/tools/schemas.go` — tool type schema definitions
  - `mcp-gateway/internal/tools/clickhouse/` — new package (to create)
- **Related patterns:** PostgreSQL tool (read-only queries, caching, rate limiting, injectable dependencies for testing)
- **Dependencies:** `github.com/ClickHouse/clickhouse-go/v2` (HTTP protocol driver, database/sql compatible)

## Development Approach

- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- Follow the PostgreSQL tool pattern closely for consistency
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Tool Methods

| Method | Purpose | Key Args |
|--------|---------|----------|
| `execute_query` | Run arbitrary SELECT queries | `query` (required), `limit`, `timeout_seconds` |
| `show_databases` | List databases | — |
| `show_tables` | List tables in a database | `database` (optional, defaults to configured DB) |
| `describe_table` | Column definitions and types | `table_name` (required), `database` |
| `get_query_log` | Recent queries from `system.query_log` | `min_duration_ms`, `limit`, `query_kind` |
| `get_running_queries` | Active queries from `system.processes` | `min_elapsed_seconds` |
| `get_merges` | Active merges from `system.merges` | `table`, `database` |
| `get_replication_status` | Replication queue from `system.replication_queue` | `table`, `database` |
| `get_parts_info` | Parts info from `system.parts` | `table_name` (required), `database`, `active_only` |
| `get_cluster_info` | Cluster topology from `system.clusters` | `cluster` |

## Implementation Steps

### Task 1: Add ClickHouse Go driver dependency

**Files:**
- Modify: `mcp-gateway/go.mod`
- Modify: `mcp-gateway/go.sum`

- [x] Run `cd mcp-gateway && go get github.com/ClickHouse/clickhouse-go/v2`
- [x] Verify the dependency resolves and `go mod tidy` succeeds
- [x] Run `make test-mcp` — must pass (no code changes yet, just dependency)

### Task 2: Define ClickHouse tool type schema

**Files:**
- Modify: `mcp-gateway/internal/tools/schemas.go`

- [x] Add `getClickHouseSchema()` function returning `ToolTypeSchema` with:
  - Name: `clickhouse`
  - Settings: `ch_host` (required), `ch_port` (default 8123, HTTP protocol), `ch_database` (required), `ch_username` (required), `ch_password` (required, secret), `ch_ssl_enabled` (boolean, default false), `ch_timeout` (default 30, range 5-300)
  - All 10 function schemas with input parameters and descriptions
- [x] Add `"clickhouse"` entry to `GetToolSchemas()` map
- [x] Write tests verifying the schema is valid and present in GetToolSchemas
- [x] Run `make test-mcp` — must pass before Task 3

### Task 3: Core ClickHouse tool — struct, config, connection

**Files:**
- Create: `mcp-gateway/internal/tools/clickhouse/clickhouse.go`

- [x] Define `ClickHouseTool` struct with logger, configCache, responseCache, rateLimiter, injectable `execQuery` and `resolveConfig` functions (same pattern as PostgreSQL)
- [x] Implement `NewClickHouseTool()` constructor
- [x] Implement `Stop()` for resource cleanup
- [x] Define `CHConfig` struct: Host, Port, Database, Username, Password, SSLEnabled, Timeout
- [x] Implement `resolveConfigFromDB()` — reads ToolInstance settings, caches for 5 minutes
- [x] Implement `buildConnString()` — constructs `clickhouse://user:pass@host:port/database` DSN with TLS and timeout params
- [x] Implement `executeQueryInternal()` — opens connection via `clickhouse-go` HTTP driver, executes query, returns JSON rows
- [x] Implement read-only query validation: `isSelectOnly()` adapted for ClickHouse SQL dialect
  - Allow: SELECT, WITH, SHOW, DESCRIBE, EXPLAIN, EXISTS
  - Block: INSERT, ALTER, DROP, CREATE, TRUNCATE, RENAME, EXCHANGE, GRANT, REVOKE, KILL, SYSTEM, OPTIMIZE, ATTACH, DETACH, MOVE
  - Block dangerous functions: `currentUser()` mutations won't apply but block `arrayJoin` in LIMIT position, etc.
- [x] Implement result size limiting (5 MB max) and row limit (default 100, max 1000)
- [x] Write tests for config resolution, connection string building, query validation (comprehensive table-driven tests for isSelectOnly)
- [x] Run `make test-mcp` — must pass before Task 4

### Task 4: Implement query and schema discovery tools

**Files:**
- Modify: `mcp-gateway/internal/tools/clickhouse/clickhouse.go`

- [x] Implement `ExecuteQuery()` — validates SELECT-only, adds LIMIT, caches responses (15s TTL)
- [x] Implement `ShowDatabases()` — `SHOW DATABASES`, cached 60s
- [x] Implement `ShowTables()` — `SHOW TABLES FROM {database}`, cached 60s
- [x] Implement `DescribeTable()` — `DESCRIBE TABLE {database}.{table}`, cached 60s
- [x] All methods: validate required params, use rate limiter, return JSON-formatted results
- [x] Write tests for each method using injectable execQuery mock
- [x] Run `make test-mcp` — must pass before Task 5

### Task 5: Implement system diagnostic tools

**Files:**
- Modify: `mcp-gateway/internal/tools/clickhouse/clickhouse.go`

- [x] Implement `GetQueryLog()` — queries `system.query_log` with filters for duration, query_kind, limit; cached 15s
- [x] Implement `GetRunningQueries()` — queries `system.processes` with elapsed filter; cached 15s
- [x] Implement `GetMerges()` — queries `system.merges` with optional table/database filter; cached 15s
- [x] Implement `GetReplicationStatus()` — queries `system.replication_queue` with optional table/database filter; cached 30s
- [x] Implement `GetPartsInfo()` — queries `system.parts` for table, with active_only filter; cached 30s
- [x] Implement `GetClusterInfo()` — queries `system.clusters` with optional cluster filter; cached 60s
- [x] Write tests for each diagnostic method
- [x] Run `make test-mcp` — must pass before Task 6

### Task 6: Register ClickHouse tools in the gateway registry

**Files:**
- Modify: `mcp-gateway/internal/tools/registry.go`

- [x] Add `clickhouseTool` and `clickhouseLimit` fields to Registry struct
- [x] Add `ClickHouseRatePerSecond` (10) and `ClickHouseBurstCapacity` (20) constants
- [x] Implement `registerClickHouseTools()` — create rate limiter, instantiate tool, register all 10 tools with MCP input schemas
- [x] Call `registerClickHouseTools()` from `RegisterAllTools()`
- [x] Add `clickhouseTool.Stop()` call in registry cleanup
- [x] Write tests verifying all 10 tools are registered with correct names and schemas
- [x] Run `make test-mcp` — must pass before Task 7

### Task 7: Verify acceptance criteria

- [ ] Manual test: create a ClickHouse tool instance via the API, verify `list_tool_types` includes `clickhouse`
- [ ] Run full test suite: `make test-mcp`
- [ ] Run linter: `cd mcp-gateway && golangci-lint run`
- [ ] Verify test coverage for `internal/tools/clickhouse` meets 80%+
- [ ] Run `make verify` for overall project health

### Task 8: Update documentation

- [ ] Update CLAUDE.md:
  - Add `clickhouse` to Key Directories tool list
  - Add ClickHouse to Gateway Tools table
  - Add ClickHouse coverage entry to MCP Gateway test coverage table
  - Add ClickHouse to tool type list in Tool Instance Routing section
- [ ] Move this plan to `docs/plans/completed/`
