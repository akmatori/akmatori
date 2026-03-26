# Add PostgreSQL Tool to MCP Gateway

## Overview

Add a PostgreSQL database integration to the MCP Gateway as a native tool. The tool provides read-only query execution plus diagnostic operations for investigating database-related incidents. 10 tool methods total: `execute_query` (SELECT only), `list_tables`, `describe_table`, `get_indexes`, `get_table_stats`, `explain_query`, `get_active_queries`, `get_locks`, `get_replication_status`, `get_database_stats`. All operations are read-only — no INSERT/UPDATE/DELETE. Connection uses the `lib/pq` or `pgx` driver over TCP with optional SSL.

## Context

- Files involved:
  - Create: `mcp-gateway/internal/tools/postgresql/postgresql.go`
  - Create: `mcp-gateway/internal/tools/postgresql/postgresql_test.go`
  - Modify: `mcp-gateway/internal/tools/schemas.go`
  - Modify: `mcp-gateway/internal/tools/schemas_test.go`
  - Modify: `mcp-gateway/internal/tools/registry.go`
  - Modify: `mcp-gateway/internal/database/db.go` (ProxySettings)
  - Modify: `internal/services/tool_service.go` (EnsureToolTypes)
  - Modify: `internal/services/skill_prompt_service.go` (generateToolUsageExample)
- Related patterns:
  - Follow `mcp-gateway/internal/tools/victoriametrics/victoriametrics.go` (cache/config/rate-limit pattern)
  - Follow `mcp-gateway/internal/tools/catchpoint/` (most recent tool addition, proven plan structure)
  - Use `mcp-gateway/internal/cache/cache.go` for TTL caching
  - Use `mcp-gateway/internal/ratelimit/limiter.go` for token bucket rate limiting
  - Use `mcp-gateway/internal/validation/` for parameter validation with typo suggestions
- Dependencies: `github.com/jackc/pgx/v5` (PostgreSQL driver, pure Go) — added to `mcp-gateway/go.mod`

## Development Approach

- **Testing approach**: TDD for core methods (query execution, SQL safety validation), regular for registration/schema
- Complete each task fully before moving to the next
- Follow VictoriaMetrics as the reference implementation for struct/cache/config pattern
- PostgreSQL tool uses direct database connections (TCP) rather than HTTP REST — this is the key difference from other tools
- **Safety**: All queries run inside a read-only transaction (`SET TRANSACTION READ ONLY`) to enforce read-only at the database level
- **Query timeout**: Per-query statement timeout via `SET statement_timeout` to prevent runaway queries
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Implementation Steps

### Task 1: Database and service registration (scaffolding)

**Files:**
- Modify: `mcp-gateway/internal/database/db.go`
- Modify: `internal/services/tool_service.go`

- [x] Add `PostgreSQLEnabled bool` field to `ProxySettings` struct in db.go (after `CatchpointEnabled`, following same pattern): `PostgreSQLEnabled bool \`gorm:"default:false" json:"postgresql_enabled"\``
- [x] Add postgresql to `EnsureToolTypes()` slice in tool_service.go: `{Name: "postgresql", Description: "PostgreSQL database integration for read-only queries and diagnostics"}`
- [x] Run `make test-mcp` and `make test` - must pass before task 2

### Task 2: Add pgx dependency

**Files:**
- Modify: `mcp-gateway/go.mod`

- [x] Run `cd mcp-gateway && go get github.com/jackc/pgx/v5` to add the PostgreSQL driver
- [x] Run `go mod tidy` to clean up
- [x] Run `make test-mcp` - must pass before task 3

### Task 3: Core tool implementation - struct, config, connection management

**Files:**
- Create: `mcp-gateway/internal/tools/postgresql/postgresql.go`

