package handlers

import (
	"testing"
)

func TestSplitPath(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected []string
	}{
		{
			name:     "empty path",
			path:     "",
			expected: []string{},
		},
		{
			name:     "single segment",
			path:     "skills",
			expected: []string{"skills"},
		},
		{
			name:     "two segments",
			path:     "skills/test-skill",
			expected: []string{"skills", "test-skill"},
		},
		{
			name:     "three segments",
			path:     "skills/test-skill/prompt",
			expected: []string{"skills", "test-skill", "prompt"},
		},
		{
			name:     "trailing slash",
			path:     "skills/test-skill/",
			expected: []string{"skills", "test-skill"},
		},
		{
			name:     "leading slash",
			path:     "/skills/test-skill",
			expected: []string{"skills", "test-skill"},
		},
		{
			name:     "multiple slashes",
			path:     "skills//test-skill///prompt",
			expected: []string{"skills", "test-skill", "prompt"},
		},
		{
			name:     "only slashes",
			path:     "///",
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := splitPath(tt.path)
			if len(result) != len(tt.expected) {
				t.Errorf("splitPath(%q) = %v (len %d), want %v (len %d)",
					tt.path, result, len(result), tt.expected, len(tt.expected))
				return
			}
			for i, v := range result {
				if v != tt.expected[i] {
					t.Errorf("splitPath(%q)[%d] = %q, want %q",
						tt.path, i, v, tt.expected[i])
				}
			}
		})
	}
}

func TestMaskToken(t *testing.T) {
	tests := []struct {
		name     string
		token    string
		expected string
	}{
		{
			name:     "empty token",
			token:    "",
			expected: "",
		},
		{
			name:     "short token (1 char)",
			token:    "a",
			expected: "****",
		},
		{
			name:     "short token (4 chars)",
			token:    "abcd",
			expected: "****",
		},
		{
			name:     "5 character token shows last 4",
			token:    "abcde",
			expected: "****bcde",
		},
		{
			name:     "normal token",
			token:    "xoxb-1234567890-abcdefgh",
			expected: "****efgh",
		},
		{
			name:     "long token",
			token:    "sk-proj-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
			expected: "****xxxx",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := maskToken(tt.token)
			if result != tt.expected {
				t.Errorf("maskToken(%q) = %q, want %q", tt.token, result, tt.expected)
			}
		})
	}
}

func TestMaskProxyURL(t *testing.T) {
	tests := []struct {
		name     string
		proxyURL string
		expected string
	}{
		{
			name:     "empty URL",
			proxyURL: "",
			expected: "",
		},
		{
			name:     "URL without auth",
			proxyURL: "http://proxy.example.com:8080",
			expected: "http://proxy.example.com:8080",
		},
		{
			name:     "URL with username only",
			proxyURL: "http://user@proxy.example.com:8080",
			expected: "http://user@proxy.example.com:8080",
		},
		{
			name:     "URL with username and password (URL-encoded asterisks)",
			proxyURL: "http://user:secret123@proxy.example.com:8080",
			expected: "http://user:%2A%2A%2A%2A@proxy.example.com:8080",
		},
		{
			name:     "HTTPS URL with auth (URL-encoded asterisks)",
			proxyURL: "https://admin:password@secure-proxy.example.com:443",
			expected: "https://admin:%2A%2A%2A%2A@secure-proxy.example.com:443",
		},
		{
			name:     "invalid URL returns as-is",
			proxyURL: "not-a-valid-url",
			expected: "not-a-valid-url",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := maskProxyURL(tt.proxyURL)
			if result != tt.expected {
				t.Errorf("maskProxyURL(%q) = %q, want %q", tt.proxyURL, result, tt.expected)
			}
		})
	}
}

func TestIsValidURL(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		expected bool
	}{
		{
			name:     "empty URL is valid (optional)",
			url:      "",
			expected: true,
		},
		{
			name:     "HTTP URL",
			url:      "http://example.com",
			expected: true,
		},
		{
			name:     "HTTPS URL",
			url:      "https://example.com",
			expected: true,
		},
		{
			name:     "HTTP URL with port",
			url:      "http://example.com:8080",
			expected: true,
		},
		{
			name:     "HTTP URL with path",
			url:      "http://example.com/api/v1",
			expected: true,
		},
		{
			name:     "FTP URL is invalid",
			url:      "ftp://example.com",
			expected: false,
		},
		{
			name:     "file URL is invalid",
			url:      "file:///etc/passwd",
			expected: false,
		},
		{
			name:     "missing scheme",
			url:      "example.com",
			expected: false,
		},
		{
			name:     "invalid URL",
			url:      "://invalid",
			expected: false,
		},
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

func TestNewAPIHandler(t *testing.T) {
	// Test with nil dependencies
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil)
	if h == nil {
		t.Fatal("NewAPIHandler returned nil")
	}

	// Check that deviceAuthService is initialized
	if h.deviceAuthService == nil {
		t.Error("deviceAuthService should be initialized")
	}
}

func TestAPIHandler_SetAlertChannelReloader(t *testing.T) {
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil)

	reloader := func() {
		// Reloader function
	}

	h.SetAlertChannelReloader(reloader)

	if h.alertChannelReloader == nil {
		t.Error("alertChannelReloader should be set")
	}
}

func TestAPIHandler_reloadAlertChannels(t *testing.T) {
	t.Run("with nil reloader", func(t *testing.T) {
		h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil)
		// Should not panic
		h.reloadAlertChannels()
	})

	t.Run("with reloader set", func(t *testing.T) {
		h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil)
		
		h.SetAlertChannelReloader(func() {
			// Reloader called in goroutine
		})

		// Should not panic
		h.reloadAlertChannels()
	})
}

func TestModelConfigs(t *testing.T) {
	// Verify that expected models exist
	expectedModels := []string{
		"gpt-5.2",
		"gpt-5.2-codex",
		"gpt-5.1-codex-max",
		"gpt-5.1-codex",
		"gpt-5.1-codex-mini",
		"gpt-5.1",
	}

	for _, model := range expectedModels {
		if _, ok := ModelConfigs[model]; !ok {
			t.Errorf("Expected model %q not found in ModelConfigs", model)
		}
	}

	// Verify each model has at least one reasoning effort option
	for model, efforts := range ModelConfigs {
		if len(efforts) == 0 {
			t.Errorf("Model %q has no reasoning effort options", model)
		}
	}
}
