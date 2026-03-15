package services

import (
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
)

func sshToolInstance(settings database.JSONB) database.ToolInstance {
	return database.ToolInstance{
		ID:      1,
		Name:    "prod-ssh",
		Enabled: true,
		Settings: settings,
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

	if !strings.Contains(example, "from ssh import") {
		t.Error("expected Python import in example")
	}
	if !strings.Contains(example, "tool_instance_id=1") {
		t.Errorf("expected tool_instance_id=1 in example, got: %s", example)
	}
	if !strings.Contains(example, "Read-only mode") {
		t.Error("expected read-only note for read-only host")
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
	if !strings.Contains(example, "get_server_info(servers=") {
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
		ID:       3,
		Name:     "prod-zabbix",
		Settings: database.JSONB{},
		ToolType: database.ToolType{ID: 2, Name: "zabbix"},
	}

	example := generateToolUsageExample(tool)

	if !strings.Contains(example, "from zabbix import") {
		t.Error("expected zabbix import in example")
	}
	if !strings.Contains(example, "tool_instance_id=3") {
		t.Errorf("expected tool_instance_id=3, got: %s", example)
	}
	// Verify all imported functions have usage examples
	for _, fn := range []string{"get_hosts", "get_problems", "get_history", "get_items(", "get_items_batch", "get_triggers", "api_request"} {
		if !strings.Contains(example, fn) {
			t.Errorf("expected example for %s, got: %s", fn, example)
		}
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
	// hostless calls like test_connectivity(tool_instance_id=X) should NOT appear
	tool := sshToolInstance(database.JSONB{
		"allow_adhoc_connections":    true,
		"adhoc_allow_write_commands": false,
	})

	example := generateToolUsageExample(tool)

	// Should have ad-hoc examples with servers param
	if !strings.Contains(example, "Ad-hoc") {
		t.Errorf("expected ad-hoc example, got: %s", example)
	}
	if !strings.Contains(example, `servers=["<hostname-or-ip>"]`) {
		t.Errorf("expected ad-hoc servers placeholder, got: %s", example)
	}
	// Should NOT have hostless calls that would error
	if strings.Contains(example, "test_connectivity(tool_instance_id=") {
		t.Errorf("ad-hoc-only config should not show hostless test_connectivity, got: %s", example)
	}
	if strings.Contains(example, "get_server_info(tool_instance_id=") {
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
	if strings.Contains(example, "from ssh import") {
		t.Errorf("misconfigured tool should not show import examples, got: %s", example)
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
		ID:       4,
		Name:     "prod-vm",
		Settings: database.JSONB{},
		ToolType: database.ToolType{ID: 3, Name: "victoria_metrics"},
	}

	example := generateToolUsageExample(tool)

	if !strings.Contains(example, "from victoriametrics import") {
		t.Error("expected victoriametrics import in example")
	}
	if !strings.Contains(example, "tool_instance_id=4") {
		t.Errorf("expected tool_instance_id=4, got: %s", example)
	}
	// Verify all imported functions have usage examples
	for _, fn := range []string{"instant_query", "range_query", "label_values", "series", "api_request"} {
		if !strings.Contains(example, fn) {
			t.Errorf("expected example for %s, got: %s", fn, example)
		}
	}
}

func TestGenerateToolUsageExample_VictoriaMetricsContainsPromQL(t *testing.T) {
	tool := database.ToolInstance{
		ID:       7,
		Name:     "staging-vm",
		Settings: database.JSONB{},
		ToolType: database.ToolType{ID: 3, Name: "victoria_metrics"},
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
		ID:       5,
		Name:     "custom-tool",
		Settings: database.JSONB{},
		ToolType: database.ToolType{ID: 3, Name: "custom"},
	}

	example := generateToolUsageExample(tool)

	if !strings.Contains(example, "tool_instance_id: 5") {
		t.Errorf("expected generic tool_instance_id hint, got: %s", example)
	}
}
