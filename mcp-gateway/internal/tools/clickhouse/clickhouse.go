package clickhouse

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/akmatori/mcp-gateway/internal/cache"
	"github.com/akmatori/mcp-gateway/internal/database"
	"github.com/akmatori/mcp-gateway/internal/ratelimit"
	"github.com/akmatori/mcp-gateway/internal/validation"

	_ "github.com/ClickHouse/clickhouse-go/v2" // register database/sql driver
)

// Cache TTL constants
const (
	ConfigCacheTTL   = 5 * time.Minute
	CacheCleanupTick = time.Minute
	QueryCacheTTL    = 15 * time.Second
	SchemaCacheTTL   = 60 * time.Second
	StatsCacheTTL    = 30 * time.Second
	MaxResultSize    = 5 * 1024 * 1024 // 5 MB
	DefaultLimit     = 100
	MaxLimit         = 1000
	DefaultTimeout   = 30
	MinTimeout       = 5
	MaxTimeout       = 300
	DefaultPort      = 8123 // ClickHouse HTTP protocol port
)

// dangerousStmtPattern matches SQL statements that modify data or schema in ClickHouse.
var dangerousStmtPattern = regexp.MustCompile(`(?i)\b(INSERT|DELETE|UPDATE|ALTER|DROP|CREATE|TRUNCATE|RENAME|EXCHANGE|GRANT|REVOKE|KILL|SYSTEM|OPTIMIZE|ATTACH|DETACH|MOVE)\b`)

// Pre-compiled regex patterns for SQL processing
var (
	blockCommentPattern = regexp.MustCompile(`/\*[\s\S]*?\*/`)
	lineCommentPattern  = regexp.MustCompile(`--[^\n]*`)
	singleQuoteLiteral  = regexp.MustCompile(`'(?:[^'\\]|\\.|\'{2})*'`)
	doubleQuotedIdent   = regexp.MustCompile("`(?:[^`\\\\]|\\\\.)*`")
	limitPattern        = regexp.MustCompile(`(?i)\bLIMIT\b`)
	parenGroupPattern   = regexp.MustCompile(`\([^()]*\)`)
	readOnlyStartPat    = regexp.MustCompile(`(?i)^\s*(SELECT|WITH|SHOW|DESCRIBE|DESC|EXPLAIN|EXISTS)\b`)
)

// CHConfig holds ClickHouse connection configuration
type CHConfig struct {
	Host       string
	Port       int
	Database   string
	Username   string
	Password   string
	SSLEnabled bool
	Timeout    int
}

// queryExecFunc is the function signature for executing queries.
type queryExecFunc func(ctx context.Context, config *CHConfig, query string) ([]map[string]interface{}, error)

// configResolverFunc is the function signature for resolving config.
type configResolverFunc func(ctx context.Context, incidentID string, logicalName ...string) (*CHConfig, error)

// ClickHouseTool handles ClickHouse database operations
type ClickHouseTool struct {
	logger        *log.Logger
	configCache   *cache.Cache
	responseCache *cache.Cache
	rateLimiter   *ratelimit.Limiter
	execQuery     queryExecFunc
	resolveConfig configResolverFunc
}

// NewClickHouseTool creates a new ClickHouse tool with optional rate limiter
func NewClickHouseTool(logger *log.Logger, limiter *ratelimit.Limiter) *ClickHouseTool {
	t := &ClickHouseTool{
		logger:        logger,
		configCache:   cache.New(ConfigCacheTTL, CacheCleanupTick),
		responseCache: cache.New(QueryCacheTTL, CacheCleanupTick),
		rateLimiter:   limiter,
	}
	t.execQuery = t.executeQueryInternal
	t.resolveConfig = t.resolveConfigFromDB
	return t
}

// Stop cleans up cache resources
func (t *ClickHouseTool) Stop() {
	if t.configCache != nil {
		t.configCache.Stop()
	}
	if t.responseCache != nil {
		t.responseCache.Stop()
	}
}

// configCacheKey returns the cache key for config/credentials
func configCacheKey(incidentID string) string {
	return fmt.Sprintf("creds:%s:clickhouse", incidentID)
}

