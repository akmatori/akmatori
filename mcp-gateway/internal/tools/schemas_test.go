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

func TestGetToolSchemas_ContainsCatchpoint(t *testing.T) {
	schemas := GetToolSchemas()

	if _, ok := schemas["catchpoint"]; !ok {
		t.Fatal("catchpoint schema not found in GetToolSchemas()")
	}
}

func TestGetToolSchema_Catchpoint(t *testing.T) {
	schema, ok := GetToolSchema("catchpoint")
	if !ok {
		t.Fatal("catchpoint schema not found")
	}

	if schema.Name != "catchpoint" {
		t.Errorf("expected name 'catchpoint', got %q", schema.Name)
	}

	if schema.Version != "1.0.0" {
		t.Errorf("expected version '1.0.0', got %q", schema.Version)
	}
}

func TestCatchpointSchema_RequiredFields(t *testing.T) {
	schema, _ := GetToolSchema("catchpoint")

	if len(schema.SettingsSchema.Required) != 1 || schema.SettingsSchema.Required[0] != "catchpoint_api_token" {
		t.Errorf("expected required field 'catchpoint_api_token', got %v", schema.SettingsSchema.Required)
	}
}

func TestCatchpointSchema_Settings(t *testing.T) {
	schema, _ := GetToolSchema("catchpoint")
	props := schema.SettingsSchema.Properties

	expectedFields := []string{"catchpoint_url", "catchpoint_api_token", "catchpoint_verify_ssl", "catchpoint_timeout"}
	for _, field := range expectedFields {
		if _, ok := props[field]; !ok {
			t.Errorf("missing settings field: %s", field)
		}
	}
}

func TestCatchpointSchema_SecretFields(t *testing.T) {
	schema, _ := GetToolSchema("catchpoint")
	props := schema.SettingsSchema.Properties

	if !props["catchpoint_api_token"].Secret {
		t.Error("expected catchpoint_api_token to be marked as secret")
	}
}

func TestCatchpointSchema_AdvancedFields(t *testing.T) {
	schema, _ := GetToolSchema("catchpoint")
	props := schema.SettingsSchema.Properties

	advancedFields := []string{"catchpoint_verify_ssl", "catchpoint_timeout"}
	for _, field := range advancedFields {
		if !props[field].Advanced {
			t.Errorf("expected %s to be marked as advanced", field)
		}
	}
}

func TestCatchpointSchema_Defaults(t *testing.T) {
	schema, _ := GetToolSchema("catchpoint")
	props := schema.SettingsSchema.Properties

	if props["catchpoint_url"].Default != "https://io.catchpoint.com/api" {
		t.Errorf("expected catchpoint_url default, got %v", props["catchpoint_url"].Default)
	}

	if props["catchpoint_verify_ssl"].Default != true {
		t.Errorf("expected catchpoint_verify_ssl default true, got %v", props["catchpoint_verify_ssl"].Default)
	}

	if props["catchpoint_timeout"].Default != 30 {
		t.Errorf("expected catchpoint_timeout default 30, got %v", props["catchpoint_timeout"].Default)
	}
}

func TestCatchpointSchema_Functions(t *testing.T) {
	schema, _ := GetToolSchema("catchpoint")

	expectedFunctions := []string{
		"get_alerts", "get_alert_details", "get_test_performance", "get_test_performance_raw",
		"get_tests", "get_test_details", "get_test_errors", "get_internet_outages",
		"get_nodes", "get_node_alerts", "acknowledge_alerts", "run_instant_test",
	}
	if len(schema.Functions) != len(expectedFunctions) {
		t.Fatalf("expected %d functions, got %d", len(expectedFunctions), len(schema.Functions))
	}
	for i, name := range expectedFunctions {
		if schema.Functions[i].Name != name {
			t.Errorf("expected function[%d] = %q, got %q", i, name, schema.Functions[i].Name)
		}
	}
}

func TestCatchpointSchema_TimeoutBounds(t *testing.T) {
	schema, _ := GetToolSchema("catchpoint")
	timeout := schema.SettingsSchema.Properties["catchpoint_timeout"]

	if timeout.Minimum == nil || *timeout.Minimum != 5 {
		t.Error("expected catchpoint_timeout minimum 5")
	}
	if timeout.Maximum == nil || *timeout.Maximum != 300 {
		t.Error("expected catchpoint_timeout maximum 300")
	}
}

