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

func TestGetToolSchemas_ContainsPostgreSQL(t *testing.T) {
	schemas := GetToolSchemas()

	if _, ok := schemas["postgresql"]; !ok {
		t.Fatal("postgresql schema not found in GetToolSchemas()")
	}
}

func TestGetToolSchema_PostgreSQL(t *testing.T) {
	schema, ok := GetToolSchema("postgresql")
	if !ok {
		t.Fatal("postgresql schema not found")
	}

	if schema.Name != "postgresql" {
		t.Errorf("expected name 'postgresql', got %q", schema.Name)
	}

	if schema.Version != "1.0.0" {
		t.Errorf("expected version '1.0.0', got %q", schema.Version)
	}

	if len(schema.Functions) != 10 {
		t.Errorf("expected 10 functions, got %d", len(schema.Functions))
	}
}

func TestPostgreSQLSchema_RequiredFields(t *testing.T) {
	schema, _ := GetToolSchema("postgresql")

	expectedRequired := []string{"pg_host", "pg_database", "pg_username", "pg_password"}
	if len(schema.SettingsSchema.Required) != len(expectedRequired) {
		t.Fatalf("expected %d required fields, got %d", len(expectedRequired), len(schema.SettingsSchema.Required))
	}
	for i, field := range expectedRequired {
		if schema.SettingsSchema.Required[i] != field {
			t.Errorf("expected required[%d] = %q, got %q", i, field, schema.SettingsSchema.Required[i])
		}
	}
}

func TestPostgreSQLSchema_Settings(t *testing.T) {
	schema, _ := GetToolSchema("postgresql")
	props := schema.SettingsSchema.Properties

	expectedFields := []string{"pg_host", "pg_port", "pg_database", "pg_username", "pg_password", "pg_ssl_mode", "pg_timeout"}
	for _, field := range expectedFields {
		if _, ok := props[field]; !ok {
			t.Errorf("missing settings field: %s", field)
		}
	}
}

func TestPostgreSQLSchema_SecretFields(t *testing.T) {
	schema, _ := GetToolSchema("postgresql")
	props := schema.SettingsSchema.Properties

	if !props["pg_password"].Secret {
		t.Error("expected pg_password to be marked as secret")
	}
}

func TestPostgreSQLSchema_AdvancedFields(t *testing.T) {
	schema, _ := GetToolSchema("postgresql")
	props := schema.SettingsSchema.Properties

	advancedFields := []string{"pg_ssl_mode", "pg_timeout"}
	for _, field := range advancedFields {
		if !props[field].Advanced {
			t.Errorf("expected %s to be marked as advanced", field)
		}
	}
}

func TestPostgreSQLSchema_SSLModeEnum(t *testing.T) {
	schema, _ := GetToolSchema("postgresql")
	sslMode := schema.SettingsSchema.Properties["pg_ssl_mode"]

	expectedEnum := []string{"disable", "require", "verify-ca", "verify-full"}
	if len(sslMode.Enum) != len(expectedEnum) {
		t.Fatalf("expected %d enum values, got %d", len(expectedEnum), len(sslMode.Enum))
	}
	for i, v := range expectedEnum {
		if sslMode.Enum[i] != v {
			t.Errorf("expected enum[%d] = %q, got %q", i, v, sslMode.Enum[i])
		}
	}

	if sslMode.Default != "require" {
		t.Errorf("expected default 'require', got %v", sslMode.Default)
	}
}

func TestPostgreSQLSchema_Defaults(t *testing.T) {
	schema, _ := GetToolSchema("postgresql")
	props := schema.SettingsSchema.Properties

	if props["pg_port"].Default != 5432 {
		t.Errorf("expected pg_port default 5432, got %v", props["pg_port"].Default)
	}

	if props["pg_timeout"].Default != 30 {
		t.Errorf("expected pg_timeout default 30, got %v", props["pg_timeout"].Default)
	}
}

func TestPostgreSQLSchema_TimeoutBounds(t *testing.T) {
	schema, _ := GetToolSchema("postgresql")
	timeout := schema.SettingsSchema.Properties["pg_timeout"]

	if timeout.Minimum == nil || *timeout.Minimum != 5 {
		t.Error("expected pg_timeout minimum 5")
	}
	if timeout.Maximum == nil || *timeout.Maximum != 300 {
		t.Error("expected pg_timeout maximum 300")
	}
}

