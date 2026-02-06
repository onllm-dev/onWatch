package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/onllm-dev/syntrack/internal/agent"
	"github.com/onllm-dev/syntrack/internal/api"
	"github.com/onllm-dev/syntrack/internal/config"
	"github.com/onllm-dev/syntrack/internal/store"
	"github.com/onllm-dev/syntrack/internal/tracker"
	"github.com/onllm-dev/syntrack/internal/web"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Parse flags and load config
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Handle version flag
	for _, arg := range os.Args[1:] {
		if arg == "--version" || arg == "-v" {
			fmt.Printf("SynTrack v%s\n", version)
			return nil
		}
		if arg == "--help" || arg == "-h" {
			printHelp()
			return nil
		}
	}

	// Setup logging
	logWriter, err := cfg.LogWriter()
	if err != nil {
		return fmt.Errorf("failed to setup logging: %w", err)
	}
	defer func() {
		if closer, ok := logWriter.(interface{ Close() error }); ok && !cfg.DebugMode {
			closer.Close()
		}
	}()

	// Parse log level
	var logLevel slog.Level
	switch cfg.LogLevel {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}

	logger := slog.New(slog.NewTextHandler(logWriter, &slog.HandlerOptions{
		Level: logLevel,
	}))
	slog.SetDefault(logger)

	// Print startup banner
	printBanner(cfg, version)

	// Open database
	db, err := store.New(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer db.Close()

	logger.Info("Database opened", "path", cfg.DBPath)

	// Create components
	client := api.NewClient(cfg.APIKey, logger)
	tr := tracker.New(db, logger)
	ag := agent.New(client, db, tr, cfg.PollInterval, logger)
	handler := web.NewHandler(db, tr, logger)
	server := web.NewServer(cfg.Port, handler, logger)

	// Setup signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start agent in goroutine
	agentErr := make(chan error, 1)
	go func() {
		logger.Info("Starting agent", "interval", cfg.PollInterval)
		if err := ag.Run(ctx); err != nil {
			agentErr <- fmt.Errorf("agent error: %w", err)
		}
	}()

	// Start web server in goroutine
	serverErr := make(chan error, 1)
	go func() {
		logger.Info("Starting web server", "port", cfg.Port)
		if err := server.Start(); err != nil {
			serverErr <- fmt.Errorf("server error: %w", err)
		}
	}()

	// Wait for signal or error
	select {
	case sig := <-sigChan:
		logger.Info("Received signal, shutting down gracefully", "signal", sig)
	case err := <-agentErr:
		logger.Error("Agent failed", "error", err)
		cancel()
	case err := <-serverErr:
		logger.Error("Server failed", "error", err)
		cancel()
	}

	// Graceful shutdown sequence
	logger.Info("Shutting down...")

	// Cancel context to stop agent
	cancel()

	// Give agent a moment to clean up
	time.Sleep(100 * time.Millisecond)

	// Shutdown server with timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("Server shutdown error", "error", err)
	}

	// Close database
	if err := db.Close(); err != nil {
		logger.Error("Database close error", "error", err)
	}

	logger.Info("Shutdown complete")
	return nil
}

func printBanner(cfg *config.Config, version string) {
	apiKeyDisplay := redactAPIKey(cfg.APIKey)
	fmt.Println()
	fmt.Println("╔══════════════════════════════════════╗")
	fmt.Printf("║  SynTrack v%-25s ║\n", version)
	fmt.Println("╠══════════════════════════════════════╣")
	fmt.Println("║  API:       synthetic.new/v2/quotas  ║")
	fmt.Printf("║  Polling:   every %s              ║\n", cfg.PollInterval)
	fmt.Printf("║  Dashboard: http://localhost:%d    ║\n", cfg.Port)
	fmt.Printf("║  Database:  %-24s ║\n", cfg.DBPath)
	fmt.Printf("║  Auth:      %s / ****             ║\n", cfg.AdminUser)
	fmt.Println("╚══════════════════════════════════════╝")
	fmt.Println()
	fmt.Printf("API Key: %s\n", apiKeyDisplay)
	fmt.Println()
}

func printHelp() {
	fmt.Println("SynTrack - Synthetic API Usage Tracker")
	fmt.Println()
	fmt.Println("Usage: syntrack [OPTIONS]")
	fmt.Println()
	fmt.Println("Options:")
	fmt.Println("  --version          Print version and exit")
	fmt.Println("  --help             Print this help message")
	fmt.Println("  --interval SEC     Polling interval in seconds (default: 60)")
	fmt.Println("  --port PORT        Dashboard HTTP port (default: 8932)")
	fmt.Println("  --db PATH          SQLite database file path (default: ./syntrack.db)")
	fmt.Println("  --debug            Run in foreground mode, log to stdout")
	fmt.Println()
	fmt.Println("Environment Variables:")
	fmt.Println("  SYNTHETIC_API_KEY       Required - Your Synthetic API key")
	fmt.Println("  SYNTRACK_POLL_INTERVAL  Polling interval in seconds")
	fmt.Println("  SYNTRACK_PORT           Dashboard HTTP port")
	fmt.Println("  SYNTRACK_ADMIN_USER     Dashboard admin username")
	fmt.Println("  SYNTRACK_ADMIN_PASS     Dashboard admin password")
	fmt.Println("  SYNTRACK_DB_PATH        SQLite database file path")
	fmt.Println("  SYNTRACK_LOG_LEVEL      Log level: debug, info, warn, error")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  syntrack                           # Run in background mode")
	fmt.Println("  syntrack --debug                   # Run in foreground mode")
	fmt.Println("  syntrack --interval 30 --port 8080 # Custom interval and port")
}

func redactAPIKey(key string) string {
	if key == "" {
		return "(empty)"
	}
	if len(key) < 8 {
		return "***"
	}
	if len(key) <= 12 {
		return key[:4] + "***"
	}
	return key[:4] + "***" + key[len(key)-4:]
}
