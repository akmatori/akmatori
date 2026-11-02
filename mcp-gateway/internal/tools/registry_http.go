package tools

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/akmatori/mcp-gateway/internal/mcp"
)

// NamespaceTool is one registered tool with its input schema, as served by
// /registry/tools. The API renders it into the connected-tools skill.
type NamespaceTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema mcp.InputSchema `json:"input_schema"`
}

// NamespaceTools returns every registered tool of one namespace, sorted by
// name, with input schemas.
func (r *Registry) NamespaceTools(namespace string) []NamespaceTool {
	r.server.Mu().RLock()
	defer r.server.Mu().RUnlock()
	out := []NamespaceTool{}
	for _, tool := range r.server.Tools() {
		ns, _ := mcp.ParseToolName(tool.Name)
		if ns != namespace {
			continue
		}
		out = append(out, NamespaceTool{Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// NamespaceToolsHandler serves GET /registry/tools?namespace=<ns>.
func (r *Registry) NamespaceToolsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		ns := req.URL.Query().Get("namespace")
		w.Header().Set("Content-Type", "application/json")
		if ns == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "namespace is required"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"tools": r.NamespaceTools(ns)})
	}
}
