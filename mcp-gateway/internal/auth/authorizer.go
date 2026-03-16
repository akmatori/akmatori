package auth

import (
	"sync"
	"time"
)

// AllowlistEntry represents one authorized tool instance for an incident.
type AllowlistEntry struct {
	InstanceID  uint   `json:"instance_id"`
	LogicalName string `json:"logical_name"`
	ToolType    string `json:"tool_type"`
}

// incidentAllowlist stores an allowlist with its expiry time.
type incidentAllowlist struct {
	entries   []AllowlistEntry
	expiresAt time.Time
}

// Authorizer enforces per-incident tool instance authorization.
// It stores allowlists keyed by incident ID with TTL-based expiry.
// When no allowlist is set for an incident, all tool calls are allowed.
// This is intentional: the gateway is a standalone service that may receive
// requests without an allowlist header (e.g., direct API calls, debugging,
// or the first request before the agent-worker sends allowlist data).
type Authorizer struct {
	mu         sync.RWMutex
	allowlists map[string]*incidentAllowlist
	ttl        time.Duration
	stopCh     chan struct{}
}

// NewAuthorizer creates an Authorizer with the given TTL for allowlist entries.
// A background goroutine cleans up expired entries every ttl/2.
func NewAuthorizer(ttl time.Duration) *Authorizer {
	a := &Authorizer{
		allowlists: make(map[string]*incidentAllowlist),
		ttl:        ttl,
		stopCh:     make(chan struct{}),
	}
	go a.cleanupLoop()
	return a
}

// SetAllowlist stores or updates the allowlist for an incident.
// Each call resets the TTL.
func (a *Authorizer) SetAllowlist(incidentID string, entries []AllowlistEntry) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.allowlists[incidentID] = &incidentAllowlist{
		entries:   entries,
		expiresAt: time.Now().Add(a.ttl),
	}
}

// IsAuthorized checks whether a tool call is permitted for the given incident.
//
// Authorization logic:
//  1. No allowlist for this incident -> allow all (safe default for unregistered incidents)
//  2. Empty allowlist -> reject everything
//  3. Check tool_type matches at least one entry
//  4. If instanceID > 0, that specific ID must be in the allowlist
//  5. If logicalName is set, that specific name must be in the allowlist
//  6. If neither instanceID nor logicalName is specified, any entry matching
//     the tool type is sufficient (the handler will pick an authorized instance)
func (a *Authorizer) IsAuthorized(incidentID string, toolType string, instanceID uint, logicalName string) bool {
	a.mu.RLock()
	al, exists := a.allowlists[incidentID]
	a.mu.RUnlock()

	// No allowlist = allow all (gateway may receive requests without allowlist data)
	if !exists {
		return true
	}

	// Expired allowlist = allow all (treat as no allowlist)
	if time.Now().After(al.expiresAt) {
		return true
	}

	// Empty allowlist = reject all
	if len(al.entries) == 0 {
		return false
	}

	// If a specific instance ID is requested, check it directly
	if instanceID > 0 {
		for _, e := range al.entries {
			if e.InstanceID == instanceID && e.ToolType == toolType {
				return true
			}
		}
		return false
	}

	// If a logical name is requested, check it directly
	if logicalName != "" {
		for _, e := range al.entries {
			if e.LogicalName == logicalName && e.ToolType == toolType {
				return true
			}
		}
		return false
	}

	// No specific instance requested: allow if any entry matches the tool type
	for _, e := range al.entries {
		if e.ToolType == toolType {
			return true
		}
	}
	return false
}

// GetAllowlist returns the allowlist entries for an incident.
// Returns nil if no allowlist is set or if it has expired.
func (a *Authorizer) GetAllowlist(incidentID string) []AllowlistEntry {
	a.mu.RLock()
	al, exists := a.allowlists[incidentID]
	a.mu.RUnlock()

	if !exists {
		return nil
	}
	if time.Now().After(al.expiresAt) {
		return nil
	}
	return al.entries
}

// RemoveAllowlist removes the allowlist for an incident.
func (a *Authorizer) RemoveAllowlist(incidentID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.allowlists, incidentID)
}

// Stop terminates the background cleanup goroutine.
func (a *Authorizer) Stop() {
	close(a.stopCh)
}

// cleanupLoop removes expired allowlists periodically.
func (a *Authorizer) cleanupLoop() {
	interval := a.ttl / 2
	if interval < 10*time.Millisecond {
		interval = 10 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-a.stopCh:
			return
		case <-ticker.C:
			a.mu.Lock()
			now := time.Now()
			for id, al := range a.allowlists {
				if now.After(al.expiresAt) {
					delete(a.allowlists, id)
				}
			}
			a.mu.Unlock()
		}
	}
}
