package auth

import (
	"sync"
	"testing"
	"time"
)

func TestAuthorizer_NoAllowlist_AllowsAll(t *testing.T) {
	a := NewAuthorizer(time.Hour)
	defer a.Stop()

	// No allowlist set for this incident — should allow everything
	if !a.IsAuthorized("incident-1", "ssh", 0, "") {
		t.Error("expected authorized when no allowlist is set")
	}
	if !a.IsAuthorized("incident-1", "zabbix", 5, "") {
		t.Error("expected authorized when no allowlist is set (with instance ID)")
	}
	if !a.IsAuthorized("incident-1", "ssh", 0, "prod-ssh") {
		t.Error("expected authorized when no allowlist is set (with logical name)")
	}
}

func TestAuthorizer_EmptyAllowlist_RejectsAll(t *testing.T) {
	a := NewAuthorizer(time.Hour)
	defer a.Stop()

	a.SetAllowlist("incident-1", []AllowlistEntry{})

	if a.IsAuthorized("incident-1", "ssh", 0, "") {
		t.Error("expected unauthorized with empty allowlist")
	}
	if a.IsAuthorized("incident-1", "zabbix", 1, "") {
		t.Error("expected unauthorized with empty allowlist (with instance ID)")
	}
}

func TestAuthorizer_AuthorizedByToolType(t *testing.T) {
	a := NewAuthorizer(time.Hour)
	defer a.Stop()

	a.SetAllowlist("incident-1", []AllowlistEntry{
		{InstanceID: 1, LogicalName: "prod-ssh", ToolType: "ssh"},
		{InstanceID: 2, LogicalName: "prod-zabbix", ToolType: "zabbix"},
	})

	// Tool type in allowlist, no specific instance — should pass
	if !a.IsAuthorized("incident-1", "ssh", 0, "") {
		t.Error("expected authorized for ssh tool type")
	}
	if !a.IsAuthorized("incident-1", "zabbix", 0, "") {
		t.Error("expected authorized for zabbix tool type")
	}

	// Tool type NOT in allowlist
	if a.IsAuthorized("incident-1", "victoria_metrics", 0, "") {
		t.Error("expected unauthorized for victoria_metrics tool type")
	}
}

func TestAuthorizer_AuthorizedByInstanceID(t *testing.T) {
	a := NewAuthorizer(time.Hour)
	defer a.Stop()

	a.SetAllowlist("incident-1", []AllowlistEntry{
		{InstanceID: 1, LogicalName: "prod-ssh", ToolType: "ssh"},
		{InstanceID: 3, LogicalName: "staging-ssh", ToolType: "ssh"},
	})

	// Authorized instance ID
	if !a.IsAuthorized("incident-1", "ssh", 1, "") {
		t.Error("expected authorized for instance ID 1")
	}
	if !a.IsAuthorized("incident-1", "ssh", 3, "") {
		t.Error("expected authorized for instance ID 3")
	}

	// Unauthorized instance ID (same tool type)
	if a.IsAuthorized("incident-1", "ssh", 99, "") {
		t.Error("expected unauthorized for instance ID 99")
	}

	// Wrong tool type for instance ID
	if a.IsAuthorized("incident-1", "zabbix", 1, "") {
		t.Error("expected unauthorized when tool type doesn't match")
	}
}

func TestAuthorizer_AuthorizedByLogicalName(t *testing.T) {
	a := NewAuthorizer(time.Hour)
	defer a.Stop()

	a.SetAllowlist("incident-1", []AllowlistEntry{
		{InstanceID: 1, LogicalName: "prod-ssh", ToolType: "ssh"},
		{InstanceID: 2, LogicalName: "prod-zabbix", ToolType: "zabbix"},
	})

	// Authorized logical name
	if !a.IsAuthorized("incident-1", "ssh", 0, "prod-ssh") {
		t.Error("expected authorized for logical name prod-ssh")
	}

	// Unauthorized logical name
	if a.IsAuthorized("incident-1", "ssh", 0, "staging-ssh") {
		t.Error("expected unauthorized for logical name staging-ssh")
	}

	// Wrong tool type for logical name
	if a.IsAuthorized("incident-1", "zabbix", 0, "prod-ssh") {
		t.Error("expected unauthorized when tool type doesn't match logical name")
	}
}

