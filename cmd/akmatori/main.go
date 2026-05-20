package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/akmatori/akmatori/internal/alerts/adapters"
	"github.com/akmatori/akmatori/internal/config"
	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/executor"
	"github.com/akmatori/akmatori/internal/handlers"
	"github.com/akmatori/akmatori/internal/logging"
	"github.com/akmatori/akmatori/internal/messaging"
	"github.com/akmatori/akmatori/internal/middleware"
	"github.com/akmatori/akmatori/internal/services"
	"github.com/akmatori/akmatori/internal/setup"
	slackutil "github.com/akmatori/akmatori/internal/slack"
	"github.com/joho/godotenv"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
	"gorm.io/gorm/logger"
)

func main() {
	logging.Init()

	// Load .env file if it exists (ignore error if file doesn't exist)
	if err := godotenv.Load(); err != nil {
		slog.Info("no .env file found or error loading it (this is fine if using environment variables)", "err", err)
	}

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load configuration", "err", err)
		os.Exit(1)
	}

	slog.Info("starting Akmatori")

	// Step 1: Initialize database connection FIRST (needed for secret resolution)
	if err := database.Connect(cfg.DatabaseURL, logger.Warn); err != nil {
		slog.Error("failed to connect to database", "err", err)
		os.Exit(1)
	}
	slog.Info("database connection established")

	// Step 2: Run database migrations (creates system_settings table)
	if err := database.AutoMigrate(); err != nil {
		slog.Error("failed to run database migrations", "err", err)
		os.Exit(1)
	}

	// Step 3: Initialize default database records
	if err := database.InitializeDefaults(); err != nil {
		slog.Error("failed to initialize database defaults", "err", err)
		os.Exit(1)
	}

	// Step 4: Resolve secrets from env > DB > auto-generate
	jwtSecret := setup.ResolveJWTSecret(cfg.JWTSecret)
	passwordHash, setupRequired, err := setup.ResolveAdminPassword(cfg.AdminPassword)
	if err != nil {
		slog.Error("failed to resolve admin password", "err", err)
		os.Exit(1)
	}

	if setupRequired {
		slog.Warn("*** SETUP MODE *** — Visit the web UI to set your admin password")
	}

	// Step 5: Create JWT middleware with resolved secrets
	jwtAuthMiddleware := middleware.NewJWTAuthMiddleware(&middleware.JWTAuthConfig{
		Enabled:           true,
		SetupMode:         setupRequired,
		AdminUsername:     cfg.AdminUsername,
		AdminPasswordHash: passwordHash,
		JWTSecret:         jwtSecret,
		JWTExpiryHours:    cfg.JWTExpiryHours,
		SkipPaths: []string{
			"/health",
			"/webhook/*",
			"/auth/login",
			"/auth/setup",
			"/auth/setup-status",
			"/ws/agent",         // WebSocket endpoint for Agent worker (internal)
			"/api/docs",         // Swagger UI (public)
			"/api/openapi.yaml", // OpenAPI spec (public)
		},
	})
	slog.Info("JWT authentication enabled", "user", cfg.AdminUsername)

	// Initialize tool service
	toolService := services.NewToolService()
	slog.Info("tool service initialized")

	// Ensure tool types exist in database
	if err := toolService.EnsureToolTypes(); err != nil {
		slog.Warn("failed to ensure tool types", "err", err)
	} else {
		slog.Info("tool types ready")
	}

	// Data directory for skills and incidents (hardcoded)
	const dataDir = "/akmatori"

	// Initialize context service
	contextService, err := services.NewContextService(dataDir)
	if err != nil {
		slog.Error("failed to initialize context service", "err", err)
		os.Exit(1)
	}
	slog.Info("context service initialized", "context_dir", contextService.GetContextDir())

	// Initialize Agent WebSocket handler for orchestrator communication.
	// Created before SkillService so it can be wired in as the OneShotLLMCaller
	// (used by TitleGenerator and any other provider-agnostic LLM call sites).
	agentWSHandler := handlers.NewAgentWSHandler()
	slog.Info("agent WebSocket handler initialized")

	// Initialize skill service
	skillService := services.NewSkillService(dataDir, toolService, contextService, agentWSHandler)
	slog.Info("skill service initialized", "data_dir", dataDir)

	// Initialize Memory service BEFORE regenerating SKILL.md files.
	// generateSkillMd embeds the per-scope MEMORY.md manifest into each
	// SKILL.md it writes; if the on-disk manifests are stale (e.g. memories
	// were inserted directly into Postgres while the API was down), the
	// regenerated SKILL.md files would bake in the stale view and stay
	// stale until the next restart or manual skill edit.
	memoryService := services.NewMemoryService(dataDir)
	if err := memoryService.SyncMemoryFiles(); err != nil {
		slog.Warn("failed to sync memory files", "err", err)
	}

	// Regenerate all SKILL.md files (now seeing the freshly synced memory manifests).
	if err := skillService.RegenerateAllSkillMds(); err != nil {
		slog.Warn("failed to regenerate SKILL.md files", "err", err)
	}

	// Initialize agent executor
	agentExecutor := executor.NewExecutor()
	slog.Info("agent executor initialized")

	// Initialize Alert service
	alertService := services.NewAlertService()
	slog.Info("alert service initialized")

	// Initialize Runbook service. Runbooks aren't embedded in SKILL.md,
	// so their sync order doesn't affect skill regeneration above.
	runbookService := services.NewRunbookService(dataDir)
	slog.Info("runbook service initialized")

	// Sync runbook files on startup
	if err := runbookService.SyncRunbookFiles(); err != nil {
		slog.Warn("failed to sync runbook files", "err", err)
	}

	// Wire post-investigation memory ingest. When skillService finishes
	// an incident with status=completed, the on-disk memory directory written
	// by the memory-writer subagent is re-ingested into Postgres so REST and
	// UI surfaces see fresh entries.
	skillService.SetMemoryIngester(memoryService)

	// Initialize default alert source types
	if err := alertService.InitializeDefaultSourceTypes(); err != nil {
		slog.Warn("failed to initialize alert source types", "err", err)
	}

	// Initialize Slack manager with hot-reload support
	slackManager := slackutil.NewManager()

	// Get initial Slack settings from database
	slackSettings, err := database.GetSlackSettings()
	if err != nil {
		slog.Warn("could not load Slack settings", "err", err)
		slackSettings = &database.SlackSettings{Enabled: false}
	}

	// Initialize Slack handler (will be used when Slack is enabled).
	// Stored in an atomic.Pointer because it is written from the slackManager
	// event-handler goroutine (on socket-mode connect or credential reload) and
	// read from HTTP request goroutines via the SetAlertChannelReloader
	// closure. Without the atomic, the read+write pair is a data race that the
	// race detector flags and that could surface as a torn pointer in production.
	var slackHandler atomic.Pointer[handlers.SlackHandler]

	// Initialize Alert handler (needed before Slack handler setup)
	// Initialize channel resolver (will be set when Slack connects)
	var channelResolver *slackutil.ChannelResolver

	alertHandler := handlers.NewAlertHandler(
		cfg,
		slackManager,
		agentExecutor,
		agentWSHandler,
		skillService,
		alertService,
		channelResolver,
	)

	// Slack summarizer compresses final agent output to fit Slack's byte cap
	// using the same provider-agnostic worker oneshot path as TitleGenerator.
	slackSummarizer := services.NewSlackSummarizer(agentWSHandler)
	alertHandler.SetSlackSummarizer(slackSummarizer)

	// Response formatter applies the configured global formatting prompt to
	// the agent's final response before it is persisted and posted to Slack.
	// Disabled by default — when off, calls passthrough to the raw response.
	responseFormatter := services.NewResponseFormatter(agentWSHandler)
	alertHandler.SetResponseFormatter(responseFormatter)

	// Channel service + messaging provider registry wire outbound posting to
	// the Integration/Channel rows. The slack provider is registered with the
	// live manager so it reads the current client at call time (manager swaps
	// clients on credential reload).
	channelService := services.NewChannelService()
	providerRegistry := messaging.NewRegistry()
	providerRegistry.Register(messaging.NewSlackProvider(slackManager))
	// Telegram is registered as a stub so the registry distinguishes
	// "known provider, not yet implemented" (ErrNotImplemented) from
	// "unknown provider" (ErrProviderNotRegistered). Without this, a
	// Telegram-configured Channel would silently no-op at post time.
	providerRegistry.Register(messaging.NewTelegramProvider())
	alertHandler.SetChannelService(channelService)
	alertHandler.SetProviderRegistry(providerRegistry)

	// Set up event handler for when Slack connects
	// Note: We receive the client directly to avoid deadlock (can't call GetClient while holding lock)
	slackManager.SetEventHandler(func(socketClient *socketmode.Client, client *slack.Client) {
		// Create handler with current client
		handler := handlers.NewSlackHandler(
			client,
			agentExecutor,
			agentWSHandler,
			skillService,
			agentWSHandler,
		)

		// Wire up alert channel support
		handler.SetAlertHandler(alertHandler)
		handler.SetAlertService(alertService)
		// ChannelService is the source of truth for listener channels after
		// Task 6 of the unified-channels plan; LoadListenerChannels reads
		// from the channels table.
		handler.SetChannelService(channelService)
		handler.SetSlackSummarizer(slackSummarizer)
		handler.SetResponseFormatter(responseFormatter)
		// Wire LLM-classified Slack feedback capture: thread replies on incident
		// threads run through the classifier and persist as global feedback memory.
		handler.SetMemoryManager(memoryService)
		handler.SetFeedbackClassifier(services.NewFeedbackClassifier(agentWSHandler))

		// Try to get bot user ID and team ID for self-message filtering and Streaming API
		if authTest, err := client.AuthTest(); err == nil {
			handler.SetBotUserID(authTest.UserID)
			handler.SetTeamID(authTest.TeamID)
			alertHandler.SetTeamID(authTest.TeamID)
			slog.Info("Slack bot user ID", "user_id", authTest.UserID, "team_id", authTest.TeamID)
		} else {
			slog.Warn("could not get bot user ID", "err", err)
		}

		// Load listener channel configurations from the channels table.
		if err := handler.LoadListenerChannels(); err != nil {
			slog.Warn("failed to load listener channels", "err", err)
		}

		// Publish the fully-initialised handler atomically so the API
		// reloader closure observes a complete value (no torn pointer or
		// partially-wired handler).
		slackHandler.Store(handler)

		handler.HandleSocketMode(socketClient)
		slog.Info("Slack components initialized (with listener channel support)")
	})

	slackEnabled := slackSettings.IsActive()
	if slackEnabled {
		slog.Info("Slack integration is ENABLED")
	} else {
		slog.Info("Slack integration is DISABLED (configure in Settings)")
	}

	// Register all alert adapters
	alertHandler.RegisterAdapter(adapters.NewAlertmanagerAdapter())
	alertHandler.RegisterAdapter(adapters.NewZabbixAdapter())
	alertHandler.RegisterAdapter(adapters.NewPagerDutyAdapter())
	alertHandler.RegisterAdapter(adapters.NewGrafanaAdapter())
	alertHandler.RegisterAdapter(adapters.NewDatadogAdapter())
	slog.Info("alert adapters registered: alertmanager, zabbix, pagerduty, grafana, datadog")

	// Initialize HTTP handler
	httpHandler := handlers.NewHTTPHandler(alertHandler)

	// Initialize API handler for skill communication and management
	httpConnectorService := services.NewHTTPConnectorService()
	mcpServerService := services.NewMCPServerService()
	apiHandler := handlers.NewAPIHandler(skillService, toolService, contextService, alertService, agentExecutor, agentWSHandler, slackManager, runbookService, memoryService, httpConnectorService, mcpServerService)
	apiHandler.SetResponseFormatter(responseFormatter)
	// Wire the Integrations + Channels CRUD surface. /api/settings/slack is
	// retired (returns 410 Gone) — operators configure Slack via
	// /api/integrations and /api/channels.
	apiHandler.SetChannelManager(channelService)
	apiHandler.SetProviderRegistry(providerRegistry)

	// Cron runner: scheduler + CRUD for /api/cron-jobs. Started below after
	// HTTP routes are registered so the runner only begins ticking once the
	// rest of the API surface is in place. agentWSHandler is the IncidentRunner
	// — every cron tick spawns a cron-agent investigation through the same
	// WebSocket as alert/Slack flows.
	cronRunner := services.NewCronRunner(channelService, providerRegistry, skillService, agentWSHandler)
	apiHandler.SetCronJobManager(cronRunner)

	// Wire listener channel reload: when channels (or, transitionally, alert
	// sources) are created/updated/deleted via API, reload the Slack handler's
	// channel mappings so changes take effect immediately.
	apiHandler.SetAlertChannelReloader(func() {
		if handler := slackHandler.Load(); handler != nil {
			handler.ReloadListenerChannels()
		}
	})

	// Wire MCP Gateway reload: when HTTP connectors are created/updated/deleted via API,
	// reload the gateway's tool registrations so changes take effect immediately.
	mcpGatewayURL := os.Getenv("MCP_GATEWAY_URL")
	if mcpGatewayURL == "" {
		mcpGatewayURL = "http://mcp-gateway:8080"
	}
	apiHandler.SetGatewayReloader(handlers.GatewayReloadFunc(mcpGatewayURL))
	apiHandler.SetMCPServerReloader(handlers.GatewayMCPReloadFunc(mcpGatewayURL))

	// Initialize auth handler
	authHandler := handlers.NewAuthHandler(jwtAuthMiddleware)

	// Set up HTTP server routes
	mux := http.NewServeMux()
	httpHandler.SetupRoutes(mux)
	apiHandler.SetupRoutes(mux)
	authHandler.SetupRoutes(mux)
	agentWSHandler.SetupRoutes(mux)

	// Wrap all routes with CORS middleware first, then JWT authentication, then request ID
	corsMiddleware := middleware.NewCORSMiddleware() // Allow all origins
	authenticatedHandler := corsMiddleware.Wrap(
		middleware.RequestIDMiddleware(jwtAuthMiddleware.Wrap(mux)))

	// Start HTTP server in goroutine
	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.HTTPPort),
		Handler: authenticatedHandler,
	}

	go func() {
		slog.Info("starting HTTP server", "port", cfg.HTTPPort)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP server error", "err", err)
			os.Exit(1)
		}
	}()

	// Set up graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Handle shutdown in a goroutine
	go func() {
		<-sigChan
		slog.Info("received shutdown signal, cleaning up")

		// Shutdown HTTP server
		slog.Info("shutting down HTTP server")
		if err := httpServer.Close(); err != nil {
			slog.Error("error shutting down HTTP server", "err", err)
		}

		slog.Info("shutdown complete")
		os.Exit(0)
	}()

	slog.Info("Bot is running! Press Ctrl+C to exit.")
	slog.Info("alert webhook endpoint", "url", fmt.Sprintf("http://localhost:%d/webhook/alert/{instance_uuid}", cfg.HTTPPort))
	slog.Info("health check endpoint", "url", fmt.Sprintf("http://localhost:%d/health", cfg.HTTPPort))
	slog.Info("API base URL", "url", fmt.Sprintf("http://localhost:%d/api", cfg.HTTPPort))
	slog.Info("agent WebSocket endpoint", "url", fmt.Sprintf("ws://localhost:%d/ws/agent", cfg.HTTPPort))

	// Create a context for background goroutines
	ctx, ctxCancel := context.WithCancel(context.Background())
	defer ctxCancel()

	// Start retention cleanup service
	retentionService := services.NewRetentionService(filepath.Join(dataDir, "incidents"), database.GetDB())
	go retentionService.StartBackgroundCleanup(ctx)
	slog.Info("retention cleanup service started")

	// Start watching for Slack settings reload requests
	go slackManager.WatchForReloads(ctx)

	// Start the cron runner so scheduled jobs begin ticking. Start is a no-op
	// when called twice; cancellation flows through ctx so SIGTERM shuts the
	// scheduler down cleanly before the HTTP server exits.
	if err := cronRunner.Start(ctx); err != nil {
		slog.Warn("failed to start cron runner", "err", err)
	}

	// Start Slack Socket Mode if enabled
	if slackEnabled {
		if err := slackManager.Start(ctx); err != nil {
			slog.Warn("failed to start Slack", "err", err)
		} else {
			slog.Info("Slack Socket Mode is ACTIVE")
		}
	} else {
		slog.Info("running in API-only mode (Slack disabled)")
	}

	// Keep the main goroutine alive
	for {
		time.Sleep(time.Hour)
	}
}
