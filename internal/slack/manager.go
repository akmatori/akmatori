package slack

import (
	"context"
	"log"
	"net/http"
	"net/url"
	"os"
	"sync"

	"github.com/akmatori/akmatori/internal/database"
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
	stopChan   chan struct{}
	doneChan   chan struct{}
	reloadChan chan struct{}

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
		log.Printf("SlackManager: Could not load Slack settings: %v", err)
		return nil // Not an error, just disabled
	}

	if !settings.IsActive() {
		log.Printf("SlackManager: Slack is disabled (not configured or not enabled)")
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
				log.Printf("SlackManager: Using proxy: %s", proxySettings.ProxyURL)
			}
		}
	}

	// Create new Slack client
	m.client = slack.New(settings.BotToken, options...)

	// Create Socket Mode client
	m.socketClient = socketmode.New(
		m.client,
		socketmode.OptionDebug(false),
		socketmode.OptionLog(log.New(os.Stdout, "socketmode: ", log.Lshortfile|log.LstdFlags)),
	)

	// Initialize control channels
	m.stopChan = make(chan struct{})
	m.doneChan = make(chan struct{})

	// Start the event handler if set - pass both clients to avoid deadlock
	if m.eventHandler != nil {
		m.eventHandler(m.socketClient, m.client)
	}

	// Start Socket Mode in a goroutine
	go func() {
		defer close(m.doneChan)
		log.Printf("SlackManager: Starting Socket Mode connection...")

		if err := m.socketClient.RunContext(ctx); err != nil {
			// Check if this was a graceful shutdown
			select {
			case <-m.stopChan:
				log.Printf("SlackManager: Socket Mode stopped gracefully")
			default:
				log.Printf("SlackManager: Socket Mode error: %v", err)
			}
		}
	}()

	m.running = true
	log.Printf("SlackManager: Slack integration is ACTIVE")
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

	log.Printf("SlackManager: Stopping Slack connection...")

	// Signal stop
	close(m.stopChan)

	// Wait for socket mode to finish (with timeout)
	select {
	case <-m.doneChan:
		log.Printf("SlackManager: Socket Mode stopped")
	default:
		log.Printf("SlackManager: Socket Mode stop signal sent")
	}

	m.running = false
	m.client = nil
	m.socketClient = nil
}

// Reload reloads Slack settings and reconnects
func (m *Manager) Reload(ctx context.Context) error {
	log.Printf("SlackManager: Reloading Slack settings...")

	settings, err := database.GetSlackSettings()
	if err != nil {
		log.Printf("SlackManager: Could not load Slack settings: %v", err)
		m.Stop()
		return err
	}

	if !settings.IsActive() {
		log.Printf("SlackManager: Slack is now disabled, stopping connection")
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
		log.Printf("SlackManager: Reload triggered")
	default:
		log.Printf("SlackManager: Reload already pending")
	}
}

// WatchForReloads runs a loop that watches for reload signals
func (m *Manager) WatchForReloads(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.reloadChan:
			if err := m.Reload(ctx); err != nil {
				log.Printf("SlackManager: Reload failed: %v", err)
			}
		}
	}
}
