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
	if !strings.Contains(example, "any-server.example.com") {
		t.Errorf("expected ad-hoc server example, got: %s", example)
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
