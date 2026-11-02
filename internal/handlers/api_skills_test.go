package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/akmatori/akmatori/internal/services"
)

func TestHandleSkillTools_Gone(t *testing.T) {
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		req := httptest.NewRequest(method, "/api/skills/linux-admin/tools", strings.NewReader(`{"tool_instance_ids":[1]}`))
		w := httptest.NewRecorder()
		h.handleSkillByName(w, req)
		if w.Code != http.StatusGone {
			t.Errorf("%s: status %d, want 410", method, w.Code)
		}
		if !strings.Contains(w.Body.String(), "/connectors") {
			t.Errorf("%s: body %q should point at /connectors", method, w.Body.String())
		}
	}
}

func TestHandleSkillByName_GeneratedSkillReadOnly(t *testing.T) {
	h := NewAPIHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	cases := []struct{ path, body string }{
		{"/api/skills/" + services.ConnectedToolsSkillName, `{"enabled":false}`},
		{"/api/skills/" + services.ConnectedToolsSkillName + "/prompt", `{"prompt":"x"}`},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodPut, c.path, strings.NewReader(c.body))
		w := httptest.NewRecorder()
		h.handleSkillByName(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("PUT %s: status %d, want 403", c.path, w.Code)
		}
	}
}