func TestAuthorizer_BothInstanceIDAndLogicalName_MustMatchSameEntry(t *testing.T) {
	a := NewAuthorizer(time.Hour)
	defer a.Stop()

	a.SetAllowlist("incident-1", []AllowlistEntry{
		{InstanceID: 1, LogicalName: "prod-ssh", ToolType: "ssh"},
		{InstanceID: 2, LogicalName: "staging-ssh", ToolType: "ssh"},
	})

	// Both match the same entry — should pass
	if !a.IsAuthorized("incident-1", "ssh", 1, "prod-ssh") {
		t.Error("expected authorized when instanceID and logicalName match same entry")
	}
	if !a.IsAuthorized("incident-1", "ssh", 2, "staging-ssh") {
		t.Error("expected authorized when instanceID and logicalName match same entry (staging)")
	}

	// Authorized instanceID + unauthorized logicalName — must reject
	// This prevents auth bypass: attacker passes authorized ID to pass auth check
	// then the handler resolves credentials from the unauthorized logical name.
	if a.IsAuthorized("incident-1", "ssh", 1, "unauthorized-ssh") {
		t.Error("expected unauthorized: instanceID=1 authorized but logicalName=unauthorized-ssh is not")
	}

	// Mismatched but both individually authorized — must reject (different entries)
	if a.IsAuthorized("incident-1", "ssh", 1, "staging-ssh") {
		t.Error("expected unauthorized: instanceID=1 is prod-ssh, not staging-ssh")
	}

	// Authorized logicalName + unauthorized instanceID — must reject
	if a.IsAuthorized("incident-1", "ssh", 99, "prod-ssh") {
		t.Error("expected unauthorized: logicalName=prod-ssh authorized but instanceID=99 is not")
	}
}

func TestAuthorizer_ExpiredAllowlist_AllowsAll(t *testing.T) {
	a := NewAuthorizer(50 * time.Millisecond)
	defer a.Stop()

	a.SetAllowlist("incident-1", []AllowlistEntry{
		{InstanceID: 1, LogicalName: "prod-ssh", ToolType: "ssh"},
	})

	// Before expiry — should enforce
	if a.IsAuthorized("incident-1", "zabbix", 0, "") {
		t.Error("expected unauthorized before expiry")
	}

	// Wait for expiry
	time.Sleep(60 * time.Millisecond)

	// After expiry — should allow all (treated as no allowlist)
	if !a.IsAuthorized("incident-1", "zabbix", 0, "") {
		t.Error("expected authorized after allowlist expiry")
	}
}

func TestAuthorizer_SetAllowlist_ResetsExpiry(t *testing.T) {
	a := NewAuthorizer(100 * time.Millisecond)
	defer a.Stop()

	a.SetAllowlist("incident-1", []AllowlistEntry{
		{InstanceID: 1, LogicalName: "prod-ssh", ToolType: "ssh"},
	})

	time.Sleep(60 * time.Millisecond)

	// Re-set should reset TTL
	a.SetAllowlist("incident-1", []AllowlistEntry{
		{InstanceID: 1, LogicalName: "prod-ssh", ToolType: "ssh"},
	})

	time.Sleep(60 * time.Millisecond)

	// Should still be enforced (not expired)
	if a.IsAuthorized("incident-1", "zabbix", 0, "") {
		t.Error("expected unauthorized — allowlist was refreshed")
	}
}

func TestAuthorizer_RemoveAllowlist(t *testing.T) {
	a := NewAuthorizer(time.Hour)
	defer a.Stop()

	a.SetAllowlist("incident-1", []AllowlistEntry{
		{InstanceID: 1, LogicalName: "prod-ssh", ToolType: "ssh"},
	})

	if a.IsAuthorized("incident-1", "zabbix", 0, "") {
		t.Error("expected unauthorized before removal")
	}

	a.RemoveAllowlist("incident-1")

	// After removal — should allow all
	if !a.IsAuthorized("incident-1", "zabbix", 0, "") {
		t.Error("expected authorized after allowlist removal")
	}
}

func TestAuthorizer_MultipleIncidents(t *testing.T) {
	a := NewAuthorizer(time.Hour)
	defer a.Stop()

	a.SetAllowlist("incident-1", []AllowlistEntry{
		{InstanceID: 1, LogicalName: "prod-ssh", ToolType: "ssh"},
	})
	a.SetAllowlist("incident-2", []AllowlistEntry{
		{InstanceID: 2, LogicalName: "prod-zabbix", ToolType: "zabbix"},
	})

	// incident-1 can use ssh, not zabbix
	if !a.IsAuthorized("incident-1", "ssh", 0, "") {
		t.Error("incident-1 should be authorized for ssh")
	}
	if a.IsAuthorized("incident-1", "zabbix", 0, "") {
		t.Error("incident-1 should not be authorized for zabbix")
	}

	// incident-2 can use zabbix, not ssh
	if !a.IsAuthorized("incident-2", "zabbix", 0, "") {
		t.Error("incident-2 should be authorized for zabbix")
	}
	if a.IsAuthorized("incident-2", "ssh", 0, "") {
		t.Error("incident-2 should not be authorized for ssh")
	}
}

