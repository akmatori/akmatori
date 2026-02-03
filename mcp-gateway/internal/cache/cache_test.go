package cache

import (
	"sync"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	c := New(time.Minute, time.Minute)
	defer c.Stop()

	if c == nil {
		t.Fatal("Expected cache to not be nil")
	}
	if c.entries == nil {
		t.Error("Expected entries map to be initialized")
	}
	if c.defaultTTL != time.Minute {
		t.Errorf("Expected defaultTTL to be 1 minute, got %v", c.defaultTTL)
	}
}

func TestCache_SetAndGet(t *testing.T) {
	c := New(time.Minute, time.Minute)
	defer c.Stop()

	c.Set("key1", "value1")

	value, ok := c.Get("key1")
	if !ok {
		t.Error("Expected key1 to exist")
	}
	if value != "value1" {
		t.Errorf("Expected value1, got %v", value)
	}
}

func TestCache_GetMissing(t *testing.T) {
	c := New(time.Minute, time.Minute)
	defer c.Stop()

	value, ok := c.Get("nonexistent")
	if ok {
		t.Error("Expected key to not exist")
	}
	if value != nil {
		t.Errorf("Expected nil value, got %v", value)
	}
}

func TestCache_SetWithTTL(t *testing.T) {
	c := New(time.Hour, time.Second)
	defer c.Stop()

	// Set with short TTL
	c.SetWithTTL("shortkey", "shortvalue", 50*time.Millisecond)

	// Should exist immediately
	value, ok := c.Get("shortkey")
	if !ok {
		t.Error("Expected shortkey to exist")
	}
	if value != "shortvalue" {
		t.Errorf("Expected shortvalue, got %v", value)
	}

	// Wait for expiration
	time.Sleep(100 * time.Millisecond)

	// Should not exist after TTL
	value, ok = c.Get("shortkey")
	if ok {
		t.Error("Expected shortkey to be expired")
	}
	if value != nil {
		t.Errorf("Expected nil value for expired key, got %v", value)
	}
}

func TestCache_TTLExpiration(t *testing.T) {
	c := New(50*time.Millisecond, time.Second)
	defer c.Stop()

	c.Set("expiring", "value")

	// Should exist initially
	_, ok := c.Get("expiring")
	if !ok {
		t.Error("Expected key to exist initially")
	}

	// Wait for expiration
	time.Sleep(100 * time.Millisecond)

	// Should be expired
	_, ok = c.Get("expiring")
	if ok {
		t.Error("Expected key to be expired")
	}
}

func TestCache_Delete(t *testing.T) {
	c := New(time.Minute, time.Minute)
	defer c.Stop()

	c.Set("key1", "value1")
	c.Set("key2", "value2")

	c.Delete("key1")

	_, ok := c.Get("key1")
	if ok {
		t.Error("Expected key1 to be deleted")
	}

	_, ok = c.Get("key2")
	if !ok {
		t.Error("Expected key2 to still exist")
	}
}

func TestCache_DeleteByPrefix(t *testing.T) {
	c := New(time.Minute, time.Minute)
	defer c.Stop()

	c.Set("creds:incident1:zabbix", "value1")
	c.Set("creds:incident1:ssh", "value2")
	c.Set("creds:incident2:zabbix", "value3")
	c.Set("other:key", "value4")

	c.DeleteByPrefix("creds:incident1:")

	// incident1 keys should be deleted
	_, ok := c.Get("creds:incident1:zabbix")
	if ok {
		t.Error("Expected creds:incident1:zabbix to be deleted")
	}
	_, ok = c.Get("creds:incident1:ssh")
	if ok {
		t.Error("Expected creds:incident1:ssh to be deleted")
	}

	// Other keys should remain
	_, ok = c.Get("creds:incident2:zabbix")
	if !ok {
		t.Error("Expected creds:incident2:zabbix to still exist")
	}
	_, ok = c.Get("other:key")
	if !ok {
		t.Error("Expected other:key to still exist")
	}
}

func TestCache_Clear(t *testing.T) {
	c := New(time.Minute, time.Minute)
	defer c.Stop()

	c.Set("key1", "value1")
	c.Set("key2", "value2")
	c.Set("key3", "value3")

	c.Clear()

	if c.Len() != 0 {
		t.Errorf("Expected cache to be empty, got %d entries", c.Len())
	}
}

func TestCache_Len(t *testing.T) {
	c := New(time.Minute, time.Minute)
	defer c.Stop()

	if c.Len() != 0 {
		t.Errorf("Expected empty cache, got %d entries", c.Len())
	}

	c.Set("key1", "value1")
	c.Set("key2", "value2")

	if c.Len() != 2 {
		t.Errorf("Expected 2 entries, got %d", c.Len())
	}
}

