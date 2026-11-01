package services

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/database"
)

// fakeAuthServer is one TLS server acting as the MCP resource (metadata
// only), the authorization server, its DCR endpoint and its token endpoint.
type fakeAuthServer struct {
	srv           *httptest.Server
	mu            sync.Mutex
	noPRM         bool
	emptyPRM      bool // 200 with {} (no authorization_servers)
	noDCR         bool
	challenge     string // set by the test from the authorize URL
	tokenForms    []url.Values
	registrations int
	refreshFails  bool
}

func newFakeAuthServer(t *testing.T) *fakeAuthServer {
	t.Helper()
	f := &fakeAuthServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, r *http.Request) {
		if f.noPRM {
			http.NotFound(w, r)
			return
		}
		if f.emptyPRM {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"resource":              f.srv.URL + "/mcp",
			"authorization_servers": []string{f.srv.URL + "/as"},
			"scopes_supported":      []string{"read", "write"},
		})
	})
	asMeta := func(w http.ResponseWriter, _ *http.Request) {
		md := map[string]interface{}{
			"issuer":                           f.srv.URL + "/as",
			"authorization_endpoint":           f.srv.URL + "/authorize",
			"token_endpoint":                   f.srv.URL + "/token",
			"code_challenge_methods_supported": []string{"S256"},
		}
		if !f.noDCR {
			md["registration_endpoint"] = f.srv.URL + "/register"
		}
		_ = json.NewEncoder(w).Encode(md)
	}
	mux.HandleFunc("/.well-known/oauth-authorization-server/as", asMeta)
	// Fallback issuer (server origin) when there is no resource metadata.
	mux.HandleFunc("/.well-known/oauth-authorization-server", asMeta)
	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		uris, _ := body["redirect_uris"].([]interface{})
		if len(uris) != 1 || uris[0] != "https://akm.example.com/api/mcp-servers/oauth/callback" {
			http.Error(w, "bad redirect", http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.registrations++
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"client_id": "cid-123"})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		f.tokenForms = append(f.tokenForms, r.PostForm)
		challenge, refreshFails := f.challenge, f.refreshFails
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.PostForm.Get("grant_type") {
		case "authorization_code":
			sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
			if r.PostForm.Get("code") != "the-code" || base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant", "error_description": "bad code or verifier"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"access_token": "at-1", "refresh_token": "rt-1", "token_type": "Bearer", "expires_in": 3600, "scope": "read",
			})
		case "refresh_token":
			if refreshFails || r.PostForm.Get("refresh_token") != "rt-1" {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"access_token": "at-2", "expires_in": 7200})
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	})
	f.srv = httptest.NewTLSServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

type oauthFixture struct {
	svc     *MCPOAuthService
	mcp     *MCPServerService
	auth    *fakeAuthServer
	server  *database.MCPServerConfig
	reloads int
	now     time.Time
}

func newOAuthFixture(t *testing.T) *oauthFixture {
	t.Helper()
	db := setupMCPServerTestDB(t)
	f := &oauthFixture{auth: newFakeAuthServer(t), now: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)}
	f.mcp = NewMCPServerServiceWithDB(db)
	srv, err := f.mcp.CreateMCPServer(&database.MCPServerConfig{Name: "Linear", URL: f.auth.srv.URL + "/mcp"})
	if err != nil {
		t.Fatal(err)
	}
	f.server = srv
	f.svc = NewMCPOAuthService(db, f.mcp, func() error { f.reloads++; return nil })
	f.svc.http = f.auth.srv.Client()
	f.svc.baseURL = func() string { return "https://akm.example.com" }
	f.svc.now = func() time.Time { return f.now }
	return f
}

