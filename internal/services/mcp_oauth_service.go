package services

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/gorm"
)

// MCP OAuth: Akmatori acts as an OAuth 2.1 public client for remote MCP
// servers that require it (MCP authorization spec, 2025-06-18):
//
//  1. discover the protected resource metadata (RFC 9728) of the server,
//     falling back to the server origin as the authorization server
//  2. read the authorization server metadata (RFC 8414 / OIDC discovery)
//  3. use the operator's client_id, or register one dynamically (RFC 7591)
//  4. send the operator's browser to the authorization endpoint with PKCE
//     (S256) and the resource indicator (RFC 8707)
//  5. exchange the code at /api/mcp-servers/oauth/callback and store the
//     tokens in auth_config (type "oauth"); the gateway sends the access
//     token as a Bearer token
//  6. refresh tokens in the background before they expire
//
// The redirect URI is <General settings base URL>/api/mcp-servers/oauth/callback,
// so the base URL must be set and reachable from the operator's browser.

// OAuthCallbackPath is the public (no login) callback route.
const OAuthCallbackPath = "/api/mcp-servers/oauth/callback"

const (
	oauthFlowTTL        = 15 * time.Minute
	oauthRefreshEvery   = time.Minute
	oauthRefreshLead    = 5 * time.Minute
	oauthHTTPTimeout    = 15 * time.Second
	oauthMaxBody        = 1 << 20
	oauthClientName     = "Akmatori"
	oauthDefaultTokenTy = "Bearer"
)

// ErrOAuthBaseURL is returned when no external base URL is configured.
var ErrOAuthBaseURL = errors.New("set the external URL in Settings → General first: the OAuth redirect URL is built from it")

// oauthFlow is a pending authorization (keyed by state).
type oauthFlow struct {
	serverID      uint
	verifier      string
	redirectURI   string
	tokenEndpoint string
	clientID      string
	clientSecret  string
	authMethod    string
	resource      string
	issuer        string
	scope         string
	created       time.Time
}

// MCPOAuthService runs the OAuth flow and token refresh for MCP servers.
type MCPOAuthService struct {
	db       *gorm.DB
	mcp      *MCPServerService
	reload   func() error
	http     *http.Client
	baseURL  func() string
	mu       sync.Mutex
	flows    map[string]*oauthFlow
	now      func() time.Time
	stopOnce sync.Once
	stop     chan struct{}
}

// NewMCPOAuthService creates the service. reload asks the gateway to pick
// up new tokens.
func NewMCPOAuthService(db *gorm.DB, mcp *MCPServerService, reload func() error) *MCPOAuthService {
	return &MCPOAuthService{
		db: db, mcp: mcp, reload: reload,
		http:  newOutboundHTTPClient(oauthHTTPTimeout),
		flows: map[string]*oauthFlow{},
		now:   time.Now,
		stop:  make(chan struct{}),
		baseURL: func() string {
			gs, err := database.GetOrCreateGeneralSettings()
			if err != nil || gs == nil {
				return ""
			}
			return strings.TrimRight(strings.TrimSpace(gs.BaseURL), "/")
		},
	}
}

// ---------- outbound HTTP with a network policy ----------

// newOutboundHTTPClient returns a client that refuses loopback, link-local
// (incl. cloud metadata), unspecified and multicast addresses at connect
// time (after DNS), and follows no redirects. MCP_ALLOW_LOOPBACK=true
// allows loopback for local development.
func newOutboundHTTPClient(timeout time.Duration) *http.Client {
	allowLoopback := strings.EqualFold(os.Getenv("MCP_ALLOW_LOOPBACK"), "true")
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return fmt.Errorf("refusing to connect to %s: not an IP", host)
			}
			if blockedOutboundIP(ip, allowLoopback) {
				return fmt.Errorf("refusing to connect to %s: address not allowed", ip)
			}
			return nil
		},
	}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: timeout,
		MaxIdleConns:          10,
		IdleConnTimeout:       60 * time.Second,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("redirects are not followed")
		},
	}
}

func blockedOutboundIP(ip net.IP, allowLoopback bool) bool {
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsLoopback() {
		return !allowLoopback
	}
	return ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() ||
		ip.IsMulticast() || ip.IsInterfaceLocalMulticast()
}

// ---------- discovery ----------

type resourceMetadata struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
	ScopesSupported      []string `json:"scopes_supported"`
}

