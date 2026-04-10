package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/agent"
	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/menubar"
	"github.com/onllm-dev/onwatch/v2/internal/notify"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
	"github.com/onllm-dev/onwatch/v2/internal/update"
	"github.com/onllm-dev/onwatch/v2/internal/web"
)

//go:embed VERSION
var embeddedVersion string

var version = "dev"

func init() {
	if version == "dev" {
		version = strings.TrimSpace(embeddedVersion)
	}
}

// dualHandler fans out log records to two slog handlers:
// a file handler (full verbosity) and a stdout handler (warn/error only).
// Used in --debug mode so systemd/journald stays clean while .onwatch.log gets everything.
type dualHandler struct {
	file   slog.Handler
	stdout slog.Handler
}

func (d dualHandler) Enabled(_ context.Context, level slog.Level) bool {
	return d.file.Enabled(context.Background(), level) || d.stdout.Enabled(context.Background(), level)
}

func (d dualHandler) Handle(ctx context.Context, r slog.Record) error {
	if d.file.Enabled(ctx, r.Level) {
		_ = d.file.Handle(ctx, r)
	}
	if d.stdout.Enabled(ctx, r.Level) {
		_ = d.stdout.Handle(ctx, r)
	}
	return nil
}

func (d dualHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return dualHandler{file: d.file.WithAttrs(attrs), stdout: d.stdout.WithAttrs(attrs)}
}

func (d dualHandler) WithGroup(name string) slog.Handler {
	return dualHandler{file: d.file.WithGroup(name), stdout: d.stdout.WithGroup(name)}
}

func main() {
	if err := runWithCrashCapture(); err != nil {
		if !errors.Is(err, errCodexProfileRefreshAborted) {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}

		// On Windows, if the error is about missing configuration and we're likely
		// running from a double-click (no arguments), show installation instructions
		// and wait for user input before closing the window.
		if runtime.GOOS == "windows" && len(os.Args) == 1 {
			if strings.Contains(err.Error(), "provider must be configured") {
				fmt.Fprintln(os.Stderr, "")
				fmt.Fprintln(os.Stderr, "To set up onWatch, run the PowerShell installer:")
				fmt.Fprintln(os.Stderr, "  irm https://raw.githubusercontent.com/onllm-dev/onwatch/main/install.ps1 | iex")
				fmt.Fprintln(os.Stderr, "")
				fmt.Fprintln(os.Stderr, "Or download install.bat from GitHub releases and double-click it.")
				fmt.Fprintln(os.Stderr, "")
				fmt.Fprint(os.Stderr, "Press Enter to exit...")
				bufio.NewReader(os.Stdin).ReadBytes('\n')
			}
		}
		os.Exit(1)
	}
}

var (
	pidDir  = defaultPIDDir()
	pidFile = filepath.Join(pidDir, "onwatch.pid")
)

// hasFlag checks if a flag exists anywhere in os.Args[1:].
func hasFlag(flag string) bool {
	for _, arg := range os.Args[1:] {
		if arg == flag {
			return true
		}
	}
	return false
}

// hasCommand checks if any of the given commands/flags exist in os.Args[1:].
func hasCommand(cmds ...string) bool {
	for _, arg := range os.Args[1:] {
		for _, cmd := range cmds {
			if arg == cmd {
				return true
			}
		}
	}
	return false
}

func printMenubarHelp() {
	fmt.Print(menubarHelpText())
}

func menubarHelpText() string {
	return "" +
		"onWatch Menubar Companion\n\n" +
		"Usage: onwatch menubar [OPTIONS]\n\n" +
		"Options:\n" +
		"  --port PORT    Dashboard port to connect to (default: 9211)\n" +
		"  --debug        Run in foreground (logs to file, stdout gets warn/error)\n" +
		"  --debugstdout  Run in foreground with all logs to stdout\n" +
		"  --help         Show this help message\n"
}

// stopPreviousInstance stops any running onwatch instance using PID file + port check.
// In test mode, only PID file is used (no port scanning) to avoid killing production.
func stopPreviousInstance(port int, testMode bool) {
	myPID := os.Getpid()
	stopped := false

	// Method 1: PID file (handles both "PID" and "PID:PORT" formats)
	if data, err := os.ReadFile(pidFile); err == nil {
		content := strings.TrimSpace(string(data))
		var pid, filePort int

		// Parse PID:PORT format (new) or just PID (legacy)
		if strings.Contains(content, ":") {
			parts := strings.Split(content, ":")
			if len(parts) >= 2 {
				pid, _ = strconv.Atoi(parts[0])
				filePort, _ = strconv.Atoi(parts[1])
			}
		} else {
			pid, _ = strconv.Atoi(content)
		}

		if pid > 0 && pid != myPID {
			if proc, err := os.FindProcess(pid); err == nil {
				if err := proc.Signal(syscall.SIGTERM); err == nil {
					fmt.Printf("Stopped previous instance (PID %d) via PID file\n", pid)
					stopped = true
				}
			}
		}
		os.Remove(pidFile)

		// If PID file had a port and we didn't stop it, try that specific port
		if !stopped && filePort > 0 {
			conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", filePort), 500*time.Millisecond)
			if err == nil {
				conn.Close()
				if pids := findOnwatchOnPort(filePort); len(pids) > 0 {
					for _, foundPID := range pids {
						if foundPID == myPID {
							continue
						}
						if proc, err := os.FindProcess(foundPID); err == nil {
							if err := proc.Signal(syscall.SIGTERM); err == nil {
								fmt.Printf("Stopped previous instance (PID %d) on port %d\n", foundPID, filePort)
								stopped = true
							}
						}
					}
				}
			}
		}
	}

	// Method 2: Check if the port is in use and kill the occupying onwatch process
	// Skip in test mode to avoid accidentally killing production instances
	if !testMode && !stopped && port > 0 {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 500*time.Millisecond)
		if err == nil {
			conn.Close()
			// Port is occupied - find which process holds it
			if pids := findOnwatchOnPort(port); len(pids) > 0 {
				for _, pid := range pids {
					if pid == myPID {
						continue
					}
					if proc, err := os.FindProcess(pid); err == nil {
						if err := proc.Signal(syscall.SIGTERM); err == nil {
							fmt.Printf("Stopped previous instance (PID %d) on port %d\n", pid, port)
							stopped = true
						}
					}
				}
			}
		}
	}

	if stopped {
		time.Sleep(500 * time.Millisecond)
	}
}

// findOnwatchOnPort uses lsof (macOS/Linux) to find onwatch processes on a port.
func findOnwatchOnPort(port int) []int {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return nil
	}

	// lsof -ti :PORT gives PIDs listening on that port
	out, err := exec.Command("lsof", "-ti", fmt.Sprintf(":%d", port)).Output()
	if err != nil {
		return nil
	}

	var pids []int
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if pid, err := strconv.Atoi(line); err == nil && pid > 0 {
			// Verify it's an onwatch process by checking the command name
			if isOnwatchProcess(pid) {
				pids = append(pids, pid)
			}
		}
	}
	return pids
}

// isOnwatchProcess checks if a PID belongs to an onwatch (or legacy syntrack) binary.
func isOnwatchProcess(pid int) bool {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		return false
	}
	cmd := strings.ToLower(strings.TrimSpace(string(out)))
	return strings.Contains(cmd, "onwatch") || strings.Contains(cmd, "syntrack")
}

func ensurePIDDir() error {
	return os.MkdirAll(pidDir, 0755)
}

// sha256hex returns the SHA-256 hex hash of a string.
func sha256hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h)
}

// deriveEncryptionKey derives the runtime encryption key for sensitive settings.
// It must stay aligned with the web layer, which encrypts SMTP passwords using
// HKDF with the stored encryption salt when available.
func deriveEncryptionKey(passwordHash string) string {
	return web.DeriveEncryptionKey(passwordHash, nil)
}

// deriveLegacyEncryptionKey returns the pre-salt legacy key derivation used by
// older releases, for migration of previously encrypted SMTP passwords.
func deriveLegacyEncryptionKey(passwordHash string) string {
	if len(passwordHash) == 64 {
		return passwordHash
	}
	h := sha256.Sum256([]byte(passwordHash))
	return fmt.Sprintf("%x", h)
}

