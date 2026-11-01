package messaging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/akmatori/akmatori/internal/database"
)

// CredentialTester is implemented by providers that can verify an
// Integration's credentials against the platform (POST
// /api/integrations/{uuid}/test). It returns the bot account name.
type CredentialTester interface {
	TestCredentials(ctx context.Context, integration *database.Integration) (string, error)
}

// botHTTPTimeout bounds one outbound Bot API request.
const botHTTPTimeout = 20 * time.Second

// maxRetryAfter caps how long a provider waits on a 429 before its single
// retry; longer waits fail fast so callers are never blocked for minutes.
const maxRetryAfter = 30 * time.Second

// errMissingBotToken is returned when the channel's integration carries no
// bot_token credential.
var errMissingBotToken = errors.New("integration has no bot_token credential")

// botHTTP holds the transport plumbing shared by the Telegram and Discord
// providers. Every field is a test seam; zero values fall back to the
// production behaviour.
type botHTTP struct {
	baseURL         string
	httpClient      func() *http.Client
	loadIntegration func(id uint) (*database.Integration, error)
	sleep           func(ctx context.Context, d time.Duration) error
}

func (b *botHTTP) client() *http.Client {
	if b.httpClient != nil {
		if c := b.httpClient(); c != nil {
			return c
		}
	}
	return defaultBotHTTPClient()
}

func (b *botHTTP) wait(ctx context.Context, d time.Duration) error {
	if b.sleep != nil {
		return b.sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// defaultBotHTTPClient builds an HTTP client that honours the configured
// HTTP proxy. There is no per-provider proxy toggle yet, so the Slack toggle
// (ProxySettings.SlackEnabled) covers all messaging providers.
func defaultBotHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if database.GetDB() != nil {
		if ps, err := database.GetOrCreateProxySettings(); err == nil && ps != nil && ps.ProxyURL != "" && ps.SlackEnabled {
			if proxyURL, perr := url.Parse(ps.ProxyURL); perr == nil {
				transport.Proxy = http.ProxyURL(proxyURL)
			} else {
				slog.Warn("messaging: invalid proxy URL, connecting directly", "err", perr)
			}
		}
	}
	return &http.Client{Timeout: botHTTPTimeout, Transport: transport}
}

func defaultLoadIntegration(id uint) (*database.Integration, error) {
	db := database.GetDB()
	if db == nil {
		return nil, errors.New("database not initialised")
	}
	var row database.Integration
	if err := db.First(&row, id).Error; err != nil {
		return nil, fmt.Errorf("load integration %d: %w", id, err)
	}
	return &row, nil
}

// integrationFor returns the channel's Integration, loading it when the
// caller did not preload it.
func (b *botHTTP) integrationFor(channel *database.Channel) (*database.Integration, error) {
	if channel.Integration.ID != 0 || channel.Integration.Credentials != nil {
		return &channel.Integration, nil
	}
	if channel.IntegrationID == 0 {
		return nil, errMissingBotToken
	}
	load := b.loadIntegration
	if load == nil {
		load = defaultLoadIntegration
	}
	return load(channel.IntegrationID)
}

func botTokenOf(integration *database.Integration) (string, error) {
	if integration == nil {
		return "", errMissingBotToken
	}
	tok, _ := integration.Credentials["bot_token"].(string)
	tok = strings.TrimSpace(tok)
	if tok == "" {
		return "", errMissingBotToken
	}
	return tok, nil
}

func (b *botHTTP) tokenFor(channel *database.Channel) (string, error) {
	integration, err := b.integrationFor(channel)
	if err != nil {
		return "", err
	}
	return botTokenOf(integration)
}

// botResponse is one raw HTTP exchange.
type botResponse struct {
	status int
	body   []byte
	header http.Header
}

// doJSON sends payload (nil = no body) as JSON and returns the raw response.
// Transport errors never include the URL, which carries the Telegram token.
func (b *botHTTP) doJSON(ctx context.Context, method, endpoint string, headers map[string]string, payload any) (*botResponse, error) {
	var body io.Reader
	if payload != nil {
		buf, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("encode request: %w", err)
		}
		body = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, errors.New("build request failed")
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := b.client().Do(req)
	if err != nil {
		var uerr *url.Error
		if errors.As(err, &uerr) {
			return nil, fmt.Errorf("%s request failed: %w", method, uerr.Err)
		}
		return nil, fmt.Errorf("%s request failed", method)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	return &botResponse{status: resp.StatusCode, body: data, header: resp.Header}, nil
}

// withRateLimitRetry runs do once and, when it reports a 429 with a
// retry-after duration, waits (capped) and runs it exactly once more.
func (b *botHTTP) withRateLimitRetry(ctx context.Context, do func() (*botResponse, time.Duration, error)) (*botResponse, error) {
	resp, retryAfter, err := do()
	if err != nil || resp.status != http.StatusTooManyRequests {
		return resp, err
	}
	if retryAfter <= 0 {
		retryAfter = time.Second
	}
	if retryAfter > maxRetryAfter {
		return resp, nil
	}
	if err := b.wait(ctx, retryAfter); err != nil {
		return nil, err
	}
	resp, _, err = do()
	return resp, err
}
