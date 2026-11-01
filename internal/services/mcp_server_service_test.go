package services

import (
	"errors"
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/testhelpers"
	"gorm.io/gorm"
)

func setupMCPServerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	return testhelpers.NewGlobalSQLiteDB(t,
		&database.MCPServerConfig{}, &database.HTTPConnector{},
		&database.ToolType{}, &database.ToolInstance{}, &database.SkillTool{},
		&database.CronJobTool{}, &database.Connector{},
	)
}

func validMCPServerConfig() *database.MCPServerConfig {
	return &database.MCPServerConfig{
		Name:            "github-mcp",
		Transport:       database.MCPServerTransportHTTP,
		URL:             "https://example.com/mcp",
		NamespacePrefix: "github_ext",
	}
}

func strp(s string) *string { return &s }
func boolp(b bool) *bool    { return &b }

// managedTool returns the managed tool type and its instances for prefix.
func managedTool(t *testing.T, db *gorm.DB, prefix string) (*database.ToolType, []database.ToolInstance) {
	t.Helper()
	var tt database.ToolType
	if err := db.Where("name = ? AND kind = ?", prefix, database.ToolTypeKindMCP).First(&tt).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		t.Fatalf("load managed tool type: %v", err)
	}
	var insts []database.ToolInstance
	if err := db.Where("tool_type_id = ?", tt.ID).Find(&insts).Error; err != nil {
		t.Fatalf("load managed instances: %v", err)
	}
	return &tt, insts
}

func TestCreateMCPServer_EnablesConfigAndCreatesManagedTool(t *testing.T) {
	db := setupMCPServerTestDB(t)
	svc := NewMCPServerServiceWithDB(db)

	config := validMCPServerConfig()
	config.Enabled = false
	config.Transport = ""
	created, err := svc.CreateMCPServer(config)
	if err != nil {
		t.Fatalf("CreateMCPServer failed: %v", err)
	}
	if !created.Enabled {
		t.Fatal("expected config to be enabled on create")
	}
	if created.Transport != database.MCPServerTransportHTTP {
		t.Fatalf("Transport = %q, want default http", created.Transport)
	}

	tt, insts := managedTool(t, db, "github_ext")
	if tt == nil {
		t.Fatal("managed tool type not created")
	}
	if len(insts) != 1 {
		t.Fatalf("managed instances = %d, want 1", len(insts))
	}
	inst := insts[0]
	if inst.Name != "github-mcp" || inst.LogicalName != "github-mcp" || !inst.Enabled {
		t.Fatalf("managed instance mismatch: %+v", inst)
	}
	if id, _ := inst.Settings["mcp_server_config_id"].(float64); uint(id) != created.ID {
		t.Fatalf("settings.mcp_server_config_id = %v, want %d", inst.Settings["mcp_server_config_id"], created.ID)
	}
	got, err := svc.ManagedToolInstanceID(created.ID)
	if err != nil || got != inst.ID {
		t.Fatalf("ManagedToolInstanceID = %d, %v; want %d", got, err, inst.ID)
	}
}

func TestCreateMCPServer_DefaultNamespaceAndSuffix(t *testing.T) {
	db := setupMCPServerTestDB(t)
	svc := NewMCPServerServiceWithDB(db)

	if got := DefaultMCPNamespace("GitHub Issues!"); got != "ext.github_issues" {
		t.Fatalf("DefaultMCPNamespace = %q", got)
	}
	if got := DefaultMCPNamespace("!!!"); got != "ext.mcp" {
		t.Fatalf("DefaultMCPNamespace(empty slug) = %q", got)
	}

	a := validMCPServerConfig()
	a.Name, a.NamespacePrefix = "GitHub", ""
	first, err := svc.CreateMCPServer(a)
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	if first.NamespacePrefix != "ext.github" {
		t.Fatalf("first namespace = %q", first.NamespacePrefix)
	}
	b := validMCPServerConfig()
	b.Name, b.NamespacePrefix = "github", ""
	second, err := svc.CreateMCPServer(b)
	if err != nil {
		t.Fatalf("create second: %v", err)
	}
	if second.NamespacePrefix != "ext.github_2" {
		t.Fatalf("second namespace = %q, want ext.github_2", second.NamespacePrefix)
	}
	// The second managed instance must get a distinct logical name.
	_, i1 := managedTool(t, db, "ext.github")
	_, i2 := managedTool(t, db, "ext.github_2")
	if len(i1) != 1 || len(i2) != 1 || i1[0].LogicalName == i2[0].LogicalName {
		t.Fatalf("logical names not unique: %+v %+v", i1, i2)
	}
}

