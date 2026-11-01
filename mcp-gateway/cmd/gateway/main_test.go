package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInternalOnly(t *testing.T) {
	h := internalOnly(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	cases := []struct {
		name   string
		header string
		want   int
	}{
		{"direct call from the API", "", http.StatusOK},
		{"via proxy X-Forwarded-For", "X-Forwarded-For", http.StatusForbidden},
		{"via proxy X-Real-IP", "X-Real-IP", http.StatusForbidden},
		{"via proxy Forwarded", "Forwarded", http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/mcp-servers/test", nil)
			if tc.header != "" {
				r.Header.Set(tc.header, "203.0.113.7")
			}
			w := httptest.NewRecorder()
			h(w, r)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d", w.Code, tc.want)
			}
		})
	}
}
