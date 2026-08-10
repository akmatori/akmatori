package slack

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/slack-go/slack"
)

// --- isChannelID tests ---

func TestIsChannelID_ValidChannelID(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"standard channel ID", "C01234567890", true},
		{"private channel ID", "G01234567890", true},
		{"short channel ID", "C01234567", true},
		{"max length channel ID", "C012345678901234", false}, // too long (16 chars)
		{"all numbers after C", "C1234567890", true},
		{"mixed alphanumeric public", "C0ABC123DEF", true},
		{"mixed alphanumeric private", "G0ABC123DEF", true},
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

func TestNewChannelResolver(t *testing.T) {
	client := slack.New("test-token")
	resolver := NewChannelResolver(client)

	if resolver == nil {
		t.Fatal("NewChannelResolver returned nil")
	}
	if resolver.client != client {
		t.Error("resolver should keep the provided Slack client")
	}
	if resolver.cache == nil {
		t.Fatal("resolver cache should be initialized")
	}
	if len(resolver.cache) != 0 {
		t.Errorf("new resolver cache should be empty, got %d entries", len(resolver.cache))
	}
}

func TestChannelResolver_ResolveChannel_AlreadyChannelID(t *testing.T) {
	// When given a valid channel ID, it should be returned as-is
	// without calling the Slack API
	resolver := &ChannelResolver{
		client: nil, // nil client is fine since we won't call API
		cache:  make(map[string]string),
	}

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "public channel", input: "C01234567890", want: "C01234567890"},
		{name: "private channel", input: "G01234567890", want: "G01234567890"},
		{name: "trims surrounding whitespace", input: "  C01234567890  ", want: "C01234567890"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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

func TestChannelResolver_ResolveChannel_EmptyInput(t *testing.T) {
	resolver := &ChannelResolver{
		client: nil,
		cache:  make(map[string]string),
	}

	tests := []string{"", "   ", "\n\t"}
	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			_, err := resolver.ResolveChannel(input)
			if err == nil {
				t.Errorf("ResolveChannel(%q) expected error, got nil", input)
			}
		})
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

func TestChannelResolver_ResolveChannel_SlackLookup(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		responses  map[string]slackConversationsResponse
		want       string
		wantTypes  []string
		wantErr    bool
		wantCached bool
	}{
		{
			name:  "public channel match caches result",
			input: "#alerts",
			responses: map[string]slackConversationsResponse{
				"public_channel": {
					Ok:       true,
					Channels: []slackTestChannel{{ID: "C111111111", Name: "alerts"}},
				},
			},
			want:       "C111111111",
			wantTypes:  []string{"public_channel"},
			wantCached: true,
		},
		{
			name:  "private channel fallback",
			input: "incidents",
			responses: map[string]slackConversationsResponse{
				"public_channel": {Ok: true},
				"private_channel": {
					Ok:       true,
					Channels: []slackTestChannel{{ID: "G222222222", Name: "incidents"}},
				},
			},
			want:       "G222222222",
			wantTypes:  []string{"public_channel", "private_channel"},
			wantCached: true,
		},
		{
			name:  "not found after public and private lookup",
			input: "missing",
			responses: map[string]slackConversationsResponse{
				"public_channel":  {Ok: true},
				"private_channel": {Ok: true},
			},
			wantTypes: []string{"public_channel", "private_channel"},
			wantErr:   true,
		},
		{
			name:  "private lookup error reports channel not found",
			input: "secret-alerts",
			responses: map[string]slackConversationsResponse{
				"public_channel":  {Ok: true},
				"private_channel": {Ok: false, Error: "missing_scope"},
			},
			wantTypes: []string{"public_channel", "private_channel"},
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, gotTypes := newSlackConversationsTestClient(t, tt.responses)
			resolver := NewChannelResolver(client)

			got, err := resolver.ResolveChannel(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ResolveChannel(%q) expected error, got nil", tt.input)
				}
			} else if err != nil {
				t.Fatalf("ResolveChannel(%q) unexpected error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("ResolveChannel(%q) = %q, want %q", tt.input, got, tt.want)
			}
			if !equalStrings(gotTypes(), tt.wantTypes) {
				t.Errorf("Slack lookup types = %v, want %v", gotTypes(), tt.wantTypes)
			}

			cacheKey := tt.input
			if cacheKey != "" && cacheKey[0] == '#' {
				cacheKey = cacheKey[1:]
			}
			_, cached := resolver.cache[cacheKey]
			if cached != tt.wantCached {
				t.Errorf("cache entry for %q exists = %v, want %v", cacheKey, cached, tt.wantCached)
			}
		})
	}
}

func TestChannelResolver_ResolveChannel_PublicLookupError(t *testing.T) {
	client, gotTypes := newSlackConversationsTestClient(t, map[string]slackConversationsResponse{
		"public_channel": {Ok: false, Error: "invalid_auth"},
	})
	resolver := NewChannelResolver(client)

	got, err := resolver.ResolveChannel("alerts")
	if err == nil {
		t.Fatal("ResolveChannel expected error, got nil")
	}
	if got != "" {
		t.Errorf("ResolveChannel returned ID %q on error, want empty", got)
	}
	if !equalStrings(gotTypes(), []string{"public_channel"}) {
		t.Errorf("Slack lookup types = %v, want [public_channel]", gotTypes())
	}
	if len(resolver.cache) != 0 {
		t.Errorf("resolver should not cache failed lookup, got %d entries", len(resolver.cache))
	}
}

func TestChannelResolver_ClearCache(t *testing.T) {
	resolver := &ChannelResolver{
		client: nil,
		cache: map[string]string{
			"alerts":  "C01234567890",
			"random":  "C09876543210",
			"general": "C11111111111",
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

type slackTestChannel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type slackConversationsResponse struct {
	Ok       bool               `json:"ok"`
	Error    string             `json:"error,omitempty"`
	Channels []slackTestChannel `json:"channels,omitempty"`
}

func newSlackConversationsTestClient(t *testing.T, responses map[string]slackConversationsResponse) (*slack.Client, func() []string) {
	t.Helper()

	var gotTypes []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/conversations.list" {
			t.Errorf("unexpected Slack API path %q", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm failed: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		typeName := r.FormValue("types")
		gotTypes = append(gotTypes, typeName)
		response, ok := responses[typeName]
		if !ok {
			t.Errorf("unexpected conversation type lookup %q", typeName)
			response = slackConversationsResponse{Ok: true}
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Errorf("encoding response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	client := slack.New("test-token", slack.OptionAPIURL(server.URL+"/"))
	return client, func() []string {
		return append([]string(nil), gotTypes...)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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