type asMetadata struct {
	Issuer                        string   `json:"issuer"`
	AuthorizationEndpoint         string   `json:"authorization_endpoint"`
	TokenEndpoint                 string   `json:"token_endpoint"`
	RegistrationEndpoint          string   `json:"registration_endpoint"`
	CodeChallengeMethodsSupported []string `json:"code_challenge_methods_supported"`
	ScopesSupported               []string `json:"scopes_supported"`
}

func (s *MCPOAuthService) getJSON(ctx context.Context, rawURL string, out interface{}) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("MCP-Protocol-Version", "2025-06-18")
	resp, err := s.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, oauthMaxBody))
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, fmt.Errorf("GET %s returned %d", rawURL, resp.StatusCode)
	}
	return resp.StatusCode, json.Unmarshal(body, out)
}

// wellKnown builds <origin>/.well-known/<name><path> (path insertion).
func wellKnown(u *url.URL, name string) string {
	p := strings.TrimRight(u.Path, "/")
	return u.Scheme + "://" + u.Host + "/.well-known/" + name + p
}

// discoverResource reads the protected resource metadata of the server.
// The server's WWW-Authenticate resource_metadata hint wins when present.
func (s *MCPOAuthService) discoverResource(ctx context.Context, serverURL *url.URL, hint string) (*resourceMetadata, error) {
	candidates := []string{}
	if hint != "" {
		candidates = append(candidates, hint)
	}
	candidates = append(candidates, wellKnown(serverURL, "oauth-protected-resource"))
	if serverURL.Path != "" && serverURL.Path != "/" {
		candidates = append(candidates, serverURL.Scheme+"://"+serverURL.Host+"/.well-known/oauth-protected-resource")
	}
	var lastErr error
	for _, c := range candidates {
		var md resourceMetadata
		if _, err := s.getJSON(ctx, c, &md); err != nil {
			lastErr = err
			continue
		}
		if len(md.AuthorizationServers) > 0 {
			return &md, nil
		}
	}
	return nil, lastErr
}

// discoverAS reads the authorization server metadata of issuer.
func (s *MCPOAuthService) discoverAS(ctx context.Context, issuer string) (*asMetadata, error) {
	u, err := url.Parse(issuer)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("invalid authorization server %q", issuer)
	}
	candidates := []string{
		wellKnown(u, "oauth-authorization-server"),
		wellKnown(u, "openid-configuration"),
		strings.TrimRight(issuer, "/") + "/.well-known/openid-configuration",
	}
	var lastErr error
	for _, c := range candidates {
		var md asMetadata
		if _, err := s.getJSON(ctx, c, &md); err != nil {
			lastErr = err
			continue
		}
		if md.AuthorizationEndpoint != "" && md.TokenEndpoint != "" {
			return &md, nil
		}
		lastErr = fmt.Errorf("%s has no authorization or token endpoint", c)
	}
	return nil, fmt.Errorf("authorization server metadata not found: %w", lastErr)
}

