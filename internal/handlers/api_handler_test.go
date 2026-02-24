package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/testhelpers"
)

// TestAPIHandler_SetupRoutes_DoesNotPanic verifies route setup doesn't panic
func TestAPIHandler_SetupRoutes_DoesNotPanic(t *testing.T) {
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil)
	mux := http.NewServeMux()
	
	// Should not panic even with nil services
	h.SetupRoutes(mux)
	
	// Verify mux has routes by checking it's not nil
	if mux == nil {
		t.Error("mux should not be nil after SetupRoutes")
	}
}

// TestAPIHandler_MethodNotAllowed tests method validation on endpoints
// Note: Only testing endpoints that validate methods before accessing DB
func TestAPIHandler_MethodNotAllowed(t *testing.T) {
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil)

	tests := []struct {
		name    string
		method  string
		path    string
		handler http.HandlerFunc
	}{
		{
			name:    "DELETE on skills sync",
			method:  http.MethodDelete,
			path:    "/api/skills/sync",
			handler: h.handleSkillsSync,
		},
		{
			name:    "GET on skills sync",
			method:  http.MethodGet,
			path:    "/api/skills/sync",
			handler: h.handleSkillsSync,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			w := httptest.NewRecorder()

			tt.handler(w, req)

			if w.Code != http.StatusMethodNotAllowed {
				t.Errorf("expected 405 Method Not Allowed, got %d", w.Code)
			}
		})
	}
}

