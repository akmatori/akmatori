package googledrive

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Google OAuth scopes. Reads use read-only scopes; only the write functions
// ask for the spreadsheets scope, so a key that is only shared read-only, or a
// domain-wide delegation grant with read-only scopes, still works for reads.
const (
	scopeRead  = "https://www.googleapis.com/auth/drive.readonly https://www.googleapis.com/auth/spreadsheets.readonly"
	scopeWrite = "https://www.googleapis.com/auth/spreadsheets"

	defaultTokenURL = "https://oauth2.googleapis.com/token"

	// tokenLead renews a cached access token this long before it expires.
	tokenLead = 2 * time.Minute
)

// serviceAccount is the part of a Google service-account JSON key we use.
type serviceAccount struct {
	Type         string `json:"type"`
	ClientEmail  string `json:"client_email"`
	PrivateKeyID string `json:"private_key_id"`
	PrivateKey   string `json:"private_key"`
	key          *rsa.PrivateKey
}

// parseServiceAccount reads a service-account JSON key. Error texts never
// include key material.
func parseServiceAccount(raw string) (*serviceAccount, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("gdrive_service_account_json is not configured")
	}
	var sa serviceAccount
	if err := json.Unmarshal([]byte(raw), &sa); err != nil {
		return nil, fmt.Errorf("gdrive_service_account_json is not valid JSON; paste the whole key file downloaded from Google Cloud")
	}
	if sa.Type != "service_account" {
		return nil, fmt.Errorf("gdrive_service_account_json has type %q; a service-account key (type \"service_account\") is required", sa.Type)
	}
	if sa.ClientEmail == "" || sa.PrivateKey == "" {
		return nil, fmt.Errorf("gdrive_service_account_json has no client_email or private_key")
	}
	block, _ := pem.Decode([]byte(sa.PrivateKey))
	if block == nil {
		return nil, fmt.Errorf("gdrive_service_account_json: private_key is not PEM")
	}
	var key *rsa.PrivateKey
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rk, ok := k.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("gdrive_service_account_json: private_key is not an RSA key")
		}
		key = rk
	} else if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		key = k
	} else {
		return nil, fmt.Errorf("gdrive_service_account_json: private_key cannot be parsed")
	}
	sa.key = key
	sa.PrivateKey = ""
	return &sa, nil
}

// signJWT builds the RS256 assertion of the OAuth 2.0 JWT bearer flow
// (RFC 7523) that Google uses for service accounts.
func signJWT(sa *serviceAccount, scope, subject, aud string, now time.Time) (string, error) {
	enc := base64.RawURLEncoding
	header := map[string]string{"alg": "RS256", "typ": "JWT"}
	if sa.PrivateKeyID != "" {
		header["kid"] = sa.PrivateKeyID
	}
	claims := map[string]interface{}{
		"iss":   sa.ClientEmail,
		"scope": scope,
		"aud":   aud,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	}
	if subject != "" {
		claims["sub"] = subject
	}
	h, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	c, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signing := enc.EncodeToString(h) + "." + enc.EncodeToString(c)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, sa.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("sign the token request: %w", err)
	}
	return signing + "." + enc.EncodeToString(sig), nil
}

type cachedToken struct {
	token   string
	expires time.Time
}

// tokenSource exchanges signed assertions for access tokens and caches them
// per key, subject and scope.
type tokenSource struct {
	mu     sync.Mutex
	tokens map[string]cachedToken
	url    string
	now    func() time.Time
}

func newTokenSource() *tokenSource {
	return &tokenSource{tokens: map[string]cachedToken{}, url: defaultTokenURL, now: time.Now}
}

func tokenCacheKey(sa *serviceAccount, subject, scope string) string {
	sum := sha256.Sum256([]byte(sa.ClientEmail + "\x00" + sa.PrivateKeyID + "\x00" + subject + "\x00" + scope))
	return hex.EncodeToString(sum[:])
}

// token returns a valid access token for the scope.
func (ts *tokenSource) token(ctx context.Context, client *http.Client, sa *serviceAccount, subject, scope string) (string, error) {
	key := tokenCacheKey(sa, subject, scope)
	now := ts.now()
	ts.mu.Lock()
	if c, ok := ts.tokens[key]; ok && now.Add(tokenLead).Before(c.expires) {
		ts.mu.Unlock()
		return c.token, nil
	}
	ts.mu.Unlock()

	assertion, err := signJWT(sa, scope, subject, defaultTokenURL, now)
	if err != nil {
		return "", err
	}
	form := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {assertion},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ts.url, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("Google token request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return "", fmt.Errorf("read Google token response: %w", err)
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	_ = json.Unmarshal(body, &out)
	if resp.StatusCode != http.StatusOK || out.AccessToken == "" {
		return "", tokenError(resp.StatusCode, out.Error, out.Description, subject)
	}
	if out.ExpiresIn <= 0 {
		out.ExpiresIn = 3600
	}
	ts.mu.Lock()
	ts.tokens[key] = cachedToken{token: out.AccessToken, expires: now.Add(time.Duration(out.ExpiresIn) * time.Second)}
	ts.mu.Unlock()
	return out.AccessToken, nil
}

func tokenError(status int, code, desc, subject string) error {
	msg := fmt.Sprintf("Google refused the service-account sign-in (HTTP %d", status)
	if code != "" {
		msg += ", " + code
	}
	msg += ")"
	if desc != "" {
		msg += ": " + desc
	}
	switch {
	case code == "invalid_grant":
		msg += ". The key may be deleted or disabled, or the server clock is wrong"
	case code == "unauthorized_client" && subject != "":
		msg += ". Impersonation needs domain-wide delegation for this client ID with the requested scopes in the Google Workspace admin console"
	}
	return fmt.Errorf("%s", msg)
}
