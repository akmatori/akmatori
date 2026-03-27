package clickhouse

import (
	"context"
	"fmt"
	"io"
	"log"
	"strings"
	"testing"

	"github.com/akmatori/mcp-gateway/internal/ratelimit"
)

func testLogger() *log.Logger {
	return log.New(io.Discard, "", 0)
}

func TestNewClickHouseTool(t *testing.T) {
	limiter := ratelimit.New(10, 20)
	tool := NewClickHouseTool(testLogger(), limiter)

	if tool == nil {
		t.Fatal("expected non-nil tool")
	}
	if tool.configCache == nil {
		t.Error("expected non-nil configCache")
	}
	if tool.responseCache == nil {
		t.Error("expected non-nil responseCache")
	}
	if tool.rateLimiter == nil {
		t.Error("expected non-nil rateLimiter")
	}
	if tool.logger == nil {
		t.Error("expected non-nil logger")
	}
	tool.Stop()
}

func TestNewClickHouseTool_NilLimiter(t *testing.T) {
	tool := NewClickHouseTool(testLogger(), nil)
	if tool == nil {
		t.Fatal("expected non-nil tool")
	}
	if tool.rateLimiter != nil {
		t.Error("expected nil rateLimiter")
	}
	tool.Stop()
}

func TestStop_Idempotent(t *testing.T) {
	tool := NewClickHouseTool(testLogger(), nil)
	tool.Stop()
	tool.Stop() // Should not panic
}

func TestConfigCacheKey(t *testing.T) {
	key := configCacheKey("incident-123")
	if key != "creds:incident-123:clickhouse" {
		t.Errorf("unexpected cache key: %s", key)
	}
}

func TestResponseCacheKey(t *testing.T) {
	key1 := responseCacheKey("SELECT 1", nil)
	key2 := responseCacheKey("SELECT 2", nil)
	if key1 == key2 {
		t.Error("expected different cache keys for different queries")
	}

	key3 := responseCacheKey("SELECT 1", nil)
	if key1 != key3 {
		t.Error("expected same cache key for same query")
	}
}