// migrateDBLocation moves the database from old default locations to the new one.
// Only runs when no explicit --db or ONWATCH_DB_PATH was set.
func migrateDBLocation(newPath string, logger *slog.Logger) {
	oldPaths := []string{
		"./onwatch.db",
	}
	oldHome := os.Getenv("HOME")
	if oldHome != "" {
		oldPaths = append(oldPaths,
			filepath.Join(oldHome, ".onwatch", "onwatch.db"),
		)
	}

	for _, oldPath := range oldPaths {
		if oldPath == newPath {
			continue
		}
		if _, err := os.Stat(oldPath); err != nil {
			continue
		}
		if _, err := os.Stat(newPath); err == nil {
			break // new already exists, skip
		}

		// Ensure target directory exists
		if err := os.MkdirAll(filepath.Dir(newPath), 0700); err != nil {
			logger.Warn("Failed to create data directory", "path", filepath.Dir(newPath), "error", err)
			continue
		}

		// Move DB + WAL/SHM files
		if err := os.Rename(oldPath, newPath); err != nil {
			logger.Warn("Failed to migrate database", "from", oldPath, "to", newPath, "error", err)
			continue
		}
		os.Rename(oldPath+"-wal", newPath+"-wal")
		os.Rename(oldPath+"-shm", newPath+"-shm")
		logger.Info("Migrated database", "from", oldPath, "to", newPath)
		break
	}
}

// fixExplicitDBPath detects when a user's .env has a misconfigured DB_PATH
// (e.g., ./onwatch.db or ./syntrack.db) while the canonical data/ path holds
// the actual historical data. It redirects to the canonical path so the
// dashboard shows existing data instead of appearing empty.
func fixExplicitDBPath(cfg *config.Config, logger *slog.Logger) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return
	}

	canonicalPath := filepath.Join(home, ".onwatch", "data", "onwatch.db")

	// Already using the canonical path - nothing to fix
	absExplicit, _ := filepath.Abs(cfg.DBPath)
	absCan, _ := filepath.Abs(canonicalPath)
	if absExplicit == absCan {
		return
	}

	// Check if canonical path exists and has data
	canInfo, err := os.Stat(canonicalPath)
	if err != nil || canInfo.Size() == 0 {
		return // canonical doesn't exist or is empty
	}

	// Check if the explicit path exists
	expInfo, err := os.Stat(cfg.DBPath)
	if err != nil {
		// Explicit path doesn't even exist - use canonical
		logger.Info("Explicit DB path not found, redirecting to canonical",
			"explicit", cfg.DBPath, "canonical", canonicalPath)
		cfg.DBPath = canonicalPath
		return
	}

	// Both exist - use whichever is larger (has more data)
	if canInfo.Size() > expInfo.Size() {
		logger.Warn("Explicit DB path has less data than canonical path, redirecting",
			"explicit", cfg.DBPath, "explicitSize", expInfo.Size(),
			"canonical", canonicalPath, "canonicalSize", canInfo.Size())
		cfg.DBPath = canonicalPath
	}
}

func writePIDFile(port int) error {
	if err := ensurePIDDir(); err != nil {
		return fmt.Errorf("failed to create PID directory: %w", err)
	}
	// Store both PID and port for reliable stopping
	content := fmt.Sprintf("%d:%d", os.Getpid(), port)
	return os.WriteFile(pidFile, []byte(content), 0644)
}

func removePIDFile() {
	os.Remove(pidFile)
}

// daemonize re-executes the current binary as a detached background process.
// The parent writes the child's PID to .onwatch.pid and exits.
func daemonize(cfg *config.Config) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	// Resolve symlinks so re-exec works correctly
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("failed to resolve executable path: %w", err)
	}

	// Open log file for child's stdout/stderr
	logName := ".onwatch.log"
	if cfg.TestMode {
		logName = ".onwatch-test.log"
	}
	logPath := filepath.Join(filepath.Dir(cfg.DBPath), logName)
	logFile, err := config.OpenRotatingLogFile(logPath)
	if err != nil {
		return fmt.Errorf("failed to open log file for daemon: %w", err)
	}

	// Build child command with same args
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = append(os.Environ(), "_ONWATCH_DAEMON=1")
	cmd.SysProcAttr = daemonSysProcAttr()

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	// Write child PID and port
	childPID := cmd.Process.Pid
	if err := ensurePIDDir(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not create PID directory: %v\n", err)
	}
	pidContent := fmt.Sprintf("%d:%d", childPID, cfg.Port)
	if err := os.WriteFile(pidFile, []byte(pidContent), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not write PID file: %v\n", err)
	}

	logFile.Close()

	fmt.Printf("Daemon started (PID %d), logs: %s\n", childPID, logPath)
	return nil
}

