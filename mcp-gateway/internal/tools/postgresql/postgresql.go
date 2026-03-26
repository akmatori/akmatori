package postgresql

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/akmatori/mcp-gateway/internal/cache"
	"github.com/akmatori/mcp-gateway/internal/database"
	"github.com/akmatori/mcp-gateway/internal/ratelimit"
	"github.com/akmatori/mcp-gateway/internal/validation"
	"github.com/jackc/pgx/v5"
)

// Cache TTL constants
const (
	ConfigCacheTTL   = 5 * time.Minute  // Credentials cache TTL
	ResponseCacheTTL = 30 * time.Second // Default response cache TTL
	CacheCleanupTick = time.Minute      // Background cleanup interval
	QueryCacheTTL    = 15 * time.Second // User query / active queries cache TTL
	SchemaCacheTTL   = 60 * time.Second // Schema metadata cache TTL
	StatsCacheTTL    = 30 * time.Second // Statistics cache TTL
	MaxResultSize    = 5 * 1024 * 1024  // 5 MB result size limit
	DefaultLimit     = 100              // Default row limit
	MaxLimit         = 1000             // Maximum row limit
	DefaultTimeout   = 30               // Default query timeout in seconds
	MinTimeout       = 5                // Minimum timeout
	MaxTimeout       = 300              // Maximum timeout
	DefaultPort      = 5432             // Default PostgreSQL port
)

// dangerousStmtPattern matches SQL statements that modify data or schema.
// This is a defense-in-depth layer — the read-only transaction is the primary guard.
var dangerousStmtPattern = regexp.MustCompile(`(?i)\b(INSERT|UPDATE|DELETE|DROP|ALTER|CREATE|TRUNCATE|GRANT|REVOKE|COPY|DO|CALL)\b`)

// Pre-compiled regex patterns for SQL comment stripping and LIMIT detection
var (
	blockCommentPattern = regexp.MustCompile(`/\*[\s\S]*?\*/`)
	lineCommentPattern  = regexp.MustCompile(`--[^\n]*`)
	limitPattern        = regexp.MustCompile(`(?i)\bLIMIT\b`)
)

// PGConfig holds PostgreSQL connection configuration
type PGConfig struct {
	Host     string
	Port     int
	Database string
	Username string
	Password string
	SSLMode  string
	Timeout  int
}

// queryExecFunc is the function signature for executing read-only queries.
// Extracted as a type to allow test injection.
type queryExecFunc func(ctx context.Context, config *PGConfig, query string, args ...interface{}) ([]map[string]interface{}, error)

// configResolverFunc is the function signature for resolving config.
type configResolverFunc func(ctx context.Context, incidentID string, logicalName ...string) (*PGConfig, error)

// PostgreSQLTool handles PostgreSQL database operations
type PostgreSQLTool struct {
	logger        *log.Logger
	configCache   *cache.Cache
	responseCache *cache.Cache
	rateLimiter   *ratelimit.Limiter
	execQuery     queryExecFunc    // overridable for testing
	resolveConfig configResolverFunc // overridable for testing
}

// NewPostgreSQLTool creates a new PostgreSQL tool with optional rate limiter
func NewPostgreSQLTool(logger *log.Logger, limiter *ratelimit.Limiter) *PostgreSQLTool {
	t := &PostgreSQLTool{
		logger:        logger,
		configCache:   cache.New(ConfigCacheTTL, CacheCleanupTick),
		responseCache: cache.New(ResponseCacheTTL, CacheCleanupTick),
		rateLimiter:   limiter,
	}
	t.execQuery = t.executeReadOnly
	t.resolveConfig = t.getConfig
	return t
}

// Stop cleans up cache resources
func (t *PostgreSQLTool) Stop() {
	if t.configCache != nil {
		t.configCache.Stop()
	}
	if t.responseCache != nil {
		t.responseCache.Stop()
	}
}

// configCacheKey returns the cache key for config/credentials
func configCacheKey(incidentID string) string {
	return fmt.Sprintf("creds:%s:postgresql", incidentID)
}

