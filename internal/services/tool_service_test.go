package services

import (
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupToolTestDB creates an in-memory SQLite database with tool-related tables
func setupToolTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to connect to test database: %v", err)
	}

	err = db.AutoMigrate(
		&database.ToolType{},
		&database.ToolInstance{},
	)
	if err != nil {
		t.Fatalf("failed to migrate test database: %v", err)
	}

	database.DB = db
	return db
}

func TestSlugifyLogicalName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"simple lowercase", "prod-ssh", "prod-ssh"},
		{"mixed case", "Production Zabbix", "production-zabbix"},
		{"special chars", "My Tool (v2.0)!", "my-tool-v2-0"},
		{"multiple spaces", "foo   bar", "foo-bar"},
		{"leading/trailing special", "---test---", "test"},
		{"empty string", "", ""},
		{"numbers", "server123", "server123"},
		{"unicode", "café-server", "caf-server"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := database.SlugifyLogicalName(tt.input)
			if got != tt.expected {
				t.Errorf("database.SlugifyLogicalName(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestCreateToolInstance_SetsLogicalName(t *testing.T) {
	db := setupToolTestDB(t)

	// Create a tool type
	toolType := database.ToolType{Name: "ssh", Description: "SSH tool"}
	if err := db.Create(&toolType).Error; err != nil {
		t.Fatalf("failed to create tool type: %v", err)
	}

	svc := &ToolService{db: db}
	instance, err := svc.CreateToolInstance(toolType.ID, "Production SSH", "", nil)
	if err != nil {
		t.Fatalf("CreateToolInstance failed: %v", err)
	}

	if instance.LogicalName != "production-ssh" {
		t.Errorf("expected logical_name 'production-ssh', got %q", instance.LogicalName)
	}
}

func TestCreateToolInstance_LogicalNameUnique(t *testing.T) {
	db := setupToolTestDB(t)

	toolType := database.ToolType{Name: "ssh", Description: "SSH tool"}
	if err := db.Create(&toolType).Error; err != nil {
		t.Fatalf("failed to create tool type: %v", err)
	}

	svc := &ToolService{db: db}

	_, err := svc.CreateToolInstance(toolType.ID, "Production SSH", "", nil)
	if err != nil {
		t.Fatalf("first CreateToolInstance failed: %v", err)
	}

	// Second instance with same name should fail due to unique constraint on Name
	_, err = svc.CreateToolInstance(toolType.ID, "Production SSH", "", nil)
	if err == nil {
		t.Error("expected error for duplicate logical name, got nil")
	}
}

func TestUpdateToolInstance_UpdatesLogicalName(t *testing.T) {
	db := setupToolTestDB(t)

	toolType := database.ToolType{Name: "zabbix", Description: "Zabbix"}
	if err := db.Create(&toolType).Error; err != nil {
		t.Fatalf("failed to create tool type: %v", err)
	}

	svc := &ToolService{db: db}
	instance, err := svc.CreateToolInstance(toolType.ID, "Old Name", "", nil)
	if err != nil {
		t.Fatalf("CreateToolInstance failed: %v", err)
	}

	if instance.LogicalName != "old-name" {
		t.Fatalf("expected logical_name 'old-name', got %q", instance.LogicalName)
	}

	err = svc.UpdateToolInstance(instance.ID, ToolInstanceUpdate{Name: ptr("New Name"), LogicalName: ptr("")})
	if err != nil {
		t.Fatalf("UpdateToolInstance failed: %v", err)
	}

	updated, err := svc.GetToolInstance(instance.ID)
	if err != nil {
		t.Fatalf("GetToolInstance failed: %v", err)
	}

	if updated.LogicalName != "new-name" {
		t.Errorf("expected logical_name 'new-name', got %q", updated.LogicalName)
	}
}

func TestLogicalName_ExposedInListResponse(t *testing.T) {
	db := setupToolTestDB(t)

	toolType := database.ToolType{Name: "ssh", Description: "SSH"}
	if err := db.Create(&toolType).Error; err != nil {
		t.Fatalf("failed to create tool type: %v", err)
	}

	svc := &ToolService{db: db}
	_, err := svc.CreateToolInstance(toolType.ID, "My Server", "", nil)
	if err != nil {
		t.Fatalf("CreateToolInstance failed: %v", err)
	}

	instances, err := svc.ListToolInstances()
	if err != nil {
		t.Fatalf("ListToolInstances failed: %v", err)
	}

	if len(instances) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(instances))
	}

	if instances[0].LogicalName != "my-server" {
		t.Errorf("expected logical_name 'my-server', got %q", instances[0].LogicalName)
	}
}