func runWithCrashCapture() (err error) {
	defer func() {
		if r := recover(); r != nil {
			stack := string(debug.Stack())
			slog.Error("Fatal panic", "panic", r, "stack", stack)
			fmt.Fprintf(os.Stderr, "Fatal panic: %v\n%s\n", r, stack)
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return run()
}

func run() error {
	// Phase 1: Detect test mode early and configure PID file for isolation
	testMode := hasFlag("--test")
	if testMode {
		pidFile = filepath.Join(pidDir, "onwatch-test.pid")
	}

	// Phase 2: Handle subcommands (both with and without -- prefix)
	// Note: "codex" must be checked before "status" because "codex profile status" contains "status"
	if hasCommand("codex") {
		return runCodexCommand()
	}
	if hasCommand("menubar") {
		if hasFlag("--help") || hasFlag("-h") {
			printMenubarHelp()
			return nil
		}
		return runMenubarCommand()
	}
	if hasCommand("stop", "--stop") {
		return runStop(testMode)
	}
	if hasCommand("status", "--status") {
		return runStatus(testMode)
	}
	if hasCommand("--version", "-v", "version") {
		fmt.Printf("onWatch v%s\n", version)
		fmt.Println("github.com/onllm-dev/onwatch")
		fmt.Println("Powered by onllm.dev")
		return nil
	}
	if hasCommand("update", "--update") {
		return runUpdate()
	}
	if hasCommand("setup", "--setup") {
		return runSetup()
	}
	if hasCommand("--help", "-h") {
		printHelp()
		return nil
	}

	// Memory tuning: GOMEMLIMIT triggers MADV_DONTNEED which actually shrinks RSS.
	// Without this, Go uses MADV_FREE on macOS - pages are reclaimable but still
	// counted in RSS, causing a permanent ratchet effect.
	debug.SetMemoryLimit(40 * 1024 * 1024) // 40 MiB soft limit
	debug.SetGCPercent(50)                 // GC at 50% heap growth (default 100)

	// Phase 3: Parse flags and load config
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Resolve auth tokens before any banner output so displayed providers
	// match the providers that will actually start.
	preflightLogger := slog.Default()
	if cfg.AnthropicToken == "" {
		if token := api.DetectAnthropicToken(preflightLogger); token != "" {
			cfg.AnthropicToken = token
			cfg.AnthropicAutoToken = true
		}
	}
	if cfg.CodexToken == "" {
		if token := api.DetectCodexToken(preflightLogger); token != "" {
			cfg.CodexToken = token
			cfg.CodexAutoToken = true
		}
	}

	// If no global Codex token, check if saved profiles exist to bootstrap the provider.
	// This allows Docker containers to start Codex polling from saved profiles alone.
	if !cfg.HasProvider("codex") {
		profilesDir := codexProfilesDirWithDataDir(filepath.Dir(cfg.DBPath))
		if hasSavedCodexProfiles(profilesDir) {
			cfg.CodexHasProfiles = true
			preflightLogger.Info("Codex saved profiles detected, enabling provider", "dir", profilesDir)
		}
	}

	isDaemonChild := os.Getenv("_ONWATCH_DAEMON") == "1"

	// Write early diagnostic to stderr (inherited log file fd) BEFORE slog is configured.
	// If the daemon child crashes during init, this ensures the log file isn't empty.
	if isDaemonChild {
		fmt.Fprintf(os.Stderr, "daemon child started (PID %d)\n", os.Getpid())
	}

	// Auto-fix systemd unit file BEFORE stopping the previous instance.
	// When a post-update child runs this, the daemon-reload completes while
	// the parent is still alive (systemd tracks it). After the child kills
	// the parent below, systemd sees Restart=always and auto-starts the new binary.
	// No-op if not under systemd or already up to date.
	update.MigrateSystemdUnit(slog.Default())

	// Stop any previous instance (parent does this, daemon child skips it)
	if !isDaemonChild {
		stopPreviousInstance(cfg.Port, testMode)
		_ = stopMenubarProcess(testMode)
	}

	// Early Gemini auto-detection for banner display
	if os.Getenv("GEMINI_ENABLED") != "false" && !cfg.GeminiEnabled {
		if creds := api.DetectGeminiCredentials(nil); creds != nil {
			cfg.GeminiEnabled = true
		}
	}

	// Daemonize: if not in debug mode, not already the daemon child, and NOT in Docker, fork
	// Docker containers should always run in foreground mode (logs to stdout)
	if !cfg.DebugMode && !isDaemonChild && !cfg.IsDockerEnvironment() {
		printBanner(cfg, version)
		return daemonize(cfg)
	}

	// From here on, we are either the daemon child or running in --debug mode.

	// In daemon mode, the parent already wrote the PID file with our PID.
	// In debug mode, we write our own PID file.
	if cfg.DebugMode {
		if err := writePIDFile(cfg.Port); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not write PID file: %v\n", err)
		}
	}
	defer removePIDFile()

	// Setup logging
	logWriter, err := cfg.LogWriter()
	if err != nil {
		return fmt.Errorf("failed to setup logging: %w", err)
	}
	defer func() {
		if closer, ok := logWriter.(interface{ Close() error }); ok {
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

	// Primary handler: writes to log file (or stdout for --debugstdout/Docker)
	fileHandler := slog.NewTextHandler(logWriter, &slog.HandlerOptions{
		Level: logLevel,
	})

	var logger *slog.Logger
	if cfg.DebugMode && !cfg.DebugStdout && !cfg.IsDockerEnvironment() {
		// --debug mode: file gets full logs, stdout gets only warn/error
		stdoutHandler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelWarn,
		})
		logger = slog.New(dualHandler{file: fileHandler, stdout: stdoutHandler})
	} else {
		logger = slog.New(fileHandler)
	}
	slog.SetDefault(logger)

	logger.Info("Runtime startup",
		"pid", os.Getpid(),
		"port", cfg.Port,
		"db_path", cfg.DBPath,
		"debug", cfg.DebugMode,
		"test_mode", cfg.TestMode,
	)

	// Warn if using default password
	if cfg.IsDefaultPassword() {
		logger.Warn("⚠️  USING DEFAULT PASSWORD - set ONWATCH_ADMIN_PASS in .env for production")
	}

	// Print startup banner (only in debug/foreground mode)
	if cfg.DebugMode {
		printBanner(cfg, version)
	}

	// Ensure data directory exists and migrate DB if needed
	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0700); err != nil {
		logger.Warn("Failed to create database directory", "error", err)
	}
	if !cfg.DBPathExplicit {
		migrateDBLocation(cfg.DBPath, logger)
	} else {
		// Fix for misconfigured DB_PATH: if the user's .env has a relative path
		// like ./onwatch.db or ./syntrack.db but the canonical data/ path has
		// existing data, redirect to the canonical path to avoid empty dashboard.
		fixExplicitDBPath(cfg, logger)
	}

	// Migrate Codex profiles from legacy ~/.onwatch/codex-profiles/ to data directory
	migrateCodexProfiles()

	// Open database
	db, err := store.New(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer db.Close()

	logger.Info("Database opened", "path", cfg.DBPath)

	// Initialize or load encryption salt for HKDF key derivation
	if err := initEncryptionSalt(db, logger); err != nil {
		logger.Warn("Failed to initialize encryption salt", "error", err)
	}

	// Password precedence: DB-stored hash takes priority over .env
	dbHash, hashErr := db.GetUser(cfg.AdminUser)
	if hashErr == nil && dbHash != "" {
		// DB has stored password - use it
		cfg.AdminPassHash = dbHash
		logger.Info("Using database-stored password for auth")
	} else {
		// No DB password - hash the .env password and store it
		cfg.AdminPassHash = sha256hex(cfg.AdminPass)
		if storeErr := db.UpsertUser(cfg.AdminUser, cfg.AdminPassHash); storeErr != nil {
			logger.Warn("Failed to store initial password hash", "error", storeErr)
		}
		logger.Info("Stored initial password hash in database")
	}

	// Close any orphaned sessions from previous runs (e.g., process was killed)
	if closed, err := db.CloseOrphanedSessions(); err != nil {
		logger.Warn("Failed to close orphaned sessions", "error", err)
	} else if closed > 0 {
		logger.Info("Closed orphaned sessions", "count", closed)
	}

	// Migrate existing sessions to usage-based detection (runs once)
	if err := db.MigrateSessionsToUsageBased(cfg.SessionIdleTimeout); err != nil {
		logger.Error("Session migration failed", "error", err)
	}

	// Run cycle migration to fix historical cycles with incorrect durations (runs once)
	if results, err := db.RunCycleMigrationIfNeeded(logger); err != nil {
		logger.Error("Cycle migration failed", "error", err)
	} else if len(results) > 0 {
		for _, r := range results {
			logger.Info("Cycle migration result",
				"provider", r.Provider,
				"quotaType", r.QuotaType,
				"cyclesFixed", r.CyclesFixed,
				"cyclesCreated", r.CyclesCreated,
				"snapshotsUsed", r.SnapshotsUsed,
			)
		}
	}

	if cfg.AnthropicAutoToken {
		logger.Info("Auto-detected Anthropic token from Claude Code credentials")
	}
	if cfg.CodexAutoToken {
		logger.Info("Auto-detected Codex token from Codex credentials")
	}

	// Apply provider_settings from DB (UI-configured keys/regions override .env)
	if db != nil {
		web.ApplyProviderSettingsFromDB(db, cfg, logger)
	}

	// Create API clients based on configured providers
	var syntheticClient *api.Client
	var zaiClient *api.ZaiClient

	if cfg.HasProvider("synthetic") {
		syntheticClient = api.NewClient(cfg.SyntheticAPIKey, logger)
		logger.Info("Synthetic API client configured")
	}

	if cfg.HasProvider("zai") {
		zaiBaseURL := cfg.ZaiBaseURL + "/monitor/usage/quota/limit"
		zaiClient = api.NewZaiClient(cfg.ZaiAPIKey, logger, api.WithZaiBaseURL(zaiBaseURL))
		logger.Info("Z.ai API client configured", "base_url", cfg.ZaiBaseURL, "region", cfg.ZaiRegion)
	}

	var anthropicClient *api.AnthropicClient
	if cfg.HasProvider("anthropic") {
		anthropicClient = api.NewAnthropicClient(cfg.AnthropicToken, logger)
		logger.Info("Anthropic API client configured")
	}

	var copilotClient *api.CopilotClient
	if cfg.HasProvider("copilot") {
		copilotClient = api.NewCopilotClient(cfg.CopilotToken, logger)
		logger.Info("Copilot API client configured")
	}

	var codexClient *api.CodexClient
	if cfg.CodexToken != "" {
		codexCreds := api.DetectCodexCredentials(logger)
		codexClient = api.NewCodexClient(cfg.CodexToken, logger)
		if codexCreds != nil && codexCreds.AccountID != "" {
			codexClient.SetAccountID(codexCreds.AccountID)
		}
		logger.Info("Codex API client configured")
	}

	var antigravityClient *api.AntigravityClient
	if cfg.HasProvider("antigravity") {
		if cfg.AntigravityBaseURL != "" {
			// Manual configuration (Docker mode)
			conn := &api.AntigravityConnection{
				BaseURL:   cfg.AntigravityBaseURL,
				CSRFToken: cfg.AntigravityCSRFToken,
				Protocol:  "https",
			}
			antigravityClient = api.NewAntigravityClient(logger, api.WithAntigravityConnection(conn))
			logger.Info("Antigravity API client configured (manual)", "baseURL", cfg.AntigravityBaseURL)
		} else {
			// Auto-detection mode
			antigravityClient = api.NewAntigravityClient(logger)
			logger.Info("Antigravity API client configured (auto-detect)")
		}
	}

	// Note: MiniMax clients are now created per-account by MiniMaxAgentManager.
	// The legacy MINIMAX_API_KEY is seeded into the default provider account at startup.

	var openrouterClient *api.OpenRouterClient
	if cfg.HasProvider("openrouter") {
		openrouterClient = api.NewOpenRouterClient(cfg.OpenRouterAPIKey, logger)
		logger.Info("OpenRouter API client configured")
	}

	// Gemini provider - env vars or auto-detect from ~/.gemini/oauth_creds.json
	var geminiClient *api.GeminiClient
	var geminiCreds *api.GeminiCredentials
	if os.Getenv("GEMINI_ENABLED") != "false" {
		geminiCreds = api.DetectGeminiCredentials(logger, db)
		if geminiCreds != nil {
			cfg.GeminiEnabled = true
			cfg.GeminiAutoToken = true
			token := geminiCreds.AccessToken
			// If only refresh token available (no access token), agent will refresh on first poll
			if token == "" && geminiCreds.RefreshToken != "" {
				token = "pending-refresh"
			}
			if token != "" {
				geminiClient = api.NewGeminiClient(token, logger)
				source := "auto-detected"
				if os.Getenv("GEMINI_REFRESH_TOKEN") != "" || os.Getenv("GEMINI_ACCESS_TOKEN") != "" {
					source = "environment variables"
				}
				logger.Info("Gemini API client configured", "source", source)
			}
		}
	}

	// Create components
	tr := tracker.New(db, logger)

	// Create agents with usage-based session managers
	idleTimeout := cfg.SessionIdleTimeout

	var ag *agent.Agent
	if syntheticClient != nil {
		sm := agent.NewSessionManager(db, "synthetic", idleTimeout, logger)
		ag = agent.New(syntheticClient, db, tr, cfg.PollInterval, logger, sm)
	}

	// Create Z.ai tracker
	var zaiTr *tracker.ZaiTracker
	if cfg.HasProvider("zai") {
		zaiTr = tracker.NewZaiTracker(db, logger)
	}

	var zaiAg *agent.ZaiAgent
	if zaiClient != nil {
		zaiSm := agent.NewSessionManager(db, "zai", idleTimeout, logger)
		zaiAg = agent.NewZaiAgent(zaiClient, db, zaiTr, cfg.PollInterval, logger, zaiSm)
	}

	// Create Anthropic tracker
	var anthropicTr *tracker.AnthropicTracker
	if cfg.HasProvider("anthropic") {
		anthropicTr = tracker.NewAnthropicTracker(db, logger)
	}

	var anthropicAg *agent.AnthropicAgent
	if anthropicClient != nil {
		// Load provider settings from DB (overrides .env)
		if db != nil {
			if provJSON, _ := db.GetSetting("provider_settings"); provJSON != "" {
				var provSettings map[string]map[string]interface{}
				if json.Unmarshal([]byte(provJSON), &provSettings) == nil {
					if anthSettings, ok := provSettings["anthropic"]; ok {
						if src, ok := anthSettings["source"].(string); ok && src != "" {
							cfg.AnthropicSource = src
						}
					}
				}
			}
		}

		// ANTHROPIC_SOURCE controls how onWatch gets Anthropic usage data:
		//   "auto"       - statusline when fresh, API polling as fallback (default)
		//   "statusline" - statusline only, no API polling
		//   "api"        - API polling only, no statusline
		anthropicSource := cfg.AnthropicSource
		logger.Info("Anthropic data source configured", "source", anthropicSource)

		// Set up statusline bridge for "auto" and "statusline" modes
		if anthropicSource == "auto" || anthropicSource == "statusline" {
			agent.SetupStatuslineBridge(logger)
		}

		anthropicSm := agent.NewSessionManager(db, "anthropic", idleTimeout, logger)
		anthropicAg = agent.NewAnthropicAgent(anthropicClient, db, anthropicTr, cfg.PollInterval, logger, anthropicSm)

		// Enable API polling for "auto" and "api" modes
		if anthropicSource == "auto" || anthropicSource == "api" {
			anthropicAg.SetTokenRefresh(func() string {
				return api.DetectAnthropicToken(logger)
			})
			anthropicAg.SetCredentialsRefresh(func() *api.AnthropicCredentials {
				return api.DetectAnthropicCredentials(logger)
			})
		}

		// Enable statusline bridge for "auto" and "statusline" modes
		if anthropicSource == "auto" || anthropicSource == "statusline" {
			anthropicAg.EnableStatuslineBridge()
			// In "statusline" mode, disable hybrid API polling since no API
			// credentials are configured. Hybrid only works in "auto" mode.
			if anthropicSource == "statusline" {
				anthropicAg.SetAPIPollCycleInterval(0)
			}
			// Apply DB-configured cycle interval (overrides default 10)
			if db != nil {
				if provJSON, _ := db.GetSetting("provider_settings"); provJSON != "" {
					var provSettings map[string]map[string]interface{}
					if json.Unmarshal([]byte(provJSON), &provSettings) == nil {
						if anthSettings, ok := provSettings["anthropic"]; ok {
							if interval, ok := anthSettings["api_poll_cycle_interval"].(float64); ok && int(interval) > 0 {
								anthropicAg.SetAPIPollCycleInterval(int(interval))
							}
							if sm, ok := anthSettings["staleness_minutes"].(float64); ok && sm > 0 {
								anthropicAg.SetStatuslineStaleness(time.Duration(sm) * time.Minute)
							}
							if cc, ok := anthSettings["cc_detection"].(string); ok {
								anthropicAg.SetCCDetectionEnabled(cc != "off")
							}
						}
					}
				}
			}
		}
	}

	// Create Copilot tracker
	var copilotTr *tracker.CopilotTracker
	if cfg.HasProvider("copilot") {
		copilotTr = tracker.NewCopilotTracker(db, logger)
	}

	var copilotAg *agent.CopilotAgent
	if copilotClient != nil {
		copilotSm := agent.NewSessionManager(db, "copilot", idleTimeout, logger)
		copilotAg = agent.NewCopilotAgent(copilotClient, db, copilotTr, cfg.PollInterval, logger, copilotSm)
	}

	// Create Codex tracker
	var codexTr *tracker.CodexTracker
	if cfg.HasProvider("codex") {
		codexTr = tracker.NewCodexTracker(db, logger)
	}

	// Create Codex agent manager for multi-account support
	var codexMgr *agent.CodexAgentManager
	if cfg.HasProvider("codex") {
		codexMgr = agent.NewCodexAgentManager(db, codexTr, cfg.PollInterval, logger)
		codexMgr.SetProfilesDir(codexProfilesDirWithDataDir(filepath.Dir(cfg.DBPath)))
		// Override profiles dir from DB if configured via UI
		if db != nil {
			if provJSON, _ := db.GetSetting("provider_settings"); provJSON != "" {
				var provSettings map[string]map[string]interface{}
				if json.Unmarshal([]byte(provJSON), &provSettings) == nil {
					if s := provSettings["codex"]; s != nil {
						if dir, _ := s["profiles_dir"].(string); dir != "" {
							codexMgr.SetProfilesDir(dir)
							logger.Info("Codex profiles directory overridden from UI", "dir", dir)
						}
					}
				}
			}
		}
	}

	// Create Antigravity tracker
	var antigravityTr *tracker.AntigravityTracker
	if cfg.HasProvider("antigravity") {
		antigravityTr = tracker.NewAntigravityTracker(db, logger)
	}

	var minimaxTr *tracker.MiniMaxTracker
	if cfg.HasProvider("minimax") {
		minimaxTr = tracker.NewMiniMaxTracker(db, logger)
	}

	var openrouterTr *tracker.OpenRouterTracker
	if cfg.HasProvider("openrouter") {
		openrouterTr = tracker.NewOpenRouterTracker(db, logger)
	}

	var geminiTr *tracker.GeminiTracker
	if cfg.HasProvider("gemini") {
		geminiTr = tracker.NewGeminiTracker(db, logger)
	}

	var antigravityAg *agent.AntigravityAgent
	if antigravityClient != nil {
		antigravitySm := agent.NewSessionManager(db, "antigravity", idleTimeout, logger)
		antigravityAg = agent.NewAntigravityAgent(antigravityClient, db, antigravityTr, cfg.PollInterval, logger, antigravitySm)
	}

	// Create MiniMax agent manager for multi-account support.
	// If a legacy MINIMAX_API_KEY is configured, seed the default account metadata.
	var minimaxMgr *agent.MiniMaxAgentManager
	if cfg.HasProvider("minimax") || minimaxTr != nil {
		minimaxMgr = agent.NewMiniMaxAgentManager(db, minimaxTr, cfg.PollInterval, logger)
		minimaxMgr.SetRegion(cfg.MiniMaxRegion)
		// Seed the default account with the legacy API key if it has no metadata yet
		if cfg.MiniMaxAPIKey != "" {
			if accounts, err := db.QueryActiveProviderAccounts("minimax"); err == nil {
				for _, acc := range accounts {
					if acc.Name == "default" && (acc.Metadata == "" || acc.Metadata == "{}") {
						meta := fmt.Sprintf(`{"api_key":%q,"region":%q}`, cfg.MiniMaxAPIKey, cfg.MiniMaxRegion)
						db.UpdateProviderAccountMetadata(acc.ID, meta)
						logger.Info("Seeded default MiniMax account with legacy API key", "id", acc.ID)
						break
					}
				}
			}
		}
	}

	var openrouterAg *agent.OpenRouterAgent
	if openrouterClient != nil {
		openrouterSm := agent.NewSessionManager(db, "openrouter", idleTimeout, logger)
		openrouterAg = agent.NewOpenRouterAgent(openrouterClient, db, openrouterTr, cfg.PollInterval, logger, openrouterSm)
	}

	var geminiAg *agent.GeminiAgent
	if geminiClient != nil {
		geminiSm := agent.NewSessionManager(db, "gemini", idleTimeout, logger)
		geminiAg = agent.NewGeminiAgent(geminiClient, db, geminiTr, cfg.PollInterval, logger, geminiSm)
		geminiAg.SetCredentialsRefresh(func() *api.GeminiCredentials {
			return api.DetectGeminiCredentials(logger, db)
		})
		geminiAg.SetClientCredentials(api.DetectGeminiClientCredentials())
	}

	// Create notification engine
	notifier := notify.New(db, logger)
	notifier.SetEncryptionKey(deriveEncryptionKey(cfg.AdminPassHash))
	notifier.SetLegacyEncryptionKey(deriveLegacyEncryptionKey(cfg.AdminPassHash))
	notifier.Reload()
	notifier.ConfigureSMTP()
	notifier.ConfigurePush()

	// Wire notifier to agents
	if ag != nil {
		ag.SetNotifier(notifier)
	}
	if zaiAg != nil {
		zaiAg.SetNotifier(notifier)
	}
	if anthropicAg != nil {
		anthropicAg.SetNotifier(notifier)
	}
	if copilotAg != nil {
		copilotAg.SetNotifier(notifier)
	}
	if codexMgr != nil {
		codexMgr.SetNotifier(notifier)
	}
	if antigravityAg != nil {
		antigravityAg.SetNotifier(notifier)
	}
	if minimaxMgr != nil {
		minimaxMgr.SetNotifier(notifier)
	}
	if openrouterAg != nil {
		openrouterAg.SetNotifier(notifier)
	}
	if geminiAg != nil {
		geminiAg.SetNotifier(notifier)
	}

	// Wire polling checks - agents skip poll when telemetry disabled
	isPollingEnabled := func(providerKey string) bool {
		v, err := db.GetSetting("provider_visibility")
		if err != nil || v == "" {
			return true // default: polling enabled
		}
		var vis map[string]map[string]bool
		if json.Unmarshal([]byte(v), &vis) != nil {
			return true
		}
		if pv, ok := vis[providerKey]; ok {
			if polling, exists := pv["polling"]; exists {
				return polling
			}
		}
		return true
	}
	if ag != nil {
		ag.SetPollingCheck(func() bool { return isPollingEnabled("synthetic") })
	}
	if zaiAg != nil {
		zaiAg.SetPollingCheck(func() bool { return isPollingEnabled("zai") })
	}
	if anthropicAg != nil {
		anthropicAg.SetPollingCheck(func() bool { return isPollingEnabled("anthropic") })
	}
	if copilotAg != nil {
		copilotAg.SetPollingCheck(func() bool { return isPollingEnabled("copilot") })
	}
	if codexMgr != nil {
		codexMgr.SetPollingCheck(func() bool { return isPollingEnabled("codex") })
		// Per-account polling check for Codex multi-account support
		// Uses flat keys like "codex:1", "codex:2" for per-account settings
		codexMgr.SetAccountPollingCheck(func(accountID int64) bool {
			v, err := db.GetSetting("provider_visibility")
			if err != nil || v == "" {
				return true // default: polling enabled
			}
			var vis map[string]interface{}
			if json.Unmarshal([]byte(v), &vis) != nil {
				return true
			}
			// Check for per-account key first (e.g., "codex:1")
			accountKey := fmt.Sprintf("codex:%d", accountID)
			if accountVis, ok := vis[accountKey].(map[string]interface{}); ok {
				if polling, exists := accountVis["polling"]; exists {
					if p, ok := polling.(bool); ok {
						return p
					}
				}
			}
			// Fallback to global codex setting
			if codexVis, ok := vis["codex"].(map[string]interface{}); ok {
				if polling, exists := codexVis["polling"]; exists {
					if p, ok := polling.(bool); ok {
						return p
					}
				}
			}
			return true
		})
	}
	if antigravityAg != nil {
		antigravityAg.SetPollingCheck(func() bool { return isPollingEnabled("antigravity") })
	}
	if minimaxMgr != nil {
		minimaxMgr.SetPollingCheck(func() bool { return isPollingEnabled("minimax") })
		minimaxMgr.SetAccountPollingCheck(func(accountID int64) bool {
			v, err := db.GetSetting("provider_visibility")
			if err != nil || v == "" {
				return true
			}
			var vis map[string]interface{}
			if json.Unmarshal([]byte(v), &vis) != nil {
				return true
			}
			accountKey := fmt.Sprintf("minimax:%d", accountID)
			if accountVis, ok := vis[accountKey].(map[string]interface{}); ok {
				if polling, exists := accountVis["polling"]; exists {
					if p, ok := polling.(bool); ok {
						return p
					}
				}
			}
			if minimaxVis, ok := vis["minimax"].(map[string]interface{}); ok {
				if polling, exists := minimaxVis["polling"]; exists {
					if p, ok := polling.(bool); ok {
						return p
					}
				}
			}
			return true
		})
	}
	if openrouterAg != nil {
		openrouterAg.SetPollingCheck(func() bool { return isPollingEnabled("openrouter") })
	}
	if geminiAg != nil {
		geminiAg.SetPollingCheck(func() bool { return isPollingEnabled("gemini") })
	}

	// Wire reset callbacks to trackers
	tr.SetOnReset(func(quotaName string) {
		notifier.Check(notify.QuotaStatus{Provider: "synthetic", QuotaKey: quotaName, ResetOccurred: true})
	})
	if zaiTr != nil {
		zaiTr.SetOnReset(func(quotaName string) {
			notifier.Check(notify.QuotaStatus{Provider: "zai", QuotaKey: quotaName, ResetOccurred: true})
		})
	}
	if anthropicTr != nil {
		anthropicTr.SetOnReset(func(quotaName string) {
			notifier.Check(notify.QuotaStatus{Provider: "anthropic", QuotaKey: quotaName, ResetOccurred: true})
		})
	}
	if copilotTr != nil {
		copilotTr.SetOnReset(func(quotaName string) {
			notifier.Check(notify.QuotaStatus{Provider: "copilot", QuotaKey: quotaName, ResetOccurred: true})
		})
	}
	if codexTr != nil {
		codexTr.SetOnReset(func(quotaName string) {
			notifier.Check(notify.QuotaStatus{Provider: "codex", QuotaKey: quotaName, ResetOccurred: true})
		})
	}
	if antigravityTr != nil {
		antigravityTr.SetOnReset(func(modelID string) {
			notifier.Check(notify.QuotaStatus{Provider: "antigravity", QuotaKey: modelID, ResetOccurred: true})
		})
	}
	if minimaxTr != nil {
		minimaxTr.SetOnReset(func(modelName string) {
			notifier.Check(notify.QuotaStatus{Provider: "minimax", QuotaKey: modelName, ResetOccurred: true})
		})
	}
	if openrouterTr != nil {
		openrouterTr.SetOnReset(func(quotaName string) {
			notifier.Check(notify.QuotaStatus{Provider: "openrouter", QuotaKey: quotaName, ResetOccurred: true})
		})
	}
	if geminiTr != nil {
		geminiTr.SetOnReset(func(modelID string) {
			notifier.Check(notify.QuotaStatus{Provider: "gemini", QuotaKey: modelID, ResetOccurred: true})
		})
	}

	handler := web.NewHandler(db, tr, logger, nil, cfg, zaiTr)
	handler.SetVersion(version)
	handler.SetNotifier(notifier)
	if anthropicTr != nil {
		handler.SetAnthropicTracker(anthropicTr)
	}
	if copilotTr != nil {
		handler.SetCopilotTracker(copilotTr)
	}
	if codexTr != nil {
		handler.SetCodexTracker(codexTr)
	}
	if antigravityTr != nil {
		handler.SetAntigravityTracker(antigravityTr)
	}
	if minimaxTr != nil {
		handler.SetMiniMaxTracker(minimaxTr)
	}
	if openrouterTr != nil {
		handler.SetOpenRouterTracker(openrouterTr)
	}
	if geminiTr != nil {
		handler.SetGeminiTracker(geminiTr)
	}
	agentMgr := agent.NewAgentManager(logger)
	if ag != nil {
		agentMgr.RegisterFactory("synthetic", func() (agent.AgentRunner, error) { return ag, nil })
	}
	if zaiAg != nil {
		agentMgr.RegisterFactory("zai", func() (agent.AgentRunner, error) { return zaiAg, nil })
	}
	if anthropicAg != nil {
		agentMgr.RegisterFactory("anthropic", func() (agent.AgentRunner, error) { return anthropicAg, nil })
	}
	if copilotAg != nil {
		agentMgr.RegisterFactory("copilot", func() (agent.AgentRunner, error) { return copilotAg, nil })
	}
	if codexMgr != nil {
		agentMgr.RegisterFactory("codex", func() (agent.AgentRunner, error) { return codexMgr, nil })
	}
	if antigravityAg != nil {
		agentMgr.RegisterFactory("antigravity", func() (agent.AgentRunner, error) { return antigravityAg, nil })
	}
	if minimaxMgr != nil {
		agentMgr.RegisterFactory("minimax", func() (agent.AgentRunner, error) { return minimaxMgr, nil })
	}
	if openrouterAg != nil {
		agentMgr.RegisterFactory("openrouter", func() (agent.AgentRunner, error) { return openrouterAg, nil })
	}
	if geminiAg != nil {
		agentMgr.RegisterFactory("gemini", func() (agent.AgentRunner, error) { return geminiAg, nil })
	}
	handler.SetAgentManager(agentMgr)
	if minimaxMgr != nil {
		handler.SetMiniMaxAgentManager(minimaxMgr)
	}
	updater := update.NewUpdater(version, logger)
	handler.SetUpdater(updater)

	// Create login rate limiter for brute force protection
	loginRateLimiter := web.NewLoginRateLimiter(1000)
	handler.SetRateLimiter(loginRateLimiter)

	server := web.NewServer(cfg.Port, handler, logger, cfg.AdminUser, cfg.AdminPassHash, cfg.Host, cfg.BasePath)

	// Setup signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start configured agents through the manager.
	startedAny := false
	for _, providerKey := range []string{"synthetic", "zai", "anthropic", "copilot", "codex", "antigravity", "minimax", "openrouter", "gemini"} {
		if !isPollingEnabled(providerKey) {
			continue
		}
		if err := agentMgr.Start(providerKey); err == nil {
			startedAny = true
			continue
		}
	}
	if !startedAny {
		logger.Info("No agents configured")
	}

	// Start web server in goroutine
	serverErr := make(chan error, 1)
	go func() {
		logger.Info("Starting web server", "port", cfg.Port)
		if err := server.Start(); err != nil {
			serverErr <- fmt.Errorf("server error: %w", err)
		}
	}()

	if runtime.GOOS == "darwin" && menubar.IsSupported() {
		go func() {
			if waitForServerReady(cfg.Port, 10*time.Second) {
				if err := startMenubarCompanion(cfg, logger); err != nil {
					logger.Warn("failed to start menubar companion", "error", err)
				}
			}
		}()
	}

	// Periodically return freed memory to the OS. On macOS, MADV_FREE pages
	// are reclaimable but still counted in RSS. FreeOSMemory forces MADV_DONTNEED.
	// Also evict stale rate limiter entries and expired session tokens to prevent memory growth.
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				debug.FreeOSMemory()
				loginRateLimiter.EvictStaleEntries(5 * time.Minute)
				// Evict expired session tokens
				if sessions := server.GetSessionStore(); sessions != nil {
					sessions.EvictExpiredTokens()
				}
			}
		}
	}()

	// Wait for signal or error
	select {
	case sig := <-sigChan:
		logger.Info("Received signal, shutting down gracefully", "signal", sig)
	case err := <-serverErr:
		logger.Error("Server failed", "error", err)
		cancel()
	}

	// Graceful shutdown sequence
	logger.Info("Shutting down...")

	// Cancel context to stop agent
	cancel()
	agentMgr.StopAll()
	_ = stopMenubarProcess(cfg.TestMode)

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

