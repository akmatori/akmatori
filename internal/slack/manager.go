package slack

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/gorilla/websocket"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
)

// Manager manages the Slack client lifecycle with hot-reload support
type Manager struct {
	mu sync.RWMutex

	// Current active clients
	client       *slack.Client
	socketClient *socketmode.Client

	// Control channels
	doneChan   chan struct{}
	reloadChan chan struct{}

	// Cancel function for the current RunContext goroutine
	cancelFunc context.CancelFunc

	// Event handler - receives both socket client and regular client
	eventHandler func(*socketmode.Client, *slack.Client)

	// State
	running bool
}

// NewManager creates a new Slack manager
func NewManager() *Manager {
	return &Manager{
		reloadChan: make(chan struct{}, 1),
	}
}

// GetClient returns the current Slack client (may be nil if not configured)
func (m *Manager) GetClient() *slack.Client {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.client
}

// GetSocketClient returns the current Socket Mode client (may be nil if not configured)
func (m *Manager) GetSocketClient() *socketmode.Client {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.socketClient
}

// IsRunning returns true if Socket Mode is currently active
func (m *Manager) IsRunning() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.running
}

// SetEventHandler sets the function that will handle socket mode events
// The handler receives both the socket mode client and the regular Slack client
func (m *Manager) SetEventHandler(handler func(*socketmode.Client, *slack.Client)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.eventHandler = handler
}

// Start initializes and starts the Slack connection based on current database settings
func (m *Manager) Start(ctx context.Context) error {
	settings, err := database.GetSlackSettings()
	if err != nil {
		slog.Error("SlackManager: could not load Slack settings", "error", err)
		return nil // Not an error, just disabled
	}

	if !settings.IsActive() {
		slog.Info("SlackManager: Slack is disabled (not configured or not enabled)")
		return nil
	}

	return m.startWithSettings(ctx, settings)
}

// startWithSettings initializes clients with specific settings
func (m *Manager) startWithSettings(ctx context.Context, settings *database.SlackSettings) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Stop existing connection if running
	if m.running {
		m.stopLocked()
	}

	// Create HTTP client with proxy if configured
	var options []slack.Option
	options = append(options,
		slack.OptionDebug(false),
		slack.OptionAppLevelToken(settings.AppToken),
	)

	// Check proxy settings for Slack
	if proxySettings, err := database.GetOrCreateProxySettings(); err == nil && proxySettings != nil {
		if proxySettings.ProxyURL != "" && proxySettings.SlackEnabled {
			proxyURL, parseErr := url.Parse(proxySettings.ProxyURL)
			if parseErr == nil {
				httpClient := &http.Client{
					Transport: &http.Transport{
						Proxy: http.ProxyURL(proxyURL),
					},
				}
				options = append(options, slack.OptionHTTPClient(httpClient))
				slog.Info("SlackManager: using proxy", "proxy_url", proxySettings.ProxyURL)
			}
		}
	}

	// Create new Slack client
	m.client = slack.New(settings.BotToken, options...)

	// Build Socket Mode options
	socketOptions := []socketmode.Option{
		socketmode.OptionDebug(false),
		socketmode.OptionLog(slog.NewLogLogger(slog.Default().Handler(), slog.LevelInfo)),
	}

	// If proxy is configured for Slack, create a custom WebSocket dialer
	if proxySettings, err := database.GetOrCreateProxySettings(); err == nil && proxySettings != nil {
		if proxySettings.ProxyURL != "" && proxySettings.SlackEnabled {
			proxyURL, parseErr := url.Parse(proxySettings.ProxyURL)
			if parseErr == nil {
				dialer := &websocket.Dialer{
					Proxy:            http.ProxyURL(proxyURL),
					HandshakeTimeout: websocket.DefaultDialer.HandshakeTimeout,
				}
				socketOptions = append(socketOptions, socketmode.OptionDialer(dialer))
				slog.Info("SlackManager: using proxy for WebSocket", "proxy_url", proxySettings.ProxyURL)
			}
		}
	}

	// Create Socket Mode client
	m.socketClient = socketmode.New(m.client, socketOptions...)

	// Create a child context so we can cancel just this connection's RunContext
	connCtx, connCancel := context.WithCancel(ctx)
	m.cancelFunc = connCancel

	// Initialize done channel (captured locally to avoid race with future reloads)
	doneChan := make(chan struct{})
	m.doneChan = doneChan

	// Start the event handler if set - pass both clients to avoid deadlock
	if m.eventHandler != nil {
		m.eventHandler(m.socketClient, m.client)
	}

	// Capture socketClient locally so the goroutine uses the correct instance
	// even if m.socketClient is reassigned by a subsequent reload
	sc := m.socketClient

	// Start Socket Mode in a goroutine
	go func() {
		defer close(doneChan)
		slog.Info("SlackManager: starting Socket Mode connection")

		if err := sc.RunContext(connCtx); err != nil {
			// Check if context was cancelled (graceful shutdown)
			if connCtx.Err() != nil {
				slog.Info("SlackManager: Socket Mode stopped gracefully")
			} else {
				slog.Error("SlackManager: Socket Mode error", "error", err)
			}
		}
	}()

	m.running = true
	slog.Info("SlackManager: Slack integration is active")
	return nil
}