- [x] Create package `postgresql` with PostgreSQLTool struct (4 fields: logger, configCache, responseCache, rateLimiter)
- [x] Define PGConfig struct: Host, Port (default 5432), Database, Username, Password, SSLMode (default "require"), Timeout (default 30, clamp 5-300)
- [x] Implement `NewPostgreSQLTool(logger, limiter)` constructor with cache initialization and `Stop()` method
- [x] Implement `getConfig(ctx, incidentID, logicalName...)` using `database.ResolveToolCredentials` with 5-min config cache. Parse settings keys: `pg_host`, `pg_port`, `pg_database`, `pg_username`, `pg_password`, `pg_ssl_mode`, `pg_timeout`
- [x] Implement `connect(ctx, config)` that builds a pgx connection string, connects, and returns `*pgx.Conn`. Connection must set `default_transaction_read_only = on` and `statement_timeout` based on config timeout
- [x] Implement `executeReadOnly(ctx, config, query string, args ...interface{})` that: checks rate limiter, opens connection, executes query inside `BEGIN; SET TRANSACTION READ ONLY; ... COMMIT`, returns `[]map[string]interface{}` rows with column names as keys, respects 5MB result size limit
- [x] Implement SQL safety check: `isSelectOnly(query string) bool` that rejects queries containing INSERT/UPDATE/DELETE/DROP/ALTER/CREATE/TRUNCATE/GRANT/REVOKE (case-insensitive, handles CTEs). This is a defense-in-depth layer on top of the read-only transaction
- [x] Implement helpers: `extractLogicalName(args)`, `configCacheKey(incidentID)`, `responseCacheKey(query, params)`, `clampTimeout(timeout)`
- [x] Implement `cachedQuery(ctx, incidentID, cacheKey string, ttl time.Duration, queryFn func() (string, error), logicalName ...string)` cache wrapper
- [x] Write tests: constructor, Stop, getConfig (with pre-populated configCache), isSelectOnly (table-driven: SELECT/WITH allowed, INSERT/UPDATE/DELETE/DROP rejected, case variations, comments, semicolons), clampTimeout
- [x] Run `make test-mcp` - must pass before task 4

### Task 4: Read-only query tools (6 methods)

**Files:**
- Modify: `mcp-gateway/internal/tools/postgresql/postgresql.go`

All methods follow signature: `(ctx context.Context, incidentID string, args map[string]interface{}) (string, error)`

- [x] Implement `ExecuteQuery` - execute arbitrary SELECT query (15s cache). Required: `query`. Validates via `isSelectOnly()`. Optional: `limit` (default 100, max 1000) — appends `LIMIT` if not present in query. Returns JSON array of row objects
- [x] Implement `ListTables` - query `information_schema.tables` (60s cache). Optional: `schema` (default "public"). Returns table names, types, row estimates
- [x] Implement `DescribeTable` - query `information_schema.columns` (60s cache). Required: `table_name`. Optional: `schema` (default "public"). Returns column names, types, nullability, defaults
- [x] Implement `GetIndexes` - query `pg_indexes` (60s cache). Required: `table_name`. Optional: `schema` (default "public"). Returns index names, definitions, uniqueness
- [x] Implement `GetTableStats` - query `pg_stat_user_tables` (30s cache). Optional: `table_name` (if omitted, returns all tables). Returns seq_scan, idx_scan, n_live_tup, n_dead_tup, last_vacuum, last_analyze
- [x] Implement `ExplainQuery` - execute `EXPLAIN (ANALYZE false, FORMAT JSON)` (15s cache). Required: `query`. Validates via `isSelectOnly()`. Returns JSON execution plan. Note: ANALYZE is false to avoid actually running the query
- [x] Write tests for each method: success case (mock pgx), error case, parameter validation, cache hit verification
- [x] Run `make test-mcp` - must pass before task 5

### Task 5: Diagnostic tools (4 methods)

**Files:**
- Modify: `mcp-gateway/internal/tools/postgresql/postgresql.go`

- [x] Implement `GetActiveQueries` - query `pg_stat_activity` (15s cache). Optional: `include_idle` (default false), `min_duration_seconds`. Returns pid, state, query, duration, wait_event, client_addr
- [x] Implement `GetLocks` - query `pg_locks` joined with `pg_stat_activity` (15s cache). Optional: `blocked_only` (default false). Returns lock type, relation, mode, granted, blocking/blocked pids, query text
- [x] Implement `GetReplicationStatus` - query `pg_stat_replication` (30s cache). Returns client_addr, state, sent_lsn, write_lsn, flush_lsn, replay_lsn, lag
- [x] Implement `GetDatabaseStats` - query `pg_stat_database` for current database (30s cache). Returns numbackends, xact_commit, xact_rollback, blks_read, blks_hit, cache_hit_ratio, tup_returned, tup_fetched, tup_inserted, tup_updated, tup_deleted, conflicts, deadlocks, temp_files, temp_bytes, db_size
- [x] Write tests for each method: success case, parameter validation, cache behavior
- [x] Run `make test-mcp` - must pass before task 6

### Task 6: Schema definition

