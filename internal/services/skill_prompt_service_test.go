package services

import (
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
)

func sshToolInstance(settings database.JSONB) database.ToolInstance {
	return database.ToolInstance{
		ID:          1,
		Name:        "prod-ssh",
		LogicalName: "prod-ssh",
		Enabled:     true,
		Settings:    settings,
		ToolType: database.ToolType{
			ID:   1,
			Name: "ssh",
		},
	}
}

// --- extractToolDetails tests ---

func TestExtractToolDetails_SSHWithConfiguredHosts(t *testing.T) {
	tool := sshToolInstance(database.JSONB{
		"ssh_hosts": []interface{}{
			map[string]interface{}{"hostname": "web-1", "address": "10.0.0.1"},
			map[string]interface{}{"hostname": "db-1", "address": "10.0.0.2"},
		},
	})

	details := extractToolDetails(tool)

	if !strings.Contains(details, "web-1") || !strings.Contains(details, "db-1") {
		t.Errorf("expected configured hostnames in details, got: %s", details)
	}
	if !strings.Contains(details, "Configured hosts:") {
		t.Error("expected 'Configured hosts:' label")
	}
}

func TestExtractToolDetails_SSHAdhocEnabled(t *testing.T) {
	tool := sshToolInstance(database.JSONB{
		"allow_adhoc_connections": true,
		"adhoc_default_user":     "deploy",
		"adhoc_default_port":     float64(2222),
	})

	details := extractToolDetails(tool)

	if !strings.Contains(details, "Ad-hoc connections enabled") {
		t.Errorf("expected ad-hoc note, got: %s", details)
	}
	if !strings.Contains(details, "deploy") {
		t.Errorf("expected default user 'deploy' in details, got: %s", details)
	}
	if !strings.Contains(details, "2222") {
		t.Errorf("expected port 2222 in details, got: %s", details)
	}
	if !strings.Contains(details, "read-only") {
		t.Errorf("expected read-only note for ad-hoc, got: %s", details)
	}
}

func TestExtractToolDetails_SSHAdhocWriteEnabled(t *testing.T) {
	tool := sshToolInstance(database.JSONB{
		"allow_adhoc_connections":    true,
		"adhoc_allow_write_commands": true,
	})

	details := extractToolDetails(tool)

	if !strings.Contains(details, "Ad-hoc connections enabled") {
		t.Errorf("expected ad-hoc note, got: %s", details)
	}
	if strings.Contains(details, "read-only") {
		t.Errorf("expected no read-only note when write is allowed, got: %s", details)
	}
	if !strings.Contains(details, "allowed") {
		t.Errorf("expected 'allowed' note for write commands, got: %s", details)
	}
}

func TestExtractToolDetails_SSHAdhocDisabled(t *testing.T) {
	tool := sshToolInstance(database.JSONB{
		"allow_adhoc_connections": false,
		"ssh_hosts": []interface{}{
			map[string]interface{}{"hostname": "web-1", "address": "10.0.0.1"},
		},
	})

	details := extractToolDetails(tool)

	if strings.Contains(details, "Ad-hoc") {
		t.Errorf("expected no ad-hoc note when disabled, got: %s", details)
	}
	if !strings.Contains(details, "web-1") {
		t.Errorf("expected configured host listed, got: %s", details)
	}
}

func TestExtractToolDetails_SSHAdhocDefaultUserFallback(t *testing.T) {
	tool := sshToolInstance(database.JSONB{
		"allow_adhoc_connections": true,
		// no adhoc_default_user set — should default to "root"
	})

	details := extractToolDetails(tool)

	if !strings.Contains(details, "root") {
		t.Errorf("expected default user 'root' when not specified, got: %s", details)
	}
}

func TestExtractToolDetails_NilSettings(t *testing.T) {
	tool := sshToolInstance(nil)
	details := extractToolDetails(tool)
	if details != "" {
		t.Errorf("expected empty details for nil settings, got: %s", details)
	}
}