func TestCreateMCPServer_RejectsConflictingInputs(t *testing.T) {
	tests := []struct {
		name         string
		seed         func(t *testing.T, db *gorm.DB)
		mutate       func(*database.MCPServerConfig)
		wantMessage  string
		wantConflict bool
	}{
		{name: "duplicate name", seed: func(t *testing.T, db *gorm.DB) { seedMCPServer(t, db, "github-mcp", "other_ns") }, wantMessage: "already exists", wantConflict: true},
		{name: "reserved namespace", mutate: func(c *database.MCPServerConfig) { c.NamespacePrefix = "grafana" }, wantMessage: "built-in tool namespace", wantConflict: true},
		{name: "reserved jira namespace", mutate: func(c *database.MCPServerConfig) { c.NamespacePrefix = "jira" }, wantMessage: "built-in tool namespace", wantConflict: true},
		{name: "reserved events namespace", mutate: func(c *database.MCPServerConfig) { c.NamespacePrefix = "events" }, wantMessage: "built-in tool namespace", wantConflict: true},
		{name: "duplicate namespace", seed: func(t *testing.T, db *gorm.DB) { seedMCPServer(t, db, "other-mcp", "github_ext") }, wantMessage: "namespace_prefix", wantConflict: true},
		{name: "HTTP connector namespace collision", seed: func(t *testing.T, db *gorm.DB) { seedHTTPConnector(t, db, "github_ext") }, wantMessage: "existing HTTP connector namespace", wantConflict: true},
		{name: "tool type collision", seed: func(t *testing.T, db *gorm.DB) {
			if err := db.Create(&database.ToolType{Name: "github_ext"}).Error; err != nil {
				t.Fatal(err)
			}
		}, wantMessage: "existing tool type", wantConflict: true},
		{name: "tool instance name clash", seed: func(t *testing.T, db *gorm.DB) {
			tt := database.ToolType{Name: "zabbix"}
			db.Create(&tt)
			if err := db.Create(&database.ToolInstance{ToolTypeID: tt.ID, Name: "github-mcp", LogicalName: "zbx"}).Error; err != nil {
				t.Fatal(err)
			}
		}, wantMessage: "a tool named", wantConflict: true},
		{name: "stdio", mutate: func(c *database.MCPServerConfig) { c.Transport = database.MCPServerTransportStdio }, wantMessage: "validation failed"},
		{name: "bad auth", mutate: func(c *database.MCPServerConfig) { c.AuthConfig = database.JSONB{"type": "bearer"} }, wantMessage: "validation failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupMCPServerTestDB(t)
			if tt.seed != nil {
				tt.seed(t, db)
			}
			config := validMCPServerConfig()
			if tt.mutate != nil {
				tt.mutate(config)
			}
			_, err := NewMCPServerServiceWithDB(db).CreateMCPServer(config)
			if err == nil {
				t.Fatal("CreateMCPServer error = nil, want error")
			}
			if !strings.Contains(err.Error(), tt.wantMessage) {
				t.Fatalf("CreateMCPServer error = %v, want message containing %q", err, tt.wantMessage)
			}
			if tt.wantConflict && !errors.Is(err, ErrMCPServerConflict) {
				t.Fatalf("error %v should wrap ErrMCPServerConflict", err)
			}
			// A failed create must not leave a config or managed tool behind.
			var n int64
			db.Model(&database.ToolType{}).Where("kind = ?", database.ToolTypeKindMCP).Count(&n)
			if n != 0 {
				t.Fatalf("managed tool types left behind: %d", n)
			}
		})
	}
}