func TestCreateToolInstance_HonorsProvidedLogicalName(t *testing.T) {
	db := setupToolTestDB(t)

	toolType := database.ToolType{Name: "ssh", Description: "SSH tool"}
	if err := db.Create(&toolType).Error; err != nil {
		t.Fatalf("failed to create tool type: %v", err)
	}

	svc := &ToolService{db: db}
	instance, err := svc.CreateToolInstance(toolType.ID, "Production SSH Server", "prod-ssh", nil)
	if err != nil {
		t.Fatalf("CreateToolInstance failed: %v", err)
	}

	// User-supplied logical_name is sanitized via SlugifyLogicalName, preserving valid slugs.
	if instance.LogicalName != "prod-ssh" {
		t.Errorf("expected logical_name 'prod-ssh', got %q", instance.LogicalName)
	}
}

func TestCreateToolInstance_SanitizesUnsafeLogicalName(t *testing.T) {
	db := setupToolTestDB(t)

	toolType := database.ToolType{Name: "ssh", Description: "SSH tool"}
	if err := db.Create(&toolType).Error; err != nil {
		t.Fatalf("failed to create tool type: %v", err)
	}

	svc := &ToolService{db: db}
	// A logical name with quotes, backticks, and newlines must be sanitized.
	instance, err := svc.CreateToolInstance(toolType.ID, "Test Server", "evil\"`name\nnewline", nil)
	if err != nil {
		t.Fatalf("CreateToolInstance failed: %v", err)
	}

	// SlugifyLogicalName strips non-alphanumeric chars, so the result should be safe.
	if instance.LogicalName != "evil-name-newline" {
		t.Errorf("expected sanitized logical_name 'evil-name-newline', got %q", instance.LogicalName)
	}
}

func TestCreateToolInstance_InvalidToolTypeID(t *testing.T) {
	setupToolTestDB(t)

	svc := &ToolService{db: database.GetDB()}

	// Use a tool type ID that doesn't exist
	_, err := svc.CreateToolInstance(99999, "Test Instance", "", nil)
	if err == nil {
		t.Error("expected error for invalid tool_type_id, got nil")
	}
	if err != nil && !strings.Contains(err.Error(), "validation failed") {
		t.Errorf("expected 'validation failed' error, got: %v", err)
	}
}

func TestEnsureToolTypes_CreatesIncidentsTypeAndInstance(t *testing.T) {
	db := setupToolTestDB(t)
	svc := &ToolService{db: db}

	if err := svc.EnsureToolTypes(); err != nil {
		t.Fatalf("EnsureToolTypes failed: %v", err)
	}

	// Verify incidents ToolType was created
	var tt database.ToolType
	if err := db.Where("name = ?", "incidents").First(&tt).Error; err != nil {
		t.Fatalf("incidents ToolType not found: %v", err)
	}
	if tt.Description == "" {
		t.Error("expected non-empty description for incidents ToolType")
	}

	// Verify seeded ToolInstance exists with logical name "incidents"
	var instance database.ToolInstance
	if err := db.Where("logical_name = ?", "incidents").First(&instance).Error; err != nil {
		t.Fatalf("incidents ToolInstance not found: %v", err)
	}
	if instance.Name != "Incidents" {
		t.Errorf("expected Name 'Incidents', got %q", instance.Name)
	}
	if !instance.Enabled {
		t.Error("expected incidents instance to be enabled")
	}
	if instance.ToolTypeID != tt.ID {
		t.Errorf("expected ToolTypeID %d, got %d", tt.ID, instance.ToolTypeID)
	}

	// Second call must be idempotent
	if err := svc.EnsureToolTypes(); err != nil {
		t.Fatalf("second EnsureToolTypes call failed: %v", err)
	}
	var count int64
	if err := db.Model(&database.ToolInstance{}).Where("logical_name = ?", "incidents").Count(&count).Error; err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if count != 1 {
		t.Errorf("expected exactly 1 incidents instance after idempotent call, got %d", count)
	}
}