func TestGetToolSchemas_ContainsGrafana(t *testing.T) {
	schemas := GetToolSchemas()

	if _, ok := schemas["grafana"]; !ok {
		t.Fatal("grafana schema not found in GetToolSchemas()")
	}
}

func TestGetToolSchema_Grafana(t *testing.T) {
	schema, ok := GetToolSchema("grafana")
	if !ok {
		t.Fatal("grafana schema not found")
	}

	if schema.Name != "grafana" {
		t.Errorf("expected name 'grafana', got %q", schema.Name)
	}

	if schema.Version != "1.0.0" {
		t.Errorf("expected version '1.0.0', got %q", schema.Version)
	}
}

func TestGrafanaSchema_RequiredFields(t *testing.T) {
	schema, _ := GetToolSchema("grafana")

	expected := []string{"grafana_url", "grafana_api_token"}
	if len(schema.SettingsSchema.Required) != len(expected) {
		t.Fatalf("expected %d required fields, got %d", len(expected), len(schema.SettingsSchema.Required))
	}
	for i, name := range expected {
		if schema.SettingsSchema.Required[i] != name {
			t.Errorf("expected required[%d] = %q, got %q", i, name, schema.SettingsSchema.Required[i])
		}
	}
}

func TestGrafanaSchema_Settings(t *testing.T) {
	schema, _ := GetToolSchema("grafana")
	props := schema.SettingsSchema.Properties

	expectedFields := []string{"grafana_url", "grafana_api_token", "grafana_verify_ssl", "grafana_timeout"}
	for _, field := range expectedFields {
		if _, ok := props[field]; !ok {
			t.Errorf("missing settings field: %s", field)
		}
	}
}

func TestGrafanaSchema_SecretFields(t *testing.T) {
	schema, _ := GetToolSchema("grafana")
	props := schema.SettingsSchema.Properties

	if !props["grafana_api_token"].Secret {
		t.Error("expected grafana_api_token to be marked as secret")
	}
}

func TestGrafanaSchema_AdvancedFields(t *testing.T) {
	schema, _ := GetToolSchema("grafana")
	props := schema.SettingsSchema.Properties

	advancedFields := []string{"grafana_verify_ssl", "grafana_timeout"}
	for _, field := range advancedFields {
		if !props[field].Advanced {
			t.Errorf("expected %s to be marked as advanced", field)
		}
	}
}

func TestGrafanaSchema_Defaults(t *testing.T) {
	schema, _ := GetToolSchema("grafana")
	props := schema.SettingsSchema.Properties

	if props["grafana_verify_ssl"].Default != true {
		t.Errorf("expected grafana_verify_ssl default true, got %v", props["grafana_verify_ssl"].Default)
	}

	if props["grafana_timeout"].Default != 30 {
		t.Errorf("expected grafana_timeout default 30, got %v", props["grafana_timeout"].Default)
	}

}

func TestGrafanaSchema_TimeoutBounds(t *testing.T) {
	schema, _ := GetToolSchema("grafana")
	timeout := schema.SettingsSchema.Properties["grafana_timeout"]

	if timeout.Minimum == nil || *timeout.Minimum != 5 {
		t.Error("expected grafana_timeout minimum 5")
	}
	if timeout.Maximum == nil || *timeout.Maximum != 300 {
		t.Error("expected grafana_timeout maximum 300")
	}
}

func TestGrafanaSchema_Functions(t *testing.T) {
	schema, _ := GetToolSchema("grafana")

	expectedFunctions := []string{
		"search_dashboards", "get_dashboard", "get_dashboard_panels",
		"get_alert_rules", "get_alert_instances", "get_alert_rule", "silence_alert",
		"list_data_sources", "query_data_source", "query_prometheus", "query_loki",
		"create_annotation", "get_annotations",
	}
	if len(schema.Functions) != len(expectedFunctions) {
		t.Fatalf("expected %d functions, got %d", len(expectedFunctions), len(schema.Functions))
	}
	for i, name := range expectedFunctions {
		if schema.Functions[i].Name != name {
			t.Errorf("expected function[%d] = %q, got %q", i, name, schema.Functions[i].Name)
		}
	}
}

func TestGetToolSchemas_AllPresent(t *testing.T) {
	schemas := GetToolSchemas()

	expected := []string{"ssh", "zabbix", "victoria_metrics", "grafana", "catchpoint"}
	for _, name := range expected {
		if _, ok := schemas[name]; !ok {
			t.Errorf("missing schema: %s", name)
		}
	}
}
