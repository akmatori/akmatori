package postgresql

import (
	"context"
	"fmt"
	"io"
	"log"
	"strings"
	"testing"
	"time"

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
		{"explain select blocked", "EXPLAIN SELECT * FROM users", false},
		{"explain analyze blocked", "EXPLAIN ANALYZE SELECT * FROM users", false},

		// Dangerous functions
		{"pg_terminate_backend", "SELECT pg_terminate_backend(123)", false},
		{"pg_cancel_backend", "SELECT pg_cancel_backend(123)", false},
		{"pg_reload_conf", "SELECT pg_reload_conf()", false},
		{"pg_switch_wal", "SELECT pg_switch_wal()", false},
		{"set_config", "SELECT set_config('statement_timeout', '0', false)", false},

		// SET and LOCK
		{"set statement", "SET statement_timeout = 0", false},
		{"set local", "SET LOCAL statement_timeout = 0", false},
		{"lock table", "LOCK TABLE users IN ACCESS SHARE MODE", false},

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
		{"copy to", "COPY users TO '/tmp/data.csv'", false},
		{"copy from", "COPY users FROM '/tmp/data.csv'", false},
		{"do block", "DO $$ BEGIN RAISE NOTICE 'test'; END $$", false},
		{"call procedure", "CALL my_procedure()", false},

		// MERGE (PostgreSQL 15+)
		{"merge statement", "MERGE INTO target USING source ON target.id = source.id WHEN MATCHED THEN UPDATE SET name = source.name", false},
		{"merge with CTE", "WITH src AS (SELECT * FROM source) MERGE INTO target USING src ON target.id = src.id WHEN MATCHED THEN DELETE", false},
		{"mixed case merge", "MeRgE INTO target USING source ON target.id = source.id WHEN NOT MATCHED THEN INSERT (id) VALUES (source.id)", false},
		{"merge keyword in string literal", "SELECT * FROM logs WHERE action = 'MERGE'", true},

		// Case variations
		{"mixed case insert", "InSeRt INTO users (name) VALUES ('test')", false},
		{"mixed case update", "uPdAtE users SET name = 'test'", false},
		{"mixed case delete", "DeLeTe FROM users WHERE id = 1", false},

		// Comments should be stripped
		{"select with line comment", "SELECT * FROM users -- this is a comment", true},
		{"select with block comment", "SELECT * FROM users /* comment */", true},
		{"dangerous in line comment only", "SELECT * FROM users -- INSERT INTO foo", true},
		{"dangerous in block comment only", "SELECT * FROM users /* DELETE FROM foo */", true},

		// Keywords inside string literals should not trigger false positives
		{"keyword DELETE in string literal", "SELECT * FROM events WHERE status = 'DELETE_PENDING'", true},
		{"keyword INSERT in string literal", "SELECT * FROM logs WHERE action = 'INSERT'", true},
		{"keyword SET in string literal", "SELECT * FROM config WHERE key = 'SET'", true},
		{"keyword DROP in string literal", "SELECT * FROM audit WHERE op = 'DROP TABLE'", true},
		{"keyword UPDATE in string literal", "SELECT * FROM history WHERE type = 'UPDATE'", true},
		{"keyword TRUNCATE in dollar-quoted literal", "SELECT * FROM t WHERE x = $$TRUNCATE$$", true},
		{"real INSERT not in string literal", "INSERT INTO users VALUES ('safe')", false},
		{"real DELETE not in string literal", "DELETE FROM users WHERE name = 'test'", false},
		{"doubled-quote escaping with keyword", "SELECT * FROM t WHERE name = 'it''s a DELETE'", true},
		{"doubled-quote escaping mid-string", "SELECT * FROM t WHERE x = 'can''t DROP this'", true},

		// Comment-like sequences inside string literals must not be treated as comments
		{"line comment delimiter in string literal", "SELECT '--' AS msg LIMIT 5", true},
		{"block comment delimiter in string literal", "SELECT '/* not a comment */' AS msg", true},
		{"keyword after fake line comment in string", "SELECT 'DELETE -- ok' AS msg", true},
		{"line comment in string with LIMIT after", "SELECT * FROM t WHERE x = '--' LIMIT 10", true},

		// Semicolons
		{"select with semicolon", "SELECT * FROM users;", true},
		{"multi-statement injection", "SELECT * FROM users; DROP TABLE users", false},

		// Positive allowlist: must start with SELECT or WITH
		{"SHOW statement", "SHOW search_path", false},
		{"SHOW ALL", "SHOW ALL", false},
		{"comment-only input", "-- just a comment", false},
		{"empty after comment strip", "/* block comment only */", false},
		{"whitespace only", "   ", false},
		{"empty string", "", false},
		{"VACUUM", "VACUUM users", false},
		{"ANALYZE statement", "ANALYZE users", false},
		{"LISTEN", "LISTEN my_channel", false},
		{"NOTIFY", "NOTIFY my_channel", false},
		{"DISCARD", "DISCARD ALL", false},
		{"FETCH", "FETCH NEXT FROM my_cursor", false},
		{"REINDEX", "REINDEX TABLE users", false},

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

func TestClampTimeout(t *testing.T) {
	tests := []struct {
		name    string
		input   int
		want    int
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

func TestBuildConnConfig(t *testing.T) {
	config := &PGConfig{
		Host:     "db.example.com",
		Port:     5433,
		Database: "mydb",
		Username: "admin",
		Password: "secret",
		SSLMode:  "verify-full",
	}
	connConfig, err := buildConnConfig(config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if connConfig.Host != "db.example.com" {
		t.Errorf("expected host 'db.example.com', got %q", connConfig.Host)
	}
	if connConfig.Port != 5433 {
		t.Errorf("expected port 5433, got %d", connConfig.Port)
	}
	if connConfig.Database != "mydb" {
		t.Errorf("expected database 'mydb', got %q", connConfig.Database)
	}
	if connConfig.User != "admin" {
		t.Errorf("expected user 'admin', got %q", connConfig.User)
	}
	if connConfig.Password != "secret" {
		t.Errorf("expected password 'secret', got %q", connConfig.Password)
	}
	// verify-full should set TLSConfig with ServerName
	if connConfig.TLSConfig == nil {
		t.Fatal("expected TLSConfig for verify-full")
	}
	if connConfig.TLSConfig.InsecureSkipVerify {
		t.Error("expected InsecureSkipVerify=false for verify-full")
	}
	if connConfig.TLSConfig.ServerName != "db.example.com" {
		t.Errorf("expected ServerName 'db.example.com', got %q", connConfig.TLSConfig.ServerName)
	}
	// RuntimeParams should include read-only and timeout defaults
	if connConfig.RuntimeParams["default_transaction_read_only"] != "on" {
		t.Error("expected default_transaction_read_only='on' in RuntimeParams")
	}
}

func TestBuildConnConfig_DisableSSL(t *testing.T) {
	config := &PGConfig{
		Host:    "localhost",
		Port:    5432,
		SSLMode: "disable",
	}
	connConfig, err := buildConnConfig(config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if connConfig.TLSConfig != nil {
		t.Error("expected nil TLSConfig for sslmode=disable")
	}
}

func TestBuildConnConfig_RequireSSL(t *testing.T) {
	config := &PGConfig{
		Host:    "localhost",
		Port:    5432,
		SSLMode: "require",
		Timeout: 30,
	}
	connConfig, err := buildConnConfig(config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if connConfig.TLSConfig == nil {
		t.Fatal("expected TLSConfig for sslmode=require")
	}
	if !connConfig.TLSConfig.InsecureSkipVerify {
		t.Error("expected InsecureSkipVerify=true for require mode")
	}
}

func TestBuildConnConfig_VerifyCA_Legacy(t *testing.T) {
	// verify-ca uses InsecureSkipVerify=true with custom VerifyPeerCertificate
	// to check the CA chain without hostname matching (matching libpq semantics)
	config := &PGConfig{
		Host:    "db.example.com",
		Port:    5432,
		SSLMode: "verify-ca",
		Timeout: 30,
	}
	connConfig, err := buildConnConfig(config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if connConfig.TLSConfig == nil {
		t.Fatal("expected TLSConfig for sslmode=verify-ca")
	}
	if !connConfig.TLSConfig.InsecureSkipVerify {
		t.Error("expected InsecureSkipVerify=true for verify-ca (hostname skip; CA verified via callback)")
	}
	if connConfig.TLSConfig.VerifyPeerCertificate == nil {
		t.Error("expected VerifyPeerCertificate callback for verify-ca")
	}
}

func TestBuildConnConfig_RuntimeParams(t *testing.T) {
	config := &PGConfig{
		Host:    "localhost",
		Port:    5432,
		SSLMode: "disable",
		Timeout: 60,
	}
	connConfig, err := buildConnConfig(config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if connConfig.RuntimeParams["default_transaction_read_only"] != "on" {
		t.Error("expected default_transaction_read_only='on'")
	}
	if connConfig.RuntimeParams["statement_timeout"] != "60000" {
		t.Errorf("expected statement_timeout='60000', got %q", connConfig.RuntimeParams["statement_timeout"])
	}
}

func TestBuildConnConfig_ConnectTimeoutMatchesConfig(t *testing.T) {
	config := &PGConfig{
		Host:    "localhost",
		Port:    5432,
		SSLMode: "disable",
		Timeout: 45,
	}
	connConfig, err := buildConnConfig(config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := time.Duration(45) * time.Second
	if connConfig.ConnectTimeout != expected {
		t.Errorf("expected ConnectTimeout=%v, got %v", expected, connConfig.ConnectTimeout)
	}
}

func TestBuildConnConfig_SpecialCharsInPassword(t *testing.T) {
	config := &PGConfig{
		Host:     "localhost",
		Port:     5432,
		Database: "testdb",
		Username: "user",
		Password: "p@ss w0rd='tricky\\value",
		SSLMode:  "disable",
	}
	connConfig, err := buildConnConfig(config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if connConfig.Password != "p@ss w0rd='tricky\\value" {
		t.Errorf("expected password preserved exactly, got %q", connConfig.Password)
	}
}

func TestBuildConnConfig_ClearsFallbacks(t *testing.T) {
	config := &PGConfig{
		Host:    "myhost",
		Port:    5432,
		SSLMode: "disable",
		Timeout: 30,
	}
	connConfig, err := buildConnConfig(config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if connConfig.Fallbacks != nil {
		t.Errorf("expected Fallbacks to be nil, got %v", connConfig.Fallbacks)
	}
}

func TestBuildConnConfig_IgnoresEnvVars(t *testing.T) {
	// Set PG* env vars that would pollute the config if pgx read them as fallbacks.
	t.Setenv("PGHOST", "env-leaked-host")
	t.Setenv("PGPORT", "9999")
	t.Setenv("PGDATABASE", "env-leaked-db")
	t.Setenv("PGUSER", "env-leaked-user")
	t.Setenv("PGPASSWORD", "env-leaked-pass")
	t.Setenv("PGSSLNEGOTIATION", "direct")
	t.Setenv("PGMINPROTOCOLVERSION", "3.2")
	t.Setenv("PGMAXPROTOCOLVERSION", "3.2")

	config := &PGConfig{
		Host:     "myhost",
		Port:     5432,
		Database: "mydb",
		Username: "myuser",
		Password: "mypass",
		SSLMode:  "disable",
		Timeout:  30,
	}
	connConfig, err := buildConnConfig(config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Verify that env vars did NOT leak into the connection config.
	if connConfig.Host != "myhost" {
		t.Errorf("expected Host 'myhost', got %q (env leak)", connConfig.Host)
	}
	if connConfig.Port != 5432 {
		t.Errorf("expected Port 5432, got %d (env leak)", connConfig.Port)
	}
	if connConfig.Database != "mydb" {
		t.Errorf("expected Database 'mydb', got %q (env leak)", connConfig.Database)
	}
	if connConfig.User != "myuser" {
		t.Errorf("expected User 'myuser', got %q (env leak)", connConfig.User)
	}
	if connConfig.Password != "mypass" {
		t.Errorf("expected Password 'mypass', got %q (env leak)", connConfig.Password)
	}
	// Verify protocol/SSL negotiation env vars are cleared
	if connConfig.Config.SSLNegotiation != "" {
		t.Errorf("expected SSLNegotiation to be empty, got %q (PGSSLNEGOTIATION env leak)", connConfig.Config.SSLNegotiation)
	}
	if connConfig.Config.MinProtocolVersion != "" {
		t.Errorf("expected MinProtocolVersion to be empty, got %q (PGMINPROTOCOLVERSION env leak)", connConfig.Config.MinProtocolVersion)
	}
	if connConfig.Config.MaxProtocolVersion != "" {
		t.Errorf("expected MaxProtocolVersion to be empty, got %q (PGMAXPROTOCOLVERSION env leak)", connConfig.Config.MaxProtocolVersion)
	}
	// Verify channel_binding and kerberos fields are cleared (could leak via PGSERVICE service-file)
	if connConfig.Config.ChannelBinding != "" {
		t.Errorf("expected ChannelBinding to be empty, got %q (env/service-file leak)", connConfig.Config.ChannelBinding)
	}
	if connConfig.Config.KerberosSrvName != "" {
		t.Errorf("expected KerberosSrvName to be empty, got %q (env/service-file leak)", connConfig.Config.KerberosSrvName)
	}
	if connConfig.Config.KerberosSpn != "" {
		t.Errorf("expected KerberosSpn to be empty, got %q (env/service-file leak)", connConfig.Config.KerberosSpn)
	}
}

func TestBuildConnConfig_PGSERVICEDoesNotFailParse(t *testing.T) {
	// PGSERVICE pointing to a nonexistent service would fail pgx.ParseConfig
	// before the post-parse cleanup. Verify that env shielding prevents this.
	t.Setenv("PGSERVICE", "nonexistent-service")
	t.Setenv("PGSERVICEFILE", "/nonexistent/pg_service.conf")

	config := &PGConfig{
		Host:     "myhost",
		Port:     5432,
		Database: "mydb",
		Username: "myuser",
		Password: "mypass",
		SSLMode:  "disable",
		Timeout:  30,
	}
	connConfig, err := buildConnConfig(config)
	if err != nil {
		t.Fatalf("PGSERVICE env var leaked into ParseConfig and caused failure: %v", err)
	}
	if connConfig.Host != "myhost" {
		t.Errorf("expected Host 'myhost', got %q", connConfig.Host)
	}
}

func TestBuildConnConfig_RequireSSL_HasServerName(t *testing.T) {
	config := &PGConfig{
		Host:    "db.example.com",
		Port:    5432,
		SSLMode: "require",
		Timeout: 30,
	}
	connConfig, err := buildConnConfig(config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if connConfig.TLSConfig.ServerName != "db.example.com" {
		t.Errorf("expected ServerName 'db.example.com' for SNI, got %q", connConfig.TLSConfig.ServerName)
	}
}

func TestBuildConnConfig_VerifyCA_SkipsHostnameVerification(t *testing.T) {
	config := &PGConfig{
		Host:    "db.example.com",
		Port:    5432,
		SSLMode: "verify-ca",
		Timeout: 30,
	}
	connConfig, err := buildConnConfig(config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if connConfig.TLSConfig == nil {
		t.Fatal("expected TLSConfig for sslmode=verify-ca")
	}
	// verify-ca uses InsecureSkipVerify=true with custom VerifyPeerCertificate
	if !connConfig.TLSConfig.InsecureSkipVerify {
		t.Error("expected InsecureSkipVerify=true for verify-ca (hostname check skipped, CA verified via callback)")
	}
	if connConfig.TLSConfig.VerifyPeerCertificate == nil {
		t.Error("expected VerifyPeerCertificate callback for verify-ca")
	}
	if connConfig.TLSConfig.ServerName != "db.example.com" {
		t.Errorf("expected ServerName for SNI, got %q", connConfig.TLSConfig.ServerName)
	}
}

func TestBuildConnConfig_VerifyFull(t *testing.T) {
	config := &PGConfig{
		Host:    "db.example.com",
		Port:    5432,
		SSLMode: "verify-full",
		Timeout: 30,
	}
	connConfig, err := buildConnConfig(config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if connConfig.TLSConfig == nil {
		t.Fatal("expected TLSConfig for sslmode=verify-full")
	}
	if connConfig.TLSConfig.InsecureSkipVerify {
		t.Error("expected InsecureSkipVerify=false for verify-full")
	}
	if connConfig.TLSConfig.ServerName != "db.example.com" {
		t.Errorf("expected ServerName 'db.example.com', got %q", connConfig.TLSConfig.ServerName)
	}
}

func TestBuildConnConfig_UnknownSSLModeReturnsError(t *testing.T) {
	config := &PGConfig{
		Host:    "db.example.com",
		Port:    5432,
		SSLMode: "verify_ca", // underscore instead of hyphen — common typo
		Timeout: 30,
	}
	_, err := buildConnConfig(config)
	if err == nil {
		t.Fatal("expected error for unknown SSL mode, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported pg_ssl_mode") {
		t.Errorf("expected error about unsupported ssl mode, got: %v", err)
	}
}

func TestIsSelectOnly_QuotedIdentifiersWithBlockedWords(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  bool
	}{
		{"double-quoted delete column", `SELECT "delete" FROM audit_log`, true},
		{"double-quoted lock column", `SELECT "lock" FROM t`, true},
		{"double-quoted insert column", `SELECT "insert", "update" FROM changelog`, true},
		{"double-quoted SET column", `SELECT "set" FROM config`, true},
		{"double-quoted DROP in table name", `SELECT * FROM "drop_log"`, true},
		{"real DELETE not in quotes", `DELETE FROM audit_log`, false},
		{"mixed quoted and real keyword", `SELECT "delete" FROM t; DROP TABLE t`, false},
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
		{"limit in string literal", "SELECT * FROM t WHERE status = 'LIMIT reached'", false},
		{"limit in subquery only", "SELECT * FROM users WHERE id IN (SELECT user_id FROM admins LIMIT 1)", false},
		{"limit in subquery and outer", "SELECT * FROM users WHERE id IN (SELECT user_id FROM admins LIMIT 1) LIMIT 50", true},
		{"nested subquery with limit", "SELECT * FROM t WHERE id IN (SELECT id FROM (SELECT id FROM s LIMIT 5) sub)", false},
		{"limit after fake comment in string", "SELECT * FROM t WHERE x = '--' LIMIT 10", true},
		{"FETCH FIRST", "SELECT * FROM users FETCH FIRST 10 ROWS ONLY", true},
		{"FETCH NEXT", "SELECT * FROM users FETCH NEXT 5 ROWS ONLY", true},
		{"fetch first lowercase", "SELECT * FROM users fetch first 10 rows only", true},
		{"FETCH FIRST in subquery only", "SELECT * FROM users WHERE id IN (SELECT id FROM t FETCH FIRST 1 ROWS ONLY)", false},
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
			if err != nil && !strings.Contains(err.Error(), "only SELECT queries are allowed") {
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
	if err != nil && !strings.Contains(err.Error(), "query is required") {
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
	if err != nil && !strings.Contains(err.Error(), "table_name is required") {
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
	if err != nil && !strings.Contains(err.Error(), "table_name is required") {
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

func TestExplainQuery_RejectsQueryWithExplain(t *testing.T) {
	tool := NewPostgreSQLTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.ExplainQuery(nil, "test-incident", map[string]interface{}{
		"query": "EXPLAIN SELECT * FROM users",
	})
	if err == nil {
		t.Fatal("expected error for query containing EXPLAIN")
	}
	if !strings.Contains(err.Error(), "do not include EXPLAIN") {
		t.Errorf("expected error about EXPLAIN, got: %v", err)
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

func TestRowsToJSON_MarshalError(t *testing.T) {
	// json.Marshal fails on channels
	rows := []map[string]interface{}{{"bad": make(chan int)}}
	_, err := rowsToJSON(rows)
	if err == nil {
		t.Error("expected marshal error")
	}
	if err != nil && !strings.Contains(err.Error(), "failed to marshal") {
		t.Errorf("unexpected error: %v", err)
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

// --- Task 4: Comprehensive tests for read-only query tools ---

func TestExecuteQuery_CacheHit(t *testing.T) {
	tool := NewPostgreSQLTool(testLogger(), nil)
	defer tool.Stop()

	// Pre-populate the config cache so getConfig succeeds
	config := &PGConfig{Host: "localhost", Port: 5432, Database: "testdb", Username: "user", Password: "pass", SSLMode: "disable", Timeout: 30}
	tool.configCache.Set(configCacheKey("test-incident"), config)

	// Pre-populate the response cache for the expected query
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

func TestExecuteQuery_AppendLimitWhenMissing(t *testing.T) {
	tool := NewPostgreSQLTool(testLogger(), nil)
	defer tool.Stop()

	// With custom limit - verify the query gets limit appended by checking the cache key
	config := &PGConfig{Host: "localhost", Port: 5432, Database: "testdb", Username: "user", Password: "pass", SSLMode: "disable", Timeout: 30}
	tool.configCache.Set(configCacheKey("test-incident"), config)

	// Pre-populate cache for query WITH limit appended (limit=50)
	queryWithLimit := "SELECT * FROM users LIMIT 50"
	cacheKey := responseCacheKey(queryWithLimit, map[string]interface{}{"query": "SELECT * FROM users", "limit": float64(50)})
	fullCacheKey := "incident:test-incident:" + cacheKey
	expectedResult := `[{"id":1}]`
	tool.responseCache.SetWithTTL(fullCacheKey, expectedResult, QueryCacheTTL)

	result, err := tool.ExecuteQuery(nil, "test-incident", map[string]interface{}{
		"query": "SELECT * FROM users",
		"limit": float64(50),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != expectedResult {
		t.Errorf("expected cached result %q, got %q", expectedResult, result)
	}
}

func TestExecuteQuery_PreservesExistingLimit(t *testing.T) {
	tool := NewPostgreSQLTool(testLogger(), nil)
	defer tool.Stop()

	config := &PGConfig{Host: "localhost", Port: 5432, Database: "testdb", Username: "user", Password: "pass", SSLMode: "disable", Timeout: 30}
	tool.configCache.Set(configCacheKey("test-incident"), config)

	// Query already has LIMIT - should NOT append another
	queryWithLimit := "SELECT * FROM users LIMIT 5"
	cacheKey := responseCacheKey(queryWithLimit, map[string]interface{}{"query": queryWithLimit})
	fullCacheKey := "incident:test-incident:" + cacheKey
	expectedResult := `[{"id":1}]`
	tool.responseCache.SetWithTTL(fullCacheKey, expectedResult, QueryCacheTTL)

	result, err := tool.ExecuteQuery(nil, "test-incident", map[string]interface{}{
		"query": queryWithLimit,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != expectedResult {
		t.Errorf("expected cached result %q, got %q", expectedResult, result)
	}
}

func TestExecuteQuery_EmptyQuery(t *testing.T) {
	tool := NewPostgreSQLTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.ExecuteQuery(nil, "test-incident", map[string]interface{}{
		"query": "",
	})
	if err == nil {
		t.Error("expected error for empty query")
	}
}

func TestExecuteQuery_WithLogicalName(t *testing.T) {
	tool := NewPostgreSQLTool(testLogger(), nil)
	defer tool.Stop()

	config := &PGConfig{Host: "prod-host", Port: 5432, Database: "proddb", Username: "admin", Password: "secret", SSLMode: "require", Timeout: 30}
	tool.configCache.Set("creds:logical:postgresql:prod-pg", config)

	query := "SELECT 1 LIMIT 100"
	cacheKey := responseCacheKey(query, map[string]interface{}{"query": "SELECT 1", "logical_name": "prod-pg"})
	fullCacheKey := "logical:prod-pg:" + cacheKey
	expectedResult := `[{"?column?":1}]`
	tool.responseCache.SetWithTTL(fullCacheKey, expectedResult, QueryCacheTTL)

	result, err := tool.ExecuteQuery(nil, "test-incident", map[string]interface{}{
		"query":        "SELECT 1",
		"logical_name": "prod-pg",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != expectedResult {
		t.Errorf("expected cached result %q, got %q", expectedResult, result)
	}
}

func TestListTables_CacheHit(t *testing.T) {
	tool := NewPostgreSQLTool(testLogger(), nil)
	defer tool.Stop()

	config := &PGConfig{Host: "localhost", Port: 5432, Database: "testdb", Username: "user", Password: "pass", SSLMode: "disable", Timeout: 30}
	tool.configCache.Set(configCacheKey("test-incident"), config)

	cacheKey := responseCacheKey("list_tables", map[string]string{"schema": "public"})
	fullCacheKey := "incident:test-incident:" + cacheKey
	expectedResult := `[{"table_name":"users","table_type":"BASE TABLE","row_estimate":1000}]`
	tool.responseCache.SetWithTTL(fullCacheKey, expectedResult, SchemaCacheTTL)

	result, err := tool.ListTables(nil, "test-incident", map[string]interface{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != expectedResult {
		t.Errorf("expected cached result %q, got %q", expectedResult, result)
	}
}

func TestListTables_CustomSchema(t *testing.T) {
	tool := NewPostgreSQLTool(testLogger(), nil)
	defer tool.Stop()

	config := &PGConfig{Host: "localhost", Port: 5432, Database: "testdb", Username: "user", Password: "pass", SSLMode: "disable", Timeout: 30}
	tool.configCache.Set(configCacheKey("test-incident"), config)

	cacheKey := responseCacheKey("list_tables", map[string]string{"schema": "myschema"})
	fullCacheKey := "incident:test-incident:" + cacheKey
	expectedResult := `[{"table_name":"orders","table_type":"BASE TABLE","row_estimate":500}]`
	tool.responseCache.SetWithTTL(fullCacheKey, expectedResult, SchemaCacheTTL)

	result, err := tool.ListTables(nil, "test-incident", map[string]interface{}{
		"schema": "myschema",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != expectedResult {
		t.Errorf("expected cached result %q, got %q", expectedResult, result)
	}
}

func TestDescribeTable_CacheHit(t *testing.T) {
	tool := NewPostgreSQLTool(testLogger(), nil)
	defer tool.Stop()

	config := &PGConfig{Host: "localhost", Port: 5432, Database: "testdb", Username: "user", Password: "pass", SSLMode: "disable", Timeout: 30}
	tool.configCache.Set(configCacheKey("test-incident"), config)

	cacheKey := responseCacheKey("describe_table", map[string]string{"schema": "public", "table": "users"})
	fullCacheKey := "incident:test-incident:" + cacheKey
	expectedResult := `[{"column_name":"id","data_type":"integer","is_nullable":"NO"}]`
	tool.responseCache.SetWithTTL(fullCacheKey, expectedResult, SchemaCacheTTL)

	result, err := tool.DescribeTable(nil, "test-incident", map[string]interface{}{
		"table_name": "users",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != expectedResult {
		t.Errorf("expected cached result %q, got %q", expectedResult, result)
	}
}

func TestDescribeTable_EmptyTableName(t *testing.T) {
	tool := NewPostgreSQLTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.DescribeTable(nil, "test-incident", map[string]interface{}{
		"table_name": "",
	})
	if err == nil {
		t.Error("expected error for empty table_name")
	}
}

func TestGetIndexes_CacheHit(t *testing.T) {
	tool := NewPostgreSQLTool(testLogger(), nil)
	defer tool.Stop()

	config := &PGConfig{Host: "localhost", Port: 5432, Database: "testdb", Username: "user", Password: "pass", SSLMode: "disable", Timeout: 30}
	tool.configCache.Set(configCacheKey("test-incident"), config)

	cacheKey := responseCacheKey("get_indexes", map[string]string{"schema": "public", "table": "users"})
	fullCacheKey := "incident:test-incident:" + cacheKey
	expectedResult := `[{"indexname":"users_pkey","indexdef":"CREATE UNIQUE INDEX users_pkey ON public.users USING btree (id)","is_unique":true}]`
	tool.responseCache.SetWithTTL(fullCacheKey, expectedResult, SchemaCacheTTL)

	result, err := tool.GetIndexes(nil, "test-incident", map[string]interface{}{
		"table_name": "users",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != expectedResult {
		t.Errorf("expected cached result %q, got %q", expectedResult, result)
	}
}

func TestGetIndexes_EmptyTableName(t *testing.T) {
	tool := NewPostgreSQLTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.GetIndexes(nil, "test-incident", map[string]interface{}{
		"table_name": "",
	})
	if err == nil {
		t.Error("expected error for empty table_name")
	}
}

func TestGetTableStats_AllTables_CacheHit(t *testing.T) {
	tool := NewPostgreSQLTool(testLogger(), nil)
	defer tool.Stop()

	config := &PGConfig{Host: "localhost", Port: 5432, Database: "testdb", Username: "user", Password: "pass", SSLMode: "disable", Timeout: 30}
	tool.configCache.Set(configCacheKey("test-incident"), config)

	cacheKey := responseCacheKey("get_table_stats", map[string]string{})
	fullCacheKey := "incident:test-incident:" + cacheKey
	expectedResult := `[{"table_name":"users","n_live_tup":1000,"n_dead_tup":50}]`
	tool.responseCache.SetWithTTL(fullCacheKey, expectedResult, StatsCacheTTL)

	result, err := tool.GetTableStats(nil, "test-incident", map[string]interface{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != expectedResult {
		t.Errorf("expected cached result %q, got %q", expectedResult, result)
	}
}

func TestGetTableStats_SpecificTable_CacheHit(t *testing.T) {
	tool := NewPostgreSQLTool(testLogger(), nil)
	defer tool.Stop()

	config := &PGConfig{Host: "localhost", Port: 5432, Database: "testdb", Username: "user", Password: "pass", SSLMode: "disable", Timeout: 30}
	tool.configCache.Set(configCacheKey("test-incident"), config)

	cacheKey := responseCacheKey("get_table_stats", map[string]string{"table": "orders"})
	fullCacheKey := "incident:test-incident:" + cacheKey
	expectedResult := `[{"table_name":"orders","n_live_tup":5000,"n_dead_tup":200}]`
	tool.responseCache.SetWithTTL(fullCacheKey, expectedResult, StatsCacheTTL)

	result, err := tool.GetTableStats(nil, "test-incident", map[string]interface{}{
		"table_name": "orders",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != expectedResult {
		t.Errorf("expected cached result %q, got %q", expectedResult, result)
	}
}

func TestExplainQuery_CacheHit(t *testing.T) {
	tool := NewPostgreSQLTool(testLogger(), nil)
	defer tool.Stop()

	config := &PGConfig{Host: "localhost", Port: 5432, Database: "testdb", Username: "user", Password: "pass", SSLMode: "disable", Timeout: 30}
	tool.configCache.Set(configCacheKey("test-incident"), config)

	cacheKey := responseCacheKey("explain", map[string]string{"query": "SELECT * FROM users"})
	fullCacheKey := "incident:test-incident:" + cacheKey
	expectedResult := `[{"QUERY PLAN":[{"Plan":{"Node Type":"Seq Scan","Relation Name":"users"}}]}]`
	tool.responseCache.SetWithTTL(fullCacheKey, expectedResult, QueryCacheTTL)

	result, err := tool.ExplainQuery(nil, "test-incident", map[string]interface{}{
		"query": "SELECT * FROM users",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != expectedResult {
		t.Errorf("expected cached result %q, got %q", expectedResult, result)
	}
}

func TestExplainQuery_EmptyQuery(t *testing.T) {
	tool := NewPostgreSQLTool(testLogger(), nil)
	defer tool.Stop()

	_, err := tool.ExplainQuery(nil, "test-incident", map[string]interface{}{
		"query": "",
	})
	if err == nil {
		t.Error("expected error for empty query")
	}
}

// Test CachedQuery directly for cache behavior verification
func TestCachedQuery_CacheHitAndMiss(t *testing.T) {
	tool := NewPostgreSQLTool(testLogger(), nil)
	defer tool.Stop()

	callCount := 0
	queryFn := func() (string, error) {
		callCount++
		return `{"result":"fresh"}`, nil
	}

	// First call - should execute queryFn (cache miss)
	result1, err := tool.cachedQuery(nil, "incident-1", "test-key", QueryCacheTTL, queryFn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 1 {
		t.Errorf("expected queryFn called once, got %d", callCount)
	}

	// Second call - should hit cache
	result2, err := tool.cachedQuery(nil, "incident-1", "test-key", QueryCacheTTL, queryFn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 1 {
		t.Errorf("expected queryFn still called once (cache hit), got %d", callCount)
	}
	if result1 != result2 {
		t.Errorf("cache hit should return same result: %q vs %q", result1, result2)
	}
}

func TestCachedQuery_DifferentIncidentsHaveDifferentCaches(t *testing.T) {
	tool := NewPostgreSQLTool(testLogger(), nil)
	defer tool.Stop()

	callCount := 0
	queryFn := func() (string, error) {
		callCount++
		return `{"result":"data"}`, nil
	}

	_, err := tool.cachedQuery(nil, "incident-1", "key", QueryCacheTTL, queryFn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 1 {
		t.Errorf("expected 1 call, got %d", callCount)
	}

	// Different incident should not hit the cache
	_, err = tool.cachedQuery(nil, "incident-2", "key", QueryCacheTTL, queryFn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 2 {
		t.Errorf("expected 2 calls (different incident), got %d", callCount)
	}
}

func TestCachedQuery_LogicalNameCacheKey(t *testing.T) {
	tool := NewPostgreSQLTool(testLogger(), nil)
	defer tool.Stop()

	callCount := 0
	queryFn := func() (string, error) {
		callCount++
		return `{"result":"data"}`, nil
	}

	// With logical name
	_, err := tool.cachedQuery(nil, "incident-1", "key", QueryCacheTTL, queryFn, "prod-pg")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 1 {
		t.Errorf("expected 1 call, got %d", callCount)
	}

	// Same logical name from different incident should hit cache
	_, err = tool.cachedQuery(nil, "incident-2", "key", QueryCacheTTL, queryFn, "prod-pg")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 1 {
		t.Errorf("expected 1 call (logical name cache hit), got %d", callCount)
	}
}

func TestCachedQuery_ErrorNotCached(t *testing.T) {
	tool := NewPostgreSQLTool(testLogger(), nil)
	defer tool.Stop()

	callCount := 0
	queryFn := func() (string, error) {
		callCount++
		return "", fmt.Errorf("connection failed")
	}

	_, err := tool.cachedQuery(nil, "incident-1", "key", QueryCacheTTL, queryFn)
	if err == nil {
		t.Error("expected error")
	}

	// Second call should also execute queryFn (errors are not cached)
	_, err = tool.cachedQuery(nil, "incident-1", "key", QueryCacheTTL, queryFn)
	if err == nil {
		t.Error("expected error")
	}
	if callCount != 2 {
		t.Errorf("expected 2 calls (errors not cached), got %d", callCount)
	}
}

// --- Task 5: Tests for diagnostic tools ---

func TestGetActiveQueries_CacheHit(t *testing.T) {
	tool := NewPostgreSQLTool(testLogger(), nil)
	defer tool.Stop()

	config := &PGConfig{Host: "localhost", Port: 5432, Database: "testdb", Username: "user", Password: "pass", SSLMode: "disable", Timeout: 30}
	tool.configCache.Set(configCacheKey("test-incident"), config)

	cacheKey := responseCacheKey("get_active_queries", map[string]interface{}{"include_idle": false})
	fullCacheKey := "incident:test-incident:" + cacheKey
	expectedResult := `[{"pid":123,"state":"active","query":"SELECT 1","duration_seconds":5.2}]`
	tool.responseCache.SetWithTTL(fullCacheKey, expectedResult, QueryCacheTTL)

	result, err := tool.GetActiveQueries(nil, "test-incident", map[string]interface{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != expectedResult {
		t.Errorf("expected cached result %q, got %q", expectedResult, result)
	}
}

func TestGetActiveQueries_IncludeIdle(t *testing.T) {
	tool := NewPostgreSQLTool(testLogger(), nil)
	defer tool.Stop()

	config := &PGConfig{Host: "localhost", Port: 5432, Database: "testdb", Username: "user", Password: "pass", SSLMode: "disable", Timeout: 30}
	tool.configCache.Set(configCacheKey("test-incident"), config)

	// Cache with include_idle=true produces a different cache key
	args := map[string]interface{}{"include_idle": true}
	cacheKey := responseCacheKey("get_active_queries", map[string]interface{}{"include_idle": true})
	fullCacheKey := "incident:test-incident:" + cacheKey
	expectedResult := `[{"pid":123,"state":"idle","query":"SELECT 1"}]`
	tool.responseCache.SetWithTTL(fullCacheKey, expectedResult, QueryCacheTTL)

	result, err := tool.GetActiveQueries(nil, "test-incident", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != expectedResult {
		t.Errorf("expected cached result %q, got %q", expectedResult, result)
	}
}

func TestGetActiveQueries_MinDuration(t *testing.T) {
	tool := NewPostgreSQLTool(testLogger(), nil)
	defer tool.Stop()

	config := &PGConfig{Host: "localhost", Port: 5432, Database: "testdb", Username: "user", Password: "pass", SSLMode: "disable", Timeout: 30}
	tool.configCache.Set(configCacheKey("test-incident"), config)

	args := map[string]interface{}{"min_duration_seconds": float64(10)}
	cacheKey := responseCacheKey("get_active_queries", map[string]interface{}{"include_idle": false, "min_duration_seconds": float64(10)})
	fullCacheKey := "incident:test-incident:" + cacheKey
	expectedResult := `[{"pid":456,"state":"active","query":"SELECT pg_sleep(30)","duration_seconds":25.1}]`
	tool.responseCache.SetWithTTL(fullCacheKey, expectedResult, QueryCacheTTL)

	result, err := tool.GetActiveQueries(nil, "test-incident", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != expectedResult {
		t.Errorf("expected cached result %q, got %q", expectedResult, result)
	}
}

func TestGetActiveQueries_WithLogicalName(t *testing.T) {
	tool := NewPostgreSQLTool(testLogger(), nil)
	defer tool.Stop()

	config := &PGConfig{Host: "prod-host", Port: 5432, Database: "proddb", Username: "admin", Password: "secret", SSLMode: "require", Timeout: 30}
	tool.configCache.Set("creds:logical:postgresql:prod-pg", config)

	args := map[string]interface{}{"logical_name": "prod-pg"}
	cacheKey := responseCacheKey("get_active_queries", map[string]interface{}{"include_idle": false})
	fullCacheKey := "logical:prod-pg:" + cacheKey
	expectedResult := `[{"pid":789,"state":"active","query":"SELECT count(*) FROM orders"}]`
	tool.responseCache.SetWithTTL(fullCacheKey, expectedResult, QueryCacheTTL)

	result, err := tool.GetActiveQueries(nil, "test-incident", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != expectedResult {
		t.Errorf("expected cached result %q, got %q", expectedResult, result)
	}
}

func TestGetLocks_CacheHit(t *testing.T) {
	tool := NewPostgreSQLTool(testLogger(), nil)
	defer tool.Stop()

	config := &PGConfig{Host: "localhost", Port: 5432, Database: "testdb", Username: "user", Password: "pass", SSLMode: "disable", Timeout: 30}
	tool.configCache.Set(configCacheKey("test-incident"), config)

	args := map[string]interface{}{}
	cacheKey := responseCacheKey("get_locks", map[string]interface{}{"blocked_only": false})
	fullCacheKey := "incident:test-incident:" + cacheKey
	expectedResult := `[{"locktype":"relation","relation":"users","mode":"AccessShareLock","granted":true,"pid":123}]`
	tool.responseCache.SetWithTTL(fullCacheKey, expectedResult, QueryCacheTTL)

	result, err := tool.GetLocks(nil, "test-incident", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != expectedResult {
		t.Errorf("expected cached result %q, got %q", expectedResult, result)
	}
}

func TestGetLocks_BlockedOnly(t *testing.T) {
	tool := NewPostgreSQLTool(testLogger(), nil)
	defer tool.Stop()

	config := &PGConfig{Host: "localhost", Port: 5432, Database: "testdb", Username: "user", Password: "pass", SSLMode: "disable", Timeout: 30}
	tool.configCache.Set(configCacheKey("test-incident"), config)

	args := map[string]interface{}{"blocked_only": true}
	cacheKey := responseCacheKey("get_locks", map[string]interface{}{"blocked_only": true})
	fullCacheKey := "incident:test-incident:" + cacheKey
	expectedResult := `[{"locktype":"relation","relation":"orders","mode":"ExclusiveLock","granted":false,"pid":456}]`
	tool.responseCache.SetWithTTL(fullCacheKey, expectedResult, QueryCacheTTL)

	result, err := tool.GetLocks(nil, "test-incident", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != expectedResult {
		t.Errorf("expected cached result %q, got %q", expectedResult, result)
	}
}

func TestGetLocks_BlockedOnlyDefault(t *testing.T) {
	tool := NewPostgreSQLTool(testLogger(), nil)
	defer tool.Stop()

	config := &PGConfig{Host: "localhost", Port: 5432, Database: "testdb", Username: "user", Password: "pass", SSLMode: "disable", Timeout: 30}
	tool.configCache.Set(configCacheKey("test-incident"), config)

	// Default blocked_only is false - different cache key than blocked_only=true
	key1 := responseCacheKey("get_locks", map[string]interface{}{"blocked_only": false})
	key2 := responseCacheKey("get_locks", map[string]interface{}{"blocked_only": true})
	if key1 == key2 {
		t.Error("expected different cache keys for different blocked_only values")
	}
}

func TestGetReplicationStatus_CacheHit(t *testing.T) {
	tool := NewPostgreSQLTool(testLogger(), nil)
	defer tool.Stop()

	config := &PGConfig{Host: "localhost", Port: 5432, Database: "testdb", Username: "user", Password: "pass", SSLMode: "disable", Timeout: 30}
	tool.configCache.Set(configCacheKey("test-incident"), config)

	cacheKey := responseCacheKey("get_replication_status", nil)
	fullCacheKey := "incident:test-incident:" + cacheKey
	expectedResult := `[{"client_addr":"10.0.0.2","state":"streaming","sent_lsn":"0/3000060","write_lsn":"0/3000060","flush_lsn":"0/3000060","replay_lsn":"0/3000060","sync_state":"async"}]`
	tool.responseCache.SetWithTTL(fullCacheKey, expectedResult, StatsCacheTTL)

	result, err := tool.GetReplicationStatus(nil, "test-incident", map[string]interface{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != expectedResult {
		t.Errorf("expected cached result %q, got %q", expectedResult, result)
	}
}

func TestGetReplicationStatus_WithLogicalName(t *testing.T) {
	tool := NewPostgreSQLTool(testLogger(), nil)
	defer tool.Stop()

	config := &PGConfig{Host: "prod-host", Port: 5432, Database: "proddb", Username: "admin", Password: "secret", SSLMode: "require", Timeout: 30}
	tool.configCache.Set("creds:logical:postgresql:prod-pg", config)

	cacheKey := responseCacheKey("get_replication_status", nil)
	fullCacheKey := "logical:prod-pg:" + cacheKey
	expectedResult := `[{"client_addr":"10.0.1.5","state":"streaming"}]`
	tool.responseCache.SetWithTTL(fullCacheKey, expectedResult, StatsCacheTTL)

	result, err := tool.GetReplicationStatus(nil, "test-incident", map[string]interface{}{
		"logical_name": "prod-pg",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != expectedResult {
		t.Errorf("expected cached result %q, got %q", expectedResult, result)
	}
}

func TestGetDatabaseStats_CacheHit(t *testing.T) {
	tool := NewPostgreSQLTool(testLogger(), nil)
	defer tool.Stop()

	config := &PGConfig{Host: "localhost", Port: 5432, Database: "testdb", Username: "user", Password: "pass", SSLMode: "disable", Timeout: 30}
	tool.configCache.Set(configCacheKey("test-incident"), config)

	cacheKey := responseCacheKey("get_database_stats", nil)
	fullCacheKey := "incident:test-incident:" + cacheKey
	expectedResult := `[{"numbackends":15,"xact_commit":50000,"xact_rollback":100,"blks_read":1000,"blks_hit":99000,"cache_hit_ratio":99.0,"deadlocks":0,"db_size":104857600}]`
	tool.responseCache.SetWithTTL(fullCacheKey, expectedResult, StatsCacheTTL)

	result, err := tool.GetDatabaseStats(nil, "test-incident", map[string]interface{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != expectedResult {
		t.Errorf("expected cached result %q, got %q", expectedResult, result)
	}
}

func TestGetDatabaseStats_WithLogicalName(t *testing.T) {
	tool := NewPostgreSQLTool(testLogger(), nil)
	defer tool.Stop()

	config := &PGConfig{Host: "prod-host", Port: 5432, Database: "proddb", Username: "admin", Password: "secret", SSLMode: "require", Timeout: 30}
	tool.configCache.Set("creds:logical:postgresql:prod-pg", config)

	cacheKey := responseCacheKey("get_database_stats", nil)
	fullCacheKey := "logical:prod-pg:" + cacheKey
	expectedResult := `[{"numbackends":50,"xact_commit":500000,"deadlocks":2,"db_size":1073741824}]`
	tool.responseCache.SetWithTTL(fullCacheKey, expectedResult, StatsCacheTTL)

	result, err := tool.GetDatabaseStats(nil, "test-incident", map[string]interface{}{
		"logical_name": "prod-pg",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != expectedResult {
		t.Errorf("expected cached result %q, got %q", expectedResult, result)
	}
}

func TestGetDatabaseStats_CacheSeparation(t *testing.T) {
	tool := NewPostgreSQLTool(testLogger(), nil)
	defer tool.Stop()

	callCount := 0
	queryFn := func() (string, error) {
		callCount++
		return `{"numbackends":10}`, nil
	}

	// Two different incidents should get separate caches for database stats
	_, err := tool.cachedQuery(nil, "incident-1", "dbstats", StatsCacheTTL, queryFn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = tool.cachedQuery(nil, "incident-2", "dbstats", StatsCacheTTL, queryFn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if callCount != 2 {
		t.Errorf("expected 2 calls for different incidents, got %d", callCount)
	}

	// Same incident should hit cache
	_, err = tool.cachedQuery(nil, "incident-1", "dbstats", StatsCacheTTL, queryFn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 2 {
		t.Errorf("expected still 2 calls (cache hit), got %d", callCount)
	}
}


// --- Task 9: Tests with mock execution for full coverage ---

// newTestToolWithMock creates a tool with mock execQuery and resolveConfig functions.
func newTestToolWithMock(mockRows []map[string]interface{}, mockErr error) *PostgreSQLTool {
	tool := NewPostgreSQLTool(testLogger(), ratelimit.New(10, 20))
	tool.execQuery = func(ctx context.Context, config *PGConfig, query string, args ...interface{}) ([]map[string]interface{}, error) {
		return mockRows, mockErr
	}
	mockConfig := &PGConfig{Host: "localhost", Port: 5432, Database: "testdb", Username: "user", Password: "pass", SSLMode: "disable", Timeout: 30}
	tool.resolveConfig = func(ctx context.Context, incidentID string, logicalName ...string) (*PGConfig, error) {
		return mockConfig, nil
	}
	// Also pre-populate config cache for tests that use it directly
	tool.configCache.Set(configCacheKey("inc-1"), mockConfig)
	return tool
}

// newTestToolWithConfigError creates a tool where resolveConfig always fails.
func newTestToolWithConfigError(configErr error) *PostgreSQLTool {
	tool := NewPostgreSQLTool(testLogger(), nil)
	tool.resolveConfig = func(ctx context.Context, incidentID string, logicalName ...string) (*PGConfig, error) {
		return nil, configErr
	}
	return tool
}

func TestExecuteQuery_FullPath(t *testing.T) {
	rows := []map[string]interface{}{{"id": 1, "name": "alice"}}
	tool := newTestToolWithMock(rows, nil)
	defer tool.Stop()

	result, err := tool.ExecuteQuery(nil, "inc-1", map[string]interface{}{"query": "SELECT * FROM users"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "alice") {
		t.Errorf("expected result to contain 'alice', got %s", result)
	}
}

func TestExecuteQuery_FullPath_WithLimit(t *testing.T) {
	rows := []map[string]interface{}{{"id": 1}}
	tool := newTestToolWithMock(rows, nil)
	defer tool.Stop()

	result, err := tool.ExecuteQuery(nil, "inc-1", map[string]interface{}{
		"query": "SELECT * FROM users",
		"limit": float64(50),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "id") {
		t.Errorf("expected result to contain 'id', got %s", result)
	}
}

func TestExecuteQuery_FullPath_ExistingLimit(t *testing.T) {
	rows := []map[string]interface{}{{"id": 1}}
	tool := newTestToolWithMock(rows, nil)
	defer tool.Stop()

	result, err := tool.ExecuteQuery(nil, "inc-1", map[string]interface{}{
		"query": "SELECT * FROM users LIMIT 5",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "id") {
		t.Errorf("expected result to contain 'id', got %s", result)
	}
}

func TestExecuteQuery_FullPath_Error(t *testing.T) {
	tool := newTestToolWithMock(nil, fmt.Errorf("connection refused"))
	defer tool.Stop()

	_, err := tool.ExecuteQuery(nil, "inc-1", map[string]interface{}{"query": "SELECT 1"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestListTables_FullPath(t *testing.T) {
	rows := []map[string]interface{}{{"table_name": "users", "table_type": "BASE TABLE", "row_estimate": 1000}}
	tool := newTestToolWithMock(rows, nil)
	defer tool.Stop()

	result, err := tool.ListTables(nil, "inc-1", map[string]interface{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "users") {
		t.Errorf("expected result to contain 'users', got %s", result)
	}
}

func TestListTables_FullPath_CustomSchema(t *testing.T) {
	rows := []map[string]interface{}{{"table_name": "orders"}}
	tool := newTestToolWithMock(rows, nil)
	defer tool.Stop()

	result, err := tool.ListTables(nil, "inc-1", map[string]interface{}{"schema": "custom"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "orders") {
		t.Errorf("expected result to contain 'orders', got %s", result)
	}
}

func TestListTables_FullPath_Error(t *testing.T) {
	tool := newTestToolWithMock(nil, fmt.Errorf("query failed"))
	defer tool.Stop()

	_, err := tool.ListTables(nil, "inc-1", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDescribeTable_FullPath(t *testing.T) {
	rows := []map[string]interface{}{{"column_name": "id", "data_type": "integer", "is_nullable": "NO"}}
	tool := newTestToolWithMock(rows, nil)
	defer tool.Stop()

	result, err := tool.DescribeTable(nil, "inc-1", map[string]interface{}{"table_name": "users"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "integer") {
		t.Errorf("expected result to contain 'integer', got %s", result)
	}
}

func TestDescribeTable_FullPath_CustomSchema(t *testing.T) {
	rows := []map[string]interface{}{{"column_name": "id"}}
	tool := newTestToolWithMock(rows, nil)
	defer tool.Stop()

	result, err := tool.DescribeTable(nil, "inc-1", map[string]interface{}{"table_name": "orders", "schema": "sales"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "id") {
		t.Errorf("expected result to contain 'id', got %s", result)
	}
}

func TestDescribeTable_FullPath_Error(t *testing.T) {
	tool := newTestToolWithMock(nil, fmt.Errorf("table not found"))
	defer tool.Stop()

	_, err := tool.DescribeTable(nil, "inc-1", map[string]interface{}{"table_name": "nonexistent"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetIndexes_FullPath(t *testing.T) {
	rows := []map[string]interface{}{{"indexname": "users_pkey", "indexdef": "CREATE UNIQUE INDEX users_pkey ON users (id)", "is_unique": true}}
	tool := newTestToolWithMock(rows, nil)
	defer tool.Stop()

	result, err := tool.GetIndexes(nil, "inc-1", map[string]interface{}{"table_name": "users"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "users_pkey") {
		t.Errorf("expected result to contain 'users_pkey', got %s", result)
	}
}

func TestGetIndexes_FullPath_Error(t *testing.T) {
	tool := newTestToolWithMock(nil, fmt.Errorf("query failed"))
	defer tool.Stop()

	_, err := tool.GetIndexes(nil, "inc-1", map[string]interface{}{"table_name": "users"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetTableStats_FullPath_AllTables(t *testing.T) {
	rows := []map[string]interface{}{{"table_name": "users", "n_live_tup": 1000, "n_dead_tup": 50}}
	tool := newTestToolWithMock(rows, nil)
	defer tool.Stop()

	result, err := tool.GetTableStats(nil, "inc-1", map[string]interface{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "n_live_tup") {
		t.Errorf("expected result to contain 'n_live_tup', got %s", result)
	}
}

func TestGetTableStats_FullPath_SpecificTable(t *testing.T) {
	rows := []map[string]interface{}{{"table_name": "orders", "n_live_tup": 5000}}
	tool := newTestToolWithMock(rows, nil)
	defer tool.Stop()

	result, err := tool.GetTableStats(nil, "inc-1", map[string]interface{}{"table_name": "orders"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "orders") {
		t.Errorf("expected result to contain 'orders', got %s", result)
	}
}

func TestGetTableStats_FullPath_Error(t *testing.T) {
	tool := newTestToolWithMock(nil, fmt.Errorf("connection lost"))
	defer tool.Stop()

	_, err := tool.GetTableStats(nil, "inc-1", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetTableStats_FullPath_SpecificTable_Error(t *testing.T) {
	tool := newTestToolWithMock(nil, fmt.Errorf("connection lost"))
	defer tool.Stop()

	_, err := tool.GetTableStats(nil, "inc-1", map[string]interface{}{"table_name": "users"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestExplainQuery_FullPath(t *testing.T) {
	rows := []map[string]interface{}{{"QUERY PLAN": "Seq Scan on users"}}
	tool := newTestToolWithMock(rows, nil)
	defer tool.Stop()

	result, err := tool.ExplainQuery(nil, "inc-1", map[string]interface{}{"query": "SELECT * FROM users"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Seq Scan") {
		t.Errorf("expected result to contain 'Seq Scan', got %s", result)
	}
}

func TestExplainQuery_FullPath_Error(t *testing.T) {
	tool := newTestToolWithMock(nil, fmt.Errorf("query failed"))
	defer tool.Stop()

	_, err := tool.ExplainQuery(nil, "inc-1", map[string]interface{}{"query": "SELECT 1"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetActiveQueries_FullPath(t *testing.T) {
	rows := []map[string]interface{}{{"pid": 123, "state": "active", "query": "SELECT 1"}}
	tool := newTestToolWithMock(rows, nil)
	defer tool.Stop()

	result, err := tool.GetActiveQueries(nil, "inc-1", map[string]interface{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "active") {
		t.Errorf("expected result to contain 'active', got %s", result)
	}
}

func TestGetActiveQueries_FullPath_IncludeIdle(t *testing.T) {
	rows := []map[string]interface{}{{"pid": 123, "state": "idle"}}
	tool := newTestToolWithMock(rows, nil)
	defer tool.Stop()

	result, err := tool.GetActiveQueries(nil, "inc-1", map[string]interface{}{"include_idle": true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "idle") {
		t.Errorf("expected result to contain 'idle', got %s", result)
	}
}

func TestGetActiveQueries_FullPath_MinDuration(t *testing.T) {
	rows := []map[string]interface{}{{"pid": 456, "duration_seconds": 25.1}}
	tool := newTestToolWithMock(rows, nil)
	defer tool.Stop()

	result, err := tool.GetActiveQueries(nil, "inc-1", map[string]interface{}{"min_duration_seconds": float64(10)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "456") {
		t.Errorf("expected result to contain '456', got %s", result)
	}
}

func TestGetActiveQueries_FullPath_Error(t *testing.T) {
	tool := newTestToolWithMock(nil, fmt.Errorf("db error"))
	defer tool.Stop()

	_, err := tool.GetActiveQueries(nil, "inc-1", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetLocks_FullPath(t *testing.T) {
	rows := []map[string]interface{}{{"locktype": "relation", "mode": "AccessShareLock", "granted": true}}
	tool := newTestToolWithMock(rows, nil)
	defer tool.Stop()

	result, err := tool.GetLocks(nil, "inc-1", map[string]interface{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "AccessShareLock") {
		t.Errorf("expected result to contain 'AccessShareLock', got %s", result)
	}
}

func TestGetLocks_FullPath_BlockedOnly(t *testing.T) {
	rows := []map[string]interface{}{{"locktype": "relation", "granted": false}}
	tool := newTestToolWithMock(rows, nil)
	defer tool.Stop()

	result, err := tool.GetLocks(nil, "inc-1", map[string]interface{}{"blocked_only": true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "false") {
		t.Errorf("expected result to contain 'false', got %s", result)
	}
}

func TestGetLocks_FullPath_Error(t *testing.T) {
	tool := newTestToolWithMock(nil, fmt.Errorf("lock query failed"))
	defer tool.Stop()

	_, err := tool.GetLocks(nil, "inc-1", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetReplicationStatus_FullPath(t *testing.T) {
	rows := []map[string]interface{}{{"client_addr": "10.0.0.2", "state": "streaming"}}
	tool := newTestToolWithMock(rows, nil)
	defer tool.Stop()

	result, err := tool.GetReplicationStatus(nil, "inc-1", map[string]interface{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "streaming") {
		t.Errorf("expected result to contain 'streaming', got %s", result)
	}
}

func TestGetReplicationStatus_FullPath_Error(t *testing.T) {
	tool := newTestToolWithMock(nil, fmt.Errorf("replication query failed"))
	defer tool.Stop()

	_, err := tool.GetReplicationStatus(nil, "inc-1", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetDatabaseStats_FullPath(t *testing.T) {
	rows := []map[string]interface{}{{"numbackends": 15, "xact_commit": 50000, "deadlocks": 0}}
	tool := newTestToolWithMock(rows, nil)
	defer tool.Stop()

	result, err := tool.GetDatabaseStats(nil, "inc-1", map[string]interface{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "numbackends") {
		t.Errorf("expected result to contain 'numbackends', got %s", result)
	}
}

func TestGetDatabaseStats_FullPath_Error(t *testing.T) {
	tool := newTestToolWithMock(nil, fmt.Errorf("stats query failed"))
	defer tool.Stop()

	_, err := tool.GetDatabaseStats(nil, "inc-1", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseSettings_FullConfig(t *testing.T) {
	settings := map[string]interface{}{
		"pg_host":     "db.example.com",
		"pg_port":     float64(5433),
		"pg_database": "mydb",
		"pg_username": "admin",
		"pg_password": "secret123",
		"pg_ssl_mode": "verify-full",
		"pg_timeout":  float64(60),
	}
	config := parseSettings(settings)

	if config.Host != "db.example.com" {
		t.Errorf("expected host 'db.example.com', got %q", config.Host)
	}
	if config.Port != 5433 {
		t.Errorf("expected port 5433, got %d", config.Port)
	}
	if config.Database != "mydb" {
		t.Errorf("expected database 'mydb', got %q", config.Database)
	}
	if config.Username != "admin" {
		t.Errorf("expected username 'admin', got %q", config.Username)
	}
	if config.Password != "secret123" {
		t.Errorf("expected password 'secret123', got %q", config.Password)
	}
	if config.SSLMode != "verify-full" {
		t.Errorf("expected ssl_mode 'verify-full', got %q", config.SSLMode)
	}
	if config.Timeout != 60 {
		t.Errorf("expected timeout 60, got %d", config.Timeout)
	}
}

func TestParseSettings_Defaults(t *testing.T) {
	config := parseSettings(map[string]interface{}{})

	if config.Port != DefaultPort {
		t.Errorf("expected default port %d, got %d", DefaultPort, config.Port)
	}
	if config.SSLMode != "require" {
		t.Errorf("expected default ssl_mode 'require', got %q", config.SSLMode)
	}
	if config.Timeout != DefaultTimeout {
		t.Errorf("expected default timeout %d, got %d", DefaultTimeout, config.Timeout)
	}
}

func TestParseSettings_TimeoutClamped(t *testing.T) {
	settings := map[string]interface{}{
		"pg_timeout": float64(999),
	}
	config := parseSettings(settings)
	if config.Timeout != MaxTimeout {
		t.Errorf("expected clamped timeout %d, got %d", MaxTimeout, config.Timeout)
	}
}

func TestParseSettings_WrongTypes(t *testing.T) {
	settings := map[string]interface{}{
		"pg_host":     123,       // wrong type
		"pg_port":     "invalid", // wrong type
		"pg_database": true,      // wrong type
	}
	config := parseSettings(settings)

	if config.Host != "" {
		t.Errorf("expected empty host for wrong type, got %q", config.Host)
	}
	if config.Port != DefaultPort {
		t.Errorf("expected default port for wrong type, got %d", config.Port)
	}
	if config.Database != "" {
		t.Errorf("expected empty database for wrong type, got %q", config.Database)
	}
}

func TestParseSettings_PortBoundsCheck(t *testing.T) {
	tests := []struct {
		name string
		port float64
		want int
	}{
		{"valid port", 5433, 5433},
		{"zero falls back to default", 0, DefaultPort},
		{"negative falls back to default", -1, DefaultPort},
		{"overflow falls back to default", 99999, DefaultPort},
		{"max valid", 65535, 65535},
		{"min valid", 1, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := parseSettings(map[string]interface{}{"pg_port": tt.port})
			if config.Port != tt.want {
				t.Errorf("port=%v: got %d, want %d", tt.port, config.Port, tt.want)
			}
		})
	}
}

func TestParseSettings_NilSettings(t *testing.T) {
	config := parseSettings(nil)
	if config.Port != DefaultPort {
		t.Errorf("expected default port, got %d", config.Port)
	}
	if config.SSLMode != "require" {
		t.Errorf("expected default ssl_mode, got %q", config.SSLMode)
	}
}

// --- Config error tests using mock resolveConfig ---

func TestExecuteQuery_ConfigError(t *testing.T) {
	tool := newTestToolWithConfigError(fmt.Errorf("no credentials found"))
	defer tool.Stop()

	_, err := tool.ExecuteQuery(nil, "inc-1", map[string]interface{}{"query": "SELECT 1"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no credentials found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestListTables_ConfigError(t *testing.T) {
	tool := newTestToolWithConfigError(fmt.Errorf("no credentials found"))
	defer tool.Stop()

	_, err := tool.ListTables(nil, "inc-1", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDescribeTable_ConfigError(t *testing.T) {
	tool := newTestToolWithConfigError(fmt.Errorf("no credentials found"))
	defer tool.Stop()

	_, err := tool.DescribeTable(nil, "inc-1", map[string]interface{}{"table_name": "users"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetIndexes_ConfigError(t *testing.T) {
	tool := newTestToolWithConfigError(fmt.Errorf("no credentials found"))
	defer tool.Stop()

	_, err := tool.GetIndexes(nil, "inc-1", map[string]interface{}{"table_name": "users"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetTableStats_ConfigError(t *testing.T) {
	tool := newTestToolWithConfigError(fmt.Errorf("no credentials found"))
	defer tool.Stop()

	_, err := tool.GetTableStats(nil, "inc-1", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetTableStats_SpecificTable_ConfigError(t *testing.T) {
	tool := newTestToolWithConfigError(fmt.Errorf("no credentials found"))
	defer tool.Stop()

	_, err := tool.GetTableStats(nil, "inc-1", map[string]interface{}{"table_name": "users"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestExplainQuery_ConfigError(t *testing.T) {
	tool := newTestToolWithConfigError(fmt.Errorf("no credentials found"))
	defer tool.Stop()

	_, err := tool.ExplainQuery(nil, "inc-1", map[string]interface{}{"query": "SELECT 1"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetActiveQueries_ConfigError(t *testing.T) {
	tool := newTestToolWithConfigError(fmt.Errorf("no credentials found"))
	defer tool.Stop()

	_, err := tool.GetActiveQueries(nil, "inc-1", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetLocks_ConfigError(t *testing.T) {
	tool := newTestToolWithConfigError(fmt.Errorf("no credentials found"))
	defer tool.Stop()

	_, err := tool.GetLocks(nil, "inc-1", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetReplicationStatus_ConfigError(t *testing.T) {
	tool := newTestToolWithConfigError(fmt.Errorf("no credentials found"))
	defer tool.Stop()

	_, err := tool.GetReplicationStatus(nil, "inc-1", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetDatabaseStats_ConfigError(t *testing.T) {
	tool := newTestToolWithConfigError(fmt.Errorf("no credentials found"))
	defer tool.Stop()

	_, err := tool.GetDatabaseStats(nil, "inc-1", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestExecuteQuery_WithLogicalName_FullPath(t *testing.T) {
	rows := []map[string]interface{}{{"count": 42}}
	tool := newTestToolWithMock(rows, nil)
	tool.configCache.Set("creds:logical:postgresql:prod-pg", &PGConfig{
		Host: "prod-host", Port: 5432, Database: "proddb", Username: "admin", Password: "secret", SSLMode: "require", Timeout: 30,
	})
	defer tool.Stop()

	result, err := tool.ExecuteQuery(nil, "inc-1", map[string]interface{}{
		"query":        "SELECT count(*) FROM users",
		"logical_name": "prod-pg",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "42") {
		t.Errorf("expected result to contain '42', got %s", result)
	}
}