func TestUpdateToolInstance_HonorsProvidedLogicalName(t *testing.T) {
	db := setupToolTestDB(t)

	toolType := database.ToolType{Name: "zabbix", Description: "Zabbix"}
	if err := db.Create(&toolType).Error; err != nil {
		t.Fatalf("failed to create tool type: %v", err)
	}

	svc := &ToolService{db: db}
	instance, err := svc.CreateToolInstance(toolType.ID, "Old Name", "", nil)
	if err != nil {
		t.Fatalf("CreateToolInstance failed: %v", err)
	}

	err = svc.UpdateToolInstance(instance.ID, ToolInstanceUpdate{Name: ptr("New Name"), LogicalName: ptr("custom-logical")})
	if err != nil {
		t.Fatalf("UpdateToolInstance failed: %v", err)
	}

	updated, err := svc.GetToolInstance(instance.ID)
	if err != nil {
		t.Fatalf("GetToolInstance failed: %v", err)
	}

	// User-supplied logical_name is sanitized via SlugifyLogicalName, preserving valid slugs.
	if updated.LogicalName != "custom-logical" {
		t.Errorf("expected logical_name 'custom-logical', got %q", updated.LogicalName)
	}
}

func TestEnsureToolTypes_SeedsEventsInstance(t *testing.T) {
	db := setupToolTestDB(t)
	svc := &ToolService{db: db}
	for i := 0; i < 2; i++ {
		if err := svc.EnsureToolTypes(); err != nil {
			t.Fatalf("EnsureToolTypes #%d: %v", i, err)
		}
	}
	var instances []database.ToolInstance
	db.Preload("ToolType").Where("logical_name = ?", "events").Find(&instances)
	if len(instances) != 1 || instances[0].ToolType.Name != EventsToolType || !instances[0].Enabled {
		t.Fatalf("events instances = %+v, want one enabled instance of the events type", instances)
	}
}

func ptr[T any](v T) *T { return &v }

// seedZabbixInstance creates a zabbix tool instance with a secret, a plain
// setting and an ssh_keys blob so partial-update tests can check each one.
func seedZabbixInstance(t *testing.T, db *gorm.DB) (*ToolService, *database.ToolInstance) {
	t.Helper()
	toolType := database.ToolType{Name: "zabbix", Description: "Zabbix"}
	if err := db.Create(&toolType).Error; err != nil {
		t.Fatalf("failed to create tool type: %v", err)
	}
	svc := &ToolService{db: db}
	instance, err := svc.CreateToolInstance(toolType.ID, "Prod Zabbix", "custom-zbx", database.JSONB{
		"zabbix_url":   "https://zbx.example.com",
		"zabbix_token": "real-token",
		"ssh_keys":     []interface{}{map[string]interface{}{"id": "k1", "private_key": "PEM"}},
	})
	if err != nil {
		t.Fatalf("CreateToolInstance failed: %v", err)
	}
	return svc, instance
}

func TestUpdateToolInstance_EmptyUpdateChangesNothing(t *testing.T) {
	db := setupToolTestDB(t)
	svc, instance := seedZabbixInstance(t, db)
	if err := db.Model(instance).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable: %v", err)
	}

	if err := svc.UpdateToolInstance(instance.ID, ToolInstanceUpdate{}); err != nil {
		t.Fatalf("UpdateToolInstance failed: %v", err)
	}

	got, err := svc.GetToolInstance(instance.ID)
	if err != nil {
		t.Fatalf("GetToolInstance failed: %v", err)
	}
	if got.Name != "Prod Zabbix" || got.LogicalName != "custom-zbx" || got.Enabled {
		t.Errorf("unexpected row after empty update: name=%q logical=%q enabled=%v", got.Name, got.LogicalName, got.Enabled)
	}
	if got.Settings["zabbix_token"] != "real-token" || got.Settings["zabbix_url"] != "https://zbx.example.com" {
		t.Errorf("settings changed by empty update: %v", got.Settings)
	}
}