// responseCacheKey returns the cache key for query responses
func responseCacheKey(query string, params interface{}) string {
	paramsJSON, _ := json.Marshal(params)
	combined := query + ":" + string(paramsJSON)
	hash := sha256.Sum256([]byte(combined))
	return fmt.Sprintf("pg:%s", hex.EncodeToString(hash[:8]))
}

// extractLogicalName extracts the optional logical_name from tool arguments.
func extractLogicalName(args map[string]interface{}) string {
	if v, ok := args["logical_name"].(string); ok {
		return v
	}
	return ""
}

// clampTimeout ensures timeout is within a safe range (5-300 seconds), defaulting to 30.
func clampTimeout(timeout int) int {
	if timeout < MinTimeout {
		return DefaultTimeout
	}
	if timeout > MaxTimeout {
		return MaxTimeout
	}
	return timeout
}

// isSelectOnly validates that a SQL query is read-only.
// Rejects queries containing INSERT, UPDATE, DELETE, DROP, ALTER, CREATE, TRUNCATE, GRANT, REVOKE.
// Allows SELECT and WITH (CTEs).
func isSelectOnly(query string) bool {
	// Strip SQL comments (both -- and /* */)
	cleaned := stripSQLComments(query)
	return !dangerousStmtPattern.MatchString(cleaned)
}

// stripSQLComments removes SQL line comments (--) and block comments (/* */)
func stripSQLComments(query string) string {
	result := blockCommentPattern.ReplaceAllString(query, " ")
	result = lineCommentPattern.ReplaceAllString(result, " ")
	return result
}

// getConfig fetches PostgreSQL configuration from database with caching.
func (t *PostgreSQLTool) getConfig(ctx context.Context, incidentID string, logicalName ...string) (*PGConfig, error) {
	cacheKey := configCacheKey(incidentID)
	if len(logicalName) > 0 && logicalName[0] != "" {
		cacheKey = fmt.Sprintf("creds:logical:%s:%s", "postgresql", logicalName[0])
	}

	// Check cache first
	if cached, ok := t.configCache.Get(cacheKey); ok {
		if config, ok := cached.(*PGConfig); ok {
			t.logger.Printf("Config cache hit for key %s", cacheKey)
			return config, nil
		}
	}

	ln := ""
	if len(logicalName) > 0 {
		ln = logicalName[0]
	}
	creds, err := database.ResolveToolCredentials(ctx, incidentID, "postgresql", nil, ln)
	if err != nil {
		return nil, fmt.Errorf("failed to get PostgreSQL credentials: %w", err)
	}

	config := parseSettings(creds.Settings)

	// Cache the config
	t.configCache.Set(cacheKey, config)
	t.logger.Printf("Config cached for key %s", cacheKey)

	return config, nil
}

// parseSettings converts a settings map into a PGConfig with defaults applied
func parseSettings(settings map[string]interface{}) *PGConfig {
	config := &PGConfig{
		Port:    DefaultPort,
		SSLMode: "require",
		Timeout: DefaultTimeout,
	}

	if v, ok := settings["pg_host"].(string); ok {
		config.Host = v
	}
	if v, ok := settings["pg_port"].(float64); ok {
		config.Port = int(v)
	}
	if v, ok := settings["pg_database"].(string); ok {
		config.Database = v
	}
	if v, ok := settings["pg_username"].(string); ok {
		config.Username = v
	}
	if v, ok := settings["pg_password"].(string); ok {
		config.Password = v
	}
	if v, ok := settings["pg_ssl_mode"].(string); ok {
		config.SSLMode = v
	}
	if v, ok := settings["pg_timeout"].(float64); ok {
		config.Timeout = int(v)
	}

	config.Timeout = clampTimeout(config.Timeout)
	return config
}

// buildConnConfig creates a pgx connection config from PGConfig.
// Uses structured config instead of string interpolation to avoid injection via special characters.
func buildConnConfig(config *PGConfig) (*pgx.ConnConfig, error) {
	// Build a minimal DSN for pgx.ParseConfig, then override fields programmatically
	connConfig, err := pgx.ParseConfig("")
	if err != nil {
		return nil, fmt.Errorf("failed to create connection config: %w", err)
	}

	connConfig.Host = config.Host
	connConfig.Port = uint16(config.Port)
	connConfig.Database = config.Database
	connConfig.User = config.Username
	connConfig.Password = config.Password

	// Configure TLS based on SSLMode
	switch config.SSLMode {
	case "disable":
		connConfig.TLSConfig = nil
	default:
		// For require/verify-ca/verify-full, use the sslmode parameter via RuntimeParams
		// pgx handles TLS negotiation; we pass sslmode so the connection string fallback works
		connConfig.RuntimeParams["sslmode"] = config.SSLMode
	}

	return connConfig, nil
}

