package slack

import (
	"testing"
)

// --- isChannelID tests ---

func TestIsChannelID_ValidChannelID(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"standard channel ID", "C01234567890", true},
		{"short channel ID", "C01234567", true},
		{"max length channel ID", "C012345678901234", false}, // too long (16 chars)
		{"all numbers after C", "C1234567890", true},
		{"mixed alphanumeric", "C0ABC123DEF", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isChannelID(tt.input); got != tt.want {
				t.Errorf("isChannelID(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestIsChannelID_InvalidChannelID(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty string", ""},
		{"too short", "C1234567"},
		{"starts with D", "D01234567890"},
		{"starts with U", "U01234567890"},
		{"lowercase letters", "C01234abcdef"},
		{"channel name", "#alerts"},
		{"channel name no hash", "alerts"},
		{"has dashes", "C0123-4567890"},
		{"has underscores", "C0123_4567890"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isChannelID(tt.input); got {
				t.Errorf("isChannelID(%q) = true, want false", tt.input)
			}
		})
	}
}

// --- ChannelResolver tests (unit tests without Slack API) ---

func TestChannelResolver_ResolveChannel_AlreadyChannelID(t *testing.T) {
	// When given a valid channel ID, it should be returned as-is
	// without calling the Slack API
	resolver := &ChannelResolver{
		client: nil, // nil client is fine since we won't call API
		cache:  make(map[string]string),
	}

	channelID := "C01234567890"
	result, err := resolver.ResolveChannel(channelID)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result != channelID {
		t.Errorf("got %q, want %q", result, channelID)
	}
}

func TestChannelResolver_ResolveChannel_EmptyInput(t *testing.T) {
	resolver := &ChannelResolver{
		client: nil,
		cache:  make(map[string]string),
	}

	_, err := resolver.ResolveChannel("")

	if err == nil {
		t.Error("expected error for empty input, got nil")
	}
}

func TestChannelResolver_ResolveChannel_CacheHit(t *testing.T) {
	resolver := &ChannelResolver{
		client: nil, // nil client - we expect cache hit so no API call
		cache: map[string]string{
			"alerts": "C01234567890",
		},
	}

	// Test with hash prefix
	result, err := resolver.ResolveChannel("#alerts")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result != "C01234567890" {
		t.Errorf("got %q, want %q", result, "C01234567890")
	}

	// Test without hash prefix
	result2, err := resolver.ResolveChannel("alerts")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result2 != "C01234567890" {
		t.Errorf("got %q, want %q", result2, "C01234567890")
	}
}

func TestChannelResolver_ClearCache(t *testing.T) {
	resolver := &ChannelResolver{
		client: nil,
		cache: map[string]string{
			"alerts":   "C01234567890",
			"random":   "C09876543210",
			"general":  "C11111111111",
		},
	}

	// Verify cache has entries
	if len(resolver.cache) != 3 {
		t.Errorf("cache should have 3 entries, got %d", len(resolver.cache))
	}

	// Clear cache
	resolver.ClearCache()

	// Verify cache is empty
	if len(resolver.cache) != 0 {
		t.Errorf("cache should be empty after clear, got %d entries", len(resolver.cache))
	}
}

func TestChannelResolver_ConcurrentCacheRead(t *testing.T) {
	resolver := &ChannelResolver{
		client: nil,
		cache:  make(map[string]string),
	}

	// Pre-populate cache so we don't hit API (nil client would panic)
	resolver.cache["alerts"] = "C01234567890"
	resolver.cache["general"] = "C11111111111"

	done := make(chan bool)

	// Concurrent reads - all should hit cache
	for i := 0; i < 10; i++ {
		go func() {
			// Use cached channel name - won't trigger API lookup
			_, _ = resolver.ResolveChannel("#alerts")
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// No panic = success (testing concurrent read safety)
}

func TestChannelResolver_ConcurrentClearAndRead(t *testing.T) {
	resolver := &ChannelResolver{
		client: nil,
		cache:  make(map[string]string),
	}

	// Pre-populate cache
	resolver.cache["alerts"] = "C01234567890"

	done := make(chan bool)

	// Concurrent cache clear
	go func() {
		resolver.ClearCache()
		done <- true
	}()

	// Concurrent reads (some may hit empty cache, but that's fine - 
	// they'll get error from nil client, which we catch)
	for i := 0; i < 5; i++ {
		go func() {
			// This might fail due to cleared cache + nil client, that's expected
			resolver.mu.RLock()
			_, cached := resolver.cache["alerts"]
			resolver.mu.RUnlock()
			_ = cached // Just test cache access, don't call ResolveChannel
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 6; i++ {
		<-done
	}

	// No panic = success (testing concurrent access safety)
}

// --- Edge cases for channel name parsing ---

func TestChannelResolver_ResolveChannel_HashPrefixVariations(t *testing.T) {
	resolver := &ChannelResolver{
		client: nil,
		cache: map[string]string{
			"alerts-prod": "C11111111111",
		},
	}

	tests := []struct {
		input string
		want  string
	}{
		{"#alerts-prod", "C11111111111"},
		{"alerts-prod", "C11111111111"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result, err := resolver.ResolveChannel(tt.input)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if result != tt.want {
				t.Errorf("ResolveChannel(%q) = %q, want %q", tt.input, result, tt.want)
			}
		})
	}
}
