package database

import (
	"testing"
)

func TestSlugifyForLogicalName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"simple", "prod-ssh", "prod-ssh"},
		{"mixed case", "Production Zabbix", "production-zabbix"},
		{"special chars", "My Tool (v2.0)!", "my-tool-v2-0"},
		{"multiple spaces", "foo   bar", "foo-bar"},
		{"leading trailing", "---test---", "test"},
		{"empty", "", ""},
		{"numbers", "server123", "server123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SlugifyLogicalName(tt.input)
			if got != tt.expected {
				t.Errorf("SlugifyLogicalName(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}
