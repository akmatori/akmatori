package cache

import (
	"sync"
	"time"
)

// Entry represents a cached item with expiration
type Entry struct {
	Value     interface{}
	ExpiresAt time.Time
}

// IsExpired returns true if the entry has expired
func (e *Entry) IsExpired() bool {
	return time.Now().After(e.ExpiresAt)
}

// Cache is a thread-safe TTL cache with background cleanup
type Cache struct {
	mu          sync.RWMutex
	entries     map[string]Entry
	defaultTTL  time.Duration
	cleanupTick time.Duration
	stopCleanup chan struct{}
	stopped     bool
}

// New creates a new cache with the specified default TTL and cleanup interval
func New(defaultTTL, cleanupInterval time.Duration) *Cache {
	c := &Cache{
		entries:     make(map[string]Entry),
		defaultTTL:  defaultTTL,
		cleanupTick: cleanupInterval,
		stopCleanup: make(chan struct{}),
	}

	// Start background cleanup goroutine
	go c.cleanupLoop()

	return c
}

// cleanupLoop periodically removes expired entries
func (c *Cache) cleanupLoop() {
	ticker := time.NewTicker(c.cleanupTick)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.cleanup()
		case <-c.stopCleanup:
			return
		}
	}
}

// cleanup removes all expired entries
func (c *Cache) cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for key, entry := range c.entries {
		if now.After(entry.ExpiresAt) {
			delete(c.entries, key)
		}
	}
}

// Get retrieves a value from the cache. Returns nil and false if not found or expired.
func (c *Cache) Get(key string) (interface{}, bool) {
	c.mu.RLock()
	entry, exists := c.entries[key]
	c.mu.RUnlock()

	if !exists {
		return nil, false
	}

	if entry.IsExpired() {
		// Don't delete here to avoid lock upgrade, cleanup will handle it
		return nil, false
	}

	return entry.Value, true
}

// Set stores a value in the cache with the default TTL
func (c *Cache) Set(key string, value interface{}) {
	c.SetWithTTL(key, value, c.defaultTTL)
}

// SetWithTTL stores a value in the cache with a custom TTL
func (c *Cache) SetWithTTL(key string, value interface{}, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[key] = Entry{
		Value:     value,
		ExpiresAt: time.Now().Add(ttl),
	}
}

// Delete removes a specific key from the cache
func (c *Cache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.entries, key)
}

// DeleteByPrefix removes all keys that start with the given prefix
func (c *Cache) DeleteByPrefix(prefix string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for key := range c.entries {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			delete(c.entries, key)
		}
	}
}

// Clear removes all entries from the cache
func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries = make(map[string]Entry)
}

// Stop stops the background cleanup goroutine
func (c *Cache) Stop() {
	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		return
	}
	c.stopped = true
	c.mu.Unlock()

	close(c.stopCleanup)
}

// Len returns the number of entries in the cache (including expired ones that haven't been cleaned up yet)
func (c *Cache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return len(c.entries)
}

// Keys returns all keys in the cache (including expired ones)
func (c *Cache) Keys() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	keys := make([]string, 0, len(c.entries))
	for key := range c.entries {
		keys = append(keys, key)
	}
	return keys
}