// connect creates a pgx connection with read-only defaults and statement timeout
func (t *PostgreSQLTool) connect(ctx context.Context, config *PGConfig) (*pgx.Conn, error) {
	connConfig, err := buildConnConfig(config)
	if err != nil {
		return nil, err
	}

	conn, err := pgx.ConnectConfig(ctx, connConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to PostgreSQL at %s:%d/%s: %w", config.Host, config.Port, config.Database, err)
	}

	// Set connection to read-only mode and configure statement timeout
	timeoutMs := config.Timeout * 1000
	_, err = conn.Exec(ctx, fmt.Sprintf("SET default_transaction_read_only = on; SET statement_timeout = %d", timeoutMs))
	if err != nil {
		conn.Close(ctx)
		return nil, fmt.Errorf("failed to configure connection: %w", err)
	}

	return conn, nil
}

// executeReadOnly runs a query inside a read-only transaction with rate limiting.
// Returns rows as []map[string]interface{} with column names as keys.
func (t *PostgreSQLTool) executeReadOnly(ctx context.Context, config *PGConfig, query string, args ...interface{}) ([]map[string]interface{}, error) {
	// Apply rate limiting
	if t.rateLimiter != nil {
		if err := t.rateLimiter.Wait(ctx); err != nil {
			return nil, fmt.Errorf("rate limit wait cancelled: %w", err)
		}
	}

	conn, err := t.connect(ctx, config)
	if err != nil {
		return nil, err
	}
	defer conn.Close(ctx)

	t.logger.Printf("PostgreSQL query: %s", truncateQuery(query))

	// Execute inside read-only transaction (default_transaction_read_only = on is set at connection level)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback on defer is best-effort

	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query execution failed: %w", err)
	}
	defer rows.Close()

	fieldDescs := rows.FieldDescriptions()
	columns := make([]string, len(fieldDescs))
	for i, fd := range fieldDescs {
		columns[i] = string(fd.Name)
	}

	var results []map[string]interface{}
	totalSize := 0

	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		row := make(map[string]interface{}, len(columns))
		for i, col := range columns {
			row[col] = values[i]
		}
		results = append(results, row)

		// Approximate size check
		rowJSON, _ := json.Marshal(row)
		totalSize += len(rowJSON)
		if totalSize > MaxResultSize {
			return nil, fmt.Errorf("result exceeds %d MB limit, use LIMIT to reduce result set", MaxResultSize/(1024*1024))
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return results, nil
}

// cachedQuery executes a query with response caching
func (t *PostgreSQLTool) cachedQuery(ctx context.Context, incidentID, cacheKey string, ttl time.Duration, queryFn func() (string, error), logicalName ...string) (string, error) {
	fullCacheKey := cacheKey
	if len(logicalName) > 0 && logicalName[0] != "" {
		fullCacheKey = fmt.Sprintf("logical:%s:%s", logicalName[0], cacheKey)
	} else {
		fullCacheKey = fmt.Sprintf("incident:%s:%s", incidentID, cacheKey)
	}

	// Check response cache
	if cached, ok := t.responseCache.Get(fullCacheKey); ok {
		if result, ok := cached.(string); ok {
			t.logger.Printf("Response cache hit for %s", cacheKey)
			return result, nil
		}
	}

	result, err := queryFn()
	if err != nil {
		return "", err
	}

	// Cache the result
	t.responseCache.SetWithTTL(fullCacheKey, result, ttl)
	t.logger.Printf("Response cached for %s (TTL: %v)", cacheKey, ttl)

	return result, nil
}

// truncateQuery truncates a query string for logging
func truncateQuery(query string) string {
	if len(query) > 200 {
		return query[:200] + "..."
	}
	return query
}