func TestUpdateMCPServer_RejectsConflictingInputs(t *testing.T) {
	tests := []struct {
		name        string
		seed        func(t *testing.T, db *gorm.DB)
		patch       MCPServerPatch
		wantMessage string
	}{
		{"duplicate name", func(t *testing.T, db *gorm.DB) { seedMCPServer(t, db, "jira-mcp", "jira_ext") }, MCPServerPatch{Name: strp("jira-mcp")}, "already exists"},
		{"duplicate namespace", func(t *testing.T, db *gorm.DB) { seedMCPServer(t, db, "jira-mcp", "jira_ext") }, MCPServerPatch{NamespacePrefix: strp("jira_ext")}, "namespace_prefix"},
		{"HTTP namespace", func(t *testing.T, db *gorm.DB) { seedHTTPConnector(t, db, "jira_ext") }, MCPServerPatch{NamespacePrefix: strp("jira_ext")}, "existing HTTP connector namespace"},
		{"reserved namespace", nil, MCPServerPatch{NamespacePrefix: strp("grafana")}, "built-in tool namespace"},
		{"invalid transport", nil, MCPServerPatch{Transport: strp("grpc")}, "validation failed"},
		{"stdio transport", nil, MCPServerPatch{Transport: strp("stdio")}, "validation failed"},
		{"bad url", nil, MCPServerPatch{URL: strp("not a url")}, "validation failed"},
		{"bad header auth", nil, MCPServerPatch{Auth: &MCPAuthPatch{Value: database.JSONB{"type": "header", "header_name": "Host", "value": "x"}}}, "reserved"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupMCPServerTestDB(t)
			svc := NewMCPServerServiceWithDB(db)
			created, err := svc.CreateMCPServer(validMCPServerConfig())
			if err != nil {
				t.Fatalf("CreateMCPServer failed: %v", err)
			}
			if tt.seed != nil {
				tt.seed(t, db)
			}
			_, err = svc.UpdateMCPServer(created.ID, tt.patch)
			if err == nil {
				t.Fatal("UpdateMCPServer error = nil, want error")
			}
			if !strings.Contains(err.Error(), tt.wantMessage) {
				t.Fatalf("UpdateMCPServer error = %v, want message containing %q", err, tt.wantMessage)
			}
		})
	}
}

func TestUpdateMCPServer_RenameAndNamespaceSyncManagedTool(t *testing.T) {
	db := setupMCPServerTestDB(t)
	svc := NewMCPServerServiceWithDB(db)
	created, err := svc.CreateMCPServer(validMCPServerConfig())
	if err != nil {
		t.Fatal(err)
	}
	_, before := managedTool(t, db, "github_ext")

	updated, err := svc.UpdateMCPServer(created.ID, MCPServerPatch{
		Name:            strp("GitHub Prod"),
		NamespacePrefix: strp("ext.gh_prod"),
		URL:             strp("https://gh.example.com/mcp"),
		DisabledTools:   &[]string{"delete_repo", " ", "delete_repo", "push"},
		Enabled:         boolp(false),
	})
	if err != nil {
		t.Fatalf("UpdateMCPServer: %v", err)
	}
	if updated.NamespacePrefix != "ext.gh_prod" || updated.Enabled {
		t.Fatalf("updated mismatch: %+v", updated)
	}
	if got := []string(updated.DisabledTools); len(got) != 2 || got[0] != "delete_repo" || got[1] != "push" {
		t.Fatalf("DisabledTools = %v", got)
	}
	if old, _ := managedTool(t, db, "github_ext"); old != nil {
		t.Fatal("old managed tool type still present")
	}
	tt, after := managedTool(t, db, "ext.gh_prod")
	if tt == nil || len(after) != 1 {
		t.Fatalf("renamed managed tool missing: %v %v", tt, after)
	}
	if after[0].ID != before[0].ID {
		t.Fatal("managed instance was recreated; skill bindings would be lost")
	}
	if after[0].Name != "GitHub Prod" || after[0].LogicalName != "github-prod" || after[0].Enabled {
		t.Fatalf("managed instance not synced: %+v", after[0])
	}
	if !strings.Contains(tt.Description, "gh.example.com") {
		t.Fatalf("type description not updated: %q", tt.Description)
	}
}