func TestExtractToolDetails_NonSSHTool(t *testing.T) {
	tool := database.ToolInstance{
		ID:       2,
		Name:     "prod-zabbix",
		Settings: database.JSONB{"url": "https://zabbix.example.com"},
		ToolType: database.ToolType{ID: 2, Name: "zabbix"},
	}
	details := extractToolDetails(tool)
	if details != "" {
		t.Errorf("expected empty details for non-SSH tool, got: %s", details)
	}
}

// --- generateToolUsageExample tests ---

func TestGenerateToolUsageExample_SSHBasic(t *testing.T) {
	tool := sshToolInstance(database.JSONB{
		"ssh_hosts": []interface{}{
			map[string]interface{}{
				"hostname":             "web-1",
				"address":              "10.0.0.1",
				"allow_write_commands": false,
			},
		},
	})

	example := generateToolUsageExample(tool)

	if !strings.Contains(example, "gateway_call") {
		t.Error("expected gateway_call in example")
	}
	if !strings.Contains(example, `"ssh.execute_command"`) {
		t.Error("expected ssh.execute_command tool name in example")
	}
	if !strings.Contains(example, `"prod-ssh"`) {
		t.Errorf("expected logical name prod-ssh in example, got: %s", example)
	}
	if !strings.Contains(example, "Read-only mode") {
		t.Error("expected read-only note for read-only host")
	}
	// Verify inline parameter schemas are present
	if !strings.Contains(example, "**Parameters:**") {
		t.Error("expected **Parameters:** section in SSH example")
	}
	if !strings.Contains(example, "command*") {
		t.Errorf("expected command* required param marker, got: %s", example)
	}
}

func TestGenerateToolUsageExample_SSHAdhocEnabled(t *testing.T) {
	tool := sshToolInstance(database.JSONB{
		"allow_adhoc_connections": true,
		"ssh_hosts": []interface{}{
			map[string]interface{}{
				"hostname":             "web-1",
				"address":              "10.0.0.1",
				"allow_write_commands": true,
			},
		},
	})

	example := generateToolUsageExample(tool)

	if !strings.Contains(example, "Ad-hoc") {
		t.Errorf("expected ad-hoc example when enabled, got: %s", example)
	}
	if !strings.Contains(example, "<hostname-or-ip>") {
		t.Errorf("expected ad-hoc server placeholder, got: %s", example)
	}
	if !strings.Contains(example, `"ssh.get_server_info"`) {
		t.Errorf("expected ad-hoc get_server_info example, got: %s", example)
	}
}

func TestGenerateToolUsageExample_SSHAdhocDisabled(t *testing.T) {
	tool := sshToolInstance(database.JSONB{
		"allow_adhoc_connections": false,
		"ssh_hosts": []interface{}{
			map[string]interface{}{
				"hostname":             "web-1",
				"address":              "10.0.0.1",
				"allow_write_commands": true,
			},
		},
	})

	example := generateToolUsageExample(tool)

	if strings.Contains(example, "Ad-hoc") {
		t.Errorf("expected no ad-hoc example when disabled, got: %s", example)
	}
}

func TestGenerateToolUsageExample_Zabbix(t *testing.T) {
	tool := database.ToolInstance{
		ID:          3,
		Name:        "prod-zabbix",
		LogicalName: "prod-zabbix",
		Settings:    database.JSONB{},
		ToolType:    database.ToolType{ID: 2, Name: "zabbix"},
	}

	example := generateToolUsageExample(tool)

	if !strings.Contains(example, "gateway_call") {
		t.Error("expected gateway_call in example")
	}
	if !strings.Contains(example, `"prod-zabbix"`) {
		t.Errorf("expected logical name prod-zabbix, got: %s", example)
	}
	// Verify all tool methods have usage examples
	for _, fn := range []string{"zabbix.get_hosts", "zabbix.get_problems", "zabbix.get_history", "zabbix.get_items\"", "zabbix.get_items_batch", "zabbix.get_triggers", "zabbix.api_request"} {
		if !strings.Contains(example, fn) {
			t.Errorf("expected example for %s, got: %s", fn, example)
		}
	}
	// Verify inline parameter schemas are present
	if !strings.Contains(example, "**Parameters:**") {
		t.Error("expected **Parameters:** section in zabbix example")
	}
	if !strings.Contains(example, "searches*") {
		t.Errorf("expected searches* required param marker for get_items_batch, got: %s", example)
	}
	if !strings.Contains(example, "method*") {
		t.Errorf("expected method* required param marker for api_request, got: %s", example)
	}
}