// runStop stops any running onwatch instance.
// In test mode, only the test PID file is used (no port scanning) to avoid killing production.
func runStop(testMode bool) error {
	myPID := os.Getpid()
	stopped := false
	label := "onwatch"
	if testMode {
		label = "onwatch (test)"
	}

	// Method 1: PID file (handles both "PID" and "PID:PORT" formats)
	if data, err := os.ReadFile(pidFile); err == nil {
		content := strings.TrimSpace(string(data))
		var pid, port int

		// Parse PID:PORT format (new) or just PID (legacy)
		if strings.Contains(content, ":") {
			parts := strings.Split(content, ":")
			if len(parts) >= 2 {
				pid, _ = strconv.Atoi(parts[0])
				port, _ = strconv.Atoi(parts[1])
			}
		} else {
			pid, _ = strconv.Atoi(content)
		}

		if pid > 0 && pid != myPID {
			if proc, err := os.FindProcess(pid); err == nil {
				if err := proc.Signal(syscall.SIGTERM); err == nil {
					if port > 0 {
						fmt.Printf("Stopped %s (PID %d) on port %d\n", label, pid, port)
					} else {
						fmt.Printf("Stopped %s (PID %d)\n", label, pid)
					}
					stopped = true
				} else {
					fmt.Printf("Process %d not running (stale PID file)\n", pid)
				}
			}
		}
		os.Remove(pidFile)

		// If we have a port from PID file, try port-based detection on that specific port first
		// Skip in test mode to avoid killing production instances
		if !testMode && !stopped && port > 0 {
			conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 500*time.Millisecond)
			if err == nil {
				conn.Close()
				if pids := findOnwatchOnPort(port); len(pids) > 0 {
					for _, foundPID := range pids {
						if foundPID == myPID {
							continue
						}
						if proc, err := os.FindProcess(foundPID); err == nil {
							if err := proc.Signal(syscall.SIGTERM); err == nil {
								fmt.Printf("Stopped %s (PID %d) on port %d\n", label, foundPID, port)
								stopped = true
							}
						}
					}
				}
			}
		}
	}

	// Method 2: Port-based fallback - check default ports
	// Skip in test mode to avoid killing production instances
	if !testMode && !stopped {
		// Check both old (8932) and new (9211) default ports for backwards compatibility
		for _, port := range []int{9211, 8932} {
			conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 500*time.Millisecond)
			if err != nil {
				continue
			}
			conn.Close()
			if pids := findOnwatchOnPort(port); len(pids) > 0 {
				for _, pid := range pids {
					if pid == myPID {
						continue
					}
					if proc, err := os.FindProcess(pid); err == nil {
						if err := proc.Signal(syscall.SIGTERM); err == nil {
							fmt.Printf("Stopped %s (PID %d) on port %d\n", label, pid, port)
							stopped = true
						}
					}
				}
			}
		}
	}

	if !stopped {
		fmt.Printf("No running %s instance found\n", label)
	}
	if menubarPID := readRuntimePID(menubarPIDPath(testMode)); menubarPID > 0 {
		_ = stopMenubarProcess(testMode)
		fmt.Printf("Stopped %s menubar companion (PID %d)\n", label, menubarPID)
	}
	return nil
}