// rowsToJSON converts query result rows to a JSON string
func rowsToJSON(rows []map[string]interface{}) (string, error) {
	data, err := json.Marshal(rows)
	if err != nil {
		return "", fmt.Errorf("failed to marshal results: %w", err)
	}
	return string(data), nil
}

// parseLimit extracts and validates the limit parameter
func parseLimit(args map[string]interface{}) int {
	if v, ok := args["limit"].(float64); ok {
		limit := int(v)
		if limit < 1 {
			return DefaultLimit
		}
		if limit > MaxLimit {
			return MaxLimit
		}
		return limit
	}
	return DefaultLimit
}

// getSchema extracts the schema parameter, defaulting to "public"
func getSchema(args map[string]interface{}) string {
	if v, ok := args["schema"].(string); ok && v != "" {
		return v
	}
	return "public"
}

// hasLimitClause checks if a query already contains a LIMIT clause
func hasLimitClause(query string) bool {
	cleaned := stripSQLComments(query)
	return limitPattern.MatchString(cleaned)
}

// --- Tool methods ---

// ExecuteQuery executes an arbitrary SELECT query with safety validation
func (t *PostgreSQLTool) ExecuteQuery(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	query, ok := args["query"].(string)
	if !ok || query == "" {
		return "", fmt.Errorf("query is required%s", validation.SuggestParam("query", args))
	}

	if !isSelectOnly(query) {
		return "", fmt.Errorf("only SELECT queries are allowed (INSERT, UPDATE, DELETE, DROP, ALTER, CREATE, TRUNCATE, GRANT, REVOKE, COPY, DO, CALL are blocked)")
	}

	limit := parseLimit(args)
	if !hasLimitClause(query) {
		query = strings.TrimRight(query, "; \t\n") + fmt.Sprintf(" LIMIT %d", limit)
	}

	cacheKey := responseCacheKey(query, args)

	return t.cachedQuery(ctx, incidentID, cacheKey, QueryCacheTTL, func() (string, error) {
		config, err := t.resolveConfig(ctx, incidentID, logicalName)
		if err != nil {
			return "", err
		}
		rows, err := t.execQuery(ctx, config, query)
		if err != nil {
			return "", err
		}
		return rowsToJSON(rows)
	}, logicalName)
}

// ListTables lists tables in the specified schema
func (t *PostgreSQLTool) ListTables(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)
	schema := getSchema(args)

	query := `SELECT table_name, table_type,
		(SELECT reltuples::bigint FROM pg_class WHERE relname = tables.table_name AND relnamespace = (SELECT oid FROM pg_namespace WHERE nspname = $1)) AS row_estimate
		FROM information_schema.tables
		WHERE table_schema = $1
		ORDER BY table_name`

	cacheKey := responseCacheKey("list_tables", map[string]string{"schema": schema})

	return t.cachedQuery(ctx, incidentID, cacheKey, SchemaCacheTTL, func() (string, error) {
		config, err := t.resolveConfig(ctx, incidentID, logicalName)
		if err != nil {
			return "", err
		}
		rows, err := t.execQuery(ctx, config, query, schema)
		if err != nil {
			return "", err
		}
		return rowsToJSON(rows)
	}, logicalName)
}

// DescribeTable describes columns of a table
func (t *PostgreSQLTool) DescribeTable(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	tableName, ok := args["table_name"].(string)
	if !ok || tableName == "" {
		return "", fmt.Errorf("table_name is required%s", validation.SuggestParam("table_name", args))
	}

	schema := getSchema(args)

	query := `SELECT column_name, data_type, is_nullable, column_default, character_maximum_length, numeric_precision
		FROM information_schema.columns
		WHERE table_schema = $1 AND table_name = $2
		ORDER BY ordinal_position`

	cacheKey := responseCacheKey("describe_table", map[string]string{"schema": schema, "table": tableName})

	return t.cachedQuery(ctx, incidentID, cacheKey, SchemaCacheTTL, func() (string, error) {
		config, err := t.resolveConfig(ctx, incidentID, logicalName)
		if err != nil {
			return "", err
		}
		rows, err := t.execQuery(ctx, config, query, schema, tableName)
		if err != nil {
			return "", err
		}
		return rowsToJSON(rows)
	}, logicalName)
}