// TestMaskToken_Comprehensive tests token masking comprehensively
func TestMaskToken_Comprehensive(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty", "", ""},
		{"1 char", "a", "****"},
		{"2 chars", "ab", "****"},
		{"3 chars", "abc", "****"},
		{"4 chars", "abcd", "****"},
		{"5 chars", "abcde", "****bcde"},
		{"6 chars", "abcdef", "****cdef"},
		{"typical API key", "sk-1234567890abcdef", "****cdef"},
		{"slack bot token", "xoxb-12345-67890-abcdefgh", "****efgh"},
		{"unicode token", "токен12345678", "****5678"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := maskToken(tt.input)
			if result != tt.expected {
				t.Errorf("maskToken(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestMaskProxyURL_Comprehensive tests proxy URL masking
func TestMaskProxyURL_Comprehensive(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		wantContains  []string
		wantNotContain []string
	}{
		{
			name:         "empty URL",
			input:        "",
			wantContains: []string{""},
		},
		{
			name:         "no auth",
			input:        "http://proxy.example.com:8080",
			wantContains: []string{"http://proxy.example.com:8080"},
		},
		{
			name:         "username only",
			input:        "http://user@proxy.example.com:8080",
			wantContains: []string{"user@", "proxy.example.com"},
		},
		{
			name:           "username and password",
			input:          "http://user:secretpassword@proxy.example.com:8080",
			wantContains:   []string{"user:", "%2A%2A%2A%2A", "proxy.example.com"}, // URL-encoded ****
			wantNotContain: []string{"secretpassword"},
		},
		{
			name:           "https with credentials",
			input:          "https://admin:p@ssw0rd!@secure.proxy.io:443/path",
			wantContains:   []string{"admin:", "%2A%2A%2A%2A", "secure.proxy.io"}, // URL-encoded ****
			wantNotContain: []string{"p@ssw0rd!"},
		},
		{
			name:         "socks5 proxy",
			input:        "socks5://proxy.example.com:1080",
			wantContains: []string{"socks5://", "proxy.example.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := maskProxyURL(tt.input)

			for _, want := range tt.wantContains {
				if !strings.Contains(result, want) {
					t.Errorf("maskProxyURL(%q) = %q, want to contain %q", tt.input, result, want)
				}
			}

			for _, notWant := range tt.wantNotContain {
				if strings.Contains(result, notWant) {
					t.Errorf("maskProxyURL(%q) = %q, should not contain %q", tt.input, result, notWant)
				}
			}
		})
	}
}

// TestIsValidURL_Comprehensive tests URL validation
func TestIsValidURL_Comprehensive(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		expected bool
	}{
		// Valid URLs
		{"empty (optional)", "", true},
		{"http", "http://example.com", true},
		{"https", "https://example.com", true},
		{"http with port", "http://example.com:8080", true},
		{"https with port", "https://example.com:443", true},
		{"with path", "http://example.com/api/v1", true},
		{"with query", "http://example.com?foo=bar", true},
		{"with fragment", "http://example.com#section", true},
		{"localhost", "http://localhost:8080", true},
		{"IP address", "http://192.168.1.1:8080", true},
		{"IPv6", "http://[::1]:8080", true},
		{"with auth", "http://user:pass@example.com", true},

		// Invalid URLs
		{"ftp", "ftp://example.com", false},
		{"file", "file:///etc/passwd", false},
		{"ws", "ws://example.com", false},
		{"wss", "wss://example.com", false},
		{"mailto", "mailto:user@example.com", false},
		{"no scheme", "example.com", false},
		{"just path", "/api/endpoint", false},
		{"broken URL", "://invalid", false},
		{"random string", "not a url at all", false},
		{"javascript", "javascript:alert(1)", false},
		{"data URL", "data:text/html,<h1>test</h1>", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidURL(tt.url)
			if result != tt.expected {
				t.Errorf("isValidURL(%q) = %v, want %v", tt.url, result, tt.expected)
			}
		})
	}
}

// TestSplitPath_EdgeCases tests path splitting edge cases
func TestSplitPath_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{"empty", "", []string{}},
		{"single slash", "/", []string{}},
		{"double slash", "//", []string{}},
		{"many slashes", "////", []string{}},
		{"single segment", "foo", []string{"foo"}},
		{"leading slash", "/foo", []string{"foo"}},
		{"trailing slash", "foo/", []string{"foo"}},
		{"both slashes", "/foo/", []string{"foo"}},
		{"two segments", "foo/bar", []string{"foo", "bar"}},
		{"three segments", "foo/bar/baz", []string{"foo", "bar", "baz"}},
		{"double slash between", "foo//bar", []string{"foo", "bar"}},
		{"complex path", "/api/v1/users/123/profile/", []string{"api", "v1", "users", "123", "profile"}},
		{"dots in segment", "foo.bar/baz.qux", []string{"foo.bar", "baz.qux"}},
		{"dashes in segment", "my-skill/sub-path", []string{"my-skill", "sub-path"}},
		{"underscores", "my_skill/sub_path", []string{"my_skill", "sub_path"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := splitPath(tt.input)

			if len(result) != len(tt.expected) {
				t.Errorf("splitPath(%q) = %v (len %d), want %v (len %d)",
					tt.input, result, len(result), tt.expected, len(tt.expected))
				return
			}

			for i := range result {
				if result[i] != tt.expected[i] {
					t.Errorf("splitPath(%q)[%d] = %q, want %q",
						tt.input, i, result[i], tt.expected[i])
				}
			}
		})
	}
}

// TestModelConfigs_Validity tests model configuration validity
func TestModelConfigs_Validity(t *testing.T) {
	// All models should have valid reasoning efforts
	for model, efforts := range ModelConfigs {
		t.Run(model, func(t *testing.T) {
			if len(efforts) == 0 {
				t.Errorf("model %q has no reasoning efforts", model)
			}

			// Check for duplicates
			seen := make(map[string]bool)
			for _, effort := range efforts {
				if seen[effort] {
					t.Errorf("model %q has duplicate effort %q", model, effort)
				}
				seen[effort] = true

				// Validate effort values
				validEfforts := []string{"low", "medium", "high", "extra_high"}
				isValid := false
				for _, valid := range validEfforts {
					if effort == valid {
						isValid = true
						break
					}
				}
				if !isValid {
					t.Errorf("model %q has invalid effort %q", model, effort)
				}
			}
		})
	}
}