// registerClient performs dynamic client registration.
func (s *MCPOAuthService) registerClient(ctx context.Context, endpoint, redirectURI string) (clientID, clientSecret, authMethod string, err error) {
	body, _ := json.Marshal(map[string]interface{}{
		"client_name":                oauthClientName,
		"redirect_uris":              []string{redirectURI},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return "", "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return "", "", "", fmt.Errorf("client registration failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, oauthMaxBody))
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", "", "", fmt.Errorf("client registration returned %d: %s", resp.StatusCode, truncateBytes(raw, 200))
	}
	var out struct {
		ClientID                string `json:"client_id"`
		ClientSecret            string `json:"client_secret"`
		TokenEndpointAuthMethod string `json:"token_endpoint_auth_method"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.ClientID == "" {
		return "", "", "", fmt.Errorf("client registration returned no client_id")
	}
	return out.ClientID, out.ClientSecret, out.TokenEndpointAuthMethod, nil
}

// ---------- flow ----------

func randomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func requireHTTPS(raw, what string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("invalid %s %q", what, raw)
	}
	if u.Scheme == "https" {
		return nil
	}
	host := u.Hostname()
	if u.Scheme == "http" && (host == "localhost" || host == "127.0.0.1" || host == "::1") && strings.EqualFold(os.Getenv("MCP_ALLOW_LOOPBACK"), "true") {
		return nil
	}
	return fmt.Errorf("%s must use https: %s", what, raw)
}

// Start begins an authorization for server id and returns the URL to send
// the operator's browser to. wwwAuthenticate is the server's 401 header,
// when a connection test returned one.
func (s *MCPOAuthService) Start(ctx context.Context, id uint, wwwAuthenticate string) (string, error) {
	base := s.baseURL()
	if base == "" {
		return "", ErrOAuthBaseURL
	}
	cfg, err := s.mcp.GetMCPServer(id)
	if err != nil {
		return "", err
	}
	serverURL, err := url.Parse(cfg.URL)
	if err != nil {
		return "", fmt.Errorf("invalid server URL: %w", err)
	}
	redirectURI := base + OAuthCallbackPath

	resource := strings.TrimRight(cfg.URL, "/")
	issuer := serverURL.Scheme + "://" + serverURL.Host
	var scopes []string
	if md, err := s.discoverResource(ctx, serverURL, resourceMetadataHint(wwwAuthenticate)); err == nil {
		issuer = md.AuthorizationServers[0]
		if md.Resource != "" {
			resource = md.Resource
		}
		scopes = md.ScopesSupported
	} else {
		slog.Info("MCP OAuth: no protected resource metadata, using the server origin as authorization server", "server", cfg.Name, "err", err)
	}
	as, err := s.discoverAS(ctx, issuer)
	if err != nil {
		return "", err
	}
	if err := requireHTTPS(as.AuthorizationEndpoint, "authorization endpoint"); err != nil {
		return "", err
	}
	if err := requireHTTPS(as.TokenEndpoint, "token endpoint"); err != nil {
		return "", err
	}
	if len(as.CodeChallengeMethodsSupported) > 0 && !containsStr(as.CodeChallengeMethodsSupported, "S256") {
		return "", fmt.Errorf("the authorization server does not support PKCE S256")
	}

	stored := cfg.AuthConfig
	clientID, _ := stored["client_id"].(string)
	clientSecret, _ := stored["client_secret"].(string)
	authMethod, _ := stored["token_endpoint_auth_method"].(string)
	storedRedirect, _ := stored["redirect_uri"].(string)
	storedIssuer, _ := stored["issuer"].(string)
	registered, _ := stored["client_registered"].(bool)
	// A dynamically registered client is bound to its redirect URI and issuer.
	if registered && (storedRedirect != redirectURI || storedIssuer != as.Issuer) {
		clientID, clientSecret, authMethod = "", "", ""
	}
	if clientID == "" {
		if as.RegistrationEndpoint == "" {
			return "", fmt.Errorf("the authorization server does not support dynamic client registration; enter a client ID for this server")
		}
		if err := requireHTTPS(as.RegistrationEndpoint, "registration endpoint"); err != nil {
			return "", err
		}
		clientID, clientSecret, authMethod, err = s.registerClient(ctx, as.RegistrationEndpoint, redirectURI)
		if err != nil {
			return "", err
		}
		registered = true
	}
	scope, _ := stored["scope"].(string)
	if scope == "" && len(scopes) > 0 {
		scope = strings.Join(scopes, " ")
	}

	// Remember the client (not the tokens) right away, so a retry reuses it.
	next := database.JSONB{}
	for k, v := range stored {
		next[k] = v
	}
	next["type"] = "oauth"
	next["client_id"] = clientID
	if clientSecret != "" {
		next["client_secret"] = clientSecret
	}
	if authMethod != "" {
		next["token_endpoint_auth_method"] = authMethod
	}
	next["client_registered"] = registered
	next["redirect_uri"] = redirectURI
	next["issuer"] = as.Issuer
	next["token_endpoint"] = as.TokenEndpoint
	next["resource"] = resource
	if err := s.mcp.StoreOAuthTokens(id, next); err != nil {
		return "", err
	}

	verifier := randomToken(48)
	sum := sha256.Sum256([]byte(verifier))
	state := randomToken(24)
	s.mu.Lock()
	s.gcFlowsLocked()
	s.flows[state] = &oauthFlow{
		serverID: id, verifier: verifier, redirectURI: redirectURI, tokenEndpoint: as.TokenEndpoint,
		clientID: clientID, clientSecret: clientSecret, authMethod: authMethod, resource: resource,
		issuer: as.Issuer, scope: scope, created: s.now(),
	}
	s.mu.Unlock()

	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(sum[:]))
	q.Set("code_challenge_method", "S256")
	q.Set("state", state)
	q.Set("resource", resource)
	if scope != "" {
		q.Set("scope", scope)
	}
	sep := "?"
	if strings.Contains(as.AuthorizationEndpoint, "?") {
		sep = "&"
	}
	return as.AuthorizationEndpoint + sep + q.Encode(), nil
}

func (s *MCPOAuthService) gcFlowsLocked() {
	for k, f := range s.flows {
		if s.now().Sub(f.created) > oauthFlowTTL {
			delete(s.flows, k)
		}
	}
}

// resourceMetadataHint extracts resource_metadata="..." from a
// WWW-Authenticate header.
func resourceMetadataHint(h string) string {
	const key = "resource_metadata="
	i := strings.Index(h, key)
	if i < 0 {
		return ""
	}
	v := h[i+len(key):]
	if strings.HasPrefix(v, `"`) {
		v = v[1:]
		if j := strings.Index(v, `"`); j >= 0 {
			return v[:j]
		}
		return ""
	}
	if j := strings.IndexAny(v, ", "); j >= 0 {
		v = v[:j]
	}
	return v
}