func TestPostgreSQLSchema_Functions(t *testing.T) {
	schema, _ := GetToolSchema("postgresql")

	expectedFunctions := []string{
		"execute_query", "list_tables", "describe_table", "get_indexes",
		"get_table_stats", "explain_query", "get_active_queries", "get_locks",
		"get_replication_status", "get_database_stats",
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

func TestGetToolSchemas_ContainsClickHouse(t *testing.T) {
	schemas := GetToolSchemas()

	if _, ok := schemas["clickhouse"]; !ok {
		t.Fatal("clickhouse schema not found in GetToolSchemas()")
	}
}

func TestGetToolSchema_ClickHouse(t *testing.T) {
	schema, ok := GetToolSchema("clickhouse")
	if !ok {
		t.Fatal("clickhouse schema not found")
	}

	if schema.Name != "clickhouse" {
		t.Errorf("expected name 'clickhouse', got %q", schema.Name)
	}

	if schema.Version != "1.0.0" {
		t.Errorf("expected version '1.0.0', got %q", schema.Version)
	}

	if len(schema.Functions) != 10 {
		t.Errorf("expected 10 functions, got %d", len(schema.Functions))
	}
}

func TestClickHouseSchema_RequiredFields(t *testing.T) {
	schema, _ := GetToolSchema("clickhouse")

	expectedRequired := []string{"ch_host", "ch_database", "ch_username", "ch_password"}
	if len(schema.SettingsSchema.Required) != len(expectedRequired) {
		t.Fatalf("expected %d required fields, got %d", len(expectedRequired), len(schema.SettingsSchema.Required))
	}
	for i, field := range expectedRequired {
		if schema.SettingsSchema.Required[i] != field {
			t.Errorf("expected required[%d] = %q, got %q", i, field, schema.SettingsSchema.Required[i])
		}
	}
}

func TestClickHouseSchema_Settings(t *testing.T) {
	schema, _ := GetToolSchema("clickhouse")
	props := schema.SettingsSchema.Properties

	expectedFields := []string{"ch_host", "ch_port", "ch_database", "ch_username", "ch_password", "ch_ssl_enabled", "ch_timeout"}
	for _, field := range expectedFields {
		if _, ok := props[field]; !ok {
			t.Errorf("missing settings field: %s", field)
		}
	}
}

func TestClickHouseSchema_SecretFields(t *testing.T) {
	schema, _ := GetToolSchema("clickhouse")
	props := schema.SettingsSchema.Properties

	if !props["ch_password"].Secret {
		t.Error("expected ch_password to be marked as secret")
	}
}

func TestClickHouseSchema_AdvancedFields(t *testing.T) {
	schema, _ := GetToolSchema("clickhouse")
	props := schema.SettingsSchema.Properties

	advancedFields := []string{"ch_ssl_enabled", "ch_timeout"}
	for _, field := range advancedFields {
		if !props[field].Advanced {
			t.Errorf("expected %s to be marked as advanced", field)
		}
	}
}

func TestClickHouseSchema_Defaults(t *testing.T) {
	schema, _ := GetToolSchema("clickhouse")
	props := schema.SettingsSchema.Properties

	if props["ch_port"].Default != 8123 {
		t.Errorf("expected ch_port default 8123, got %v", props["ch_port"].Default)
	}

	if props["ch_ssl_enabled"].Default != false {
		t.Errorf("expected ch_ssl_enabled default false, got %v", props["ch_ssl_enabled"].Default)
	}

	if props["ch_timeout"].Default != 30 {
		t.Errorf("expected ch_timeout default 30, got %v", props["ch_timeout"].Default)
	}
}

func TestClickHouseSchema_TimeoutBounds(t *testing.T) {
	schema, _ := GetToolSchema("clickhouse")
	timeout := schema.SettingsSchema.Properties["ch_timeout"]

	if timeout.Minimum == nil || *timeout.Minimum != 5 {
		t.Error("expected ch_timeout minimum 5")
	}
	if timeout.Maximum == nil || *timeout.Maximum != 300 {
		t.Error("expected ch_timeout maximum 300")
	}
}

func TestClickHouseSchema_PortBounds(t *testing.T) {
	schema, _ := GetToolSchema("clickhouse")
	port := schema.SettingsSchema.Properties["ch_port"]

	if port.Minimum == nil || *port.Minimum != 1 {
		t.Error("expected ch_port minimum 1")
	}
	if port.Maximum == nil || *port.Maximum != 65535 {
		t.Error("expected ch_port maximum 65535")
	}
}

func TestClickHouseSchema_Functions(t *testing.T) {
	schema, _ := GetToolSchema("clickhouse")

	expectedFunctions := []string{
		"execute_query", "show_databases", "show_tables", "describe_table",
		"get_query_log", "get_running_queries", "get_merges",
		"get_replication_status", "get_parts_info", "get_cluster_info",
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

	expected := []string{"ssh", "zabbix", "victoria_metrics", "catchpoint", "postgresql", "grafana", "clickhouse", "pagerduty", "netbox", "kubernetes"}
	for _, name := range expected {
		if _, ok := schemas[name]; !ok {
			t.Errorf("missing schema: %s", name)
		}
	}
}

func TestGetToolSchemas_ContainsPagerDuty(t *testing.T) {
	schemas := GetToolSchemas()

	if _, ok := schemas["pagerduty"]; !ok {
		t.Fatal("pagerduty schema not found in GetToolSchemas()")
	}
}

func TestGetToolSchema_PagerDuty(t *testing.T) {
	schema, ok := GetToolSchema("pagerduty")
	if !ok {
		t.Fatal("pagerduty schema not found")
	}

	if schema.Name != "pagerduty" {
		t.Errorf("expected name 'pagerduty', got %q", schema.Name)
	}

	if schema.Version != "1.0.0" {
		t.Errorf("expected version '1.0.0', got %q", schema.Version)
	}
}

func TestPagerDutySchema_RequiredFields(t *testing.T) {
	schema, _ := GetToolSchema("pagerduty")

	if len(schema.SettingsSchema.Required) != 1 || schema.SettingsSchema.Required[0] != "pagerduty_api_token" {
		t.Errorf("expected required field 'pagerduty_api_token', got %v", schema.SettingsSchema.Required)
	}
}

func TestPagerDutySchema_Settings(t *testing.T) {
	schema, _ := GetToolSchema("pagerduty")
	props := schema.SettingsSchema.Properties

	expectedFields := []string{"pagerduty_api_token", "pagerduty_url", "pagerduty_verify_ssl", "pagerduty_timeout"}
	for _, field := range expectedFields {
		if _, ok := props[field]; !ok {
			t.Errorf("missing settings field: %s", field)
		}
	}
}

func TestPagerDutySchema_SecretFields(t *testing.T) {
	schema, _ := GetToolSchema("pagerduty")
	props := schema.SettingsSchema.Properties

	if !props["pagerduty_api_token"].Secret {
		t.Error("expected pagerduty_api_token to be marked as secret")
	}
}

func TestPagerDutySchema_AdvancedFields(t *testing.T) {
	schema, _ := GetToolSchema("pagerduty")
	props := schema.SettingsSchema.Properties

	advancedFields := []string{"pagerduty_verify_ssl", "pagerduty_timeout"}
	for _, field := range advancedFields {
		if !props[field].Advanced {
			t.Errorf("expected %s to be marked as advanced", field)
		}
	}
}

func TestPagerDutySchema_Defaults(t *testing.T) {
	schema, _ := GetToolSchema("pagerduty")
	props := schema.SettingsSchema.Properties

	if props["pagerduty_url"].Default != "https://api.pagerduty.com" {
		t.Errorf("expected pagerduty_url default, got %v", props["pagerduty_url"].Default)
	}

	if props["pagerduty_verify_ssl"].Default != true {
		t.Errorf("expected pagerduty_verify_ssl default true, got %v", props["pagerduty_verify_ssl"].Default)
	}

	if props["pagerduty_timeout"].Default != 30 {
		t.Errorf("expected pagerduty_timeout default 30, got %v", props["pagerduty_timeout"].Default)
	}
}

func TestPagerDutySchema_TimeoutBounds(t *testing.T) {
	schema, _ := GetToolSchema("pagerduty")
	timeout := schema.SettingsSchema.Properties["pagerduty_timeout"]

	if timeout.Minimum == nil || *timeout.Minimum != 5 {
		t.Error("expected pagerduty_timeout minimum 5")
	}
	if timeout.Maximum == nil || *timeout.Maximum != 300 {
		t.Error("expected pagerduty_timeout maximum 300")
	}
}

func TestPagerDutySchema_Functions(t *testing.T) {
	schema, _ := GetToolSchema("pagerduty")

	expectedFunctions := []string{
		"get_incidents", "get_incident", "get_incident_notes", "get_incident_alerts",
		"get_services", "get_on_calls", "get_escalation_policies", "list_recent_changes",
		"acknowledge_incident", "resolve_incident", "reassign_incident", "add_incident_note",
		"send_event",
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

func TestGetToolSchemas_ContainsNetBox(t *testing.T) {
	schemas := GetToolSchemas()

	if _, ok := schemas["netbox"]; !ok {
		t.Fatal("netbox schema not found in GetToolSchemas()")
	}
}

func TestGetToolSchema_NetBox(t *testing.T) {
	schema, ok := GetToolSchema("netbox")
	if !ok {
		t.Fatal("netbox schema not found")
	}

	if schema.Name != "netbox" {
		t.Errorf("expected name 'netbox', got %q", schema.Name)
	}

	if schema.Version != "1.0.0" {
		t.Errorf("expected version '1.0.0', got %q", schema.Version)
	}
}

func TestNetBoxSchema_RequiredFields(t *testing.T) {
	schema, _ := GetToolSchema("netbox")

	expectedRequired := []string{"netbox_url", "netbox_api_token"}
	if len(schema.SettingsSchema.Required) != len(expectedRequired) {
		t.Fatalf("expected %d required fields, got %d", len(expectedRequired), len(schema.SettingsSchema.Required))
	}
	for i, field := range expectedRequired {
		if schema.SettingsSchema.Required[i] != field {
			t.Errorf("expected required[%d] = %q, got %q", i, field, schema.SettingsSchema.Required[i])
		}
	}
}

func TestNetBoxSchema_Settings(t *testing.T) {
	schema, _ := GetToolSchema("netbox")
	props := schema.SettingsSchema.Properties

	expectedFields := []string{"netbox_url", "netbox_api_token", "netbox_verify_ssl", "netbox_timeout"}
	for _, field := range expectedFields {
		if _, ok := props[field]; !ok {
			t.Errorf("missing settings field: %s", field)
		}
	}
}

func TestNetBoxSchema_SecretFields(t *testing.T) {
	schema, _ := GetToolSchema("netbox")
	props := schema.SettingsSchema.Properties

	if !props["netbox_api_token"].Secret {
		t.Error("expected netbox_api_token to be marked as secret")
	}
}

func TestNetBoxSchema_AdvancedFields(t *testing.T) {
	schema, _ := GetToolSchema("netbox")
	props := schema.SettingsSchema.Properties

	advancedFields := []string{"netbox_verify_ssl", "netbox_timeout"}
	for _, field := range advancedFields {
		if !props[field].Advanced {
			t.Errorf("expected %s to be marked as advanced", field)
		}
	}
}

func TestNetBoxSchema_Defaults(t *testing.T) {
	schema, _ := GetToolSchema("netbox")
	props := schema.SettingsSchema.Properties

	if props["netbox_verify_ssl"].Default != true {
		t.Errorf("expected netbox_verify_ssl default true, got %v", props["netbox_verify_ssl"].Default)
	}

	if props["netbox_timeout"].Default != 30 {
		t.Errorf("expected netbox_timeout default 30, got %v", props["netbox_timeout"].Default)
	}
}

func TestNetBoxSchema_TimeoutBounds(t *testing.T) {
	schema, _ := GetToolSchema("netbox")
	timeout := schema.SettingsSchema.Properties["netbox_timeout"]

	if timeout.Minimum == nil || *timeout.Minimum != 5 {
		t.Error("expected netbox_timeout minimum 5")
	}
	if timeout.Maximum == nil || *timeout.Maximum != 300 {
		t.Error("expected netbox_timeout maximum 300")
	}
}

func TestNetBoxSchema_Functions(t *testing.T) {
	schema, _ := GetToolSchema("netbox")

	expectedFunctions := []string{
		"get_devices", "get_device", "get_interfaces", "get_sites", "get_racks", "get_cables", "get_device_types",
		"get_ip_addresses", "get_prefixes", "get_vlans", "get_vrfs",
		"get_circuits", "get_providers",
		"get_virtual_machines", "get_clusters", "get_vm_interfaces",
		"get_tenants", "get_tenant_groups",
		"api_request",
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

func TestNetBoxSchema_FunctionCount(t *testing.T) {
	schema, _ := GetToolSchema("netbox")

	if len(schema.Functions) != 19 {
		t.Errorf("expected 19 functions (all NetBox tools), got %d", len(schema.Functions))
	}
}

// Kubernetes schema tests

func TestGetToolSchemas_ContainsKubernetes(t *testing.T) {
	schemas := GetToolSchemas()

	if _, ok := schemas["kubernetes"]; !ok {
		t.Fatal("kubernetes schema not found in GetToolSchemas()")
	}
}

func TestGetToolSchema_Kubernetes(t *testing.T) {
	schema, ok := GetToolSchema("kubernetes")
	if !ok {
		t.Fatal("kubernetes schema not found")
	}

	if schema.Name != "kubernetes" {
		t.Errorf("expected name 'kubernetes', got %q", schema.Name)
	}

	if schema.Version != "1.0.0" {
		t.Errorf("expected version '1.0.0', got %q", schema.Version)
	}
}

func TestK8sSchema_RequiredFields(t *testing.T) {
	schema, _ := GetToolSchema("kubernetes")

	expectedRequired := []string{"k8s_url", "k8s_token"}
	if len(schema.SettingsSchema.Required) != len(expectedRequired) {
		t.Fatalf("expected %d required fields, got %d", len(expectedRequired), len(schema.SettingsSchema.Required))
	}
	for i, field := range expectedRequired {
		if schema.SettingsSchema.Required[i] != field {
			t.Errorf("expected required[%d] = %q, got %q", i, field, schema.SettingsSchema.Required[i])
		}
	}
}

func TestK8sSchema_Settings(t *testing.T) {
	schema, _ := GetToolSchema("kubernetes")
	props := schema.SettingsSchema.Properties

	expectedFields := []string{"k8s_url", "k8s_token", "k8s_ca_cert", "k8s_verify_ssl", "k8s_timeout"}
	for _, field := range expectedFields {
		if _, ok := props[field]; !ok {
			t.Errorf("missing settings field: %s", field)
		}
	}
}

func TestK8sSchema_SecretFields(t *testing.T) {
	schema, _ := GetToolSchema("kubernetes")
	props := schema.SettingsSchema.Properties

	if !props["k8s_token"].Secret {
		t.Error("expected k8s_token to be marked as secret")
	}
	if !props["k8s_ca_cert"].Secret {
		t.Error("expected k8s_ca_cert to be marked as secret")
	}
}

func TestK8sSchema_AdvancedFields(t *testing.T) {
	schema, _ := GetToolSchema("kubernetes")
	props := schema.SettingsSchema.Properties

	advancedFields := []string{"k8s_ca_cert", "k8s_verify_ssl", "k8s_timeout"}
	for _, field := range advancedFields {
		if !props[field].Advanced {
			t.Errorf("expected %s to be marked as advanced", field)
		}
	}
}

func TestK8sSchema_Defaults(t *testing.T) {
	schema, _ := GetToolSchema("kubernetes")
	props := schema.SettingsSchema.Properties

	if props["k8s_verify_ssl"].Default != true {
		t.Errorf("expected k8s_verify_ssl default true, got %v", props["k8s_verify_ssl"].Default)
	}

	if props["k8s_timeout"].Default != 30 {
		t.Errorf("expected k8s_timeout default 30, got %v", props["k8s_timeout"].Default)
	}
}

func TestK8sSchema_TimeoutBounds(t *testing.T) {
	schema, _ := GetToolSchema("kubernetes")
	timeout := schema.SettingsSchema.Properties["k8s_timeout"]

	if timeout.Minimum == nil || *timeout.Minimum != 5 {
		t.Error("expected k8s_timeout minimum 5")
	}
	if timeout.Maximum == nil || *timeout.Maximum != 300 {
		t.Error("expected k8s_timeout maximum 300")
	}
}

func TestK8sSchema_Functions(t *testing.T) {
	schema, _ := GetToolSchema("kubernetes")

	expectedFunctions := []string{
		"get_namespaces",
		"get_pods", "get_pod_detail", "get_pod_logs",
		"get_events",
		"get_deployments", "get_deployment_detail",
		"get_statefulsets", "get_daemonsets",
		"get_jobs", "get_cronjobs",
		"get_nodes", "get_node_detail",
		"get_services", "get_configmaps", "get_ingresses",
		"api_request",
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

func TestK8sSchema_FunctionCount(t *testing.T) {
	schema, _ := GetToolSchema("kubernetes")

	if len(schema.Functions) != 17 {
		t.Errorf("expected 17 functions (all Kubernetes tools), got %d", len(schema.Functions))
	}
}