// responseCacheKey returns the cache key for query responses
func responseCacheKey(query string, params interface{}) string {
	paramsJSON, _ := json.Marshal(params)
	combined := query + ":" + string(paramsJSON)
	hash := sha256.Sum256([]byte(combined))
	return fmt.Sprintf("ch:%s", hex.EncodeToString(hash[:]))
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
		return MinTimeout
	}
	if timeout > MaxTimeout {
		return MaxTimeout
	}
	return timeout
}

// isSelectOnly validates that a SQL query is read-only for ClickHouse.
// Allows: SELECT, WITH, SHOW, DESCRIBE, EXPLAIN, EXISTS
// Blocks: INSERT, ALTER, DROP, CREATE, TRUNCATE, RENAME, EXCHANGE, GRANT, REVOKE, KILL, SYSTEM, OPTIMIZE, ATTACH, DETACH, MOVE
func isSelectOnly(query string) bool {
	cleaned := stripSQLComments(stripSQLLiterals(query))
	if !readOnlyStartPat.MatchString(cleaned) {
		return false
	}
	if dangerousStmtPattern.MatchString(cleaned) {
		return false
	}
	return true
}

// stripSQLComments removes SQL line comments (--) and block comments (/* */)
func stripSQLComments(query string) string {
	result := blockCommentPattern.ReplaceAllString(query, " ")
	result = lineCommentPattern.ReplaceAllString(result, " ")
	return result
}

// stripSQLLiterals removes string literals and backtick-quoted identifiers
func stripSQLLiterals(query string) string {
	result := singleQuoteLiteral.ReplaceAllString(query, "''")
	result = doubleQuotedIdent.ReplaceAllString(result, "``")
	return result
}

// resolveConfigFromDB fetches ClickHouse configuration from database with caching.
func (t *ClickHouseTool) resolveConfigFromDB(ctx context.Context, incidentID string, logicalName ...string) (*CHConfig, error) {
	cacheKey := configCacheKey(incidentID)
	if len(logicalName) > 0 && logicalName[0] != "" {
		cacheKey = fmt.Sprintf("creds:logical:%s:%s", "clickhouse", logicalName[0])
	}

	if cached, ok := t.configCache.Get(cacheKey); ok {
		if config, ok := cached.(*CHConfig); ok {
			t.logger.Printf("Config cache hit for key %s", cacheKey)
			return config, nil
		}
	}

	ln := ""
	if len(logicalName) > 0 {
		ln = logicalName[0]
	}
	creds, err := database.ResolveToolCredentials(ctx, incidentID, "clickhouse", nil, ln)
	if err != nil {
		return nil, fmt.Errorf("failed to get ClickHouse credentials: %w", err)
	}

	config := parseSettings(creds.Settings)
	t.configCache.Set(cacheKey, config)
	t.logger.Printf("Config cached for key %s", cacheKey)
	return config, nil
}

// parseSettings converts a settings map into a CHConfig with defaults applied
func parseSettings(settings map[string]interface{}) *CHConfig {
	config := &CHConfig{
		Port:    DefaultPort,
		Timeout: DefaultTimeout,
	}

	if v, ok := settings["ch_host"].(string); ok {
		config.Host = v
	}
	if v, ok := settings["ch_port"].(float64); ok {
		p := int(v)
		if p >= 1 && p <= 65535 {
			config.Port = p
		}
	}
	if v, ok := settings["ch_database"].(string); ok {
		config.Database = v
	}
	if v, ok := settings["ch_username"].(string); ok {
		config.Username = v
	}
	if v, ok := settings["ch_password"].(string); ok {
		config.Password = v
	}
	if v, ok := settings["ch_ssl_enabled"].(bool); ok {
		config.SSLEnabled = v
	}
	if v, ok := settings["ch_timeout"].(float64); ok {
		config.Timeout = int(v)
	}

	config.Timeout = clampTimeout(config.Timeout)
	return config
}