// Stop gracefully stops the Slack connection
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopLocked()
}

// stopLocked stops the connection (caller must hold the lock)
func (m *Manager) stopLocked() {
	if !m.running {
		return
	}

	slog.Info("SlackManager: stopping Slack connection")

	// Cancel the RunContext goroutine's context
	if m.cancelFunc != nil {
		m.cancelFunc()
	}

	// Wait for socket mode to finish with a timeout
	if m.doneChan != nil {
		select {
		case <-m.doneChan:
			slog.Info("SlackManager: Socket Mode stopped")
		case <-time.After(5 * time.Second):
			slog.Warn("SlackManager: Socket Mode stop timed out after 5s")
		}
	}

	m.running = false
	m.cancelFunc = nil
	m.client = nil
	m.socketClient = nil
}

// Reload reloads Slack settings and reconnects
func (m *Manager) Reload(ctx context.Context) error {
	slog.Info("SlackManager: reloading Slack settings")

	settings, err := database.GetSlackSettings()
	if err != nil {
		slog.Error("SlackManager: could not load Slack settings", "error", err)
		m.Stop()
		return err
	}

	if !settings.IsActive() {
		slog.Info("SlackManager: Slack is now disabled, stopping connection")
		m.Stop()
		return nil
	}

	// Start with new settings (this will stop existing connection first)
	return m.startWithSettings(ctx, settings)
}

// TriggerReload signals that a reload is needed (non-blocking)
func (m *Manager) TriggerReload() {
	select {
	case m.reloadChan <- struct{}{}:
		slog.Info("SlackManager: reload triggered")
	default:
		slog.Info("SlackManager: reload already pending")
	}
}

// WatchForReloads runs a loop that watches for reload signals.
// Debounces rapid reloads to prevent connection storms.
func (m *Manager) WatchForReloads(ctx context.Context) {
	const debounceDuration = 2 * time.Second

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.reloadChan:
			// Debounce: wait and drain any additional reload signals
			slog.Info("SlackManager: reload requested, debouncing", "debounce_duration", debounceDuration)
			timer := time.NewTimer(debounceDuration)
		drainLoop:
			for {
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-m.reloadChan:
					// Another reload came in during debounce; reset timer
					if !timer.Stop() {
						<-timer.C
					}
					timer.Reset(debounceDuration)
					slog.Info("SlackManager: additional reload request received, resetting debounce")
				case <-timer.C:
					break drainLoop
				}
			}

			slog.Info("SlackManager: debounce complete, reloading now")
			if err := m.Reload(ctx); err != nil {
				slog.Error("SlackManager: reload failed", "error", err)
			}
		}
	}
}