// GetIndexes returns indexes for a table
func (t *PostgreSQLTool) GetIndexes(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	tableName, ok := args["table_name"].(string)
	if !ok || tableName == "" {
		return "", fmt.Errorf("table_name is required%s", validation.SuggestParam("table_name", args))
	}

	schema := getSchema(args)

	query := `SELECT indexname, indexdef,
		(SELECT indisunique FROM pg_index WHERE indexrelid = (
			SELECT oid FROM pg_class WHERE relname = pg_indexes.indexname
			AND relnamespace = (SELECT oid FROM pg_namespace WHERE nspname = $1)
		)) AS is_unique
		FROM pg_indexes
		WHERE schemaname = $1 AND tablename = $2
		ORDER BY indexname`

	cacheKey := responseCacheKey("get_indexes", map[string]string{"schema": schema, "table": tableName})

	return t.cachedQuery(ctx, incidentID, cacheKey, SchemaCacheTTL, func() (string, error) {
		config, err := t.resolveConfig(ctx, incidentID, logicalName)
		if err != nil {
			return "", err
		}
		rows, err := t.execQuery(ctx, config, query, schema, tableName)
		if err != nil {
			return "", err
		}
		return rowsToJSON(rows)
	}, logicalName)
}

// GetTableStats returns table statistics from pg_stat_user_tables
func (t *PostgreSQLTool) GetTableStats(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	query := `SELECT schemaname, relname AS table_name,
		seq_scan, seq_tup_read, idx_scan, idx_tup_fetch,
		n_tup_ins, n_tup_upd, n_tup_del, n_live_tup, n_dead_tup,
		last_vacuum, last_autovacuum, last_analyze, last_autoanalyze
		FROM pg_stat_user_tables`

	params := map[string]string{}
	var queryArgs []interface{}

	if tableName, ok := args["table_name"].(string); ok && tableName != "" {
		query += " WHERE relname = $1"
		params["table"] = tableName
		queryArgs = append(queryArgs, tableName)
	} else {
		query += " ORDER BY n_live_tup DESC"
	}

	cacheKey := responseCacheKey("get_table_stats", params)

	return t.cachedQuery(ctx, incidentID, cacheKey, StatsCacheTTL, func() (string, error) {
		config, err := t.resolveConfig(ctx, incidentID, logicalName)
		if err != nil {
			return "", err
		}
		rows, err := t.execQuery(ctx, config, query, queryArgs...)
		if err != nil {
			return "", err
		}
		return rowsToJSON(rows)
	}, logicalName)
}

// ExplainQuery returns the execution plan for a SELECT query
func (t *PostgreSQLTool) ExplainQuery(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	query, ok := args["query"].(string)
	if !ok || query == "" {
		return "", fmt.Errorf("query is required%s", validation.SuggestParam("query", args))
	}

	if !isSelectOnly(query) {
		return "", fmt.Errorf("only SELECT queries are allowed (INSERT, UPDATE, DELETE, DROP, ALTER, CREATE, TRUNCATE, GRANT, REVOKE, COPY, DO, CALL are blocked)")
	}

	explainQuery := "EXPLAIN (ANALYZE false, FORMAT JSON) " + query

	cacheKey := responseCacheKey("explain", map[string]string{"query": query})

	return t.cachedQuery(ctx, incidentID, cacheKey, QueryCacheTTL, func() (string, error) {
		config, err := t.resolveConfig(ctx, incidentID, logicalName)
		if err != nil {
			return "", err
		}
		rows, err := t.execQuery(ctx, config, explainQuery)
		if err != nil {
			return "", err
		}
		return rowsToJSON(rows)
	}, logicalName)
}