// TestAPIHandler_MaskSSHKeys tests SSH key masking
func TestAPIHandler_MaskSSHKeys(t *testing.T) {
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil)

	tests := []struct {
		name     string
		input    *database.ToolInstance
		verify   func(*database.ToolInstance) bool
	}{
		{
			name:  "nil instance",
			input: nil,
			verify: func(ti *database.ToolInstance) bool {
				return true // Should not panic
			},
		},
		{
			name: "nil settings",
			input: &database.ToolInstance{
				Settings: nil,
			},
			verify: func(ti *database.ToolInstance) bool {
				return ti.Settings == nil
			},
		},
		{
			name: "empty settings",
			input: &database.ToolInstance{
				Settings: database.JSONB{},
			},
			verify: func(ti *database.ToolInstance) bool {
				return len(ti.Settings) == 0
			},
		},
		{
			name: "settings without ssh_keys",
			input: &database.ToolInstance{
				Settings: database.JSONB{
					"host": "example.com",
					"port": 22,
				},
			},
			verify: func(ti *database.ToolInstance) bool {
				return ti.Settings["host"] == "example.com"
			},
		},
		{
			name: "settings with ssh_keys",
			input: &database.ToolInstance{
				Settings: database.JSONB{
					"ssh_keys": []interface{}{
						map[string]interface{}{
							"name":        "default",
							"private_key": "-----BEGIN RSA PRIVATE KEY-----\nMIIE...",
							"is_default":  true,
						},
					},
				},
			},
			verify: func(ti *database.ToolInstance) bool {
				keys, ok := ti.Settings["ssh_keys"].([]interface{})
				if !ok || len(keys) == 0 {
					return false
				}
				keyMap := keys[0].(map[string]interface{})
				// private_key should be removed
				_, hasPrivateKey := keyMap["private_key"]
				return !hasPrivateKey && keyMap["name"] == "default"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h.maskSSHKeys(tt.input)
			if !tt.verify(tt.input) {
				t.Errorf("maskSSHKeys verification failed")
			}
		})
	}
}

// TestAPIHandler_DeviceAuthService tests device auth service initialization
func TestAPIHandler_DeviceAuthService(t *testing.T) {
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil)

	if h.deviceAuthService == nil {
		t.Error("deviceAuthService should be initialized")
	}
}

// TestAPIHandler_AlertChannelReloader tests reloader callback
func TestAPIHandler_AlertChannelReloader(t *testing.T) {
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil)

	// Initially nil
	if h.alertChannelReloader != nil {
		t.Error("alertChannelReloader should be nil initially")
	}

	// Set reloader
	h.SetAlertChannelReloader(func() {
		// Reloader called
	})

	// Should not be nil now
	if h.alertChannelReloader == nil {
		t.Error("alertChannelReloader should be set")
	}

	// reloadAlertChannels should trigger the callback (in goroutine)
	h.reloadAlertChannels()
	// Give goroutine time to run
	// Note: This is a bit racy, but we're just checking it doesn't panic

	// Test with nil reloader (should not panic)
	h2 := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil)
	h2.reloadAlertChannels() // Should not panic
}

// TestAPIHandler_HTTPContext demonstrates using testhelpers.HTTPTestContext
func TestAPIHandler_HTTPContext(t *testing.T) {
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil)

	// Only test handlers that check method before accessing DB
	t.Run("skills sync invalid method", func(t *testing.T) {
		ctx := testhelpers.NewHTTPTestContext(t, http.MethodGet, "/api/skills/sync", nil)
		ctx.ExecuteFunc(h.handleSkillsSync).
			AssertStatus(http.StatusMethodNotAllowed)
	})

	t.Run("skills sync DELETE not allowed", func(t *testing.T) {
		ctx := testhelpers.NewHTTPTestContext(t, http.MethodDelete, "/api/skills/sync", nil)
		ctx.ExecuteFunc(h.handleSkillsSync).
			AssertStatus(http.StatusMethodNotAllowed)
	})
}

// BenchmarkMaskToken benchmarks token masking
func BenchmarkMaskToken(b *testing.B) {
	token := "sk-proj-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
	for i := 0; i < b.N; i++ {
		_ = maskToken(token)
	}
}

// BenchmarkSplitPath benchmarks path splitting
func BenchmarkSplitPath(b *testing.B) {
	path := "/api/v1/users/123/profile/settings"
	for i := 0; i < b.N; i++ {
		_ = splitPath(path)
	}
}

// BenchmarkIsValidURL benchmarks URL validation
func BenchmarkIsValidURL(b *testing.B) {
	url := "https://example.com:8080/api/v1?foo=bar"
	for i := 0; i < b.N; i++ {
		_ = isValidURL(url)
	}
}
