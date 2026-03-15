package httpconnector

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/akmatori/mcp-gateway/internal/cache"
	"github.com/akmatori/mcp-gateway/internal/ratelimit"
)

// Cache TTL constants
const (
	ResponseCacheTTL = 30 * time.Second // Cache GET responses for 30 seconds
	CacheCleanupTick = time.Minute      // Background cleanup interval
)

// AuthMethod represents how the executor should authenticate requests
type AuthMethod string

const (
	AuthBearer AuthMethod = "bearer_token"
	AuthBasic  AuthMethod = "basic_auth"
	AuthAPIKey AuthMethod = "api_key"
)

// AuthConfig holds authentication configuration for an HTTP connector
type AuthConfig struct {
	Method     AuthMethod `json:"method"`
	TokenField string     `json:"token_field,omitempty"` // field name in credentials holding the token/key
	HeaderName string     `json:"header_name,omitempty"` // custom header name (for api_key method)
}

// ToolParam defines a parameter for an HTTP connector tool
type ToolParam struct {
	Name     string      `json:"name"`
	Type     string      `json:"type"`
	Required bool        `json:"required"`
	In       string      `json:"in"` // path, query, body, header
	Default  interface{} `json:"default,omitempty"`
}

// ToolDef defines a single tool within an HTTP connector
type ToolDef struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	HTTPMethod  string      `json:"http_method"` // GET, POST, PUT, DELETE
	Path        string      `json:"path"`        // URL path with {{param}} templates
	Params      []ToolParam `json:"params,omitempty"`
	ReadOnly    *bool       `json:"read_only,omitempty"` // default true
}

// IsReadOnly returns whether this tool is read-only (defaults to true if not set)
func (d ToolDef) IsReadOnly() bool {
	if d.ReadOnly == nil {
		return true
	}
	return *d.ReadOnly
}

// ConnectorDef holds the full connector definition needed for execution
type ConnectorDef struct {
	ToolTypeName string     `json:"tool_type_name"`
	BaseURL      string     `json:"base_url"` // resolved base URL (from instance settings)
	AuthConfig   *AuthConfig `json:"auth_config,omitempty"`
	Tools        []ToolDef  `json:"tools"`
}

// Credentials holds resolved credential values from the tool instance settings
type Credentials map[string]string

// ExecuteResult holds the result of an HTTP connector tool execution
type ExecuteResult struct {
	StatusCode int             `json:"status_code"`
	Body       json.RawMessage `json:"body,omitempty"`
	Headers    http.Header     `json:"headers,omitempty"`
}

// HTTPConnectorExecutor executes declarative HTTP connector tool calls
type HTTPConnectorExecutor struct {
	client        *http.Client
	responseCache *cache.Cache
	mu            sync.RWMutex
	rateLimiters  map[string]*ratelimit.Limiter // per connector instance
}

// New creates a new HTTPConnectorExecutor
func New() *HTTPConnectorExecutor {
	return &HTTPConnectorExecutor{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		responseCache: cache.New(ResponseCacheTTL, CacheCleanupTick),
		rateLimiters:  make(map[string]*ratelimit.Limiter),
	}
}

// NewWithClient creates a new HTTPConnectorExecutor with a custom HTTP client (useful for testing)
func NewWithClient(client *http.Client) *HTTPConnectorExecutor {
	return &HTTPConnectorExecutor{
		client:        client,
		responseCache: cache.New(ResponseCacheTTL, CacheCleanupTick),
		rateLimiters:  make(map[string]*ratelimit.Limiter),
	}
}

// Stop cleans up cache resources
func (e *HTTPConnectorExecutor) Stop() {
	if e.responseCache != nil {
		e.responseCache.Stop()
	}
}