// GetActiveQueries returns currently active queries from pg_stat_activity
func (t *PostgreSQLTool) GetActiveQueries(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	query := `SELECT pid, state, query, usename,
		EXTRACT(EPOCH FROM (now() - query_start))::numeric(10,2) AS duration_seconds,
		wait_event_type, wait_event, client_addr, application_name,
		backend_start, query_start
		FROM pg_stat_activity
		WHERE pid != pg_backend_pid()`

	var queryArgs []interface{}
	paramIdx := 1

	includeIdle := false
	if v, ok := args["include_idle"].(bool); ok {
		includeIdle = v
	}
	if !includeIdle {
		query += " AND state != 'idle'"
	}

	if v, ok := args["min_duration_seconds"].(float64); ok && v > 0 {
		query += fmt.Sprintf(" AND EXTRACT(EPOCH FROM (now() - query_start)) > $%d", paramIdx)
		queryArgs = append(queryArgs, v)
		paramIdx++ //nolint:ineffassign // keep paramIdx pattern consistent for future parameters
	}

	query += " ORDER BY duration_seconds DESC NULLS LAST"

	cacheKey := responseCacheKey("get_active_queries", args)

	return t.cachedQuery(ctx, incidentID, cacheKey, QueryCacheTTL, func() (string, error) {
		config, err := t.resolveConfig(ctx, incidentID, logicalName)
		if err != nil {
			return "", err
		}
		rows, err := t.execQuery(ctx, config, query, queryArgs...)
		if err != nil {
			return "", err
		}
		return rowsToJSON(rows)
	}, logicalName)
}

// GetLocks returns lock information joined with pg_stat_activity
func (t *PostgreSQLTool) GetLocks(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	query := `SELECT l.locktype, l.relation::regclass AS relation, l.mode, l.granted, l.pid,
		a.usename, a.state, a.query,
		EXTRACT(EPOCH FROM (now() - a.query_start))::numeric(10,2) AS duration_seconds,
		l.waitstart
		FROM pg_locks l
		JOIN pg_stat_activity a ON l.pid = a.pid
		WHERE l.pid != pg_backend_pid()`

	blockedOnly := false
	if v, ok := args["blocked_only"].(bool); ok {
		blockedOnly = v
	}
	if blockedOnly {
		query += " AND NOT l.granted"
	}

	query += " ORDER BY l.granted, duration_seconds DESC NULLS LAST"

	cacheKey := responseCacheKey("get_locks", args)

	return t.cachedQuery(ctx, incidentID, cacheKey, QueryCacheTTL, func() (string, error) {
		config, err := t.resolveConfig(ctx, incidentID, logicalName)
		if err != nil {
			return "", err
		}
		rows, err := t.execQuery(ctx, config, query)
		if err != nil {
			return "", err
		}
		return rowsToJSON(rows)
	}, logicalName)
}

// GetReplicationStatus returns replication status from pg_stat_replication
func (t *PostgreSQLTool) GetReplicationStatus(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	query := `SELECT client_addr, usename, application_name, state,
		sent_lsn, write_lsn, flush_lsn, replay_lsn,
		write_lag, flush_lag, replay_lag,
		sync_state, sync_priority
		FROM pg_stat_replication
		ORDER BY client_addr`

	cacheKey := responseCacheKey("get_replication_status", nil)

	return t.cachedQuery(ctx, incidentID, cacheKey, StatsCacheTTL, func() (string, error) {
		config, err := t.resolveConfig(ctx, incidentID, logicalName)
		if err != nil {
			return "", err
		}
		rows, err := t.execQuery(ctx, config, query)
		if err != nil {
			return "", err
		}
		return rowsToJSON(rows)
	}, logicalName)
}

// GetDatabaseStats returns database-level statistics
func (t *PostgreSQLTool) GetDatabaseStats(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	query := `SELECT numbackends, xact_commit, xact_rollback,
		blks_read, blks_hit,
		CASE WHEN (blks_hit + blks_read) > 0
			THEN round(blks_hit::numeric / (blks_hit + blks_read) * 100, 2)
			ELSE 0 END AS cache_hit_ratio,
		tup_returned, tup_fetched, tup_inserted, tup_updated, tup_deleted,
		conflicts, deadlocks, temp_files, temp_bytes,
		pg_database_size(current_database()) AS db_size
		FROM pg_stat_database
		WHERE datname = current_database()`

	cacheKey := responseCacheKey("get_database_stats", nil)

	return t.cachedQuery(ctx, incidentID, cacheKey, StatsCacheTTL, func() (string, error) {
		config, err := t.resolveConfig(ctx, incidentID, logicalName)
		if err != nil {
			return "", err
		}
		rows, err := t.execQuery(ctx, config, query)
		if err != nil {
			return "", err
		}
		return rowsToJSON(rows)
	}, logicalName)
}
