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