func TestUpdateToolInstance_OnlyEnabled(t *testing.T) {
	db := setupToolTestDB(t)
	svc, instance := seedZabbixInstance(t, db)

	if err := svc.UpdateToolInstance(instance.ID, ToolInstanceUpdate{Enabled: ptr(false)}); err != nil {
		t.Fatalf("UpdateToolInstance failed: %v", err)
	}

	got, _ := svc.GetToolInstance(instance.ID)
	if got.Enabled {
		t.Error("expected tool to be disabled")
	}
	if got.LogicalName != "custom-zbx" {
		t.Errorf("logical_name changed: %q", got.LogicalName)
	}
	if got.Settings["zabbix_token"] != "real-token" {
		t.Errorf("settings changed: %v", got.Settings)
	}
}

func TestUpdateToolInstance_NameWithoutLogicalNameKeepsSlug(t *testing.T) {
	db := setupToolTestDB(t)
	svc, instance := seedZabbixInstance(t, db)

	if err := svc.UpdateToolInstance(instance.ID, ToolInstanceUpdate{Name: ptr("Renamed")}); err != nil {
		t.Fatalf("UpdateToolInstance failed: %v", err)
	}

	got, _ := svc.GetToolInstance(instance.ID)
	if got.Name != "Renamed" || got.LogicalName != "custom-zbx" {
		t.Errorf("name=%q logical=%q, want Renamed/custom-zbx", got.Name, got.LogicalName)
	}
	if !got.Enabled {
		t.Error("omitted enabled must not disable the tool")
	}
}

func TestUpdateToolInstance_RejectsEmptyName(t *testing.T) {
	db := setupToolTestDB(t)
	svc, instance := seedZabbixInstance(t, db)

	err := svc.UpdateToolInstance(instance.ID, ToolInstanceUpdate{Name: ptr("  ")})
	if err == nil || !strings.Contains(err.Error(), "validation failed") {
		t.Fatalf("expected validation error, got %v", err)
	}
}

func TestUpdateToolInstance_SettingsSecretsAndSSHKeys(t *testing.T) {
	tests := []struct {
		name      string
		settings  database.JSONB
		wantToken interface{}
		wantURL   interface{}
	}{
		{
			name:      "masked secret keeps stored value",
			settings:  database.JSONB{"zabbix_url": "https://new.example.com", "zabbix_token": database.ToolSecretMask},
			wantToken: "real-token",
			wantURL:   "https://new.example.com",
		},
		{
			name:      "omitted secret keeps stored value",
			settings:  database.JSONB{"zabbix_url": "https://new.example.com"},
			wantToken: "real-token",
			wantURL:   "https://new.example.com",
		},
		{
			name:      "new secret replaces stored value",
			settings:  database.JSONB{"zabbix_url": "https://zbx.example.com", "zabbix_token": "rotated"},
			wantToken: "rotated",
			wantURL:   "https://zbx.example.com",
		},
		{
			name:      "empty string clears secret",
			settings:  database.JSONB{"zabbix_url": "https://zbx.example.com", "zabbix_token": ""},
			wantToken: "",
			wantURL:   "https://zbx.example.com",
		},
		{
			name: "client ssh_keys are ignored",
			settings: database.JSONB{
				"zabbix_url": "https://zbx.example.com",
				"ssh_keys":   []interface{}{map[string]interface{}{"id": "evil"}},
			},
			wantToken: "real-token",
			wantURL:   "https://zbx.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupToolTestDB(t)
			svc, instance := seedZabbixInstance(t, db)

			if err := svc.UpdateToolInstance(instance.ID, ToolInstanceUpdate{Settings: &tt.settings}); err != nil {
				t.Fatalf("UpdateToolInstance failed: %v", err)
			}

			got, _ := svc.GetToolInstance(instance.ID)
			if got.Settings["zabbix_token"] != tt.wantToken {
				t.Errorf("zabbix_token = %v, want %v", got.Settings["zabbix_token"], tt.wantToken)
			}
			if got.Settings["zabbix_url"] != tt.wantURL {
				t.Errorf("zabbix_url = %v, want %v", got.Settings["zabbix_url"], tt.wantURL)
			}
			keys, _ := got.Settings["ssh_keys"].([]interface{})
			if len(keys) != 1 || keys[0].(map[string]interface{})["private_key"] != "PEM" {
				t.Errorf("ssh_keys not preserved: %v", got.Settings["ssh_keys"])
			}
		})
	}
}
