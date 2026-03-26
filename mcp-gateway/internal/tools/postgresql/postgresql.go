package postgresql

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
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
var dangerousStmtPattern = regexp.MustCompile(`(?i)\b(INSERT|UPDATE|DELETE|DROP|ALTER|CREATE|TRUNCATE|GRANT|REVOKE|COPY|DO|CALL|SET|LOCK|MERGE)\b`)

// dangerousFuncPattern matches SQL functions that can affect other sessions or server state.
var dangerousFuncPattern = regexp.MustCompile(`(?i)\b(pg_terminate_backend|pg_cancel_backend|pg_reload_conf|pg_rotate_logfile|pg_switch_wal|set_config)\s*\(`)

// Pre-compiled regex patterns for SQL comment stripping and LIMIT detection
var (
	blockCommentPattern  = regexp.MustCompile(`/\*[\s\S]*?\*/`)
	lineCommentPattern   = regexp.MustCompile(`--[^\n]*`)
	singleQuoteLiteral   = regexp.MustCompile(`'(?:[^'\\]|\\.|\'{2})*'`)
	dollarQuoteLiteral   = regexp.MustCompile(`\$[^$]*\$[\s\S]*?\$[^$]*\$`)
	doubleQuotedIdent    = regexp.MustCompile(`"(?:[^"\\]|\\.|""){0,128}"`)
	validSSLModes        = map[string]bool{"disable": true, "require": true, "verify-ca": true, "verify-full": true}
	limitPattern         = regexp.MustCompile(`(?i)(\bLIMIT\b|\bFETCH\s+(FIRST|NEXT)\b)`)
	explainPattern       = regexp.MustCompile(`(?i)^\s*EXPLAIN\b`)
	selectStartPattern   = regexp.MustCompile(`(?i)^\s*(SELECT|WITH)\b`)
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
	return fmt.Sprintf("pg:%s", hex.EncodeToString(hash[:]))
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

// isSelectOnly validates that a SQL query is read-only.
// Uses a positive allowlist: query must start with SELECT or WITH (after stripping comments/literals).
// Also rejects dangerous statements (INSERT, UPDATE, etc.) and dangerous functions as defense in depth.
// Rejects EXPLAIN statements (use ExplainQuery tool instead).
func isSelectOnly(query string) bool {
	// Strip string literals BEFORE comments so that -- or /* */ inside quoted strings
	// are not mistaken for real comment delimiters.
	cleaned := stripSQLComments(stripSQLLiterals(query))
	// Positive allowlist: query must start with SELECT or WITH
	if !selectStartPattern.MatchString(cleaned) {
		return false
	}
	if dangerousStmtPattern.MatchString(cleaned) {
		return false
	}
	if dangerousFuncPattern.MatchString(cleaned) {
		return false
	}
	// Block EXPLAIN statements — users should use the dedicated ExplainQuery tool
	if explainPattern.MatchString(cleaned) {
		return false
	}
	return true
}

// isReadOnlyQuery validates that a SQL query contains no dangerous statements.
// Uses a positive allowlist: query must start with SELECT or WITH (after stripping comments/literals).
// Unlike isSelectOnly, this does not block EXPLAIN — used by ExplainQuery which wraps the query.
func isReadOnlyQuery(query string) bool {
	cleaned := stripSQLComments(stripSQLLiterals(query))
	// Positive allowlist: query must start with SELECT or WITH
	if !selectStartPattern.MatchString(cleaned) {
		return false
	}
	if dangerousStmtPattern.MatchString(cleaned) {
		return false
	}
	if dangerousFuncPattern.MatchString(cleaned) {
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

// stripSQLLiterals removes string literals and quoted identifiers so keyword detection
// does not match inside quoted values or column/table names.
// Handles single-quoted ('...'), dollar-quoted ($$...$$), and double-quoted ("...") strings.
func stripSQLLiterals(query string) string {
	result := dollarQuoteLiteral.ReplaceAllString(query, "''")
	result = singleQuoteLiteral.ReplaceAllString(result, "''")
	result = doubleQuotedIdent.ReplaceAllString(result, "\"_\"")
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
		p := int(v)
		if p >= 1 && p <= 65535 {
			config.Port = p
		}
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

// verifyCAOnly verifies the server certificate chain against the system root CAs
// without checking the hostname. This implements PostgreSQL's verify-ca SSL mode.
func verifyCAOnly(rawCerts [][]byte, _ [][]*x509.Certificate) error {
	if len(rawCerts) == 0 {
		return fmt.Errorf("no server certificates provided")
	}
	certs := make([]*x509.Certificate, len(rawCerts))
	for i, raw := range rawCerts {
		cert, err := x509.ParseCertificate(raw)
		if err != nil {
			return fmt.Errorf("failed to parse certificate: %w", err)
		}
		certs[i] = cert
	}
	pool, err := x509.SystemCertPool()
	if err != nil {
		return fmt.Errorf("failed to load system cert pool: %w", err)
	}
	opts := x509.VerifyOptions{
		Roots:         pool,
		Intermediates: x509.NewCertPool(),
	}
	for _, cert := range certs[1:] {
		opts.Intermediates.AddCert(cert)
	}
	_, err = certs[0].Verify(opts)
	return err
}

// escapeConnParam escapes a value for use in a pgx keyword=value connection string.
// Values are single-quoted; internal single-quotes and backslashes are backslash-escaped.
func escapeConnParam(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return "'" + s + "'"
}

// parseConfigMu serializes calls to pgx.ParseConfig so that the temporary
// clearing of PG* environment variables is safe under concurrent use.
//
// Why not construct pgx.ConnConfig directly? pgconn.Config has an unexported
// createdByParseConfig field that pgx.ConnectConfig enforces with a panic —
// ParseConfig is the only supported way to create a ConnConfig in pgx v5.
// The mutex protects concurrent buildConnConfig calls from interfering with
// each other. Other goroutines that independently read PG* env vars could
// theoretically observe the temporary unsets; this is accepted because (a) the
// window is sub-millisecond, (b) tool connections are the only PG* consumer
// during request handling, and (c) the gateway's own DB connection is
// established once at startup before any tool calls.
var parseConfigMu sync.Mutex

// pgEnvVarsToShield lists every PG* environment variable that pgx.ParseConfig
// reads. We temporarily clear them before parsing so that ambient env state
// (including PGSERVICE/PGSERVICEFILE which can fail the parse itself) never
// leaks into tool connections.
var pgEnvVarsToShield = []string{
	"PGHOST", "PGPORT", "PGDATABASE", "PGUSER", "PGPASSWORD", "PGPASSFILE",
	"PGAPPNAME", "PGCONNECT_TIMEOUT", "PGSSLMODE", "PGSSLKEY", "PGSSLCERT",
	"PGSSLSNI", "PGSSLROOTCERT", "PGSSLPASSWORD", "PGSSLNEGOTIATION",
	"PGTARGETSESSIONATTRS", "PGSERVICE", "PGSERVICEFILE", "PGTZ", "PGOPTIONS",
	"PGMINPROTOCOLVERSION", "PGMAXPROTOCOLVERSION",
}

// buildConnConfig creates a pgx connection config from PGConfig.
// All PG* environment variables are temporarily cleared during parsing so
// pgx.ParseConfig cannot read ambient env state or service files.
func buildConnConfig(config *PGConfig) (*pgx.ConnConfig, error) {
	// Build a connection string with all parameters explicitly set.
	// sslmode=disable here because we configure TLS manually below via connConfig.TLSConfig.
	// target_session_attrs=any prevents PGTARGETSESSIONATTRS env leak.
	// connect_timeout uses the configured pg_timeout so a blackholed host doesn't hang indefinitely.
	connStr := fmt.Sprintf(
		"host=%s port=%d dbname=%s user=%s password=%s sslmode=disable connect_timeout=%d target_session_attrs=any",
		escapeConnParam(config.Host),
		config.Port,
		escapeConnParam(config.Database),
		escapeConnParam(config.Username),
		escapeConnParam(config.Password),
		config.Timeout,
	)

	// Temporarily clear all PG* env vars so pgx.ParseConfig cannot consult
	// them — including PGSERVICE/PGSERVICEFILE which could fail the parse
	// before we reach the post-parse cleanup.
	parseConfigMu.Lock()
	saved := make(map[string]string)
	for _, key := range pgEnvVarsToShield {
		if val, ok := os.LookupEnv(key); ok {
			saved[key] = val
			os.Unsetenv(key)
		}
	}

	connConfig, err := pgx.ParseConfig(connStr)

	// Restore env vars before releasing the lock.
	for key, val := range saved {
		os.Setenv(key, val)
	}
	parseConfigMu.Unlock()

	if err != nil {
		return nil, fmt.Errorf("failed to create connection config: %w", err)
	}

	// Defense-in-depth: clear fields that may have been set by pgx defaults
	// (not from env vars which are now shielded, but from pgx internal defaults).
	connConfig.Fallbacks = nil
	connConfig.RuntimeParams = map[string]string{}
	connConfig.ValidateConnect = nil
	connConfig.SSLNegotiation = ""
	connConfig.MinProtocolVersion = ""
	connConfig.MaxProtocolVersion = ""
	connConfig.ChannelBinding = ""
	connConfig.KerberosSrvName = ""
	connConfig.KerberosSpn = ""

	// Configure TLS based on SSLMode via pgx TLSConfig (RuntimeParams["sslmode"] is not honored by pgx)
	switch config.SSLMode {
	case "disable":
		connConfig.TLSConfig = nil
	case "require":
		connConfig.TLSConfig = &tls.Config{InsecureSkipVerify: true, ServerName: config.Host} //nolint:gosec // require mode skips cert verification by design; ServerName enables SNI
	case "verify-ca":
		// verify-ca: verify the server certificate is signed by a trusted CA, but do NOT
		// verify the hostname. We must set InsecureSkipVerify=true and use a custom
		// VerifyPeerCertificate to check the chain without hostname matching.
		connConfig.TLSConfig = &tls.Config{
			InsecureSkipVerify: true, //nolint:gosec // hostname verification intentionally skipped for verify-ca; chain is verified below
			ServerName:         config.Host,
			VerifyPeerCertificate: verifyCAOnly,
		}
	case "verify-full":
		connConfig.TLSConfig = &tls.Config{
			InsecureSkipVerify: false,
			ServerName:         config.Host,
		}
	default:
		return nil, fmt.Errorf("unsupported pg_ssl_mode %q: valid values are disable, require, verify-ca, verify-full", config.SSLMode)
	}

	// Set read-only and timeout via RuntimeParams so they apply before any queries
	connConfig.RuntimeParams["default_transaction_read_only"] = "on"
	connConfig.RuntimeParams["statement_timeout"] = strconv.Itoa(config.Timeout * 1000)

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

	// Execute inside explicit read-only transaction for defense in depth
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
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

// rowsToJSON converts query result rows to a JSON string.
// Returns "[]" for nil/empty slices to satisfy the JSON-array contract.
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

// getSchema extracts the schema parameter, defaulting to "public"
func getSchema(args map[string]interface{}) string {
	if v, ok := args["schema"].(string); ok && v != "" {
		return v
	}
	return "public"
}

// hasLimitClause checks if the outermost query already contains a LIMIT clause.
// A LIMIT inside a subquery (parenthesized) does not count as bounding the top-level result.
func hasLimitClause(query string) bool {
	cleaned := stripSQLComments(stripSQLLiterals(query))
	// Remove all parenthesized groups (subqueries) so we only inspect the top-level SQL.
	// Nested parens are handled by repeated stripping until stable.
	for {
		stripped := regexp.MustCompile(`\([^()]*\)`).ReplaceAllString(cleaned, " ")
		if stripped == cleaned {
			break
		}
		cleaned = stripped
	}
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
		return "", fmt.Errorf("only SELECT queries are allowed (write statements, SET, LOCK, EXPLAIN, and dangerous functions are blocked; use explain_query for execution plans)")
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
	paramIdx := 1
	var conditions []string

	if tableName, ok := args["table_name"].(string); ok && tableName != "" {
		conditions = append(conditions, fmt.Sprintf("relname = $%d", paramIdx))
		params["table"] = tableName
		queryArgs = append(queryArgs, tableName)
		paramIdx++
	}

	if schema, ok := args["schema"].(string); ok && schema != "" {
		conditions = append(conditions, fmt.Sprintf("schemaname = $%d", paramIdx))
		params["schema"] = schema
		queryArgs = append(queryArgs, schema)
		paramIdx++ //nolint:ineffassign // keep paramIdx pattern consistent for future parameters
	}

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY n_live_tup DESC"

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

	// Reject queries that already start with EXPLAIN — the tool adds the wrapper automatically.
	// Check this before isReadOnlyQuery so users get a helpful message instead of a generic rejection.
	if explainPattern.MatchString(stripSQLComments(stripSQLLiterals(query))) {
		return "", fmt.Errorf("do not include EXPLAIN in the query; the explain_query tool adds it automatically")
	}

	// ExplainQuery uses isReadOnlyQuery (not isSelectOnly) since EXPLAIN prefix is not expected here
	if !isReadOnlyQuery(query) {
		return "", fmt.Errorf("only SELECT queries are allowed (write statements, SET, LOCK, and dangerous functions are blocked)")
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

	cacheParams := map[string]interface{}{"include_idle": includeIdle}
	if v, ok := args["min_duration_seconds"].(float64); ok {
		cacheParams["min_duration_seconds"] = v
	}
	cacheKey := responseCacheKey("get_active_queries", cacheParams)

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

	query := `SELECT l.locktype, CASE WHEN l.relation IS NOT NULL AND l.database = (SELECT oid FROM pg_database WHERE datname = current_database()) THEN l.relation::regclass::text ELSE NULL END AS relation, l.mode, l.granted, l.pid,
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

	cacheKey := responseCacheKey("get_locks", map[string]interface{}{"blocked_only": blockedOnly})

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
