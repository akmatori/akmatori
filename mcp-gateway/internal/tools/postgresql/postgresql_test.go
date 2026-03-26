package postgresql

import (
	"io"
	"log"
	"testing"

	"github.com/akmatori/mcp-gateway/internal/ratelimit"
)

func testLogger() *log.Logger {
	return log.New(io.Discard, "", 0)
}

func TestNewPostgreSQLTool(t *testing.T) {
	limiter := ratelimit.New(10, 20)
	tool := NewPostgreSQLTool(testLogger(), limiter)

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

func TestNewPostgreSQLTool_NilLimiter(t *testing.T) {
	tool := NewPostgreSQLTool(testLogger(), nil)
	if tool == nil {
		t.Fatal("expected non-nil tool")
	}
	if tool.rateLimiter != nil {
		t.Error("expected nil rateLimiter")
	}
	tool.Stop()
}

func TestStop_Idempotent(t *testing.T) {
	tool := NewPostgreSQLTool(testLogger(), nil)
	tool.Stop()
	tool.Stop() // Should not panic
}

func TestGetConfig_CacheHit(t *testing.T) {
	tool := NewPostgreSQLTool(testLogger(), nil)
	defer tool.Stop()

	config := &PGConfig{
		Host:     "localhost",
		Port:     5432,
		Database: "testdb",
		Username: "user",
		Password: "pass",
		SSLMode:  "disable",
		Timeout:  30,
	}
	tool.configCache.Set(configCacheKey("test-incident"), config)

	got, err := tool.getConfig(nil, "test-incident")
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
	tool := NewPostgreSQLTool(testLogger(), nil)
	defer tool.Stop()

	config := &PGConfig{
		Host:     "prod-host",
		Port:     5432,
		Database: "proddb",
		Username: "admin",
		Password: "secret",
		SSLMode:  "require",
		Timeout:  30,
	}
	tool.configCache.Set("creds:logical:postgresql:prod-pg", config)

	got, err := tool.getConfig(nil, "test-incident", "prod-pg")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Host != "prod-host" {
		t.Errorf("expected host 'prod-host', got %q", got.Host)
	}
}

func TestIsSelectOnly(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  bool
	}{
		{"simple select", "SELECT * FROM users", true},
		{"select with where", "SELECT id, name FROM users WHERE active = true", true},
		{"select with join", "SELECT u.name, o.id FROM users u JOIN orders o ON u.id = o.user_id", true},
		{"CTE with select", "WITH active_users AS (SELECT * FROM users WHERE active = true) SELECT * FROM active_users", true},
		{"nested subquery", "SELECT * FROM (SELECT id FROM users) sub", true},
		{"select count", "SELECT COUNT(*) FROM orders", true},
		{"explain select", "EXPLAIN SELECT * FROM users", true},

		// Dangerous statements
		{"insert", "INSERT INTO users (name) VALUES ('test')", false},
		{"INSERT uppercase", "INSERT INTO users (name) VALUES ('test')", false},
		{"update", "UPDATE users SET name = 'test' WHERE id = 1", false},
		{"delete", "DELETE FROM users WHERE id = 1", false},
		{"drop table", "DROP TABLE users", false},
		{"alter table", "ALTER TABLE users ADD COLUMN age int", false},
		{"create table", "CREATE TABLE test (id int)", false},
		{"truncate", "TRUNCATE TABLE users", false},
		{"grant", "GRANT SELECT ON users TO readonly", false},
		{"revoke", "REVOKE ALL ON users FROM public", false},

		// Case variations
		{"mixed case insert", "InSeRt INTO users (name) VALUES ('test')", false},
		{"mixed case update", "uPdAtE users SET name = 'test'", false},
		{"mixed case delete", "DeLeTe FROM users WHERE id = 1", false},

		// Comments should be stripped
		{"select with line comment", "SELECT * FROM users -- this is a comment", true},
		{"select with block comment", "SELECT * FROM users /* comment */", true},
		{"dangerous in line comment only", "SELECT * FROM users -- INSERT INTO foo", true},
		{"dangerous in block comment only", "SELECT * FROM users /* DELETE FROM foo */", true},

		// Semicolons
		{"select with semicolon", "SELECT * FROM users;", true},
		{"multi-statement injection", "SELECT * FROM users; DROP TABLE users", false},
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

func TestClampTimeout(t *testing.T) {
	tests := []struct {
		name    string
		input   int
		want    int
	}{
		{"zero defaults to 30", 0, DefaultTimeout},
		{"negative defaults to 30", -1, DefaultTimeout},
		{"below min defaults to 30", 3, DefaultTimeout},
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

func TestExtractLogicalName(t *testing.T) {
	tests := []struct {
		name string
		args map[string]interface{}
		want string
	}{
		{"with logical name", map[string]interface{}{"logical_name": "prod-pg"}, "prod-pg"},
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

func TestConfigCacheKey(t *testing.T) {
	key := configCacheKey("incident-123")
	if key != "creds:incident-123:postgresql" {
		t.Errorf("unexpected cache key: %s", key)
	}
}

func TestResponseCacheKey(t *testing.T) {
	key1 := responseCacheKey("SELECT 1", nil)
	key2 := responseCacheKey("SELECT 2", nil)
	if key1 == key2 {
		t.Error("expected different cache keys for different queries")
	}

	// Same query and params should produce the same key
	key3 := responseCacheKey("SELECT 1", nil)
	if key1 != key3 {
		t.Error("expected same cache key for same query")
	}
}

func TestConnString(t *testing.T) {
	config := &PGConfig{
		Host:     "db.example.com",
		Port:     5433,
		Database: "mydb",
		Username: "admin",
		Password: "secret",
		SSLMode:  "verify-full",
	}
	got := connString(config)
	expected := "host=db.example.com port=5433 dbname=mydb user=admin password=secret sslmode=verify-full"
	if got != expected {
		t.Errorf("connString() = %q, want %q", got, expected)
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

func TestGetSchema(t *testing.T) {
	tests := []struct {
		name string
		args map[string]interface{}
		want string
	}{
		{"default", map[string]interface{}{}, "public"},
		{"custom", map[string]interface{}{"schema": "myschema"}, "myschema"},
		{"empty string", map[string]interface{}{"schema": ""}, "public"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getSchema(tt.args)
			if got != tt.want {
				t.Errorf("getSchema() = %q, want %q", got, tt.want)
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

	long := ""
	for i := 0; i < 300; i++ {
		long += "x"
	}
	truncated := truncateQuery(long)
	if len(truncated) != 203 { // 200 + "..."
		t.Errorf("expected truncated length 203, got %d", len(truncated))
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

// Test that ExecuteQuery rejects non-SELECT queries
func TestExecuteQuery_RejectsNonSelect(t *testing.T) {
	tool := NewPostgreSQLTool(testLogger(), nil)
	defer tool.Stop()

	tests := []struct {
		name  string
		query string
	}{
		{"insert", "INSERT INTO users VALUES (1, 'test')"},
		{"update", "UPDATE users SET name = 'test'"},
		{"delete", "DELETE FROM users"},
		{"drop", "DROP TABLE users"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tool.ExecuteQuery(nil, "test-incident", map[string]interface{}{
				"query": tt.query,
			})
			if err == nil {
				t.Error("expected error for non-SELECT query")
			}
			if err != nil && !contains(err.Error(), "only SELECT queries are allowed") {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestExecuteQuery_RequiresQuery(t *testing.T) {
	tool := NewPostgreSQLTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.ExecuteQuery(nil, "test-incident", map[string]interface{}{})
	if err == nil {
		t.Error("expected error for missing query")
	}
	if err != nil && !contains(err.Error(), "query is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDescribeTable_RequiresTableName(t *testing.T) {
	tool := NewPostgreSQLTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.DescribeTable(nil, "test-incident", map[string]interface{}{})
	if err == nil {
		t.Error("expected error for missing table_name")
	}
	if err != nil && !contains(err.Error(), "table_name is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGetIndexes_RequiresTableName(t *testing.T) {
	tool := NewPostgreSQLTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.GetIndexes(nil, "test-incident", map[string]interface{}{})
	if err == nil {
		t.Error("expected error for missing table_name")
	}
	if err != nil && !contains(err.Error(), "table_name is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestExplainQuery_RejectsNonSelect(t *testing.T) {
	tool := NewPostgreSQLTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.ExplainQuery(nil, "test-incident", map[string]interface{}{
		"query": "DELETE FROM users",
	})
	if err == nil {
		t.Error("expected error for non-SELECT query")
	}
}

func TestExplainQuery_RequiresQuery(t *testing.T) {
	tool := NewPostgreSQLTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.ExplainQuery(nil, "test-incident", map[string]interface{}{})
	if err == nil {
		t.Error("expected error for missing query")
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
	if !contains(result, "test") || !contains(result, "foo") {
		t.Errorf("unexpected result: %s", result)
	}
}

func TestRowsToJSON_Empty(t *testing.T) {
	result, err := rowsToJSON(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "null" {
		t.Errorf("expected 'null', got %q", result)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