// start runs Start and returns the parsed authorize query, recording the
// PKCE challenge on the fake server.
func (f *oauthFixture) start(t *testing.T) url.Values {
	t.Helper()
	raw, err := f.svc.Start(context.Background(), f.server.ID, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if u.Path != "/authorize" {
		t.Fatalf("authorize URL path = %q", u.Path)
	}
	q := u.Query()
	f.auth.mu.Lock()
	f.auth.challenge = q.Get("code_challenge")
	f.auth.mu.Unlock()
	return q
}

func TestMCPOAuth_HappyPath(t *testing.T) {
	f := newOAuthFixture(t)
	q := f.start(t)

	checks := map[string]string{
		"response_type":         "code",
		"client_id":             "cid-123",
		"redirect_uri":          "https://akm.example.com/api/mcp-servers/oauth/callback",
		"code_challenge_method": "S256",
		"resource":              f.auth.srv.URL + "/mcp",
		"scope":                 "read write",
	}
	for k, want := range checks {
		if got := q.Get(k); got != want {
			t.Errorf("authorize %s = %q, want %q", k, got, want)
		}
	}
	if q.Get("state") == "" || len(q.Get("code_challenge")) < 40 {
		t.Fatalf("missing state or challenge: %v", q)
	}

	// The client registration is stored right away (no tokens yet).
	stored, _ := f.mcp.GetMCPServer(f.server.ID)
	if stored.AuthConfig["client_id"] != "cid-123" || stored.AuthConfig["type"] != "oauth" || stored.AuthConfig["access_token"] != nil {
		t.Fatalf("auth after Start = %v", stored.AuthConfig)
	}

	id, err := f.svc.Callback(context.Background(), q.Get("state"), "the-code")
	if err != nil || id != f.server.ID {
		t.Fatalf("Callback = %d, %v", id, err)
	}
	stored, _ = f.mcp.GetMCPServer(f.server.ID)
	a := stored.AuthConfig
	if a["access_token"] != "at-1" || a["refresh_token"] != "rt-1" || a["scope"] != "read" || a["token_type"] != "Bearer" {
		t.Fatalf("tokens not stored: %v", a)
	}
	if a["expires_at"] != f.now.Add(time.Hour).Format(time.RFC3339) {
		t.Fatalf("expires_at = %v", a["expires_at"])
	}
	if f.reloads != 1 {
		t.Fatalf("gateway reloads = %d, want 1", f.reloads)
	}
	form := f.auth.tokenForms[0]
	if form.Get("resource") != f.auth.srv.URL+"/mcp" || form.Get("client_id") != "cid-123" || form.Get("redirect_uri") == "" {
		t.Fatalf("token form = %v", form)
	}

	// The state is single-use.
	if _, err := f.svc.Callback(context.Background(), q.Get("state"), "the-code"); err == nil {
		t.Fatal("state reused")
	}

	// Signing in again reuses the registered client.
	f.start(t)
	if f.auth.registrations != 1 {
		t.Fatalf("registrations = %d, want 1", f.auth.registrations)
	}

	// Disconnect drops tokens, keeps the client.
	if err := f.svc.Disconnect(f.server.ID); err != nil {
		t.Fatal(err)
	}
	stored, _ = f.mcp.GetMCPServer(f.server.ID)
	if stored.AuthConfig["access_token"] != nil || stored.AuthConfig["client_id"] != "cid-123" {
		t.Fatalf("after disconnect: %v", stored.AuthConfig)
	}
}

func TestMCPOAuth_CallbackFailures(t *testing.T) {
	f := newOAuthFixture(t)
	if _, err := f.svc.Callback(context.Background(), "unknown", "the-code"); err == nil {
		t.Fatal("unknown state accepted")
	}

	q := f.start(t)
	if _, err := f.svc.Callback(context.Background(), q.Get("state"), "wrong-code"); err == nil || !strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("bad code err = %v", err)
	}

	q = f.start(t)
	f.now = f.now.Add(oauthFlowTTL + time.Minute)
	if _, err := f.svc.Callback(context.Background(), q.Get("state"), "the-code"); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expired state err = %v", err)
	}

	q = f.start(t)
	if got := f.svc.Cancel(q.Get("state")); got != f.server.ID {
		t.Fatalf("Cancel = %d", got)
	}
	if got := f.svc.Cancel(q.Get("state")); got != 0 {
		t.Fatalf("second Cancel = %d", got)
	}
}

func TestMCPOAuth_StartErrors(t *testing.T) {
	f := newOAuthFixture(t)
	f.svc.baseURL = func() string { return "" }
	if _, err := f.svc.Start(context.Background(), f.server.ID, ""); !errors.Is(err, ErrOAuthBaseURL) {
		t.Fatalf("no base URL err = %v", err)
	}
	f.svc.baseURL = func() string { return "https://akm.example.com" }
	if _, err := f.svc.Start(context.Background(), 999, ""); !errors.Is(err, ErrMCPServerNotFound) {
		t.Fatalf("missing server err = %v", err)
	}
	f.auth.noDCR = true
	if _, err := f.svc.Start(context.Background(), f.server.ID, ""); err == nil || !strings.Contains(err.Error(), "client ID") {
		t.Fatalf("no DCR err = %v", err)
	}
	// With an operator-provided client ID, no registration is needed.
	if err := f.mcp.StoreOAuthTokens(f.server.ID, database.JSONB{"type": "oauth", "client_id": "manual"}); err != nil {
		t.Fatal(err)
	}
	raw, err := f.svc.Start(context.Background(), f.server.ID, "")
	if err != nil || !strings.Contains(raw, "client_id=manual") {
		t.Fatalf("manual client Start = %q %v", raw, err)
	}
}

func TestMCPOAuth_FallsBackToServerOrigin(t *testing.T) {
	f := newOAuthFixture(t)
	f.auth.noPRM = true
	q := f.start(t)
	if q.Get("resource") != f.auth.srv.URL+"/mcp" {
		t.Fatalf("resource = %q", q.Get("resource"))
	}
	if q.Get("scope") != "" {
		t.Fatalf("scope without metadata = %q", q.Get("scope"))
	}
}

