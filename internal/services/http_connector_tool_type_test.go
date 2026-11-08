package services

import (
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/gorm"
)

func findToolType(t *testing.T, db *gorm.DB, name string) *database.ToolType {
	t.Helper()
	var tt database.ToolType
	if err := db.Where("name = ?", name).First(&tt).Error; err != nil {
		return nil
	}
	return &tt
}

func TestHTTPConnector_CreateEnsuresToolType(t *testing.T) {
	db := setupHTTPConnectorTestDB(t)
	svc := &HTTPConnectorService{db: db}

	c := validHTTPConnector()
	c.AuthConfig = database.JSONB{"method": "api_key", "token_field": "api_key", "header_name": "X-Billing-Key"}
	if _, err := svc.CreateHTTPConnector(c); err != nil {
		t.Fatalf("create: %v", err)
	}

	tt := findToolType(t, db, "internal-billing")
	if tt == nil {
		t.Fatal("expected a tool type named after the connector")
	}
	if tt.Description != "Internal billing API" {
		t.Errorf("description = %q, want connector description", tt.Description)
	}
	props, _ := tt.Schema["properties"].(map[string]interface{})
	if _, ok := props["base_url"]; !ok {
		t.Errorf("schema missing base_url field: %#v", tt.Schema)
	}
	key, _ := props["api_key"].(map[string]interface{})
	if key["secret"] != true {
		t.Errorf("schema api_key must be secret: %#v", tt.Schema)
	}
	req, _ := tt.Schema["required"].([]interface{})
	if len(req) != 2 || req[0] != "base_url" || req[1] != "api_key" {
		t.Errorf("required = %#v, want [base_url api_key]", req)
	}

	// An instance of that type now resolves the way the gateway does
	// (JOIN tool_types ON name = connector.tool_type_name).
	inst := database.ToolInstance{ToolTypeID: tt.ID, Name: "Billing prod", LogicalName: "billing-prod", Enabled: true,
		Settings: database.JSONB{"base_url": "https://billing", "api_key": "k"}}
	if err := db.Create(&inst).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}
	var got database.ToolInstance
	err := db.Preload("ToolType").
		Joins("JOIN tool_types ON tool_types.id = tool_instances.tool_type_id").
		Where("tool_types.name = ? AND tool_instances.enabled = ?", c.ToolTypeName, true).
		First(&got).Error
	if err != nil || got.ID != inst.ID {
		t.Fatalf("gateway-style lookup failed: %v (got %+v)", err, got)
	}
}

func TestHTTPConnector_CreateAdoptsExistingToolType(t *testing.T) {
	db := setupHTTPConnectorTestDB(t)
	svc := &HTTPConnectorService{db: db}
	if err := db.Create(&database.ToolType{Name: "internal-billing", Description: "old"}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := svc.CreateHTTPConnector(validHTTPConnector()); err != nil {
		t.Fatalf("create: %v", err)
	}
	var count int64
	db.Model(&database.ToolType{}).Where("name = ?", "internal-billing").Count(&count)
	if count != 1 {
		t.Fatalf("tool types named internal-billing = %d, want 1", count)
	}
	if tt := findToolType(t, db, "internal-billing"); tt.Description != "Internal billing API" {
		t.Errorf("description = %q, want refreshed from connector", tt.Description)
	}
}

func TestHTTPConnector_RenameMovesToolTypeAndInstances(t *testing.T) {
	db := setupHTTPConnectorTestDB(t)
	svc := &HTTPConnectorService{db: db}
	c, err := svc.CreateHTTPConnector(validHTTPConnector())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	tt := findToolType(t, db, "internal-billing")
	inst := database.ToolInstance{ToolTypeID: tt.ID, Name: "Billing", LogicalName: "billing", Enabled: true}
	if err := db.Create(&inst).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}

	if _, err := svc.UpdateHTTPConnector(c.ID, map[string]interface{}{"tool_type_name": "billing-v2", "description": "Billing v2"}); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if findToolType(t, db, "internal-billing") != nil {
		t.Error("old tool type should be renamed away")
	}
	renamed := findToolType(t, db, "billing-v2")
	if renamed == nil || renamed.ID != tt.ID || renamed.Description != "Billing v2" {
		t.Fatalf("renamed tool type = %+v, want same row with new name/description", renamed)
	}
}

func TestHTTPConnector_DeleteRemovesUnusedToolTypeOnly(t *testing.T) {
	db := setupHTTPConnectorTestDB(t)
	svc := &HTTPConnectorService{db: db}

	unused, err := svc.CreateHTTPConnector(validHTTPConnector())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.DeleteHTTPConnector(unused.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if findToolType(t, db, "internal-billing") != nil {
		t.Error("unused tool type must be deleted with its connector")
	}

	used := validHTTPConnector()
	used.ToolTypeName = "used-api"
	if _, err := svc.CreateHTTPConnector(used); err != nil {
		t.Fatalf("create: %v", err)
	}
	tt := findToolType(t, db, "used-api")
	if err := db.Create(&database.ToolInstance{ToolTypeID: tt.ID, Name: "Used", LogicalName: "used"}).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}
	if err := svc.DeleteHTTPConnector(used.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if findToolType(t, db, "used-api") == nil {
		t.Error("tool type with instances must be kept")
	}

	if err := svc.DeleteHTTPConnector(9999); err == nil {
		t.Error("deleting a missing connector must fail")
	}
}

func TestHTTPConnector_EnsureConnectorToolTypesBackfills(t *testing.T) {
	db := setupHTTPConnectorTestDB(t)
	svc := &HTTPConnectorService{db: db}
	// A connector row written before tool types were synced.
	c := validHTTPConnector()
	if err := db.Create(c).Error; err != nil {
		t.Fatalf("seed connector: %v", err)
	}
	if findToolType(t, db, c.ToolTypeName) != nil {
		t.Fatal("precondition: no tool type yet")
	}

	if err := svc.EnsureConnectorToolTypes(); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if findToolType(t, db, c.ToolTypeName) == nil {
		t.Fatal("backfill must create the tool type")
	}
	// Idempotent.
	if err := svc.EnsureConnectorToolTypes(); err != nil {
		t.Fatalf("second backfill: %v", err)
	}
}

func TestHTTPConnector_RejectsBuiltInSeededNamespaces(t *testing.T) {
	db := setupHTTPConnectorTestDB(t)
	svc := &HTTPConnectorService{db: db}
	for _, name := range []string{"jira", "incidents", "proposals"} {
		c := validHTTPConnector()
		c.ToolTypeName = name
		if _, err := svc.CreateHTTPConnector(c); err == nil {
			t.Errorf("connector %q must be rejected as a built-in namespace", name)
		}
	}
}

func TestConnectorSettingsSchema_AuthMethods(t *testing.T) {
	tests := []struct {
		auth     database.JSONB
		wantKeys []string
	}{
		{nil, []string{"base_url"}},
		{database.JSONB{"method": "bearer_token"}, []string{"base_url", "token"}},
		{database.JSONB{"method": "basic_auth"}, []string{"base_url", "username", "password"}},
	}
	for _, tt := range tests {
		c := validHTTPConnector()
		c.AuthConfig = tt.auth
		props, _ := connectorSettingsSchema(c)["properties"].(map[string]interface{})
		if len(props) != len(tt.wantKeys) {
			t.Errorf("auth %v: properties = %v, want keys %v", tt.auth, props, tt.wantKeys)
		}
		for _, k := range tt.wantKeys {
			if _, ok := props[k]; !ok {
				t.Errorf("auth %v: missing property %q", tt.auth, k)
			}
		}
	}
}
