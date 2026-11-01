package mcpproxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// authHeader is one HTTP header the gateway adds to every request sent to an
// external MCP server. The value is a secret and must never be logged.
type authHeader struct {
	name  string
	value string
}

// String hides the secret if the struct is ever printed.
func (a *authHeader) String() string {
	if a == nil {
		return "<none>"
	}
	return a.name + ": <redacted>"
}

func (a *authHeader) apply(h http.Header) {
	if a != nil {
		h.Set(a.name, a.value)
	}
}

// reservedAuthHeaders cannot be set through a "header" auth config because the
// transport owns them.
var reservedAuthHeaders = map[string]bool{
	"Host":                 true,
	"Content-Type":         true,
	"Content-Length":       true,
	"Accept":               true,
	"Connection":           true,
	"Transfer-Encoding":    true,
	"Mcp-Session-Id":       true,
	"Mcp-Protocol-Version": true,
}

// parseAuthConfig turns the stored auth_config JSON into a header. Supported:
//
//	{"type":"bearer","token":"..."}                         -> Authorization: Bearer <token>
//	{"type":"header","header_name":"X-Api-Key","value":"..."} -> X-Api-Key: <value>
//	{"type":"oauth","access_token":"...", ...}              -> Authorization: Bearer <access_token>
//
// null, empty, {} and {"type":"none"} mean no auth. Errors never contain the secret.
func parseAuthConfig(raw json.RawMessage) (*authHeader, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	var cfg struct {
		Type        string `json:"type"`
		Token       string `json:"token"`
		HeaderName  string `json:"header_name"`
		Value       string `json:"value"`
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(trimmed, &cfg); err != nil {
		return nil, fmt.Errorf("auth_config is not a JSON object")
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Type)) {
	case "", "none":
		return nil, nil
	case "bearer":
		if cfg.Token == "" {
			return nil, fmt.Errorf("auth_config: bearer token is empty")
		}
		return &authHeader{name: "Authorization", value: "Bearer " + cfg.Token}, nil
	case "oauth":
		if cfg.AccessToken == "" {
			return nil, fmt.Errorf("auth_config: oauth access_token is empty (authorization not completed?)")
		}
		// token_type is ignored: MCP requires Bearer tokens.
		return &authHeader{name: "Authorization", value: "Bearer " + cfg.AccessToken}, nil
	case "header":
		name := http.CanonicalHeaderKey(strings.TrimSpace(cfg.HeaderName))
		if !validHeaderName(name) {
			return nil, fmt.Errorf("auth_config: invalid header_name %q", cfg.HeaderName)
		}
		if reservedAuthHeaders[name] {
			return nil, fmt.Errorf("auth_config: header %q cannot be used for auth", name)
		}
		if cfg.Value == "" {
			return nil, fmt.Errorf("auth_config: header value is empty")
		}
		if strings.ContainsAny(cfg.Value, "\r\n\x00") {
			return nil, fmt.Errorf("auth_config: header value contains invalid characters")
		}
		return &authHeader{name: name, value: cfg.Value}, nil
	default:
		return nil, fmt.Errorf("auth_config: unsupported type %q", cfg.Type)
	}
}

// validHeaderName checks the RFC 7230 token grammar.
func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("!#$%&'*+-.^_`|~", r):
		default:
			return false
		}
	}
	return true
}