// buildDSN constructs a ClickHouse DSN for the database/sql driver.
// Format: clickhouse://user:pass@host:port/database?dial_timeout=Ns&read_timeout=Ns&secure=true
func buildDSN(config *CHConfig) string {
	params := []string{
		fmt.Sprintf("dial_timeout=%ds", config.Timeout),
		fmt.Sprintf("read_timeout=%ds", config.Timeout),
	}
	if config.SSLEnabled {
		params = append(params, "secure=true")
	}

	return fmt.Sprintf("clickhouse://%s:%s@%s:%d/%s?%s",
		url.QueryEscape(config.Username),
		url.QueryEscape(config.Password),
		config.Host,
		config.Port,
		config.Database,
		strings.Join(params, "&"),
	)
}

// openConn opens a database/sql connection to ClickHouse using the HTTP protocol.
func openConn(config *CHConfig) (*sql.DB, error) {
	dsn := buildDSN(config)

	db, err := sql.Open("clickhouse", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open ClickHouse connection: %w", err)
	}

	// Configure for short-lived tool calls
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(0)
	db.SetConnMaxLifetime(time.Duration(config.Timeout) * time.Second)

	return db, nil
}

// executeQueryInternal runs a query against ClickHouse and returns JSON rows.
func (t *ClickHouseTool) executeQueryInternal(ctx context.Context, config *CHConfig, query string) ([]map[string]interface{}, error) {
	if t.rateLimiter != nil {
		if err := t.rateLimiter.Wait(ctx); err != nil {
			return nil, fmt.Errorf("rate limit wait cancelled: %w", err)
		}
	}

	db, err := openConn(config)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	t.logger.Printf("ClickHouse query: %s", truncateQuery(query))

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query execution failed: %w", err)
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("failed to get columns: %w", err)
	}

	var results []map[string]interface{}
	totalSize := 0

	for rows.Next() {
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		row := make(map[string]interface{}, len(columns))
		for i, col := range columns {
			row[col] = values[i]
		}
		results = append(results, row)

		rowJSON, _ := json.Marshal(row)
		totalSize += len(rowJSON)
		if totalSize > MaxResultSize {
			return nil, fmt.Errorf("result exceeds %d MB limit, use LIMIT to reduce result set", MaxResultSize/(1024*1024))
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	return results, nil
}

// cachedQuery executes a query with response caching
func (t *ClickHouseTool) cachedQuery(ctx context.Context, incidentID, cacheKey string, ttl time.Duration, queryFn func() (string, error), logicalName ...string) (string, error) {
	fullCacheKey := cacheKey
	if len(logicalName) > 0 && logicalName[0] != "" {
		fullCacheKey = fmt.Sprintf("logical:%s:%s", logicalName[0], cacheKey)
	} else {
		fullCacheKey = fmt.Sprintf("incident:%s:%s", incidentID, cacheKey)
	}

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

// rowsToJSON converts query result rows to a JSON string.
func rowsToJSON(rows []map[string]interface{}) (string, error) {
	if rows == nil {
		rows = []map[string]interface{}{}
	}
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

// getDatabase extracts the database parameter, defaulting to empty (use configured DB)
func getDatabase(args map[string]interface{}) string {
	if v, ok := args["database"].(string); ok && v != "" {
		return v
	}
	return ""
}

// hasLimitClause checks if the outermost query already contains a LIMIT clause.
func hasLimitClause(query string) bool {
	cleaned := stripSQLComments(stripSQLLiterals(query))
	for {
		stripped := parenGroupPattern.ReplaceAllString(cleaned, " ")
		if stripped == cleaned {
			break
		}
		cleaned = stripped
	}
	return limitPattern.MatchString(cleaned)
}

// --- Tool methods ---

// ExecuteQuery executes an arbitrary SELECT query with safety validation
func (t *ClickHouseTool) ExecuteQuery(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	query, ok := args["query"].(string)
	if !ok || query == "" {
		return "", fmt.Errorf("query is required%s", validation.SuggestParam("query", args))
	}

	if !isSelectOnly(query) {
		return "", fmt.Errorf("only SELECT queries are allowed (write statements and dangerous operations are blocked)")
	}

	// Apply per-query timeout if specified
	if v, ok := args["timeout_seconds"].(float64); ok {
		timeout := clampTimeout(int(v))
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
		defer cancel()
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

// ShowDatabases lists all databases
func (t *ClickHouseTool) ShowDatabases(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	cacheKey := responseCacheKey("show_databases", nil)

	return t.cachedQuery(ctx, incidentID, cacheKey, SchemaCacheTTL, func() (string, error) {
		config, err := t.resolveConfig(ctx, incidentID, logicalName)
		if err != nil {
			return "", err
		}
		rows, err := t.execQuery(ctx, config, "SHOW DATABASES")
		if err != nil {
			return "", err
		}
		return rowsToJSON(rows)
	}, logicalName)
}

// ShowTables lists tables in a database
func (t *ClickHouseTool) ShowTables(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)
	db := getDatabase(args)

	query := "SHOW TABLES"
	if db != "" {
		query = fmt.Sprintf("SHOW TABLES FROM %s", sanitizeIdentifier(db))
	}

	cacheKey := responseCacheKey("show_tables", map[string]string{"database": db})

	return t.cachedQuery(ctx, incidentID, cacheKey, SchemaCacheTTL, func() (string, error) {
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

// DescribeTable describes columns of a table
func (t *ClickHouseTool) DescribeTable(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	tableName, ok := args["table_name"].(string)
	if !ok || tableName == "" {
		return "", fmt.Errorf("table_name is required%s", validation.SuggestParam("table_name", args))
	}

	db := getDatabase(args)
	var query string
	if db != "" {
		query = fmt.Sprintf("DESCRIBE TABLE %s.%s", sanitizeIdentifier(db), sanitizeIdentifier(tableName))
	} else {
		query = fmt.Sprintf("DESCRIBE TABLE %s", sanitizeIdentifier(tableName))
	}

	cacheKey := responseCacheKey("describe_table", map[string]string{"table": tableName, "database": db})

	return t.cachedQuery(ctx, incidentID, cacheKey, SchemaCacheTTL, func() (string, error) {
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

// GetQueryLog retrieves recent queries from system.query_log
func (t *ClickHouseTool) GetQueryLog(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)
	limit := parseLimit(args)

	conditions := []string{"type != 1"} // Exclude running queries (type=1 is QueryStart)

	if v, ok := args["min_duration_ms"].(float64); ok && v > 0 {
		conditions = append(conditions, fmt.Sprintf("query_duration_ms >= %d", int(v)))
	}
	if v, ok := args["query_kind"].(string); ok && v != "" {
		conditions = append(conditions, fmt.Sprintf("query_kind = '%s'", sanitizeStringValue(v)))
	}

	query := fmt.Sprintf(
		"SELECT query_id, query, user, query_duration_ms, read_rows, read_bytes, result_rows, result_bytes, memory_usage, event_time "+
			"FROM system.query_log WHERE %s ORDER BY event_time DESC LIMIT %d",
		strings.Join(conditions, " AND "), limit,
	)

	cacheKey := responseCacheKey("get_query_log", args)

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

// GetRunningQueries retrieves active queries from system.processes
func (t *ClickHouseTool) GetRunningQueries(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)
	limit := parseLimit(args)

	conditions := []string{"1=1"}

	if v, ok := args["min_elapsed_seconds"].(float64); ok && v > 0 {
		conditions = append(conditions, fmt.Sprintf("elapsed >= %d", int(v)))
	}

	query := fmt.Sprintf(
		"SELECT query_id, query, user, elapsed, read_rows, read_bytes, memory_usage, is_cancelled "+
			"FROM system.processes WHERE %s ORDER BY elapsed DESC LIMIT %d",
		strings.Join(conditions, " AND "), limit,
	)

	cacheKey := responseCacheKey("get_running_queries", args)

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

// GetMerges retrieves active merges from system.merges
func (t *ClickHouseTool) GetMerges(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)
	limit := parseLimit(args)

	conditions := []string{"1=1"}

	if v, ok := args["database"].(string); ok && v != "" {
		conditions = append(conditions, fmt.Sprintf("database = '%s'", sanitizeStringValue(v)))
	}
	if v, ok := args["table"].(string); ok && v != "" {
		conditions = append(conditions, fmt.Sprintf("table = '%s'", sanitizeStringValue(v)))
	}

	query := fmt.Sprintf(
		"SELECT database, table, elapsed, progress, num_parts, total_size_bytes_compressed, result_part_name "+
			"FROM system.merges WHERE %s ORDER BY elapsed DESC LIMIT %d",
		strings.Join(conditions, " AND "), limit,
	)

	cacheKey := responseCacheKey("get_merges", args)

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

// GetReplicationStatus retrieves replication queue from system.replication_queue
func (t *ClickHouseTool) GetReplicationStatus(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)
	limit := parseLimit(args)

	conditions := []string{"1=1"}

	if v, ok := args["database"].(string); ok && v != "" {
		conditions = append(conditions, fmt.Sprintf("database = '%s'", sanitizeStringValue(v)))
	}
	if v, ok := args["table"].(string); ok && v != "" {
		conditions = append(conditions, fmt.Sprintf("table = '%s'", sanitizeStringValue(v)))
	}

	query := fmt.Sprintf(
		"SELECT database, table, type, create_time, num_tries, last_exception, num_postponed, postpone_reason "+
			"FROM system.replication_queue WHERE %s ORDER BY create_time DESC LIMIT %d",
		strings.Join(conditions, " AND "), limit,
	)

	cacheKey := responseCacheKey("get_replication_status", args)

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

// GetPartsInfo retrieves parts info from system.parts
func (t *ClickHouseTool) GetPartsInfo(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)
	limit := parseLimit(args)

	tableName, ok := args["table_name"].(string)
	if !ok || tableName == "" {
		return "", fmt.Errorf("table_name is required%s", validation.SuggestParam("table_name", args))
	}

	conditions := []string{fmt.Sprintf("table = '%s'", sanitizeStringValue(tableName))}

	db := getDatabase(args)
	if db != "" {
		conditions = append(conditions, fmt.Sprintf("database = '%s'", sanitizeStringValue(db)))
	}

	if v, ok := args["active_only"].(bool); ok && v {
		conditions = append(conditions, "active = 1")
	}

	query := fmt.Sprintf(
		"SELECT database, table, name, partition, rows, bytes_on_disk, data_compressed_bytes, data_uncompressed_bytes, modification_time, active "+
			"FROM system.parts WHERE %s ORDER BY modification_time DESC LIMIT %d",
		strings.Join(conditions, " AND "), limit,
	)

	cacheKey := responseCacheKey("get_parts_info", args)

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

// GetClusterInfo retrieves cluster topology from system.clusters
func (t *ClickHouseTool) GetClusterInfo(ctx context.Context, incidentID string, args map[string]interface{}) (string, error) {
	logicalName := extractLogicalName(args)

	conditions := []string{"1=1"}

	if v, ok := args["cluster"].(string); ok && v != "" {
		conditions = append(conditions, fmt.Sprintf("cluster = '%s'", sanitizeStringValue(v)))
	}

	limit := parseLimit(args)

	query := fmt.Sprintf(
		"SELECT cluster, shard_num, replica_num, host_name, host_address, port, is_local "+
			"FROM system.clusters WHERE %s ORDER BY cluster, shard_num, replica_num LIMIT %d",
		strings.Join(conditions, " AND "), limit,
	)

	cacheKey := responseCacheKey("get_cluster_info", args)

	return t.cachedQuery(ctx, incidentID, cacheKey, SchemaCacheTTL, func() (string, error) {
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

// --- Helpers ---

// sanitizeIdentifier sanitizes a SQL identifier (database/table name) to prevent injection.
// Only allows alphanumeric, underscore, and dot characters.
var identifierPattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_.]*$`)

func sanitizeIdentifier(name string) string {
	if identifierPattern.MatchString(name) {
		return name
	}
	// Backtick-quote non-conforming identifiers
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

// sanitizeStringValue escapes string values for safe embedding in ClickHouse queries.
// Escapes backslashes first (to prevent \' from being interpreted as an escaped quote
// when allow_backslash_escaping_in_strings=1, which is the ClickHouse default), then
// escapes single quotes using standard SQL '' escaping.
func sanitizeStringValue(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	return strings.ReplaceAll(s, "'", "''")
}