**Files:**
- Modify: `mcp-gateway/internal/tools/schemas.go`
- Modify: `mcp-gateway/internal/tools/schemas_test.go`

- [ ] Add `"postgresql": getPostgreSQLSchema()` to `GetToolSchemas()` map
- [ ] Implement `getPostgreSQLSchema()` function with:
  - Settings: `pg_host` (string, required), `pg_port` (integer, default 5432), `pg_database` (string, required), `pg_username` (string, required), `pg_password` (string, secret, required), `pg_ssl_mode` (string, enum: disable/require/verify-ca/verify-full, default "require"), `pg_timeout` (integer, advanced, default 30, min 5, max 300)
  - Functions: 10 entries matching all public methods with Parameters as comma-separated lists
  - Required: `["pg_host", "pg_database", "pg_username", "pg_password"]`
- [ ] Add tests: `TestGetToolSchemas_ContainsPostgreSQL`, `TestGetToolSchema_PostgreSQL` (verify name, version, required settings, function count), update `TestGetToolSchemas_AllPresent` to include "postgresql"
- [ ] Run `make test-mcp` - must pass before task 7

### Task 7: Registry integration

**Files:**
- Modify: `mcp-gateway/internal/tools/registry.go`

- [ ] Add import for `"github.com/akmatori/mcp-gateway/internal/tools/postgresql"` package
- [ ] Add constants: `PostgreSQLRatePerSecond = 10`, `PostgreSQLBurstCapacity = 20`
- [ ] Add fields to Registry struct: `postgresqlTool *postgresql.PostgreSQLTool`, `postgresqlLimit *ratelimit.Limiter`
- [ ] Add to `RegisterAllTools()`: create limiter, call `r.registerPostgreSQLTools()`
- [ ] Add to `Stop()`: stop postgresql tool
- [ ] Implement `registerPostgreSQLTools()` method: instantiate tool, register 10 MCP tools with `r.server.RegisterTool()` using proper `mcp.Tool{Name, Description, InputSchema}` and handler functions. Tool names: `postgresql.execute_query`, `postgresql.list_tables`, `postgresql.describe_table`, `postgresql.get_indexes`, `postgresql.get_table_stats`, `postgresql.explain_query`, `postgresql.get_active_queries`, `postgresql.get_locks`, `postgresql.get_replication_status`, `postgresql.get_database_stats`
- [ ] Run `make test-mcp` - must pass before task 8

### Task 8: Skill prompt integration

**Files:**
- Modify: `internal/services/skill_prompt_service.go`

- [ ] Add `case "postgresql":` to `generateToolUsageExample()` with usage examples:
  ```
  gateway_call("postgresql.execute_query", {"query": "SELECT * FROM users LIMIT 10"}, "<logical_name>")
  gateway_call("postgresql.list_tables", {}, "<logical_name>")
  gateway_call("postgresql.describe_table", {"table_name": "users"}, "<logical_name>")
  gateway_call("postgresql.get_active_queries", {}, "<logical_name>")
  gateway_call("postgresql.get_locks", {"blocked_only": true}, "<logical_name>")
  gateway_call("postgresql.get_database_stats", {}, "<logical_name>")
  ```
- [ ] No changes needed to `extractToolDetails()` — PostgreSQL doesn't expose agent-relevant config (same as Zabbix/VM)
- [ ] Run `make test` - must pass before task 9

### Task 9: Verify acceptance criteria

- [ ] Run full test suite: `make test-all`
- [ ] Run linter: `golangci-lint run ./mcp-gateway/...`
- [ ] Run vet: `go vet ./mcp-gateway/...`
- [ ] Run `make verify`
- [ ] Manual test: rebuild mcp-gateway container (`docker-compose build mcp-gateway && docker-compose up -d mcp-gateway`)
- [ ] Manual test: create PostgreSQL tool instance via web UI, verify schema renders correctly with all settings fields
- [ ] Manual test: verify tool discovery works (list_tool_types shows postgresql, list_tools_for_tool_type returns all 10 methods)
- [ ] Manual test: connect to a real PostgreSQL instance, run `execute_query` with a SELECT, verify read-only enforcement
- [ ] Verify test coverage for postgresql package meets 80%+

### Task 10: Update documentation

- [ ] Update CLAUDE.md: add PostgreSQL to key directories section, add to MCP Gateway coverage table
- [ ] Move this plan to `docs/plans/completed/`