func containsStr(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

type tokenResponse struct {
	AccessToken  string      `json:"access_token"`
	TokenType    string      `json:"token_type"`
	ExpiresIn    json.Number `json:"expires_in"`
	RefreshToken string      `json:"refresh_token"`
	Scope        string      `json:"scope"`
	Error        string      `json:"error"`
	ErrorDesc    string      `json:"error_description"`
}

// tokenRequest posts a token request (code exchange or refresh).
func (s *MCPOAuthService) tokenRequest(ctx context.Context, endpoint, clientID, clientSecret, authMethod string, form url.Values) (*tokenResponse, error) {
	form.Set("client_id", clientID)
	if clientSecret != "" && authMethod != "client_secret_basic" {
		form.Set("client_secret", clientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if clientSecret != "" && authMethod == "client_secret_basic" {
		req.SetBasicAuth(url.QueryEscape(clientID), url.QueryEscape(clientSecret))
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, oauthMaxBody))
	var tr tokenResponse
	if err := json.Unmarshal(raw, &tr); err != nil {
		// Some servers answer form-encoded.
		if vals, perr := url.ParseQuery(string(raw)); perr == nil && vals.Get("access_token") != "" {
			tr.AccessToken, tr.TokenType, tr.RefreshToken, tr.Scope = vals.Get("access_token"), vals.Get("token_type"), vals.Get("refresh_token"), vals.Get("scope")
			tr.ExpiresIn = json.Number(vals.Get("expires_in"))
		} else {
			return nil, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, truncateBytes(raw, 200))
		}
	}
	if resp.StatusCode != http.StatusOK || tr.AccessToken == "" {
		msg := tr.Error
		if tr.ErrorDesc != "" {
			msg += ": " + tr.ErrorDesc
		}
		if msg == "" {
			msg = fmt.Sprintf("status %d", resp.StatusCode)
		}
		return &tr, fmt.Errorf("token endpoint error: %s", msg)
	}
	return &tr, nil
}

// applyTokens merges a token response into an auth_config.
func (s *MCPOAuthService) applyTokens(cfg database.JSONB, tr *tokenResponse) database.JSONB {
	next := database.JSONB{}
	for k, v := range cfg {
		next[k] = v
	}
	next["type"] = "oauth"
	next["access_token"] = tr.AccessToken
	tt := tr.TokenType
	if tt == "" {
		tt = oauthDefaultTokenTy
	}
	next["token_type"] = tt
	if tr.RefreshToken != "" {
		next["refresh_token"] = tr.RefreshToken
	}
	if tr.Scope != "" {
		next["scope"] = tr.Scope
	}
	delete(next, "expires_at")
	if secs, err := strconv.ParseInt(tr.ExpiresIn.String(), 10, 64); err == nil && secs > 0 {
		next["expires_at"] = s.now().Add(time.Duration(secs) * time.Second).UTC().Format(time.RFC3339)
	}
	delete(next, "oauth_error")
	return next
}

// Callback completes the flow for state with code. It returns the server
// ID so the caller can redirect to its connector.
func (s *MCPOAuthService) Callback(ctx context.Context, state, code string) (uint, error) {
	s.mu.Lock()
	flow, ok := s.flows[state]
	if ok {
		delete(s.flows, state)
	}
	s.mu.Unlock()
	if !ok || s.now().Sub(flow.created) > oauthFlowTTL {
		return 0, fmt.Errorf("this sign-in link has expired or was already used; start again from the connector page")
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", flow.redirectURI)
	form.Set("code_verifier", flow.verifier)
	form.Set("resource", flow.resource)
	tr, err := s.tokenRequest(ctx, flow.tokenEndpoint, flow.clientID, flow.clientSecret, flow.authMethod, form)
	if err != nil {
		return flow.serverID, err
	}
	cfg, err := s.mcp.GetMCPServer(flow.serverID)
	if err != nil {
		return flow.serverID, err
	}
	if err := s.mcp.StoreOAuthTokens(flow.serverID, s.applyTokens(cfg.AuthConfig, tr)); err != nil {
		return flow.serverID, err
	}
	s.reloadGateway()
	return flow.serverID, nil
}

// Cancel drops a pending flow (the authorization server returned an
// error) and returns its server ID, or 0.
func (s *MCPOAuthService) Cancel(state string) uint {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f, ok := s.flows[state]; ok {
		delete(s.flows, state)
		return f.serverID
	}
	return 0
}

// Disconnect forgets the tokens of server id. The client registration is
// kept so signing in again does not register a new client.
func (s *MCPOAuthService) Disconnect(id uint) error {
	cfg, err := s.mcp.GetMCPServer(id)
	if err != nil {
		return err
	}
	next := database.JSONB{}
	for k, v := range cfg.AuthConfig {
		next[k] = v
	}
	for _, k := range []string{"access_token", "refresh_token", "expires_at", "oauth_error"} {
		delete(next, k)
	}
	next["type"] = "oauth"
	if err := s.mcp.StoreOAuthTokens(id, next); err != nil {
		return err
	}
	s.reloadGateway()
	return nil
}

func (s *MCPOAuthService) reloadGateway() {
	if s.reload == nil {
		return
	}
	if err := s.reload(); err != nil {
		slog.Warn("MCP OAuth: gateway reload failed", "err", err)
	}
}

// RefreshDue refreshes every OAuth token that expires within the lead time.
func (s *MCPOAuthService) RefreshDue(ctx context.Context) {
	configs, err := s.mcp.ListMCPServers()
	if err != nil {
		slog.Warn("MCP OAuth refresh: list servers failed", "err", err)
		return
	}
	changed := false
	for _, cfg := range configs {
		a := cfg.AuthConfig
		if a == nil || a["type"] != "oauth" || !cfg.Enabled {
			continue
		}
		refresh, _ := a["refresh_token"].(string)
		expRaw, _ := a["expires_at"].(string)
		endpoint, _ := a["token_endpoint"].(string)
		if refresh == "" || expRaw == "" || endpoint == "" {
			continue
		}
		exp, err := time.Parse(time.RFC3339, expRaw)
		if err != nil || exp.Sub(s.now()) > oauthRefreshLead {
			continue
		}
		clientID, _ := a["client_id"].(string)
		secret, _ := a["client_secret"].(string)
		method, _ := a["token_endpoint_auth_method"].(string)
		form := url.Values{}
		form.Set("grant_type", "refresh_token")
		form.Set("refresh_token", refresh)
		if res, _ := a["resource"].(string); res != "" {
			form.Set("resource", res)
		}
		tr, err := s.tokenRequest(ctx, endpoint, clientID, secret, method, form)
		if err != nil {
			slog.Warn("MCP OAuth refresh failed", "server", cfg.Name, "err", err)
			if tr != nil && (tr.Error == "invalid_grant" || tr.Error == "invalid_client") {
				next := database.JSONB{}
				for k, v := range a {
					next[k] = v
				}
				delete(next, "access_token")
				delete(next, "refresh_token")
				next["oauth_error"] = "Sign-in expired. Connect again."
				if err := s.mcp.StoreOAuthTokens(cfg.ID, next); err == nil {
					changed = true
				}
			}
			continue
		}
		if err := s.mcp.StoreOAuthTokens(cfg.ID, s.applyTokens(a, tr)); err != nil {
			slog.Warn("MCP OAuth refresh: store failed", "server", cfg.Name, "err", err)
			continue
		}
		changed = true
	}
	if changed {
		s.reloadGateway()
	}
}

// StartRefresher runs RefreshDue every minute until ctx ends or Stop.
func (s *MCPOAuthService) StartRefresher(ctx context.Context) {
	go func() {
		t := time.NewTicker(oauthRefreshEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.stop:
				return
			case <-t.C:
				rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
				s.RefreshDue(rctx)
				cancel()
			}
		}
	}()
}

// Stop ends the refresher.
func (s *MCPOAuthService) Stop() { s.stopOnce.Do(func() { close(s.stop) }) }