// getRateLimiter returns or creates a rate limiter for the given connector instance
func (e *HTTPConnectorExecutor) getRateLimiter(instanceKey string) *ratelimit.Limiter {
	e.mu.RLock()
	limiter, ok := e.rateLimiters[instanceKey]
	e.mu.RUnlock()
	if ok {
		return limiter
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if limiter, ok = e.rateLimiters[instanceKey]; ok {
		return limiter
	}
	limiter = ratelimit.New(10, 20) // 10 req/sec, burst 20
	e.rateLimiters[instanceKey] = limiter
	return limiter
}

// Execute runs a tool call against an HTTP connector
func (e *HTTPConnectorExecutor) Execute(ctx context.Context, connector ConnectorDef, toolName string, args map[string]interface{}, creds Credentials) (*ExecuteResult, error) {
	// Find the tool definition
	var toolDef *ToolDef
	for i := range connector.Tools {
		if connector.Tools[i].Name == toolName {
			toolDef = &connector.Tools[i]
			break
		}
	}
	if toolDef == nil {
		return nil, fmt.Errorf("tool %q not found in connector %q", toolName, connector.ToolTypeName)
	}

	// Enforce read-only: reject non-GET requests for read-only tools
	if toolDef.IsReadOnly() && toolDef.HTTPMethod != "GET" {
		return nil, fmt.Errorf("tool %q is read-only but uses HTTP method %s; set read_only to false to allow writes", toolName, toolDef.HTTPMethod)
	}

	// Apply rate limiting — key by connector type + instance ID to isolate
	// rate limits and caching across different tool instances of the same connector.
	instanceKey := connector.ToolTypeName
	if v, ok := args["tool_instance_id"].(float64); ok && v > 0 {
		instanceKey = fmt.Sprintf("%s:inst:%d", connector.ToolTypeName, int(v))
	} else if v, ok := args["logical_name"].(string); ok && v != "" {
		instanceKey = fmt.Sprintf("%s:ln:%s", connector.ToolTypeName, v)
	}
	limiter := e.getRateLimiter(instanceKey)
	if err := limiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait cancelled: %w", err)
	}

	// Build the request
	req, err := e.buildRequest(ctx, connector, toolDef, args, creds)
	if err != nil {
		return nil, fmt.Errorf("failed to build request: %w", err)
	}

	// Check cache for GET requests (include instanceKey to avoid cross-instance leakage)
	if toolDef.HTTPMethod == "GET" {
		cacheKey := instanceKey + ":" + req.URL.String()
		if cached, ok := e.responseCache.Get(cacheKey); ok {
			if result, ok := cached.(*ExecuteResult); ok {
				return result, nil
			}
		}
	}

	// Execute the request
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response body (limit to 10MB to prevent OOM from large responses)
	const maxResponseBytes = 10 * 1024 * 1024
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	if len(body) > maxResponseBytes {
		return nil, fmt.Errorf("response body exceeds %d bytes limit", maxResponseBytes)
	}

	result := &ExecuteResult{
		StatusCode: resp.StatusCode,
		Headers:    resp.Header,
	}

	// Try to parse as JSON, otherwise wrap as string
	if json.Valid(body) {
		result.Body = json.RawMessage(body)
	} else {
		quoted, _ := json.Marshal(string(body))
		result.Body = json.RawMessage(quoted)
	}

	// Cache GET responses (include instanceKey to avoid cross-instance leakage)
	if toolDef.HTTPMethod == "GET" {
		cacheKey := instanceKey + ":" + req.URL.String()
		e.responseCache.Set(cacheKey, result)
	}

	return result, nil
}

