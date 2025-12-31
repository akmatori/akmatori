package main

import (
	"context"
	"fmt"
	"log"
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
	"github.com/akmatori/akmatori/internal/middleware"
	"github.com/akmatori/akmatori/internal/services"
	slackutil "github.com/akmatori/akmatori/internal/slack"
	"github.com/joho/godotenv"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
	"gorm.io/gorm/logger"
)

func main() {
	// Load .env file if it exists (ignore error if file doesn't exist)
	if err := godotenv.Load(); err != nil {
		log.Printf("No .env file found or error loading it (this is fine if using environment variables): %v", err)
	}

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	log.Printf("Starting AIOps Codex Bot...")

	// Initialize JWT authentication middleware
	if cfg.AdminPassword == "" {
		log.Fatalf("ADMIN_PASSWORD is not set")
	}

	// Hash the admin password
	passwordHash, err := middleware.HashPassword(cfg.AdminPassword)
	if err != nil {
		log.Fatalf("Failed to hash admin password: %v", err)
	}

	jwtAuthMiddleware := middleware.NewJWTAuthMiddleware(&middleware.JWTAuthConfig{
		Enabled:           true,
		AdminUsername:     cfg.AdminUsername,
		AdminPasswordHash: passwordHash,
		JWTSecret:         cfg.JWTSecret,
		JWTExpiryHours:    cfg.JWTExpiryHours,
		SkipPaths: []string{
			"/health",
			"/webhook/*",
			"/auth/login",
		},
	})
	log.Printf("JWT authentication enabled for user: %s", cfg.AdminUsername)

	// Initialize database connection
	if err := database.Connect(cfg.DatabaseURL, logger.Warn); err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	log.Printf("Database connection established")

	// Run database migrations
	if err := database.AutoMigrate(); err != nil {
		log.Fatalf("Failed to run database migrations: %v", err)
	}

	// Initialize default database records
	if err := database.InitializeDefaults(); err != nil {
		log.Fatalf("Failed to initialize database defaults: %v", err)
	}

	// Initialize tool service
	toolService := services.NewToolService(cfg.ToolsDir)
	log.Printf("Tool service initialized with tools dir: %s", cfg.ToolsDir)

	// Load tool types from filesystem into database
	if err := toolService.LoadToolTypes(); err != nil {
		log.Printf("Warning: Failed to load tool types: %v", err)
	} else {
		log.Printf("Tool types loaded from filesystem")
	}

	// Data directory for skills and incidents (hardcoded)
	const dataDir = "/akmatori"

	// Initialize context service
	contextService, err := services.NewContextService(dataDir)
	if err != nil {
		log.Fatalf("Failed to initialize context service: %v", err)
	}
	log.Printf("Context service initialized with context dir: %s", contextService.GetContextDir())

	// Initialize skill service
	skillService := services.NewSkillService(dataDir, toolService, contextService)
	log.Printf("Skill service initialized with data dir: %s", dataDir)

	// Initialize Codex executor
	codexExecutor := executor.NewExecutor()
	log.Printf("Codex executor initialized")

	// Initialize Alert service
	alertService := services.NewAlertService()
	log.Printf("Alert service initialized")

	// Initialize default alert source types
	if err := alertService.InitializeDefaultSourceTypes(); err != nil {
		log.Printf("Warning: Failed to initialize alert source types: %v", err)
	}

	// Initialize Slack manager with hot-reload support
	slackManager := slackutil.NewManager()

	// Get initial Slack settings from database
	slackSettings, err := database.GetSlackSettings()
	if err != nil {
		log.Printf("Warning: Could not load Slack settings: %v", err)
		slackSettings = &database.SlackSettings{Enabled: false}
	}

	// Initialize Slack handler (will be used when Slack is enabled)
	var slackHandler *handlers.SlackHandler

	// Set up event handler for when Slack connects
	// Note: We receive the client directly to avoid deadlock (can't call GetClient while holding lock)
	slackManager.SetEventHandler(func(socketClient *socketmode.Client, client *slack.Client) {
		// Create handler with current client
		slackHandler = handlers.NewSlackHandler(
			client,
			codexExecutor,
			skillService,
		)
		slackHandler.HandleSocketMode(socketClient)
		log.Printf("Slack components initialized")
	})

	slackEnabled := slackSettings.IsActive()
	if slackEnabled {
		log.Printf("Slack integration is ENABLED")
	} else {
		log.Printf("Slack integration is DISABLED (configure in Settings)")
	}

	// Initialize channel resolver (will be set when Slack connects)
	var channelResolver *slackutil.ChannelResolver

	// Initialize Alert handler
	alertHandler := handlers.NewAlertHandler(
		cfg,
		slackManager.GetClient(), // Can be nil if Slack is disabled
		codexExecutor,
		skillService,
		alertService,
		channelResolver,
		slackSettings.AlertsChannel,
	)

	// Register all alert adapters
	alertHandler.RegisterAdapter(adapters.NewAlertmanagerAdapter())
	alertHandler.RegisterAdapter(adapters.NewZabbixAdapter())
	alertHandler.RegisterAdapter(adapters.NewPagerDutyAdapter())
	alertHandler.RegisterAdapter(adapters.NewGrafanaAdapter())
	alertHandler.RegisterAdapter(adapters.NewDatadogAdapter())
	log.Printf("Alert adapters registered: alertmanager, zabbix, pagerduty, grafana, datadog")

	// Initialize HTTP handler
	httpHandler := handlers.NewHTTPHandler(alertHandler)

	// Initialize API handler for skill communication and management
	apiHandler := handlers.NewAPIHandler(skillService, toolService, contextService, alertService, codexExecutor, slackManager)

	// Initialize auth handler
	authHandler := handlers.NewAuthHandler(jwtAuthMiddleware)

	// Set up HTTP server routes
	mux := http.NewServeMux()
	httpHandler.SetupRoutes(mux)
	apiHandler.SetupRoutes(mux)
	authHandler.SetupRoutes(mux)

	// Wrap all routes with CORS middleware first, then JWT authentication
	corsMiddleware := middleware.NewCORSMiddleware() // Allow all origins
	authenticatedHandler := corsMiddleware.Wrap(jwtAuthMiddleware.Wrap(mux))

	// Start HTTP server in goroutine
	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.HTTPPort),
		Handler: authenticatedHandler,
	}

	go func() {
		log.Printf("Starting HTTP server on port %d", cfg.HTTPPort)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	// Set up graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Handle shutdown in a goroutine
	go func() {
		<-sigChan
		log.Println("\nReceived shutdown signal, cleaning up...")

		// Shutdown HTTP server
		log.Println("Shutting down HTTP server...")
		if err := httpServer.Close(); err != nil {
			log.Printf("Error shutting down HTTP server: %v", err)
		}

		log.Println("Shutdown complete")
		os.Exit(0)
	}()

	log.Println("Bot is running! Press Ctrl+C to exit.")
	log.Printf("Alert webhook endpoint: http://localhost:%d/webhook/alert/{instance_uuid}", cfg.HTTPPort)
	log.Printf("Health check endpoint: http://localhost:%d/health", cfg.HTTPPort)
	log.Printf("API base URL: http://localhost:%d/api", cfg.HTTPPort)

	// Create a context for the Slack manager
	ctx, ctxCancel := context.WithCancel(context.Background())
	defer ctxCancel()

	// Start watching for Slack settings reload requests
	go slackManager.WatchForReloads(ctx)

	// Start Slack Socket Mode if enabled
	if slackEnabled {
		if err := slackManager.Start(ctx); err != nil {
			log.Printf("Warning: Failed to start Slack: %v", err)
		} else {
			log.Println("Slack Socket Mode is ACTIVE")
		}
	} else {
		log.Println("Running in API-only mode (Slack disabled)")
	}

	// Keep the main goroutine alive
	for {
		time.Sleep(time.Hour)
	}
}
