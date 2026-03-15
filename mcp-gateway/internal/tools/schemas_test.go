package tools

import (
	"testing"
)

func TestGetToolSchemas_ContainsVictoriaMetrics(t *testing.T) {
	schemas := GetToolSchemas()

	if _, ok := schemas["victoria_metrics"]; !ok {
		t.Fatal("victoria_metrics schema not found in GetToolSchemas()")
	}
}

func TestGetToolSchema_VictoriaMetrics(t *testing.T) {
	schema, ok := GetToolSchema("victoria_metrics")
	if !ok {
		t.Fatal("victoria_metrics schema not found")
	}

	if schema.Name != "victoria_metrics" {
		t.Errorf("expected name 'victoria_metrics', got %q", schema.Name)
	}

	if schema.Version != "1.0.0" {
		t.Errorf("expected version '1.0.0', got %q", schema.Version)
	}
}

func TestVictoriaMetricsSchema_RequiredFields(t *testing.T) {
	schema, _ := GetToolSchema("victoria_metrics")

	if len(schema.SettingsSchema.Required) != 1 || schema.SettingsSchema.Required[0] != "vm_url" {
		t.Errorf("expected required field 'vm_url', got %v", schema.SettingsSchema.Required)
	}
}

func TestVictoriaMetricsSchema_Settings(t *testing.T) {
	schema, _ := GetToolSchema("victoria_metrics")
	props := schema.SettingsSchema.Properties

	expectedFields := []string{"vm_url", "vm_auth_method", "vm_bearer_token", "vm_username", "vm_password", "vm_verify_ssl", "vm_timeout"}
	for _, field := range expectedFields {
		if _, ok := props[field]; !ok {
			t.Errorf("missing settings field: %s", field)
		}
	}
}

func TestVictoriaMetricsSchema_AuthMethodEnum(t *testing.T) {
	schema, _ := GetToolSchema("victoria_metrics")
	authMethod := schema.SettingsSchema.Properties["vm_auth_method"]

	expectedEnum := []string{"none", "bearer_token", "basic_auth"}
	if len(authMethod.Enum) != len(expectedEnum) {
		t.Fatalf("expected %d enum values, got %d", len(expectedEnum), len(authMethod.Enum))
	}
	for i, v := range expectedEnum {
		if authMethod.Enum[i] != v {
			t.Errorf("expected enum[%d] = %q, got %q", i, v, authMethod.Enum[i])
		}
	}

	if authMethod.Default != "bearer_token" {
		t.Errorf("expected default 'bearer_token', got %v", authMethod.Default)
	}
}

func TestVictoriaMetricsSchema_SecretFields(t *testing.T) {
	schema, _ := GetToolSchema("victoria_metrics")
	props := schema.SettingsSchema.Properties

	secretFields := []string{"vm_bearer_token", "vm_password"}
	for _, field := range secretFields {
		if !props[field].Secret {
			t.Errorf("expected %s to be marked as secret", field)
		}
	}
}

func TestVictoriaMetricsSchema_AdvancedFields(t *testing.T) {
	schema, _ := GetToolSchema("victoria_metrics")
	props := schema.SettingsSchema.Properties

	advancedFields := []string{"vm_username", "vm_password", "vm_verify_ssl", "vm_timeout"}
	for _, field := range advancedFields {
		if !props[field].Advanced {
			t.Errorf("expected %s to be marked as advanced", field)
		}
	}
}

func TestVictoriaMetricsSchema_Functions(t *testing.T) {
	schema, _ := GetToolSchema("victoria_metrics")

	expectedFunctions := []string{"instant_query", "range_query", "label_values", "series", "api_request"}
	if len(schema.Functions) != len(expectedFunctions) {
		t.Fatalf("expected %d functions, got %d", len(expectedFunctions), len(schema.Functions))
	}
	for i, name := range expectedFunctions {
		if schema.Functions[i].Name != name {
			t.Errorf("expected function[%d] = %q, got %q", i, name, schema.Functions[i].Name)
		}
	}
}

func TestVictoriaMetricsSchema_Defaults(t *testing.T) {
	schema, _ := GetToolSchema("victoria_metrics")
	props := schema.SettingsSchema.Properties

	if props["vm_verify_ssl"].Default != true {
		t.Errorf("expected vm_verify_ssl default true, got %v", props["vm_verify_ssl"].Default)
	}

	if props["vm_timeout"].Default != 30 {
		t.Errorf("expected vm_timeout default 30, got %v", props["vm_timeout"].Default)
	}
}

func TestGetToolSchemas_AllPresent(t *testing.T) {
	schemas := GetToolSchemas()

	expected := []string{"ssh", "zabbix", "victoria_metrics"}
	for _, name := range expected {
		if _, ok := schemas[name]; !ok {
			t.Errorf("missing schema: %s", name)
		}
	}
}