// buildRequest constructs the HTTP request from the connector definition, tool definition, args, and credentials
func (e *HTTPConnectorExecutor) buildRequest(ctx context.Context, connector ConnectorDef, toolDef *ToolDef, args map[string]interface{}, creds Credentials) (*http.Request, error) {
	// Resolve path template
	path := resolvePath(toolDef.Path, toolDef.Params, args)

	// Build full URL
	fullURL := strings.TrimRight(connector.BaseURL, "/") + "/" + strings.TrimLeft(path, "/")

	// Collect query, body, and header params
	queryParams := url.Values{}
	bodyParams := make(map[string]interface{})
	headerParams := make(map[string]string)

	for _, param := range toolDef.Params {
		val, hasVal := args[param.Name]
		if !hasVal {
			if param.Default != nil {
				val = param.Default
			} else if param.Required {
				return nil, fmt.Errorf("required parameter %q is missing", param.Name)
			} else {
				continue
			}
		}

		switch param.In {
		case "query":
			queryParams.Set(param.Name, fmt.Sprintf("%v", val))
		case "body":
			bodyParams[param.Name] = val
		case "header":
			headerParams[param.Name] = fmt.Sprintf("%v", val)
		case "path":
			// Already handled in resolvePath
		}
	}

	// Append query params to URL
	if len(queryParams) > 0 {
		parsedURL, err := url.Parse(fullURL)
		if err != nil {
			return nil, fmt.Errorf("invalid URL %q: %w", fullURL, err)
		}
		existing := parsedURL.Query()
		for k, v := range queryParams {
			for _, vv := range v {
				existing.Set(k, vv)
			}
		}
		parsedURL.RawQuery = existing.Encode()
		fullURL = parsedURL.String()
	}

	// Build request body for POST/PUT
	var reqBody io.Reader
	if len(bodyParams) > 0 && (toolDef.HTTPMethod == "POST" || toolDef.HTTPMethod == "PUT") {
		bodyJSON, err := json.Marshal(bodyParams)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal body params: %w", err)
		}
		reqBody = bytes.NewReader(bodyJSON)
	}

	req, err := http.NewRequestWithContext(ctx, toolDef.HTTPMethod, fullURL, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set content type for POST/PUT
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	// Set header params
	for k, v := range headerParams {
		req.Header.Set(k, v)
	}

	// Inject authentication
	if connector.AuthConfig != nil {
		if err := injectAuth(req, connector.AuthConfig, creds); err != nil {
			return nil, fmt.Errorf("auth injection failed: %w", err)
		}
	}

	return req, nil
}

// resolvePath replaces {{param}} templates in the path with actual values from args
func resolvePath(pathTemplate string, params []ToolParam, args map[string]interface{}) string {
	result := pathTemplate
	for _, param := range params {
		if param.In != "path" {
			continue
		}
		placeholder := "{{" + param.Name + "}}"
		val, ok := args[param.Name]
		if !ok && param.Default != nil {
			val = param.Default
		}
		if val != nil {
			result = strings.ReplaceAll(result, placeholder, url.PathEscape(fmt.Sprintf("%v", val)))
		}
	}
	return result
}

// injectAuth adds authentication headers to the request based on the auth config
func injectAuth(req *http.Request, authConfig *AuthConfig, creds Credentials) error {
	if authConfig.Method == "" {
		return nil
	}

	tokenField := authConfig.TokenField
	if tokenField == "" {
		tokenField = "token" // default field name
	}

	switch authConfig.Method {
	case AuthBearer:
		token, ok := creds[tokenField]
		if !ok || token == "" {
			return fmt.Errorf("bearer token not found in credentials field %q", tokenField)
		}
		req.Header.Set("Authorization", "Bearer "+token)

	case AuthBasic:
		username := creds["username"]
		password := creds["password"]
		if username == "" {
			return fmt.Errorf("username not found in credentials for basic auth")
		}
		encoded := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
		req.Header.Set("Authorization", "Basic "+encoded)

	case AuthAPIKey:
		token, ok := creds[tokenField]
		if !ok || token == "" {
			return fmt.Errorf("API key not found in credentials field %q", tokenField)
		}
		headerName := authConfig.HeaderName
		if headerName == "" {
			headerName = "X-API-Key"
		}
		req.Header.Set(headerName, token)

	default:
		return fmt.Errorf("unsupported auth method: %q", authConfig.Method)
	}

	return nil
}