func TestAuthorizer_ConcurrentAccess(t *testing.T) {
	a := NewAuthorizer(time.Hour)
	defer a.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(id int) {
			defer wg.Done()
			a.SetAllowlist("incident-concurrent", []AllowlistEntry{
				{InstanceID: uint(id), ToolType: "ssh"},
			})
		}(i)
		go func() {
			defer wg.Done()
			a.IsAuthorized("incident-concurrent", "ssh", 1, "")
		}()
	}
	wg.Wait()
}

func TestAuthorizer_ProxyToolType_BypassesAllowlist(t *testing.T) {
	// QMD and other MCP proxy tools use dotted namespaces (e.g., "qmd.query").
	// The authorization bypass happens at the server level (server.go), not in
	// the Authorizer itself. This test documents that the Authorizer correctly
	// rejects unknown tool types — the server skips calling IsAuthorized for
	// proxy tools entirely.
	a := NewAuthorizer(time.Hour)
	defer a.Stop()

	// Set allowlist that only permits SSH
	a.SetAllowlist("incident-1", []AllowlistEntry{
		{InstanceID: 1, LogicalName: "prod-ssh", ToolType: "ssh"},
	})

	// Authorizer itself doesn't know about proxy tools — it rejects "qmd" as a tool type
	// because it's not in the allowlist. The bypass is at the server layer.
	if a.IsAuthorized("incident-1", "qmd", 0, "") {
		t.Error("authorizer should reject unknown tool type 'qmd' — bypass is at server layer")
	}

	// Standard tool types work as expected
	if !a.IsAuthorized("incident-1", "ssh", 0, "") {
		t.Error("ssh should be authorized")
	}
}

func TestIsAuthorizedFromEntries_NilAllowsAll(t *testing.T) {
	if !IsAuthorizedFromEntries(nil, "ssh", 0, "") {
		t.Error("nil entries should allow all")
	}
	if !IsAuthorizedFromEntries(nil, "ssh", 5, "prod-ssh") {
		t.Error("nil entries should allow all regardless of instance/name")
	}
}

func TestIsAuthorizedFromEntries_EmptyRejectsAll(t *testing.T) {
	entries := []AllowlistEntry{}
	if IsAuthorizedFromEntries(entries, "ssh", 0, "") {
		t.Error("empty entries should reject all")
	}
}

func TestIsAuthorizedFromEntries_MatchesSameAsAuthorizer(t *testing.T) {
	entries := []AllowlistEntry{
		{InstanceID: 1, LogicalName: "prod-ssh", ToolType: "ssh"},
		{InstanceID: 2, LogicalName: "staging-ssh", ToolType: "ssh"},
		{InstanceID: 3, LogicalName: "prod-zabbix", ToolType: "zabbix"},
	}

	// Tool type match
	if !IsAuthorizedFromEntries(entries, "ssh", 0, "") {
		t.Error("should allow ssh by tool type")
	}
	if IsAuthorizedFromEntries(entries, "victoria_metrics", 0, "") {
		t.Error("should reject unknown tool type")
	}

	// Instance ID match
	if !IsAuthorizedFromEntries(entries, "ssh", 1, "") {
		t.Error("should allow instance ID 1")
	}
	if IsAuthorizedFromEntries(entries, "ssh", 99, "") {
		t.Error("should reject unknown instance ID")
	}

	// Logical name match
	if !IsAuthorizedFromEntries(entries, "ssh", 0, "prod-ssh") {
		t.Error("should allow logical name prod-ssh")
	}
	if IsAuthorizedFromEntries(entries, "ssh", 0, "unknown-ssh") {
		t.Error("should reject unknown logical name")
	}

	// Both must match same entry
	if !IsAuthorizedFromEntries(entries, "ssh", 1, "prod-ssh") {
		t.Error("should allow when both match same entry")
	}
	if IsAuthorizedFromEntries(entries, "ssh", 1, "staging-ssh") {
		t.Error("should reject when instanceID and logicalName are from different entries")
	}
}

func TestAuthorizer_CleanupRemovesExpired(t *testing.T) {
	a := NewAuthorizer(50 * time.Millisecond)
	defer a.Stop()

	a.SetAllowlist("incident-cleanup", []AllowlistEntry{
		{InstanceID: 1, ToolType: "ssh"},
	})

	// Wait for expiry + cleanup cycle (ttl=50ms, cleanup interval=ttl/2=25ms)
	time.Sleep(200 * time.Millisecond)

	// After cleanup, the entry should be gone from the map
	a.mu.RLock()
	_, exists := a.allowlists["incident-cleanup"]
	a.mu.RUnlock()

	if exists {
		t.Error("expected expired allowlist to be cleaned up")
	}
}
