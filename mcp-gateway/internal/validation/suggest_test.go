package validation

import (
	"strings"
	"testing"
)

func TestSuggestParam(t *testing.T) {
	tests := []struct {
		name          string
		requiredParam string
		args          map[string]interface{}
		wantEmpty     bool   // true → expect ""
		wantContains  string // non-empty → suggestion must contain this substring
	}{
		{
			name:          "substring match: label suggests label_name",
			requiredParam: "label_name",
			args:          map[string]interface{}{"label": "job"},
			wantEmpty:     false,
			wantContains:  "label",
		},
		{
			name:          "substring match: match[] suggests match",
			requiredParam: "match",
			args:          map[string]interface{}{"match[]": `{job="api"}`},
			wantEmpty:     false,
			wantContains:  "match[]",
		},
		{
			name:          "prefix match: labels suggests label_name",
			requiredParam: "label_name",
			args:          map[string]interface{}{"labels": "job"},
			wantEmpty:     false,
			wantContains:  "labels",
		},
		{
			name:          "exact match: query with required query returns empty",
			requiredParam: "query",
			args:          map[string]interface{}{"query": ""},
			wantEmpty:     true,
		},
		{
			name:          "substring match: start_time suggests start",
			requiredParam: "start",
			args:          map[string]interface{}{"start_time": "2024-01-01T00:00:00Z"},
			wantEmpty:     false,
			wantContains:  "start_time",
		},
		{
			name:          "tool_instance_id is skipped",
			requiredParam: "query",
			args:          map[string]interface{}{"tool_instance_id": float64(1)},
			wantEmpty:     true,
		},
		{
			name:          "logical_name is skipped",
			requiredParam: "query",
			args:          map[string]interface{}{"logical_name": "prod-vm"},
			wantEmpty:     true,
		},
		{
			name:          "no matching keys returns empty",
			requiredParam: "query",
			args:          map[string]interface{}{"host": "server-1", "port": float64(9090)},
			wantEmpty:     true,
		},
		{
			name:          "empty args returns empty",
			requiredParam: "query",
			args:          map[string]interface{}{},
			wantEmpty:     true,
		},
		{
			name:          "nil args returns empty",
			requiredParam: "query",
			args:          nil,
			wantEmpty:     true,
		},
		{
			name:          "suggestion includes both passed key and required param",
			requiredParam: "label_name",
			args:          map[string]interface{}{"label": "job"},
			wantEmpty:     false,
			wantContains:  "label_name",
		},
		{
			name:          "case-insensitive exact match skipped",
			requiredParam: "Query",
			args:          map[string]interface{}{"query": "up"},
			wantEmpty:     true,
		},
		{
			name:          "case-insensitive substring match: QUERY_STR suggests query",
			requiredParam: "query",
			args:          map[string]interface{}{"QUERY_STR": "up"},
			wantEmpty:     false,
			wantContains:  "QUERY_STR",
		},
		{
			name:          "short key under 3 chars does not trigger prefix match",
			requiredParam: "path",
			args:          map[string]interface{}{"pa": "/api/v1/query"},
			// "pa" is only 2 chars, prefix heuristic requires >= 3 chars.
			// However substring: "path" contains "pa"? Yes it does — "pa" is in "path".
			// So this will match via substring heuristic.
			wantEmpty:    false,
			wantContains: "pa",
		},
		{
			name:          "unrelated short key returns empty",
			requiredParam: "path",
			args:          map[string]interface{}{"xy": "value"},
			wantEmpty:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SuggestParam(tt.requiredParam, tt.args)

			if tt.wantEmpty {
				if got != "" {
					t.Errorf("SuggestParam(%q, %v) = %q, want empty string", tt.requiredParam, tt.args, got)
				}
				return
			}

			// Non-empty expected: must be non-empty and contain the expected substring.
			if got == "" {
				t.Errorf("SuggestParam(%q, %v) = \"\", want non-empty suggestion containing %q", tt.requiredParam, tt.args, tt.wantContains)
				return
			}
			if tt.wantContains != "" && !strings.Contains(got, tt.wantContains) {
				t.Errorf("SuggestParam(%q, %v) = %q, want suggestion containing %q", tt.requiredParam, tt.args, got, tt.wantContains)
			}
		})
	}
}

func BenchmarkSuggestParam(b *testing.B) {
	args := map[string]interface{}{
		"tool_instance_id": float64(1),
		"logical_name":     "prod-vm",
		"lable_name":       "job",
		"unrelated":        "value",
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		SuggestParam("label_name", args)
	}
}