// runStatus reports the status of any running onwatch instance.
// In test mode, only the test PID file is checked (no port scanning).
func statusLogCandidates(dbPath string, names ...string) []string {
	if len(names) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(names)*3)
	paths := make([]string, 0, len(names)*3)
	appendPath := func(path string) {
		if path == "" {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}

	if dbPath != "" {
		dir := filepath.Dir(dbPath)
		for _, name := range names {
			appendPath(filepath.Join(dir, name))
		}
	}

	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		for _, name := range names {
			appendPath(filepath.Join(home, ".onwatch", name))
		}
	}

	if dbPath == "" {
		for _, name := range names {
			appendPath(filepath.Join(pidDir, name))
		}
	}

	for _, name := range names {
		appendPath(filepath.Join(".", name))
	}

	return paths
}

func firstExistingFile(paths []string) (string, int64, bool) {
	for _, path := range paths {
		if info, err := os.Stat(path); err == nil {
			return path, info.Size(), true
		}
	}
	return "", 0, false
}

func runStatus(testMode bool) error {
	myPID := os.Getpid()
	label := "onwatch"
	if testMode {
		label = "onwatch (test)"
	}

	// Check PID file (handles both "PID" and "PID:PORT" formats)
	if data, err := os.ReadFile(pidFile); err == nil {
		content := strings.TrimSpace(string(data))
		var pid, port int

		// Parse PID:PORT format (new) or just PID (legacy)
		if strings.Contains(content, ":") {
			parts := strings.Split(content, ":")
			if len(parts) >= 2 {
				pid, _ = strconv.Atoi(parts[0])
				port, _ = strconv.Atoi(parts[1])
			}
		} else {
			pid, _ = strconv.Atoi(content)
		}

		if pid > 0 && pid != myPID {
			if proc, err := os.FindProcess(pid); err == nil {
				// On Unix, signal 0 checks if process exists without killing it
				if err := proc.Signal(syscall.Signal(0)); err == nil {
					fmt.Printf("%s is running (PID %d)\n", label, pid)

					// If we have port from PID file, show it directly
					if port > 0 {
						fmt.Printf("  Dashboard: http://localhost:%d\n", port)
					} else if !testMode {
						// Check which port it's listening on (skip in test mode)
						for _, checkPort := range []int{9211, 8932, 8080, 9000} {
							if pids := findOnwatchOnPort(checkPort); len(pids) > 0 {
								for _, p := range pids {
									if p == pid {
										fmt.Printf("  Dashboard: http://localhost:%d\n", checkPort)
										break
									}
								}
							}
						}
					}

					fmt.Printf("  PID file:  %s\n", pidFile)
					menubarPID := readRuntimePID(menubarPIDPath(testMode))
					if processRunning(menubarPID) {
						fmt.Printf("  Menubar:   running (PID %d)\n", menubarPID)
					}

					home, _ := os.UserHomeDir()
					dbCandidates := []string{}
					if home != "" {
						dbCandidates = append(dbCandidates, filepath.Join(home, ".onwatch", "data", "onwatch.db"))
					}
					dbCandidates = append(dbCandidates, "./onwatch.db")
					dbPath := ""
					var dbSize int64
					for _, candidate := range dbCandidates {
						if info, err := os.Stat(candidate); err == nil {
							dbPath = candidate
							dbSize = info.Size()
							break
						}
					}

					mainLogName := ".onwatch.log"
					menubarNames := menubarLogNames(false)
					if testMode {
						mainLogName = ".onwatch-test.log"
						menubarNames = menubarLogNames(true)
					}
					if logPath, logSize, ok := firstExistingFile(statusLogCandidates(dbPath, mainLogName)); ok {
						fmt.Printf("  Log file:  %s (%s)\n", logPath, humanSize(logSize))
					}
					if logPath, logSize, ok := firstExistingFile(statusLogCandidates(dbPath, menubarNames...)); ok {
						fmt.Printf("  Menubar log: %s (%s)\n", logPath, humanSize(logSize))
					}
					if dbPath != "" {
						fmt.Printf("  Database:  %s (%s)\n", dbPath, humanSize(dbSize))
					}

					return nil
				}
			}
			// Stale PID file
			fmt.Printf("%s is not running (stale PID file for PID %d)\n", label, pid)
			return nil
		}
	}

	// No PID file - try port check (skip in test mode to avoid confusion with production)
	if !testMode {
		for _, port := range []int{9211, 8932} {
			conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 500*time.Millisecond)
			if err != nil {
				continue
			}
			conn.Close()
			if pids := findOnwatchOnPort(port); len(pids) > 0 {
				for _, pid := range pids {
					if pid == myPID {
						continue
					}
					fmt.Printf("%s is running (PID %d) on port %d\n", label, pid, port)
					fmt.Printf("  Dashboard: http://localhost:%d\n", port)
					return nil
				}
			}
		}
	}

	fmt.Printf("%s is not running\n", label)
	return nil
}

