package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNamespaceToolsHandler(t *testing.T) {
	registry, _ := newTestRegistry()
	h := registry.NamespaceToolsHandler()

	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/registry/tools?namespace=ssh", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body struct {
		Tools []NamespaceTool `json:"tools"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Tools) == 0 {
		t.Fatal("expected ssh tools")
	}
	for i, tool := range body.Tools {
		if tool.Name[:4] != "ssh." {
			t.Errorf("tool %q is not in the ssh namespace", tool.Name)
		}
		if i > 0 && body.Tools[i-1].Name > tool.Name {
			t.Errorf("tools not sorted: %q before %q", body.Tools[i-1].Name, tool.Name)
		}
	}
	if body.Tools[0].Name == "ssh.execute_command" && len(body.Tools[0].InputSchema.Required) == 0 {
		t.Error("input schema missing")
	}

	rec = httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/registry/tools?namespace=nope", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "{\"tools\":[]}\n" {
		t.Errorf("unknown namespace: %d %q", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/registry/tools", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing namespace: status %d", rec.Code)
	}
}