func TestUpdateMCPServer_AuthPatchSemantics(t *testing.T) {
	db := setupMCPServerTestDB(t)
	svc := NewMCPServerServiceWithDB(db)
	cfg := validMCPServerConfig()
	cfg.AuthConfig = database.JSONB{"type": "header", "header_name": "X-Api-Key", "value": "s3cret"}
	created, err := svc.CreateMCPServer(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Same type, masked secret, new header name: secret kept.
	got, err := svc.UpdateMCPServer(created.ID, MCPServerPatch{Auth: &MCPAuthPatch{Value: database.JSONB{"type": "header", "header_name": "X-Key", "value": MCPMaskedSecret}}})
	if err != nil {
		t.Fatal(err)
	}
	if got.AuthConfig["value"] != "s3cret" || got.AuthConfig["header_name"] != "X-Key" {
		t.Fatalf("masked secret not kept: %v", got.AuthConfig)
	}
	// No auth patch: unchanged.
	got, err = svc.UpdateMCPServer(created.ID, MCPServerPatch{Name: strp("renamed")})
	if err != nil || got.AuthConfig["value"] != "s3cret" {
		t.Fatalf("auth changed without patch: %v %v", got.AuthConfig, err)
	}
	// Type change without a secret: rejected (bearer needs a token).
	if _, err := svc.UpdateMCPServer(created.ID, MCPServerPatch{Auth: &MCPAuthPatch{Value: database.JSONB{"type": "bearer"}}}); err == nil {
		t.Fatal("bearer without token accepted")
	}
	// Type change with a secret: replaced, old fields dropped.
	got, err = svc.UpdateMCPServer(created.ID, MCPServerPatch{Auth: &MCPAuthPatch{Value: database.JSONB{"type": "bearer", "token": "tok"}}})
	if err != nil {
		t.Fatal(err)
	}
	if got.AuthConfig["token"] != "tok" || got.AuthConfig["value"] != nil || got.AuthConfig["header_name"] != nil {
		t.Fatalf("type change not a replace: %v", got.AuthConfig)
	}
	// Clear.
	got, err = svc.UpdateMCPServer(created.ID, MCPServerPatch{Auth: &MCPAuthPatch{Clear: true}})
	if err != nil || len(got.AuthConfig) != 0 {
		t.Fatalf("clear failed: %v %v", got.AuthConfig, err)
	}
	stored, _ := svc.GetMCPServer(created.ID)
	if len(stored.AuthConfig) != 0 {
		t.Fatalf("clear not persisted: %v", stored.AuthConfig)
	}
}

func TestMergeAuth_Rules(t *testing.T) {
	stored := database.JSONB{"type": "oauth", "client_id": "c1", "access_token": "at", "refresh_token": "rt", "expires_at": "2030-01-01T00:00:00Z"}
	// OAuth: UI sends only type + scope; token metadata kept.
	got := mergeAuth(stored, &MCPAuthPatch{Value: database.JSONB{"type": "oauth", "scope": "read"}})
	for _, k := range []string{"client_id", "access_token", "refresh_token", "expires_at"} {
		if got[k] != stored[k] {
			t.Errorf("oauth %s not kept: %v", k, got[k])
		}
	}
	if got["scope"] != "read" {
		t.Errorf("scope = %v", got["scope"])
	}
	// Different type: no merge.
	got = mergeAuth(stored, &MCPAuthPatch{Value: database.JSONB{"type": "bearer", "token": "x"}})
	if _, ok := got["access_token"]; ok {
		t.Error("access_token leaked across a type change")
	}
	// Empty value / clear.
	if mergeAuth(stored, &MCPAuthPatch{Value: database.JSONB{}}) != nil {
		t.Error("empty value should clear")
	}
	if mergeAuth(stored, &MCPAuthPatch{Clear: true}) != nil {
		t.Error("clear should clear")
	}
	// Missing secret with no stored secret: key dropped, not set to "".
	got = mergeAuth(database.JSONB{"type": "bearer"}, &MCPAuthPatch{Value: database.JSONB{"type": "bearer", "token": ""}})
	if _, ok := got["token"]; ok {
		t.Errorf("empty token kept: %v", got)
	}
	// Test helper uses the same rules.
	got = MergeMCPAuthForTest(database.JSONB{"type": "bearer", "token": "real"}, database.JSONB{"type": "bearer", "token": MCPMaskedSecret})
	if got["token"] != "real" {
		t.Errorf("MergeMCPAuthForTest = %v", got)
	}
}

func TestValidateMCPAuth(t *testing.T) {
	ok := []database.JSONB{
		nil,
		{},
		{"type": "bearer", "token": "t"},
		{"type": "header", "header_name": "X-Api-Key", "value": "v"},
		{"type": "oauth"},
	}
	for _, c := range ok {
		if err := ValidateMCPAuth(c); err != nil {
			t.Errorf("ValidateMCPAuth(%v) = %v", c, err)
		}
	}
	bad := []database.JSONB{
		{"type": "bearer"},
		{"type": "bearer", "token": "  "},
		{"type": "header", "header_name": "X Bad", "value": "v"},
		{"type": "header", "header_name": "Content-Type", "value": "v"},
		{"type": "header", "header_name": "mcp-session-id", "value": "v"},
		{"type": "header", "header_name": "X-Key"},
		{"type": "basic", "token": "t"},
		{"token": "t"},
	}
	for _, c := range bad {
		if err := ValidateMCPAuth(c); err == nil {
			t.Errorf("ValidateMCPAuth(%v) = nil, want error", c)
		}
	}
	if !IsMCPSecretField("client_secret") || IsMCPSecretField("header_name") {
		t.Error("IsMCPSecretField wrong")
	}
}

func TestUpdateMCPServer_LegacyStdioRowCanBeDisabled(t *testing.T) {
	db := setupMCPServerTestDB(t)
	svc := NewMCPServerServiceWithDB(db)
	row := database.MCPServerConfig{Name: "local", Transport: database.MCPServerTransportStdio, Command: "/bin/mcp", NamespacePrefix: "ext.local", Enabled: true}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateMCPServer(row.ID, MCPServerPatch{Enabled: boolp(false)}); err != nil {
		t.Fatalf("disable legacy stdio row: %v", err)
	}
	// Re-enabling it as stdio is refused.
	if _, err := svc.UpdateMCPServer(row.ID, MCPServerPatch{Enabled: boolp(true)}); err == nil {
		t.Fatal("re-enabling stdio row accepted")
	}
	// Converting it to a remote server works and drops the command fields.
	got, err := svc.UpdateMCPServer(row.ID, MCPServerPatch{Transport: strp("http"), URL: strp("https://x.example.com/mcp"), Enabled: boolp(true)})
	if err != nil {
		t.Fatalf("convert to http: %v", err)
	}
	if got.Command != "" || got.Args != nil || got.EnvVars != nil {
		t.Fatalf("command fields kept: %+v", got)
	}
}

func TestDeleteMCPServer_RemovesManagedToolAndBindings(t *testing.T) {
	db := setupMCPServerTestDB(t)
	svc := NewMCPServerServiceWithDB(db)
	created, err := svc.CreateMCPServer(validMCPServerConfig())
	if err != nil {
		t.Fatal(err)
	}
	instID, err := svc.ManagedToolInstanceID(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.SkillTool{SkillID: 7, ToolInstanceID: instID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.CronJobTool{CronJobID: 3, ToolInstanceID: instID}).Error; err != nil {
		t.Fatal(err)
	}

	if err := svc.DeleteMCPServer(created.ID); err != nil {
		t.Fatalf("DeleteMCPServer: %v", err)
	}
	for name, model := range map[string]interface{}{
		"configs": &database.MCPServerConfig{}, "tool types": &database.ToolType{}, "tool instances": &database.ToolInstance{},
		"skill_tools": &database.SkillTool{}, "cron_job_tools": &database.CronJobTool{},
	} {
		var n int64
		db.Model(model).Count(&n)
		if n != 0 {
			t.Errorf("%s left: %d", name, n)
		}
	}
}

func TestSyncAllManagedTools_CreatesForPreexistingConfigs(t *testing.T) {
	db := setupMCPServerTestDB(t)
	svc := NewMCPServerServiceWithDB(db)
	seedMCPServer(t, db, "github", "github_ext")
	seedMCPServer(t, db, "jira", "jira_ext")

	if err := svc.SyncAllManagedTools(); err != nil {
		t.Fatalf("SyncAllManagedTools: %v", err)
	}
	if err := svc.SyncAllManagedTools(); err != nil { // idempotent
		t.Fatalf("second SyncAllManagedTools: %v", err)
	}
	for _, ns := range []string{"github_ext", "jira_ext"} {
		tt, insts := managedTool(t, db, ns)
		if tt == nil || len(insts) != 1 {
			t.Fatalf("%s: managed tool %v, instances %d", ns, tt, len(insts))
		}
	}
}

func TestStoreOAuthTokens(t *testing.T) {
	db := setupMCPServerTestDB(t)
	svc := NewMCPServerServiceWithDB(db)
	created, err := svc.CreateMCPServer(validMCPServerConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.StoreOAuthTokens(created.ID, database.JSONB{"type": "oauth", "access_token": "a"}); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.GetMCPServer(created.ID)
	if got.AuthConfig["access_token"] != "a" {
		t.Fatalf("tokens not stored: %v", got.AuthConfig)
	}
	if err := svc.StoreOAuthTokens(999, database.JSONB{"type": "oauth"}); !errors.Is(err, ErrMCPServerNotFound) {
		t.Fatalf("missing id err = %v", err)
	}
}

func TestMCPServerService_NotFoundPaths(t *testing.T) {
	db := setupMCPServerTestDB(t)
	svc := NewMCPServerServiceWithDB(db)

	if _, err := svc.GetMCPServer(999); !errors.Is(err, ErrMCPServerNotFound) {
		t.Fatalf("GetMCPServer missing error = %v, want not found", err)
	}
	if _, err := svc.UpdateMCPServer(999, MCPServerPatch{Name: strp("missing")}); !errors.Is(err, ErrMCPServerNotFound) {
		t.Fatalf("UpdateMCPServer missing error = %v, want not found", err)
	}
	if err := svc.DeleteMCPServer(999); !errors.Is(err, ErrMCPServerNotFound) {
		t.Fatalf("DeleteMCPServer missing error = %v, want not found", err)
	}
}

func TestListMCPServers_ReturnsPersistedRows(t *testing.T) {
	db := setupMCPServerTestDB(t)
	svc := NewMCPServerServiceWithDB(db)

	seedMCPServer(t, db, "jira", "jira_ext")
	seedMCPServer(t, db, "github", "github_ext")

	configs, err := svc.ListMCPServers()
	if err != nil {
		t.Fatalf("ListMCPServers failed: %v", err)
	}
	if len(configs) != 2 || configs[0].Name != "github" {
		t.Fatalf("ListMCPServers = %+v, want 2 rows sorted by name", configs)
	}
}

func seedMCPServer(t *testing.T, db *gorm.DB, name, namespace string) {
	t.Helper()

	if err := db.Create(&database.MCPServerConfig{
		Name:            name,
		Transport:       database.MCPServerTransportSSE,
		URL:             "https://example.com/sse",
		NamespacePrefix: namespace,
		Enabled:         true,
	}).Error; err != nil {
		t.Fatalf("failed to seed MCP server: %v", err)
	}
}

func seedHTTPConnector(t *testing.T, db *gorm.DB, toolTypeName string) {
	t.Helper()

	if err := db.Create(&database.HTTPConnector{
		ToolTypeName: toolTypeName,
		BaseURLField: "base_url",
		Tools: database.JSONB{
			"tools": []interface{}{
				map[string]interface{}{"name": "get_status", "http_method": "GET", "path": "/status"},
			},
		},
		Enabled: true,
	}).Error; err != nil {
		t.Fatalf("failed to seed HTTP connector: %v", err)
	}
}