// humanSize returns a human-readable file size.
func humanSize(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%dB", bytes)
	}
	if bytes < 1024*1024 {
		return fmt.Sprintf("%.1fKB", float64(bytes)/1024)
	}
	return fmt.Sprintf("%.1fMB", float64(bytes)/(1024*1024))
}

type cliUpdater interface {
	Check() (update.UpdateInfo, error)
	Apply() error
}

var newCLIUpdater = func(v string, logger *slog.Logger) cliUpdater {
	return update.NewUpdater(v, logger)
}

func runUpdate() error {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	u := newCLIUpdater(version, logger)

	fmt.Printf("onWatch v%s - checking for updates...\n", version)

	info, err := u.Check()
	if err != nil {
		return fmt.Errorf("update check failed: %w", err)
	}

	if !info.Available {
		fmt.Printf("Already at the latest version (v%s)\n", version)
		return nil
	}

	fmt.Printf("Update available: v%s → v%s\n", info.CurrentVersion, info.LatestVersion)
	fmt.Printf("Downloading from %s\n", info.DownloadURL)

	if err := u.Apply(); err != nil {
		return fmt.Errorf("update failed: %w", err)
	}

	fmt.Printf("Updated successfully to v%s\n", info.LatestVersion)

	// If a daemon is running, stop it and start a fresh one
	if data, err := os.ReadFile(pidFile); err == nil {
		content := strings.TrimSpace(string(data))
		var pid int
		if strings.Contains(content, ":") {
			parts := strings.Split(content, ":")
			if len(parts) >= 1 {
				pid, _ = strconv.Atoi(parts[0])
			}
		} else {
			pid, _ = strconv.Atoi(content)
		}
		if pid > 0 && pid != os.Getpid() {
			fmt.Println("Restarting daemon...")
			// Stop old daemon
			if proc, err := os.FindProcess(pid); err == nil {
				_ = proc.Signal(syscall.SIGTERM)
				time.Sleep(1 * time.Second)
			}
			// Start new daemon with the updated binary (no args = daemonize with .env config)
			exePath, err := os.Executable()
			if err == nil {
				exePath, _ = filepath.EvalSymlinks(exePath)
				cmd := exec.Command(exePath)
				cmd.Env = os.Environ()
				if err := cmd.Start(); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: restart failed: %v\n", err)
					fmt.Println("Please restart onwatch manually.")
				} else {
					fmt.Printf("New daemon started (PID %d)\n", cmd.Process.Pid)
				}
			}
		}
	}

	return nil
}

