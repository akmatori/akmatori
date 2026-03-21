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
// It resolves the allowlist for the incident and delegates to IsAuthorizedFromEntries.
//
// Authorization logic:
//  1. No allowlist for this incident -> allow all (safe default for unregistered incidents)
//  2. Empty allowlist -> reject everything
//  3. If both instanceID and logicalName are set, both must match the SAME entry
//  4. If only instanceID > 0, that specific ID must be in the allowlist
//  5. If only logicalName is set, that specific name must be in the allowlist
//  6. If neither instanceID nor logicalName is specified, any entry matching
//     the tool type is sufficient (the handler will pick an authorized instance)
func (a *Authorizer) IsAuthorized(incidentID string, toolType string, instanceID uint, logicalName string) bool {
	entries := a.GetAllowlist(incidentID)
	return IsAuthorizedFromEntries(entries, toolType, instanceID, logicalName)
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
	// Return a copy so callers get a true snapshot that is safe to mutate.
	result := make([]AllowlistEntry, len(al.entries))
	copy(result, al.entries)
	return result
}

// IsAuthorizedFromEntries checks authorization against a pre-fetched allowlist
// snapshot. This avoids TOCTOU races when the caller needs to use the same
// snapshot for both authorization and subsequent operations (e.g., looking up
// the logical_name for an authorized instance ID).
//
// A nil entries slice means no allowlist is active — all calls are allowed.
func IsAuthorizedFromEntries(entries []AllowlistEntry, toolType string, instanceID uint, logicalName string) bool {
	// No allowlist = allow all
	if entries == nil {
		return true
	}

	// Empty allowlist = reject all
	if len(entries) == 0 {
		return false
	}

	if instanceID > 0 && logicalName != "" {
		for _, e := range entries {
			if e.InstanceID == instanceID && e.LogicalName == logicalName && e.ToolType == toolType {
				return true
			}
		}
		return false
	}

	if instanceID > 0 {
		for _, e := range entries {
			if e.InstanceID == instanceID && e.ToolType == toolType {
				return true
			}
		}
		return false
	}

	if logicalName != "" {
		for _, e := range entries {
			if e.LogicalName == logicalName && e.ToolType == toolType {
				return true
			}
		}
		return false
	}

	for _, e := range entries {
		if e.ToolType == toolType {
			return true
		}
	}
	return false
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
