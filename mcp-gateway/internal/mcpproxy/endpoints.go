package mcpproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"time"
)

// TestConnectionTimeout bounds POST /mcp-servers/test.
const TestConnectionTimeout = 20 * time.Second

// TestServerRequest is the body of POST /mcp-servers/test.
type TestServerRequest struct {
	ID         *uint           `json:"id,omitempty"`
	URL        string          `json:"url"`
	Transport  TransportType   `json:"transport"`
	AuthConfig json.RawMessage `json:"auth_config,omitempty"`
}

// TestServerTool is one tool in a successful test result.
type TestServerTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// TestServerResult is the response of POST /mcp-servers/test (always HTTP 200).
type TestServerResult struct {
	OK              bool             `json:"ok"`
	ServerInfo      *ServerInfo      `json:"server_info,omitempty"`
	ProtocolVersion string           `json:"protocol_version,omitempty"`
	Tools           []TestServerTool `json:"tools,omitempty"`
	Error           string           `json:"error,omitempty"`
	AuthRequired    bool             `json:"auth_required"`
	WWWAuthenticate string           `json:"www_authenticate,omitempty"`
	StatusCode      int              `json:"status_code,omitempty"`
}

// StoredConfigLookup loads a stored MCP server config (URL, transport and
// auth_config) by its database ID.
type StoredConfigLookup func(ctx context.Context, id uint) (MCPServerConfig, error)

// TestServer opens a throwaway connection, runs initialize and tools/list,
// and reports the result. It never touches the pool's stored connections.
func (h *ProxyHandler) TestServer(ctx context.Context, req TestServerRequest, lookup StoredConfigLookup) TestServerResult {
	ctx, cancel := context.WithTimeout(ctx, TestConnectionTimeout)
	defer cancel()

	cfg := MCPServerConfig{Transport: req.Transport, URL: req.URL, AuthConfig: req.AuthConfig}
	if req.ID != nil && *req.ID > 0 {
		if lookup == nil {
			return TestServerResult{Error: "stored config lookup is not available"}
		}
		stored, err := lookup(ctx, *req.ID)
		if err != nil {
			return TestServerResult{Error: fmt.Sprintf("load MCP server config %d: %v", *req.ID, err)}
		}
		// Start from the stored server and apply each override on its own:
		// a new token with no URL tests the new token against the stored URL.
		cfg = stored
		if req.URL != "" {
			cfg.URL = req.URL
		}
		if len(req.AuthConfig) != 0 {
			cfg.AuthConfig = req.AuthConfig
		}
		if req.Transport != "" {
			cfg.Transport = req.Transport
		}
	}
	if cfg.Transport == "" {
		cfg.Transport = TransportHTTP
	}

	probe, err := h.pool.Probe(ctx, cfg)
	if err != nil {
		res := TestServerResult{Error: err.Error()}
		var statusErr *HTTPStatusError
		if errors.As(err, &statusErr) {
			res.StatusCode = statusErr.StatusCode
			if statusErr.StatusCode == http.StatusUnauthorized {
				res.AuthRequired = true
				res.WWWAuthenticate = statusErr.WWWAuthenticate
			}
		}
		if errors.Is(err, context.DeadlineExceeded) {
			res.Error = fmt.Sprintf("timed out after %s: %v", TestConnectionTimeout, err)
		}
		return res
	}

	info := probe.ServerInfo
	tools := make([]TestServerTool, 0, len(probe.Tools))
	for _, t := range probe.Tools {
		tools = append(tools, TestServerTool{Name: t.Name, Description: t.Description})
	}
	return TestServerResult{
		OK:              true,
		ServerInfo:      &info,
		ProtocolVersion: probe.ProtocolVersion,
		Tools:           tools,
	}
}

// TestHTTPHandler serves POST /mcp-servers/test.
func (h *ProxyHandler) TestHTTPHandler(lookup StoredConfigLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read body"})
			return
		}
		var req TestServerRequest
		if len(bytes.TrimSpace(body)) > 0 {
			if err := json.Unmarshal(body, &req); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
				return
			}
		}
		res := h.TestServer(r.Context(), req, lookup)
		if !res.OK {
			h.logger.Info("MCP server test failed", "host", urlHost(req.URL), "status_code", res.StatusCode)
		}
		writeJSON(w, http.StatusOK, res)
	}
}

// ServerStatus is one entry of GET /mcp-servers/status.
type ServerStatus struct {
	ID              uint     `json:"id"`
	NamespacePrefix string   `json:"namespace_prefix"`
	Connected       bool     `json:"connected"`
	ToolCount       int      `json:"tool_count"`
	Tools           []string `json:"tools"`
	Error           string   `json:"error"`
	CheckedAt       string   `json:"checked_at"`
}

// Status reports every loaded database registration, including the ones that
// failed to connect. "connected" means the last attempt succeeded and the pool
// can serve calls (an idle-closed connection reconnects on demand).
func (h *ProxyHandler) Status() []ServerStatus {
	h.mu.RLock()
	regs := append([]ServerRegistration(nil), h.registrations...)
	toolsByID := make(map[uint][]string)
	for _, entry := range h.toolMap {
		toolsByID[entry.instanceID] = append(toolsByID[entry.instanceID], entry.originalName)
	}
	statuses := make(map[uint]registrationStatus, len(h.status))
	for id, st := range h.status {
		statuses[id] = st
	}
	h.mu.RUnlock()

	out := make([]ServerStatus, 0, len(regs))
	for _, reg := range regs {
		st := statuses[reg.InstanceID]
		tools := toolsByID[reg.InstanceID]
		sort.Strings(tools)
		if tools == nil {
			tools = []string{}
		}
		checked := ""
		if !st.checkedAt.IsZero() {
			checked = st.checkedAt.Format(time.RFC3339)
		}
		out = append(out, ServerStatus{
			ID:              reg.InstanceID,
			NamespacePrefix: reg.NamespacePrefix,
			Connected:       st.err == "" && !st.checkedAt.IsZero() && (h.pool.IsConnected(reg.InstanceID) || h.pool.HasConfig(reg.InstanceID)),
			ToolCount:       len(tools),
			Tools:           tools,
			Error:           st.err,
			CheckedAt:       checked,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// StatusHTTPHandler serves GET /mcp-servers/status.
func (h *ProxyHandler) StatusHTTPHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"servers": h.Status()})
	}
}

// urlHost returns only the host of raw, so logs never carry query secrets.
func urlHost(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Host
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