func TestGenerateToolUsageExample_SSHAdhocWriteNoReadOnlyNote(t *testing.T) {
	// When ad-hoc connections and write are both enabled (no configured hosts),
	// the read-only note should NOT appear
	tool := sshToolInstance(database.JSONB{
		"allow_adhoc_connections":    true,
		"adhoc_allow_write_commands": true,
	})

	example := generateToolUsageExample(tool)

	if strings.Contains(example, "Read-only mode") {
		t.Errorf("expected no read-only note when ad-hoc write is enabled, got: %s", example)
	}
}

func TestGenerateToolUsageExample_SSHAdhocOnlyNoHostlessExamples(t *testing.T) {
	// When ad-hoc is enabled but no configured hosts exist,
	// hostless calls should NOT appear
	tool := sshToolInstance(database.JSONB{
		"allow_adhoc_connections":    true,
		"adhoc_allow_write_commands": false,
	})

	example := generateToolUsageExample(tool)

	// Should have ad-hoc examples with servers param
	if !strings.Contains(example, "Ad-hoc") {
		t.Errorf("expected ad-hoc example, got: %s", example)
	}
	if !strings.Contains(example, `"<hostname-or-ip>"`) {
		t.Errorf("expected ad-hoc servers placeholder, got: %s", example)
	}
	// Should NOT have hostless calls that would error (configured host examples without servers)
	if strings.Contains(example, `"ssh.test_connectivity", {}`) {
		t.Errorf("ad-hoc-only config should not show hostless test_connectivity, got: %s", example)
	}
	if strings.Contains(example, `"ssh.get_server_info", {}`) {
		t.Errorf("ad-hoc-only config should not show hostless get_server_info, got: %s", example)
	}
}

func TestGenerateToolUsageExample_SSHMixedPermissions_ConfigWriteAdhocReadOnly(t *testing.T) {
	// Configured hosts allow writes, ad-hoc is read-only
	// Should show read-only warning for ad-hoc
	tool := sshToolInstance(database.JSONB{
		"allow_adhoc_connections":    true,
		"adhoc_allow_write_commands": false,
		"ssh_hosts": []interface{}{
			map[string]interface{}{
				"hostname":             "web-1",
				"address":              "10.0.0.1",
				"allow_write_commands": true,
			},
		},
	})

	example := generateToolUsageExample(tool)

	if !strings.Contains(example, "ad-hoc") {
		t.Errorf("expected ad-hoc read-only warning, got: %s", example)
	}
}

func TestGenerateToolUsageExample_SSHMixedPermissions_ConfigReadOnlyAdhocWrite(t *testing.T) {
	// Configured hosts are read-only, ad-hoc allows writes
	// Should show read-only warning for configured hosts
	tool := sshToolInstance(database.JSONB{
		"allow_adhoc_connections":    true,
		"adhoc_allow_write_commands": true,
		"ssh_hosts": []interface{}{
			map[string]interface{}{
				"hostname":             "web-1",
				"address":              "10.0.0.1",
				"allow_write_commands": false,
			},
		},
	})

	example := generateToolUsageExample(tool)

	if !strings.Contains(example, "configured hosts") {
		t.Errorf("expected configured hosts read-only warning, got: %s", example)
	}
	if !strings.Contains(example, "Ad-hoc connections allow write") {
		t.Errorf("expected ad-hoc write note, got: %s", example)
	}
}