func TestCache_Keys(t *testing.T) {
	c := New(time.Minute, time.Minute)
	defer c.Stop()

	c.Set("key1", "value1")
	c.Set("key2", "value2")

	keys := c.Keys()
	if len(keys) != 2 {
		t.Errorf("Expected 2 keys, got %d", len(keys))
	}

	// Check both keys exist (order not guaranteed)
	keyMap := make(map[string]bool)
	for _, k := range keys {
		keyMap[k] = true
	}
	if !keyMap["key1"] || !keyMap["key2"] {
		t.Error("Expected both key1 and key2 in keys")
	}
}

func TestCache_ConcurrentAccess(t *testing.T) {
	c := New(time.Minute, time.Minute)
	defer c.Stop()

	var wg sync.WaitGroup
	numGoroutines := 100
	numOperations := 100

	// Start multiple goroutines doing concurrent operations
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				key := "key"
				// Mix of operations
				switch j % 4 {
				case 0:
					c.Set(key, id)
				case 1:
					c.Get(key)
				case 2:
					c.SetWithTTL(key, id, time.Second)
				case 3:
					c.Delete(key)
				}
			}
		}(i)
	}

	wg.Wait()
	// If we get here without deadlock or panic, concurrent access is safe
}

func TestCache_BackgroundCleanup(t *testing.T) {
	// Create cache with very short cleanup interval
	c := New(50*time.Millisecond, 30*time.Millisecond)
	defer c.Stop()

	c.Set("key1", "value1")

	// Initially should have 1 entry
	if c.Len() != 1 {
		t.Errorf("Expected 1 entry, got %d", c.Len())
	}

	// Wait for TTL + cleanup cycle
	time.Sleep(150 * time.Millisecond)

	// Entry should be cleaned up
	if c.Len() != 0 {
		t.Errorf("Expected 0 entries after cleanup, got %d", c.Len())
	}
}

func TestCache_Stop(t *testing.T) {
	c := New(time.Minute, time.Minute)

	// Should be able to stop multiple times without panic
	c.Stop()
	c.Stop()
}

func TestCache_DifferentValueTypes(t *testing.T) {
	c := New(time.Minute, time.Minute)
	defer c.Stop()

	// Test different types
	c.Set("string", "hello")
	c.Set("int", 42)
	c.Set("float", 3.14)
	c.Set("bool", true)
	c.Set("slice", []string{"a", "b"})
	c.Set("map", map[string]int{"x": 1})

	// Verify types are preserved
	if v, ok := c.Get("string"); !ok || v != "hello" {
		t.Errorf("String mismatch: %v", v)
	}
	if v, ok := c.Get("int"); !ok || v != 42 {
		t.Errorf("Int mismatch: %v", v)
	}
	if v, ok := c.Get("float"); !ok || v != 3.14 {
		t.Errorf("Float mismatch: %v", v)
	}
	if v, ok := c.Get("bool"); !ok || v != true {
		t.Errorf("Bool mismatch: %v", v)
	}
	if v, ok := c.Get("slice"); !ok {
		t.Error("Slice not found")
	} else if slice, ok := v.([]string); !ok || len(slice) != 2 {
		t.Errorf("Slice mismatch: %v", v)
	}
}

func TestCache_OverwriteValue(t *testing.T) {
	c := New(time.Minute, time.Minute)
	defer c.Stop()

	c.Set("key", "value1")
	c.Set("key", "value2")

	value, ok := c.Get("key")
	if !ok {
		t.Error("Expected key to exist")
	}
	if value != "value2" {
		t.Errorf("Expected value2, got %v", value)
	}
}

func TestEntry_IsExpired(t *testing.T) {
	// Entry that hasn't expired
	futureEntry := Entry{
		Value:     "test",
		ExpiresAt: time.Now().Add(time.Hour),
	}
	if futureEntry.IsExpired() {
		t.Error("Future entry should not be expired")
	}

	// Entry that has expired
	pastEntry := Entry{
		Value:     "test",
		ExpiresAt: time.Now().Add(-time.Hour),
	}
	if !pastEntry.IsExpired() {
		t.Error("Past entry should be expired")
	}
}

func TestCache_DeleteByPrefix_EmptyPrefix(t *testing.T) {
	c := New(time.Minute, time.Minute)
	defer c.Stop()

	c.Set("key1", "value1")
	c.Set("key2", "value2")

	// Empty prefix should match all keys
	c.DeleteByPrefix("")

	if c.Len() != 0 {
		t.Errorf("Expected all keys to be deleted with empty prefix, got %d", c.Len())
	}
}

func TestCache_DeleteByPrefix_NoMatch(t *testing.T) {
	c := New(time.Minute, time.Minute)
	defer c.Stop()

	c.Set("key1", "value1")
	c.Set("key2", "value2")

	c.DeleteByPrefix("nonexistent:")

	if c.Len() != 2 {
		t.Errorf("Expected 2 keys to remain, got %d", c.Len())
	}
}
