package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/akmatori/akmatori/internal/alerts/adapters"
	"github.com/akmatori/akmatori/internal/config"
	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/executor"
	"github.com/akmatori/akmatori/internal/handlers"
	"github.com/akmatori/akmatori/internal/logging"
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

	slog.Info("starting AIOps Codex Bot")

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
			"/ws/agent",         // WebSocket endpoint for Codex worker (internal)
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

	// Initialize skill service
	skillService := services.NewSkillService(dataDir, toolService, contextService)
	slog.Info("skill service initialized", "data_dir", dataDir)

	// Regenerate all SKILL.md files to ensure they have latest template
	if err := skillService.RegenerateAllSkillMds(); err != nil {
		slog.Warn("failed to regenerate SKILL.md files", "err", err)
	}

	// Initialize Codex executor
	codexExecutor := executor.NewExecutor()
	slog.Info("Codex executor initialized")

	// Initialize Alert service
	alertService := services.NewAlertService()
	slog.Info("alert service initialized")

	// Initialize Runbook service
	runbookService := services.NewRunbookService(dataDir)
	slog.Info("runbook service initialized")

	// Sync runbook files on startup
	if err := runbookService.SyncRunbookFiles(); err != nil {
		slog.Warn("failed to sync runbook files", "err", err)
	}

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

	// Initialize Agent WebSocket handler for orchestrator communication
	// This must be created before Slack event handler so it can be captured in closure
	agentWSHandler := handlers.NewAgentWSHandler()
	slog.Info("agent WebSocket handler initialized")

	// Initialize Slack handler (will be used when Slack is enabled)
	var slackHandler *handlers.SlackHandler

	// Initialize Alert handler (needed before Slack handler setup)
	// Initialize channel resolver (will be set when Slack connects)
	var channelResolver *slackutil.ChannelResolver

	alertHandler := handlers.NewAlertHandler(
		cfg,
		slackManager,
		codexExecutor,
		agentWSHandler,
		skillService,
		alertService,
		channelResolver,
	)

	// Set up event handler for when Slack connects
	// Note: We receive the client directly to avoid deadlock (can't call GetClient while holding lock)
	slackManager.SetEventHandler(func(socketClient *socketmode.Client, client *slack.Client) {
		// Create handler with current client
		slackHandler = handlers.NewSlackHandler(
			client,
			codexExecutor,
			agentWSHandler,
			skillService,
		)

		// Wire up alert channel support
		slackHandler.SetAlertHandler(alertHandler)
		slackHandler.SetAlertService(alertService)

		// Try to get bot user ID for self-message filtering
		if authTest, err := client.AuthTest(); err == nil {
			slackHandler.SetBotUserID(authTest.UserID)
			slog.Info("Slack bot user ID", "user_id", authTest.UserID)
		} else {
			slog.Warn("could not get bot user ID", "err", err)
		}

		// Load alert channel configurations
		if err := slackHandler.LoadAlertChannels(); err != nil {
			slog.Warn("failed to load alert channels", "err", err)
		}

		slackHandler.HandleSocketMode(socketClient)
		slog.Info("Slack components initialized (with alert channel support)")
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
	apiHandler := handlers.NewAPIHandler(skillService, toolService, contextService, alertService, codexExecutor, agentWSHandler, slackManager, runbookService, httpConnectorService, mcpServerService)

	// Wire alert channel reload: when alert sources are created/updated/deleted via API,
	// reload the Slack handler's channel mappings so changes take effect immediately.
	apiHandler.SetAlertChannelReloader(func() {
		if slackHandler != nil {
			slackHandler.ReloadAlertChannels()
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

	// Create a context for the Slack manager
	ctx, ctxCancel := context.WithCancel(context.Background())
	defer ctxCancel()

	// Start watching for Slack settings reload requests
	go slackManager.WatchForReloads(ctx)

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