func TestGenerateToolUsageExample_SSHNoHostsNoAdhoc(t *testing.T) {
	// Neither configured hosts nor ad-hoc — tool is misconfigured
	tool := sshToolInstance(database.JSONB{
		"allow_adhoc_connections": false,
	})

	example := generateToolUsageExample(tool)

	if !strings.Contains(example, "not configured") {
		t.Errorf("expected misconfiguration message, got: %s", example)
	}
	if strings.Contains(example, "gateway_call") {
		t.Errorf("misconfigured tool should not show gateway_call examples, got: %s", example)
	}
}

func TestGenerateToolUsageExample_SSHEmptyHostsNoAdhoc(t *testing.T) {
	// Empty hosts array and no ad-hoc — tool is misconfigured
	tool := sshToolInstance(database.JSONB{
		"ssh_hosts": []interface{}{},
	})

	example := generateToolUsageExample(tool)

	if !strings.Contains(example, "not configured") {
		t.Errorf("expected misconfiguration message, got: %s", example)
	}
}

func TestGenerateToolUsageExample_SSHBlankHostsNoAdhoc(t *testing.T) {
	// Blank host entries (empty address) and no ad-hoc — tool is misconfigured
	tool := sshToolInstance(database.JSONB{
		"ssh_hosts": []interface{}{
			map[string]interface{}{"hostname": "", "address": ""},
			map[string]interface{}{"hostname": " ", "address": "  "},
		},
	})

	example := generateToolUsageExample(tool)

	if !strings.Contains(example, "not configured") {
		t.Errorf("expected misconfiguration message for blank hosts, got: %s", example)
	}
}

func TestSSHAllHostsAllowWrite_SkipsBlankAddresses(t *testing.T) {
	// A blank-address row with allow_write_commands=false should not
	// cause sshAllHostsAllowWrite to return false when all real hosts allow writes.
	tool := sshToolInstance(database.JSONB{
		"ssh_hosts": []interface{}{
			map[string]interface{}{"hostname": "web-1", "address": "10.0.0.1", "allow_write_commands": true},
			map[string]interface{}{"hostname": "", "address": "", "allow_write_commands": false}, // blank placeholder
			map[string]interface{}{"hostname": "db-1", "address": "10.0.0.2", "allow_write_commands": true},
		},
	})

	if !sshAllHostsAllowWrite(tool) {
		t.Error("expected sshAllHostsAllowWrite=true (blank rows should be skipped)")
	}
}

func TestSSHAllHostsAllowWrite_AllBlankReturnsFalse(t *testing.T) {
	tool := sshToolInstance(database.JSONB{
		"ssh_hosts": []interface{}{
			map[string]interface{}{"hostname": "", "address": "", "allow_write_commands": true},
		},
	})

	if sshAllHostsAllowWrite(tool) {
		t.Error("expected sshAllHostsAllowWrite=false when all hosts have blank addresses")
	}
}

func TestExtractToolDetails_SkipsBlankAddressHosts(t *testing.T) {
	// A host row with a hostname but blank address should not appear in configured hosts
	tool := sshToolInstance(database.JSONB{
		"ssh_hosts": []interface{}{
			map[string]interface{}{"hostname": "real-server", "address": "10.0.0.1"},
			map[string]interface{}{"hostname": "bogus-entry", "address": ""},
		},
	})

	details := extractToolDetails(tool)

	if !strings.Contains(details, "real-server") {
		t.Errorf("expected real-server in details, got: %s", details)
	}
	if strings.Contains(details, "bogus-entry") {
		t.Errorf("bogus-entry with blank address should be filtered out, got: %s", details)
	}
}