func printBanner(cfg *config.Config, version string) {
	fmt.Println()
	fmt.Println("╔══════════════════════════════════════╗")
	fmt.Printf("║  onWatch v%-26s ║\n", version)
	fmt.Println("╠══════════════════════════════════════╣")

	// Show configured providers
	providers := cfg.AvailableProviders()
	if len(providers) > 0 {
		fmt.Printf("║  Providers: %-24s ║\n", strings.Join(providers, ", "))
	}

	if cfg.HasProvider("synthetic") {
		fmt.Println("║  API:       synthetic.new/v2/quotas  ║")
	}
	if cfg.HasProvider("zai") {
		fmt.Println("║  API:       z.ai/api                ║")
	}
	if cfg.HasProvider("anthropic") {
		if cfg.AnthropicAutoToken {
			fmt.Println("║  API:       anthropic (auto-detect)  ║")
		} else {
			fmt.Println("║  API:       anthropic.com/usage      ║")
		}
	}
	if cfg.HasProvider("copilot") {
		fmt.Println("║  API:       github.com/copilot (β)   ║")
	}
	if cfg.HasProvider("codex") {
		if cfg.CodexAutoToken {
			fmt.Println("║  API:       chatgpt.com/wham (auto)  ║")
		} else {
			fmt.Println("║  API:       chatgpt.com/wham         ║")
		}
	}
	if cfg.HasProvider("minimax") {
		fmt.Println("║  API:       minimax.io/coding_plan   ║")
	}
	if cfg.HasProvider("openrouter") {
		fmt.Println("║  API:       openrouter.ai/api         ║")
	}
	if cfg.HasProvider("gemini") {
		fmt.Println("║  API:       cloudcode-pa.google.com  ║")
	}

	fmt.Printf("║  Polling:   every %s              ║\n", cfg.PollInterval)
	fmt.Printf("║  Dashboard: http://localhost:%d    ║\n", cfg.Port)
	fmt.Printf("║  Database:  %-24s ║\n", cfg.DBPath)
	fmt.Printf("║  Auth:      %s / ****             ║\n", cfg.AdminUser)
	if cfg.TestMode {
		fmt.Println("║  Mode:      TEST (isolated)          ║")
	}
	fmt.Println("╚══════════════════════════════════════╝")
	fmt.Println()

	// Show API keys
	if cfg.HasProvider("synthetic") {
		fmt.Printf("Synthetic API Key: %s\n", redactAPIKey(cfg.SyntheticAPIKey))
	}
	if cfg.HasProvider("zai") {
		fmt.Printf("Z.ai API Key:      %s\n", redactAPIKey(cfg.ZaiAPIKey))
	}
	if cfg.HasProvider("anthropic") {
		label := "Anthropic Token:   "
		if cfg.AnthropicAutoToken {
			label = "Anthropic (auto):  "
		}
		fmt.Printf("%s%s\n", label, redactAPIKey(cfg.AnthropicToken))
	}
	if cfg.HasProvider("copilot") {
		fmt.Printf("Copilot Token:     %s\n", redactAPIKey(cfg.CopilotToken))
	}
	if cfg.HasProvider("codex") {
		label := "Codex Token:       "
		if cfg.CodexAutoToken {
			label = "Codex (auto):      "
		}
		fmt.Printf("%s%s\n", label, redactAPIKey(cfg.CodexToken))
	}
	if cfg.HasProvider("antigravity") {
		fmt.Printf("Antigravity:       %s\n", "auto-detect")
	}
	if cfg.HasProvider("minimax") {
		fmt.Printf("MiniMax API Key:   %s\n", redactAPIKey(cfg.MiniMaxAPIKey))
	}
	if cfg.HasProvider("openrouter") {
		fmt.Printf("OpenRouter Key:    %s\n", redactAPIKey(cfg.OpenRouterAPIKey))
	}
	if cfg.HasProvider("gemini") {
		source := "auto-detect"
		if cfg.GeminiRefreshToken != "" || cfg.GeminiAccessToken != "" {
			source = "env vars"
		}
		fmt.Printf("Gemini:            %s\n", source)
	}
	fmt.Println()
}

