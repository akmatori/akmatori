package victoriametrics

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestIsReadOnlyPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/api/v1/query", true},
		{"/api/v1/query_range", true},
		{"/api/v1/query_exemplars", true},
		{"/api/v1/series", true},
		{"/api/v1/series/count", true},
		{"/api/v1/labels", true},
		{"/api/v1/label/__name__/values", true},
		{"/api/v1/label/job/values/", true},
		{"/api/v1/metadata", true},
		{"/api/v1/export", true},
		{"/api/v1/export/csv", true},
		{"/api/v1/export/native", true},
		{"/api/v1/status/tsdb", true},
		{"/api/v1/status/buildinfo", true},
		{"/api/v1/status/active_queries", true},
		{"/federate", true},
		{"/health", true},
		{"/metrics", true},
		{"/prometheus/api/v1/query", true},
		{"/select/0/prometheus/api/v1/query_range", true},
		{"/select/1:2/prometheus/api/v1/label/job/values", true},
		{"//api/v1/query", true},

		// write and admin endpoints
		{"/api/v1/admin/tsdb/delete_series", false},
		{"/prometheus/api/v1/admin/tsdb/delete_series", false},
		{"/select/0/prometheus/api/v1/admin/tsdb/delete_series", false},
		{"/delete/0/prometheus/api/v1/admin/tsdb/delete_series", false},
		{"//api/v1/admin/tsdb/delete_series", false},
		{"/api/v1/import", false},
		{"/api/v1/import/csv", false},
		{"/api/v1/import/native", false},
		{"/api/v1/import/prometheus", false},
		{"/api/v1/write", false},
		{"/insert/0/prometheus/api/v1/write", false},
		{"/influx/write", false},
		{"/write", false},
		{"/snapshot/create", false},
		{"/snapshot/delete", false},
		{"/snapshot/delete_all", false},
		{"/internal/force_merge", false},
		{"/internal/force_flush", false},
		{"/internal/resetRollupResultCache", false},
		{"/api/v1/admin/snapshot", false},

		// shape tricks
		{"", false},
		{"/", false},
		{"/api/v1/query/../admin", false},
		{"/api/v1/queryx", false},
		{"/api/v1/label/a/b/values", false},
		{"/api/v1/status/tsdb/extra", false},
		{"/prometheusx/api/v1/query", false},
		{"/API/V1/QUERY", false},
	}
	for _, tt := range tests {
		if got := IsReadOnlyPath(tt.path); got != tt.want {
			t.Errorf("IsReadOnlyPath(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

// TestAPIRequest_RejectsWriteEndpoints checks that admin and write endpoints
// fail before any HTTP request, for GET and POST, and when percent-encoded.
func TestAPIRequest_RejectsWriteEndpoints(t *testing.T) {
	tool, _, counter := newTestTool(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	paths := []string{
		"/api/v1/admin/tsdb/delete_series",
		"/api/v1/import",
		"/api/v1/write",
		"/snapshot/delete_all",
		"/internal/force_merge",
		"/api/v1/%61dmin/tsdb/delete_series",
		"%2Fapi%2Fv1%2Fadmin%2Ftsdb%2Fdelete_series",
		"/api/v1/%2561dmin/tsdb/delete_series",
	}
	for _, path := range paths {
		for _, method := range []string{"GET", "POST"} {
			_, err := tool.APIRequest(context.Background(), "test-incident", map[string]interface{}{
				"path":   path,
				"method": method,
				"params": map[string]interface{}{"match[]": "up"},
			})
			if err == nil {
				t.Errorf("APIRequest(%s %q) succeeded, want rejection", method, path)
				continue
			}
			if !strings.Contains(err.Error(), "read-only endpoints only") {
				t.Errorf("APIRequest(%s %q) error = %v, want read-only rejection", method, path, err)
			}
		}
	}
	if n := counter.Load(); n != 0 {
		t.Errorf("expected no HTTP requests to VictoriaMetrics, got %d", n)
	}
}