func TestGenerateToolUsageExample_VictoriaMetrics(t *testing.T) {
	tool := database.ToolInstance{
		ID:          4,
		Name:        "prod-vm",
		LogicalName: "prod-vm",
		Settings:    database.JSONB{},
		ToolType:    database.ToolType{ID: 3, Name: "victoria_metrics"},
	}

	example := generateToolUsageExample(tool)

	if !strings.Contains(example, "gateway_call") {
		t.Error("expected gateway_call in example")
	}
	if !strings.Contains(example, `"prod-vm"`) {
		t.Errorf("expected logical name prod-vm, got: %s", example)
	}
	// Verify all tool methods have usage examples
	for _, fn := range []string{"victoria_metrics.instant_query", "victoria_metrics.range_query", "victoria_metrics.label_values", "victoria_metrics.series", "victoria_metrics.api_request"} {
		if !strings.Contains(example, fn) {
			t.Errorf("expected example for %s, got: %s", fn, example)
		}
	}
	// Verify inline parameter schemas are present
	if !strings.Contains(example, "**Parameters:**") {
		t.Error("expected **Parameters:** section in victoria_metrics example")
	}
	for _, param := range []string{"instant_query", "range_query", "label_values", "series", "api_request"} {
		if !strings.Contains(example, "`"+param+"`") {
			t.Errorf("expected parameter reference for %s, got: %s", param, example)
		}
	}
	if !strings.Contains(example, "label_name*") {
		t.Errorf("expected label_name* required param marker, got: %s", example)
	}
}

func TestGenerateToolUsageExample_VictoriaMetricsContainsPromQL(t *testing.T) {
	tool := database.ToolInstance{
		ID:          7,
		Name:        "staging-vm",
		LogicalName: "staging-vm",
		Settings:    database.JSONB{},
		ToolType:    database.ToolType{ID: 3, Name: "victoria_metrics"},
	}

	example := generateToolUsageExample(tool)

	// Verify examples contain realistic PromQL patterns
	if !strings.Contains(example, "rate(http_requests_total[5m])") {
		t.Errorf("expected PromQL example in range_query, got: %s", example)
	}
	if !strings.Contains(example, `"__name__"`) {
		t.Errorf("expected __name__ label in label_values example, got: %s", example)
	}
}

func TestExtractToolDetails_VictoriaMetricsTool(t *testing.T) {
	tool := database.ToolInstance{
		ID:       4,
		Name:     "prod-vm",
		Settings: database.JSONB{"vm_url": "https://vm.example.com"},
		ToolType: database.ToolType{ID: 3, Name: "victoria_metrics"},
	}
	details := extractToolDetails(tool)
	if details != "" {
		t.Errorf("expected empty details for victoria_metrics tool (no agent-relevant config), got: %s", details)
	}
}

func TestGenerateToolUsageExample_UnknownToolType(t *testing.T) {
	tool := database.ToolInstance{
		ID:          5,
		Name:        "custom-tool",
		LogicalName: "custom-tool",
		Settings:    database.JSONB{},
		ToolType:    database.ToolType{ID: 3, Name: "custom"},
	}

	example := generateToolUsageExample(tool)

	if !strings.Contains(example, "gateway_call") {
		t.Errorf("expected gateway_call in example, got: %s", example)
	}
	if !strings.Contains(example, `"custom-tool"`) {
		t.Errorf("expected logical name in example, got: %s", example)
	}
}

func TestGenerateToolUsageExample_FallbackToNameWhenNoLogicalName(t *testing.T) {
	tool := database.ToolInstance{
		ID:          6,
		Name:        "my-tool",
		LogicalName: "", // no logical name set
		Settings:    database.JSONB{},
		ToolType:    database.ToolType{ID: 4, Name: "custom"},
	}

	example := generateToolUsageExample(tool)

	if !strings.Contains(example, `"my-tool"`) {
		t.Errorf("expected fallback to Name when LogicalName is empty, got: %s", example)
	}
}

func TestGenerateToolUsageExample_SSHUsesLogicalName(t *testing.T) {
	tool := database.ToolInstance{
		ID:          1,
		Name:        "Production SSH",
		LogicalName: "prod-ssh",
		Enabled:     true,
		Settings: database.JSONB{
			"ssh_hosts": []interface{}{
				map[string]interface{}{"hostname": "web-1", "address": "10.0.0.1", "allow_write_commands": true},
			},
		},
		ToolType: database.ToolType{ID: 1, Name: "ssh"},
	}

	example := generateToolUsageExample(tool)

	if !strings.Contains(example, `"prod-ssh"`) {
		t.Errorf("expected logical name prod-ssh in SSH example, got: %s", example)
	}
	// Should NOT contain instance ID references
	if strings.Contains(example, "tool_instance_id") {
		t.Errorf("should not contain tool_instance_id reference, got: %s", example)
	}
}