func TestMCPOAuth_RefreshDue(t *testing.T) {
	f := newOAuthFixture(t)
	q := f.start(t)
	if _, err := f.svc.Callback(context.Background(), q.Get("state"), "the-code"); err != nil {
		t.Fatal(err)
	}
	f.reloads = 0

	// Not due yet: nothing happens.
	f.svc.RefreshDue(context.Background())
	if f.reloads != 0 || len(f.auth.tokenForms) != 1 {
		t.Fatalf("refreshed too early: reloads=%d forms=%d", f.reloads, len(f.auth.tokenForms))
	}

	// Due: refreshed, refresh token kept when the server omits a new one.
	f.now = f.now.Add(58 * time.Minute)
	f.svc.RefreshDue(context.Background())
	stored, _ := f.mcp.GetMCPServer(f.server.ID)
	if stored.AuthConfig["access_token"] != "at-2" || stored.AuthConfig["refresh_token"] != "rt-1" || f.reloads != 1 {
		t.Fatalf("after refresh: %v reloads=%d", stored.AuthConfig, f.reloads)
	}
	if stored.AuthConfig["expires_at"] != f.now.Add(2*time.Hour).Format(time.RFC3339) {
		t.Fatalf("expires_at = %v", stored.AuthConfig["expires_at"])
	}

	// invalid_grant: tokens cleared, error recorded.
	f.now = f.now.Add(2 * time.Hour)
	f.auth.refreshFails = true
	f.svc.RefreshDue(context.Background())
	stored, _ = f.mcp.GetMCPServer(f.server.ID)
	if stored.AuthConfig["access_token"] != nil || stored.AuthConfig["refresh_token"] != nil || stored.AuthConfig["oauth_error"] == nil {
		t.Fatalf("after invalid_grant: %v", stored.AuthConfig)
	}
	if f.reloads != 2 {
		t.Fatalf("reloads = %d, want 2", f.reloads)
	}
}

func TestResourceMetadataHint(t *testing.T) {
	cases := map[string]string{
		`Bearer resource_metadata="https://x.example.com/.well-known/oauth-protected-resource"`:  "https://x.example.com/.well-known/oauth-protected-resource",
		`Bearer realm="mcp", resource_metadata=https://y.example.com/prm, error="invalid_token"`: "https://y.example.com/prm",
		`Bearer realm="mcp"`:                     "",
		``:                                       "",
		`Bearer resource_metadata="unterminated`: "",
	}
	for in, want := range cases {
		if got := resourceMetadataHint(in); got != want {
			t.Errorf("resourceMetadataHint(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBlockedOutboundIP(t *testing.T) {
	cases := []struct {
		ip            string
		allowLoopback bool
		want          bool
	}{
		{"127.0.0.1", false, true},
		{"127.0.0.1", true, false},
		{"::1", false, true},
		{"169.254.169.254", false, true},
		{"169.254.169.254", true, true},
		{"fe80::1", false, true},
		{"0.0.0.0", false, true},
		{"224.0.0.1", false, true},
		{"::ffff:127.0.0.1", false, true},
		{"10.0.0.5", false, false},
		{"192.168.1.10", false, false},
		{"8.8.8.8", false, false},
		{"2001:4860:4860::8888", false, false},
	}
	for _, c := range cases {
		if got := blockedOutboundIP(net.ParseIP(c.ip), c.allowLoopback); got != c.want {
			t.Errorf("blockedOutboundIP(%s, %v) = %v, want %v", c.ip, c.allowLoopback, got, c.want)
		}
	}
}

func TestRequireHTTPS(t *testing.T) {
	if err := requireHTTPS("https://as.example.com/token", "token endpoint"); err != nil {
		t.Errorf("https rejected: %v", err)
	}
	if err := requireHTTPS("http://as.example.com/token", "token endpoint"); err == nil {
		t.Error("http accepted")
	}
	if err := requireHTTPS("http://localhost:9000/token", "token endpoint"); err == nil {
		t.Error("http localhost accepted without MCP_ALLOW_LOOPBACK")
	}
	t.Setenv("MCP_ALLOW_LOOPBACK", "true")
	if err := requireHTTPS("http://localhost:9000/token", "token endpoint"); err != nil {
		t.Errorf("http localhost rejected with MCP_ALLOW_LOOPBACK: %v", err)
	}
	if err := requireHTTPS("::bad", "token endpoint"); err == nil {
		t.Error("garbage accepted")
	}
}

func TestOutboundHTTPClient_RefusesLoopbackAndRedirects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/target", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	t.Setenv("MCP_ALLOW_LOOPBACK", "")
	c := newOutboundHTTPClient(5 * time.Second)
	if resp, err := c.Get(srv.URL); err == nil {
		_ = resp.Body.Close()
		t.Fatal("loopback request allowed")
	}

	t.Setenv("MCP_ALLOW_LOOPBACK", "true")
	c = newOutboundHTTPClient(5 * time.Second)
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("loopback with MCP_ALLOW_LOOPBACK: %v", err)
	}
	_ = resp.Body.Close()
	if resp, err := c.Get(srv.URL + "/redirect"); err == nil {
		_ = resp.Body.Close()
		t.Fatal("redirect followed")
	}
}