func TestExtractLogicalName(t *testing.T) {
	tests := []struct {
		name string
		args map[string]interface{}
		want string
	}{
		{"with logical name", map[string]interface{}{"logical_name": "prod-ch"}, "prod-ch"},
		{"without logical name", map[string]interface{}{}, ""},
		{"wrong type", map[string]interface{}{"logical_name": 123}, ""},
		{"nil args", nil, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractLogicalName(tt.args)
			if got != tt.want {
				t.Errorf("extractLogicalName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClampTimeout(t *testing.T) {
	tests := []struct {
		name  string
		input int
		want  int
	}{
		{"zero defaults to min", 0, MinTimeout},
		{"negative defaults to min", -1, MinTimeout},
		{"below min defaults to min", 3, MinTimeout},
		{"at min", 5, 5},
		{"normal", 60, 60},
		{"at max", 300, 300},
		{"above max clamped", 500, 300},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := clampTimeout(tt.input)
			if got != tt.want {
				t.Errorf("clampTimeout(%d) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestIsSelectOnly(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  bool
	}{
		// Allowed statements
		{"simple select", "SELECT * FROM users", true},
		{"select with where", "SELECT id, name FROM users WHERE active = 1", true},
		{"select with join", "SELECT u.name, o.id FROM users u JOIN orders o ON u.id = o.user_id", true},
		{"CTE with select", "WITH active_users AS (SELECT * FROM users WHERE active = 1) SELECT * FROM active_users", true},
		{"nested subquery", "SELECT * FROM (SELECT id FROM users) sub", true},
		{"select count", "SELECT COUNT(*) FROM orders", true},
		{"SHOW databases", "SHOW DATABASES", true},
		{"SHOW tables", "SHOW TABLES FROM default", true},
		{"DESCRIBE table", "DESCRIBE TABLE users", true},
		{"DESC table", "DESC users", true},
		{"EXPLAIN select", "EXPLAIN SELECT * FROM users", true},
		{"EXISTS subquery", "EXISTS (SELECT 1 FROM users)", true},

		// Blocked statements
		{"insert", "INSERT INTO users VALUES (1, 'test')", false},
		{"alter table", "ALTER TABLE users ADD COLUMN age UInt32", false},
		{"drop table", "DROP TABLE users", false},
		{"create table", "CREATE TABLE test (id UInt32) ENGINE = MergeTree()", false},
		{"truncate", "TRUNCATE TABLE users", false},
		{"rename table", "RENAME TABLE users TO users_old", false},
		{"exchange tables", "EXCHANGE TABLES users AND users_new", false},
		{"grant", "GRANT SELECT ON users TO readonly", false},
		{"revoke", "REVOKE ALL ON users FROM public", false},
		{"kill query", "KILL QUERY WHERE query_id = 'abc'", false},
		{"system reload", "SYSTEM RELOAD CONFIG", false},
		{"optimize table", "OPTIMIZE TABLE users FINAL", false},
		{"attach table", "ATTACH TABLE users", false},
		{"detach table", "DETACH TABLE users", false},
		{"move partition", "ALTER TABLE users MOVE PARTITION 202301 TO DISK 'cold'", false},

		// Case variations
		{"mixed case insert", "InSeRt INTO users VALUES (1)", false},
		{"mixed case drop", "dRoP TABLE users", false},

		// Comments should be stripped
		{"select with line comment", "SELECT * FROM users -- this is a comment", true},
		{"select with block comment", "SELECT * FROM users /* comment */", true},
		{"dangerous in line comment only", "SELECT * FROM users -- INSERT INTO foo", true},
		{"dangerous in block comment only", "SELECT * FROM users /* DROP TABLE foo */", true},

		// Keywords inside string literals should not trigger false positives
		{"keyword DELETE in string literal", "SELECT * FROM events WHERE status = 'DELETE_PENDING'", true},
		{"keyword INSERT in string literal", "SELECT * FROM logs WHERE action = 'INSERT'", true},
		{"keyword DROP in string literal", "SELECT * FROM audit WHERE op = 'DROP TABLE'", true},
		{"keyword SYSTEM in string literal", "SELECT * FROM t WHERE msg = 'SYSTEM reload'", true},

		// Semicolons
		{"select with semicolon", "SELECT * FROM users;", true},
		{"multi-statement injection", "SELECT * FROM users; DROP TABLE users", false},

		// Non-query inputs
		{"comment-only input", "-- just a comment", false},
		{"empty after comment strip", "/* block comment only */", false},
		{"whitespace only", "   ", false},
		{"empty string", "", false},

		// SELECT/WITH with leading comments should still pass
		{"select with leading line comment", "-- a comment\nSELECT 1", true},
		{"select with leading block comment", "/* comment */ SELECT 1", true},
		{"WITH after leading comment", "-- intro\nWITH cte AS (SELECT 1) SELECT * FROM cte", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isSelectOnly(tt.query)
			if got != tt.want {
				t.Errorf("isSelectOnly(%q) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}

func TestParseSettings(t *testing.T) {
	tests := []struct {
		name     string
		settings map[string]interface{}
		check    func(*CHConfig) error
	}{
		{
			"all defaults",
			map[string]interface{}{},
			func(c *CHConfig) error {
				if c.Port != DefaultPort {
					return fmt.Errorf("expected port %d, got %d", DefaultPort, c.Port)
				}
				if c.Timeout != DefaultTimeout {
					return fmt.Errorf("expected timeout %d, got %d", DefaultTimeout, c.Timeout)
				}
				if c.SSLEnabled {
					return fmt.Errorf("expected SSLEnabled=false")
				}
				return nil
			},
		},
		{
			"all fields set",
			map[string]interface{}{
				"ch_host":        "ch.example.com",
				"ch_port":        float64(9000),
				"ch_database":    "analytics",
				"ch_username":    "admin",
				"ch_password":    "secret",
				"ch_ssl_enabled": true,
				"ch_timeout":     float64(60),
			},
			func(c *CHConfig) error {
				if c.Host != "ch.example.com" {
					return fmt.Errorf("expected host 'ch.example.com', got %q", c.Host)
				}
				if c.Port != 9000 {
					return fmt.Errorf("expected port 9000, got %d", c.Port)
				}
				if c.Database != "analytics" {
					return fmt.Errorf("expected database 'analytics', got %q", c.Database)
				}
				if c.Username != "admin" {
					return fmt.Errorf("expected username 'admin', got %q", c.Username)
				}
				if c.Password != "secret" {
					return fmt.Errorf("expected password 'secret', got %q", c.Password)
				}
				if !c.SSLEnabled {
					return fmt.Errorf("expected SSLEnabled=true")
				}
				if c.Timeout != 60 {
					return fmt.Errorf("expected timeout 60, got %d", c.Timeout)
				}
				return nil
			},
		},
		{
			"invalid port ignored",
			map[string]interface{}{
				"ch_port": float64(0),
			},
			func(c *CHConfig) error {
				if c.Port != DefaultPort {
					return fmt.Errorf("expected default port %d, got %d", DefaultPort, c.Port)
				}
				return nil
			},
		},
		{
			"timeout clamped to min",
			map[string]interface{}{
				"ch_timeout": float64(1),
			},
			func(c *CHConfig) error {
				if c.Timeout != MinTimeout {
					return fmt.Errorf("expected timeout %d, got %d", MinTimeout, c.Timeout)
				}
				return nil
			},
		},
		{
			"timeout clamped to max",
			map[string]interface{}{
				"ch_timeout": float64(999),
			},
			func(c *CHConfig) error {
				if c.Timeout != MaxTimeout {
					return fmt.Errorf("expected timeout %d, got %d", MaxTimeout, c.Timeout)
				}
				return nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := parseSettings(tt.settings)
			if err := tt.check(config); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestBuildDSN(t *testing.T) {
	tests := []struct {
		name   string
		config *CHConfig
		want   []string // substrings that must be present
	}{
		{
			"basic DSN",
			&CHConfig{Host: "localhost", Port: 8123, Database: "default", Username: "user", Password: "pass", Timeout: 30},
			[]string{"clickhouse://user:pass@localhost:8123/default", "dial_timeout=30s", "read_timeout=30s"},
		},
		{
			"with SSL",
			&CHConfig{Host: "ch.example.com", Port: 8443, Database: "analytics", Username: "admin", Password: "secret", SSLEnabled: true, Timeout: 60},
			[]string{"clickhouse://admin:secret@ch.example.com:8443/analytics", "secure=true", "dial_timeout=60s"},
		},
		{
			"without SSL",
			&CHConfig{Host: "localhost", Port: 8123, Database: "test", Username: "u", Password: "p", SSLEnabled: false, Timeout: 30},
			[]string{"clickhouse://u:p@localhost:8123/test"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dsn := buildDSN(tt.config)
			for _, want := range tt.want {
				if !strings.Contains(dsn, want) {
					t.Errorf("DSN %q missing expected substring %q", dsn, want)
				}
			}
			if !tt.config.SSLEnabled && strings.Contains(dsn, "secure=true") {
				t.Errorf("DSN %q should not contain secure=true when SSL is disabled", dsn)
			}
		})
	}
}

func TestGetConfig_CacheHit(t *testing.T) {
	tool := NewClickHouseTool(testLogger(), nil)
	defer tool.Stop()

	config := &CHConfig{
		Host:     "localhost",
		Port:     8123,
		Database: "testdb",
		Username: "user",
		Password: "pass",
		Timeout:  30,
	}
	tool.configCache.Set(configCacheKey("test-incident"), config)

	got, err := tool.resolveConfigFromDB(nil, "test-incident")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Host != "localhost" {
		t.Errorf("expected host 'localhost', got %q", got.Host)
	}
	if got.Database != "testdb" {
		t.Errorf("expected database 'testdb', got %q", got.Database)
	}
}

func TestGetConfig_LogicalNameCacheHit(t *testing.T) {
	tool := NewClickHouseTool(testLogger(), nil)
	defer tool.Stop()

	config := &CHConfig{
		Host:     "prod-host",
		Port:     8123,
		Database: "proddb",
		Username: "admin",
		Password: "secret",
		Timeout:  30,
	}
	tool.configCache.Set("creds:logical:clickhouse:prod-ch", config)

	got, err := tool.resolveConfigFromDB(nil, "test-incident", "prod-ch")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Host != "prod-host" {
		t.Errorf("expected host 'prod-host', got %q", got.Host)
	}
}

func TestParseLimit(t *testing.T) {
	tests := []struct {
		name string
		args map[string]interface{}
		want int
	}{
		{"default", map[string]interface{}{}, DefaultLimit},
		{"custom", map[string]interface{}{"limit": float64(50)}, 50},
		{"zero", map[string]interface{}{"limit": float64(0)}, DefaultLimit},
		{"negative", map[string]interface{}{"limit": float64(-1)}, DefaultLimit},
		{"above max", map[string]interface{}{"limit": float64(2000)}, MaxLimit},
		{"at max", map[string]interface{}{"limit": float64(1000)}, MaxLimit},
		{"wrong type", map[string]interface{}{"limit": "50"}, DefaultLimit},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseLimit(tt.args)
			if got != tt.want {
				t.Errorf("parseLimit() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestGetDatabase(t *testing.T) {
	tests := []struct {
		name string
		args map[string]interface{}
		want string
	}{
		{"default", map[string]interface{}{}, ""},
		{"custom", map[string]interface{}{"database": "analytics"}, "analytics"},
		{"empty string", map[string]interface{}{"database": ""}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getDatabase(tt.args)
			if got != tt.want {
				t.Errorf("getDatabase() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHasLimitClause(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  bool
	}{
		{"no limit", "SELECT * FROM users", false},
		{"with limit", "SELECT * FROM users LIMIT 10", true},
		{"lowercase limit", "SELECT * FROM users limit 10", true},
		{"limit in comment", "SELECT * FROM users -- LIMIT 10", false},
		{"limit in string literal", "SELECT * FROM t WHERE status = 'LIMIT reached'", false},
		{"limit in subquery only", "SELECT * FROM users WHERE id IN (SELECT user_id FROM admins LIMIT 1)", false},
		{"limit in subquery and outer", "SELECT * FROM users WHERE id IN (SELECT user_id FROM admins LIMIT 1) LIMIT 50", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasLimitClause(tt.query)
			if got != tt.want {
				t.Errorf("hasLimitClause(%q) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}

func TestTruncateQuery(t *testing.T) {
	short := "SELECT 1"
	if truncateQuery(short) != short {
		t.Error("short query should not be truncated")
	}

	long := strings.Repeat("x", 300)
	truncated := truncateQuery(long)
	if len(truncated) != 203 {
		t.Errorf("expected truncated length 203, got %d", len(truncated))
	}
}

func TestRowsToJSON(t *testing.T) {
	rows := []map[string]interface{}{
		{"id": 1, "name": "test"},
		{"id": 2, "name": "foo"},
	}
	result, err := rowsToJSON(rows)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "test") || !strings.Contains(result, "foo") {
		t.Errorf("unexpected result: %s", result)
	}
}

func TestRowsToJSON_Empty(t *testing.T) {
	result, err := rowsToJSON(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "[]" {
		t.Errorf("expected '[]', got %q", result)
	}
}

func TestRowsToJSON_EmptySlice(t *testing.T) {
	result, err := rowsToJSON([]map[string]interface{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "[]" {
		t.Errorf("expected '[]', got %q", result)
	}
}

func TestRowsToJSON_MarshalError(t *testing.T) {
	rows := []map[string]interface{}{{"bad": make(chan int)}}
	_, err := rowsToJSON(rows)
	if err == nil {
		t.Error("expected marshal error")
	}
	if err != nil && !strings.Contains(err.Error(), "failed to marshal") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSanitizeIdentifier(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"simple name", "users", "users"},
		{"with underscore", "my_table", "my_table"},
		{"with dot", "default.users", "default.users"},
		{"needs quoting - space", "my table", "`my table`"},
		{"needs quoting - special chars", "table-name", "`table-name`"},
		{"needs quoting - starts with digit", "1table", "`1table`"},
		{"backtick injection", "tab`le", "`tab``le`"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeIdentifier(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeIdentifier(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSanitizeStringValue(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"no special chars", "default", "default"},
		{"single quote", "it's", "it\\'s"},
		{"multiple quotes", "a'b'c", "a\\'b\\'c"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeStringValue(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeStringValue(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// --- Tool method tests with mock execQuery ---

func newToolWithMock(mockExec queryExecFunc) *ClickHouseTool {
	tool := NewClickHouseTool(testLogger(), nil)
	tool.execQuery = mockExec
	tool.resolveConfig = func(ctx context.Context, incidentID string, logicalName ...string) (*CHConfig, error) {
		return &CHConfig{Host: "localhost", Port: 8123, Database: "default", Username: "user", Password: "pass", Timeout: 30}, nil
	}
	return tool
}

func TestExecuteQuery_RequiresQuery(t *testing.T) {
	tool := NewClickHouseTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.ExecuteQuery(nil, "test-incident", map[string]interface{}{})
	if err == nil {
		t.Error("expected error for missing query")
	}
	if err != nil && !strings.Contains(err.Error(), "query is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestExecuteQuery_EmptyQuery(t *testing.T) {
	tool := NewClickHouseTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.ExecuteQuery(nil, "test-incident", map[string]interface{}{"query": ""})
	if err == nil {
		t.Error("expected error for empty query")
	}
}

func TestExecuteQuery_RejectsNonSelect(t *testing.T) {
	tool := NewClickHouseTool(testLogger(), nil)
	defer tool.Stop()

	tests := []struct {
		name  string
		query string
	}{
		{"insert", "INSERT INTO users VALUES (1, 'test')"},
		{"drop", "DROP TABLE users"},
		{"alter", "ALTER TABLE users ADD COLUMN age UInt32"},
		{"create", "CREATE TABLE test (id UInt32) ENGINE = MergeTree()"},
		{"truncate", "TRUNCATE TABLE users"},
		{"system", "SYSTEM RELOAD CONFIG"},
		{"kill", "KILL QUERY WHERE query_id = 'abc'"},
		{"optimize", "OPTIMIZE TABLE users FINAL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tool.ExecuteQuery(nil, "test-incident", map[string]interface{}{"query": tt.query})
			if err == nil {
				t.Error("expected error for non-SELECT query")
			}
			if err != nil && !strings.Contains(err.Error(), "only SELECT queries are allowed") {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestExecuteQuery_WithMock(t *testing.T) {
	mockRows := []map[string]interface{}{{"id": 1, "name": "alice"}}
	tool := newToolWithMock(func(ctx context.Context, config *CHConfig, query string) ([]map[string]interface{}, error) {
		return mockRows, nil
	})
	defer tool.Stop()

	result, err := tool.ExecuteQuery(context.Background(), "test-incident", map[string]interface{}{
		"query": "SELECT * FROM users",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "alice") {
		t.Errorf("expected result to contain 'alice', got %q", result)
	}
}

func TestExecuteQuery_AppendsLimit(t *testing.T) {
	var capturedQuery string
	tool := newToolWithMock(func(ctx context.Context, config *CHConfig, query string) ([]map[string]interface{}, error) {
		capturedQuery = query
		return nil, nil
	})
	defer tool.Stop()

	_, err := tool.ExecuteQuery(context.Background(), "test-incident", map[string]interface{}{
		"query": "SELECT * FROM users",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(capturedQuery, " LIMIT 100") {
		t.Errorf("expected LIMIT 100 appended, got %q", capturedQuery)
	}
}

func TestExecuteQuery_PreservesExistingLimit(t *testing.T) {
	var capturedQuery string
	tool := newToolWithMock(func(ctx context.Context, config *CHConfig, query string) ([]map[string]interface{}, error) {
		capturedQuery = query
		return nil, nil
	})
	defer tool.Stop()

	_, err := tool.ExecuteQuery(context.Background(), "test-incident", map[string]interface{}{
		"query": "SELECT * FROM users LIMIT 5",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(capturedQuery, "LIMIT 100") {
		t.Errorf("should not append LIMIT when already present, got %q", capturedQuery)
	}
}

func TestExecuteQuery_CacheHit(t *testing.T) {
	tool := NewClickHouseTool(testLogger(), nil)
	defer tool.Stop()

	config := &CHConfig{Host: "localhost", Port: 8123, Database: "testdb", Username: "user", Password: "pass", Timeout: 30}
	tool.configCache.Set(configCacheKey("test-incident"), config)

	query := "SELECT * FROM users LIMIT 100"
	cacheKey := responseCacheKey(query, map[string]interface{}{"query": "SELECT * FROM users"})
	fullCacheKey := "incident:test-incident:" + cacheKey
	expectedResult := `[{"id":1,"name":"alice"}]`
	tool.responseCache.SetWithTTL(fullCacheKey, expectedResult, QueryCacheTTL)

	result, err := tool.ExecuteQuery(nil, "test-incident", map[string]interface{}{
		"query": "SELECT * FROM users",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != expectedResult {
		t.Errorf("expected cached result %q, got %q", expectedResult, result)
	}
}

func TestShowDatabases_WithMock(t *testing.T) {
	mockRows := []map[string]interface{}{{"name": "default"}, {"name": "system"}}
	tool := newToolWithMock(func(ctx context.Context, config *CHConfig, query string) ([]map[string]interface{}, error) {
		if query != "SHOW DATABASES" {
			t.Errorf("expected 'SHOW DATABASES', got %q", query)
		}
		return mockRows, nil
	})
	defer tool.Stop()

	result, err := tool.ShowDatabases(context.Background(), "test-incident", map[string]interface{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "default") {
		t.Errorf("expected result to contain 'default', got %q", result)
	}
}

func TestShowTables_WithDatabase(t *testing.T) {
	var capturedQuery string
	tool := newToolWithMock(func(ctx context.Context, config *CHConfig, query string) ([]map[string]interface{}, error) {
		capturedQuery = query
		return nil, nil
	})
	defer tool.Stop()

	_, err := tool.ShowTables(context.Background(), "test-incident", map[string]interface{}{
		"database": "analytics",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(capturedQuery, "FROM analytics") {
		t.Errorf("expected query to contain 'FROM analytics', got %q", capturedQuery)
	}
}

func TestShowTables_DefaultDatabase(t *testing.T) {
	var capturedQuery string
	tool := newToolWithMock(func(ctx context.Context, config *CHConfig, query string) ([]map[string]interface{}, error) {
		capturedQuery = query
		return nil, nil
	})
	defer tool.Stop()

	_, err := tool.ShowTables(context.Background(), "test-incident", map[string]interface{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedQuery != "SHOW TABLES" {
		t.Errorf("expected 'SHOW TABLES', got %q", capturedQuery)
	}
}

func TestDescribeTable_RequiresTableName(t *testing.T) {
	tool := NewClickHouseTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.DescribeTable(nil, "test-incident", map[string]interface{}{})
	if err == nil {
		t.Error("expected error for missing table_name")
	}
	if err != nil && !strings.Contains(err.Error(), "table_name is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDescribeTable_WithDatabase(t *testing.T) {
	var capturedQuery string
	tool := newToolWithMock(func(ctx context.Context, config *CHConfig, query string) ([]map[string]interface{}, error) {
		capturedQuery = query
		return nil, nil
	})
	defer tool.Stop()

	_, err := tool.DescribeTable(context.Background(), "test-incident", map[string]interface{}{
		"table_name": "events",
		"database":   "analytics",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(capturedQuery, "analytics.events") {
		t.Errorf("expected query to contain 'analytics.events', got %q", capturedQuery)
	}
}

func TestGetQueryLog_WithFilters(t *testing.T) {
	var capturedQuery string
	tool := newToolWithMock(func(ctx context.Context, config *CHConfig, query string) ([]map[string]interface{}, error) {
		capturedQuery = query
		return nil, nil
	})
	defer tool.Stop()

	_, err := tool.GetQueryLog(context.Background(), "test-incident", map[string]interface{}{
		"min_duration_ms": float64(1000),
		"query_kind":      "Select",
		"limit":           float64(50),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(capturedQuery, "query_duration_ms >= 1000") {
		t.Errorf("expected duration filter, got %q", capturedQuery)
	}
	if !strings.Contains(capturedQuery, "query_kind = 'Select'") {
		t.Errorf("expected query_kind filter, got %q", capturedQuery)
	}
	if !strings.Contains(capturedQuery, "LIMIT 50") {
		t.Errorf("expected LIMIT 50, got %q", capturedQuery)
	}
}

func TestGetRunningQueries_WithFilter(t *testing.T) {
	var capturedQuery string
	tool := newToolWithMock(func(ctx context.Context, config *CHConfig, query string) ([]map[string]interface{}, error) {
		capturedQuery = query
		return nil, nil
	})
	defer tool.Stop()

	_, err := tool.GetRunningQueries(context.Background(), "test-incident", map[string]interface{}{
		"min_elapsed_seconds": float64(10),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(capturedQuery, "elapsed >= 10") {
		t.Errorf("expected elapsed filter, got %q", capturedQuery)
	}
}

func TestGetMerges_WithFilters(t *testing.T) {
	var capturedQuery string
	tool := newToolWithMock(func(ctx context.Context, config *CHConfig, query string) ([]map[string]interface{}, error) {
		capturedQuery = query
		return nil, nil
	})
	defer tool.Stop()

	_, err := tool.GetMerges(context.Background(), "test-incident", map[string]interface{}{
		"database": "analytics",
		"table":    "events",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(capturedQuery, "database = 'analytics'") {
		t.Errorf("expected database filter, got %q", capturedQuery)
	}
	if !strings.Contains(capturedQuery, "table = 'events'") {
		t.Errorf("expected table filter, got %q", capturedQuery)
	}
}

func TestGetReplicationStatus_WithFilters(t *testing.T) {
	var capturedQuery string
	tool := newToolWithMock(func(ctx context.Context, config *CHConfig, query string) ([]map[string]interface{}, error) {
		capturedQuery = query
		return nil, nil
	})
	defer tool.Stop()

	_, err := tool.GetReplicationStatus(context.Background(), "test-incident", map[string]interface{}{
		"database": "main",
		"table":    "orders",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(capturedQuery, "database = 'main'") {
		t.Errorf("expected database filter, got %q", capturedQuery)
	}
	if !strings.Contains(capturedQuery, "table = 'orders'") {
		t.Errorf("expected table filter, got %q", capturedQuery)
	}
}

func TestGetPartsInfo_RequiresTableName(t *testing.T) {
	tool := NewClickHouseTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.GetPartsInfo(nil, "test-incident", map[string]interface{}{})
	if err == nil {
		t.Error("expected error for missing table_name")
	}
	if err != nil && !strings.Contains(err.Error(), "table_name is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGetPartsInfo_WithActiveOnly(t *testing.T) {
	var capturedQuery string
	tool := newToolWithMock(func(ctx context.Context, config *CHConfig, query string) ([]map[string]interface{}, error) {
		capturedQuery = query
		return nil, nil
	})
	defer tool.Stop()

	_, err := tool.GetPartsInfo(context.Background(), "test-incident", map[string]interface{}{
		"table_name":  "events",
		"active_only": true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(capturedQuery, "active = 1") {
		t.Errorf("expected active_only filter, got %q", capturedQuery)
	}
}

func TestGetClusterInfo_WithFilter(t *testing.T) {
	var capturedQuery string
	tool := newToolWithMock(func(ctx context.Context, config *CHConfig, query string) ([]map[string]interface{}, error) {
		capturedQuery = query
		return nil, nil
	})
	defer tool.Stop()

	_, err := tool.GetClusterInfo(context.Background(), "test-incident", map[string]interface{}{
		"cluster": "main_cluster",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(capturedQuery, "cluster = 'main_cluster'") {
		t.Errorf("expected cluster filter, got %q", capturedQuery)
	}
}

func TestGetClusterInfo_NoFilter(t *testing.T) {
	var capturedQuery string
	tool := newToolWithMock(func(ctx context.Context, config *CHConfig, query string) ([]map[string]interface{}, error) {
		capturedQuery = query
		return nil, nil
	})
	defer tool.Stop()

	_, err := tool.GetClusterInfo(context.Background(), "test-incident", map[string]interface{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(capturedQuery, "system.clusters") {
		t.Errorf("expected query against system.clusters, got %q", capturedQuery)
	}
}

func TestExecuteQuery_MockError(t *testing.T) {
	tool := newToolWithMock(func(ctx context.Context, config *CHConfig, query string) ([]map[string]interface{}, error) {
		return nil, fmt.Errorf("connection refused")
	})
	defer tool.Stop()

	_, err := tool.ExecuteQuery(context.Background(), "test-incident", map[string]interface{}{
		"query": "SELECT 1",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestStripSQLComments(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"no comments", "SELECT 1", "SELECT 1"},
		{"line comment", "SELECT 1 -- comment", "SELECT 1  "},
		{"block comment", "SELECT /* comment */ 1", "SELECT   1"},
		{"multi-line block", "SELECT /* line1\nline2 */ 1", "SELECT   1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripSQLComments(tt.input)
			if got != tt.want {
				t.Errorf("stripSQLComments(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestExecuteQuery_WithLogicalName(t *testing.T) {
	tool := NewClickHouseTool(testLogger(), nil)
	defer tool.Stop()

	config := &CHConfig{Host: "prod-host", Port: 8123, Database: "proddb", Username: "admin", Password: "secret", Timeout: 30}
	tool.configCache.Set("creds:logical:clickhouse:prod-ch", config)

	query := "SELECT 1 LIMIT 100"
	cacheKey := responseCacheKey(query, map[string]interface{}{"query": "SELECT 1", "logical_name": "prod-ch"})
	fullCacheKey := "logical:prod-ch:" + cacheKey
	expectedResult := `[{"1":1}]`
	tool.responseCache.SetWithTTL(fullCacheKey, expectedResult, QueryCacheTTL)

	result, err := tool.ExecuteQuery(nil, "test-incident", map[string]interface{}{
		"query":        "SELECT 1",
		"logical_name": "prod-ch",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != expectedResult {
		t.Errorf("expected cached result %q, got %q", expectedResult, result)
	}
}