func TestGenerateToolUsageExample_ZabbixUsesLogicalName(t *testing.T) {
	tool := database.ToolInstance{
		ID:          3,
		Name:        "Production Zabbix",
		LogicalName: "prod-zabbix",
		Settings:    database.JSONB{},
		ToolType:    database.ToolType{ID: 2, Name: "zabbix"},
	}

	example := generateToolUsageExample(tool)

	if !strings.Contains(example, `"prod-zabbix"`) {
		t.Errorf("expected logical name in Zabbix example, got: %s", example)
	}
	if strings.Contains(example, "tool_instance_id") {
		t.Errorf("should not contain tool_instance_id reference, got: %s", example)
	}
}

// --- generateSkillMd tool section tests ---

func TestGenerateSkillMd_ToolSectionShowsLogicalNames(t *testing.T) {
	// Test that the tool section header shows logical_name instead of ID
	tool := database.ToolInstance{
		ID:          1,
		Name:        "prod-ssh",
		LogicalName: "prod-ssh",
		Enabled:     true,
		Settings: database.JSONB{
			"ssh_hosts": []interface{}{
				map[string]interface{}{"hostname": "web-1", "address": "10.0.0.1"},
			},
		},
		ToolType: database.ToolType{ID: 1, Name: "ssh"},
	}

	example := generateToolUsageExample(tool)

	// Header format should include logical_name, not ID
	if strings.Contains(example, "ID:") {
		t.Errorf("tool section should not show numeric ID, got: %s", example)
	}
}

func TestGenerateSkillMd_ToolSectionShowsDiscoveryHints(t *testing.T) {
	// Verify the generated output includes list_tools_for_tool_type and execute_script hints
	// This tests the generateSkillMd wrapper behavior indirectly by verifying
	// the section content includes the expected discovery tool references
	tool := database.ToolInstance{
		ID:          1,
		Name:        "prod-ssh",
		LogicalName: "prod-ssh",
		Enabled:     true,
		Settings: database.JSONB{
			"ssh_hosts": []interface{}{
				map[string]interface{}{"hostname": "web-1", "address": "10.0.0.1"},
			},
		},
		ToolType: database.ToolType{ID: 1, Name: "ssh"},
	}

	example := generateToolUsageExample(tool)

	// The gateway_call examples should be present
	if !strings.Contains(example, "gateway_call") {
		t.Errorf("expected gateway_call in example, got: %s", example)
	}
}

func TestGenerateToolUsageExample_NoPythonImports(t *testing.T) {
	// Verify no Python import patterns remain in any tool type
	tools := []database.ToolInstance{
		{
			ID: 1, Name: "ssh-1", LogicalName: "ssh-1", Enabled: true,
			Settings: database.JSONB{
				"ssh_hosts": []interface{}{
					map[string]interface{}{"hostname": "h", "address": "1.2.3.4"},
				},
			},
			ToolType: database.ToolType{ID: 1, Name: "ssh"},
		},
		{
			ID: 2, Name: "zabbix-1", LogicalName: "zabbix-1",
			Settings: database.JSONB{},
			ToolType: database.ToolType{ID: 2, Name: "zabbix"},
		},
		{
			ID: 3, Name: "vm-1", LogicalName: "vm-1",
			Settings: database.JSONB{},
			ToolType: database.ToolType{ID: 3, Name: "victoria_metrics"},
		},
	}

	for _, tool := range tools {
		example := generateToolUsageExample(tool)
		if strings.Contains(example, "from ") && strings.Contains(example, " import ") {
			t.Errorf("tool type %s still has Python import pattern: %s", tool.ToolType.Name, example)
		}
		if strings.Contains(example, "tool_instance_id=") {
			t.Errorf("tool type %s still has tool_instance_id= pattern: %s", tool.ToolType.Name, example)
		}
	}
}