func printHelp() {
	fmt.Println("onWatch - Multi-Provider API Usage Tracker")
	fmt.Println()
	fmt.Println("Usage: onwatch [COMMAND] [OPTIONS]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  setup, --setup     Interactive setup wizard (configure providers and .env)")
	fmt.Println("  stop, --stop       Stop the running onwatch instance")
	fmt.Println("  status, --status   Show status of the running instance")
	fmt.Println("  update, --update   Check for updates and self-update")
	fmt.Println()
	fmt.Println("Codex Profile Management:")
	fmt.Println("  codex profile save <name>    Save current Codex credentials as a named profile")
	fmt.Println("  codex profile refresh <name> Refresh a saved profile from current Codex auth session")
	fmt.Println("  codex profile list           List saved Codex profiles")
	fmt.Println("  codex profile delete <name>  Delete a saved Codex profile")
	fmt.Println("  codex profile status         Show polling status for all profiles")
	fmt.Println()
	fmt.Println("Options:")
	fmt.Println("  version, --version Print version and exit")
	fmt.Println("  --help             Print this help message")
	fmt.Println("  --interval SEC     Polling interval in seconds (default: 120)")
	fmt.Println("  --port PORT        Dashboard HTTP port (default: 9211)")
	fmt.Println("  --db PATH          SQLite database file path (default: ~/.onwatch/data/onwatch.db)")
	fmt.Println("  --debug            Run in foreground mode (logs to file, stdout gets warn/error)")
	fmt.Println("  --debugstdout      Run in foreground mode with all logs to stdout")
	fmt.Println("  --test             Test mode: isolated PID/log files, won't affect production")
	fmt.Println()
	fmt.Println("Environment Variables:")
	fmt.Println("  SYNTHETIC_API_KEY       Synthetic API key")
	fmt.Println("  ZAI_API_KEY            Z.ai API key")
	fmt.Println("  ZAI_BASE_URL           Z.ai base URL (default: https://api.z.ai/api)")
	fmt.Println("  ZAI_REGION             Z.ai region: global or cn (default: global)")
	fmt.Println("  ANTHROPIC_TOKEN         Anthropic token (auto-detected if not set)")
	fmt.Println("  COPILOT_TOKEN           GitHub Copilot token (PAT with copilot scope)")
	fmt.Println("  CODEX_TOKEN             Codex OAuth token (recommended; required for Codex-only)")
	fmt.Println("  MINIMAX_API_KEY         MiniMax API key")
	fmt.Println("  MINIMAX_REGION          MiniMax region: global or cn (default: global)")
	fmt.Println("  OPENROUTER_API_KEY      OpenRouter API key")
	fmt.Println("  CODEX_HOME              Optional Codex auth directory (uses CODEX_HOME/auth.json)")
	fmt.Println("  ONWATCH_POLL_INTERVAL   Polling interval in seconds")
	fmt.Println("  ONWATCH_PORT            Dashboard HTTP port")
	fmt.Println("  ONWATCH_ADMIN_USER      Dashboard admin username")
	fmt.Println("  ONWATCH_ADMIN_PASS      Dashboard admin password")
	fmt.Println("  ONWATCH_DB_PATH         SQLite database file path")
	fmt.Println("  ONWATCH_LOG_LEVEL       Log level: debug, info, warn, error")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  onwatch setup                     # Interactive setup wizard")
	fmt.Println("  onwatch                           # Run in background mode")
	fmt.Println("  onwatch --debug                   # Run in foreground mode")
	fmt.Println("  onwatch --interval 30 --port 8080 # Custom interval and port")
	fmt.Println("  onwatch stop                      # Stop running instance")
	fmt.Println("  onwatch --stop                    # Same as 'stop'")
	fmt.Println("  onwatch status                    # Check if running")
	fmt.Println("  onwatch --status                  # Same as 'status'")
	fmt.Println("  onwatch update                    # Check for updates and self-update")
	fmt.Println("  onwatch --test --debug            # Run test instance (isolated)")
	fmt.Println("  onwatch --test stop               # Stop only test instance")
	fmt.Println("  onwatch --test status             # Check test instance status")
	fmt.Println()
	fmt.Println("Test Mode (--test):")
	fmt.Println("  Uses separate PID file (onwatch-test.pid) and log file (.onwatch-test.log).")
	fmt.Println("  Test instances never kill production instances and vice versa.")
	fmt.Println("  Use --db and --port to further isolate test from production.")
	fmt.Println()
	fmt.Println("Anthropic and Codex tokens can be auto-detected from local auth when another provider is already configured.")
	fmt.Println("Configure providers in .env file or environment variables (or enable via Settings).")
}

func redactAPIKey(key string) string {
	if key == "" {
		return "(not set)"
	}
	if len(key) < 8 {
		return "***"
	}

	// Handle "syn_" prefix for Synthetic keys
	prefix := ""
	if strings.HasPrefix(key, "syn_") {
		prefix = "syn_"
		key = key[4:]
	}

	if len(key) <= 8 {
		return prefix + key[:4] + "***"
	}
	return prefix + key[:4] + "***" + key[len(key)-4:]
}

// initEncryptionSalt loads or generates the encryption salt for HKDF key derivation.
// If no salt exists in the database, a new one is generated and stored.
func initEncryptionSalt(db *store.Store, logger *slog.Logger) error {
	// Try to load existing salt
	saltHex, err := db.GetSetting("encryption_salt")
	if err == nil && saltHex != "" {
		// Decode and use existing salt
		salt, err := hex.DecodeString(saltHex)
		if err == nil && len(salt) == 16 {
			web.SetEncryptionSalt(salt)
			logger.Debug("Loaded encryption salt from database")
			return nil
		}
	}

	// Generate new salt
	salt, err := web.GenerateEncryptionSalt()
	if err != nil {
		return fmt.Errorf("failed to generate encryption salt: %w", err)
	}

	// Store salt in database
	if err := db.SetSetting("encryption_salt", hex.EncodeToString(salt)); err != nil {
		return fmt.Errorf("failed to store encryption salt: %w", err)
	}

	web.SetEncryptionSalt(salt)
	logger.Info("Generated and stored new encryption salt")
	return nil
}
