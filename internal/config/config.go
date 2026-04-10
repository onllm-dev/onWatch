// Package config handles loading and validation of onWatch configuration.
// It loads from .env files, environment variables, and CLI flags.
package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

const (
	maxLogFileBytes = 50 * 1024 * 1024
	maxLogBackups   = 3
)

// Config holds all application configuration.
type Config struct {
	// Synthetic provider configuration
	SyntheticAPIKey string // SYNTHETIC_API_KEY

	// Z.ai provider configuration
	ZaiAPIKey  string // ZAI_API_KEY
	ZaiBaseURL string // ZAI_BASE_URL (auto-selected based on ZaiRegion)
	ZaiRegion  string // ZAI_REGION ( "global" | "cn", default: "global" )

	// Anthropic provider configuration
	AnthropicToken     string // ANTHROPIC_TOKEN or auto-detected
	AnthropicAutoToken bool   // true if token was auto-detected
	AnthropicSource    string // ANTHROPIC_SOURCE: "auto" (default), "statusline", "api"

	// Copilot provider configuration
	CopilotToken string // COPILOT_TOKEN (GitHub PAT with copilot scope)

	// Codex provider configuration
	CodexToken        string // CODEX_TOKEN or auto-detected
	CodexAutoToken    bool   // true if token was auto-detected
	CodexHasProfiles  bool   // true if saved profiles exist (enables bootstrap without token)
	CodexShowAvailable string // CODEX_SHOW_AVAILABLE: "usage" | "available", default "usage"

	// Antigravity provider configuration (auto-detected from local process)
	AntigravityBaseURL   string // ANTIGRAVITY_BASE_URL (for Docker)
	AntigravityCSRFToken string // ANTIGRAVITY_CSRF_TOKEN (for Docker)
	AntigravityEnabled   bool   // true if auto-detection should be attempted

	// MiniMax provider configuration
	MiniMaxAPIKey  string // MINIMAX_API_KEY
	MiniMaxRegion  string // MINIMAX_REGION ( "global" | "cn", default: "global" )

	// OpenRouter provider configuration
	OpenRouterAPIKey string // OPENROUTER_API_KEY

	// Gemini provider configuration (auto-detected from ~/.gemini/oauth_creds.json or env vars)
	GeminiEnabled      bool   // true if auto-detected or GEMINI_ENABLED=true
	GeminiAutoToken    bool   // true if token was auto-detected
	GeminiRefreshToken string // GEMINI_REFRESH_TOKEN (for Docker/headless)
	GeminiAccessToken  string // GEMINI_ACCESS_TOKEN (for Docker/headless)

	// Shared configuration
	PollInterval       time.Duration // ONWATCH_POLL_INTERVAL (seconds → Duration)
	Port               int           // ONWATCH_PORT
	Host               string        // ONWATCH_HOST (bind address, default: 0.0.0.0)
	SecureCookies      bool          // ONWATCH_SECURE_COOKIES (set Secure flag on cookies)
	AdminUser          string        // ONWATCH_ADMIN_USER
	AdminPass          string        // ONWATCH_ADMIN_PASS
	AdminPassHash      string        // SHA-256 hash of password (set after DB check)
	DBPath             string        // ONWATCH_DB_PATH
	DBPathExplicit     bool          // true if user explicitly set --db or ONWATCH_DB_PATH
	LogLevel           string        // ONWATCH_LOG_LEVEL
	SessionIdleTimeout time.Duration // ONWATCH_SESSION_IDLE_TIMEOUT (seconds → Duration)
	BasePath           string        // ONWATCH_BASE_PATH (subdirectory hosting, e.g. "/onwatch")
	DebugMode          bool          // --debug flag (foreground mode)
	DebugStdout        bool          // --debugstdout flag (foreground + all logs to stdout)
	TestMode           bool          // --test flag (test mode isolation)
}

// envWithFallback reads the primary env var, falling back to the legacy name.
// This provides backward compatibility for SYNTRACK_* → ONWATCH_* rename.
func envWithFallback(primary, fallback string) string {
	if v := os.Getenv(primary); v != "" {
		return v
	}
	return os.Getenv(fallback)
}

// expandTilde replaces a leading ~ with the user's home directory.
// Shell does this automatically, but Go's os.Getenv returns the literal ~.
func expandTilde(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	return filepath.Join(home, path[2:])
}

// flagValues holds parsed CLI flags.
type flagValues struct {
	interval int
	port     int
	db       string
	debug       bool
	debugStdout bool
	test        bool
}

// Load reads configuration from .env file, environment variables, and CLI flags.
// Flags take precedence over environment variables.
func Load() (*Config, error) {
	return loadWithArgs(os.Args[1:])
}

// loadWithArgs loads config with specific arguments (for testing).
func loadWithArgs(args []string) (*Config, error) {
	flags := &flagValues{}

	// Parse CLI flags manually to avoid flag.ExitOnError in tests
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--debug":
			flags.debug = true
		case arg == "--debugstdout":
			flags.debug = true
			flags.debugStdout = true
		case arg == "--test":
			flags.test = true
		case strings.HasPrefix(arg, "--interval="):
			val := strings.TrimPrefix(arg, "--interval=")
			if v, err := strconv.Atoi(val); err == nil {
				flags.interval = v
			}
		case arg == "--interval":
			if i+1 < len(args) {
				if v, err := strconv.Atoi(args[i+1]); err == nil {
					flags.interval = v
					i++
				}
			}
		case strings.HasPrefix(arg, "--port="):
			val := strings.TrimPrefix(arg, "--port=")
			if v, err := strconv.Atoi(val); err == nil {
				flags.port = v
			}
		case arg == "--port":
			if i+1 < len(args) {
				if v, err := strconv.Atoi(args[i+1]); err == nil {
					flags.port = v
					i++
				}
			}
		case strings.HasPrefix(arg, "--db="):
			flags.db = strings.TrimPrefix(arg, "--db=")
		case arg == "--db":
			if i+1 < len(args) {
				flags.db = args[i+1]
				i++
			}
		}
	}

	return loadFromEnvAndFlags(flags)
}

// onwatchEnvKeys are the keys that identify an onwatch-specific .env file.
var onwatchEnvKeys = []string{
	"SYNTHETIC_API_KEY",
	"ZAI_API_KEY",
	"ANTHROPIC_TOKEN",
	"COPILOT_TOKEN",
	"CODEX_TOKEN",
	"ANTIGRAVITY_ENABLED",
	"MINIMAX_API_KEY",
	"OPENROUTER_API_KEY",
	"GEMINI_ENABLED",
	"GEMINI_REFRESH_TOKEN",
	"GEMINI_ACCESS_TOKEN",
	"ONWATCH_",
}

// isOnwatchEnvFile checks if a file contains onwatch-specific configuration.
func isOnwatchEnvFile(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	content := string(data)
	for _, key := range onwatchEnvKeys {
		if strings.Contains(content, key) {
			return true
		}
	}
	return false
}

// loadEnvFile loads the .env file from the appropriate location.
// Priority:
//  1. ~/.onwatch/.env (standard install location)
//  2. ./.env (current directory) - only if it contains onwatch-specific keys
func loadEnvFile() {
	// Try standard install location first: ~/.onwatch/.env
	if home, err := os.UserHomeDir(); err == nil {
		standardPath := filepath.Join(home, ".onwatch", ".env")
		if _, err := os.Stat(standardPath); err == nil {
			if err := godotenv.Load(standardPath); err == nil {
				fmt.Fprintf(os.Stderr, "  config: loaded %s\n", standardPath)
				return
			}
		}
	}

	// Fallback to current directory .env - only if it's onwatch-specific
	localPath := ".env"
	if _, err := os.Stat(localPath); err == nil {
		if isOnwatchEnvFile(localPath) {
			if err := godotenv.Load(localPath); err == nil {
				fmt.Fprintf(os.Stderr, "  config: loaded %s\n", localPath)
				return
			}
		}
	}

	// No .env file found - will rely on environment variables
}

// loadFromEnvAndFlags combines environment variables with CLI flags.
func loadFromEnvAndFlags(flags *flagValues) (*Config, error) {
	// Load .env file from the appropriate location
	loadEnvFile()

	cfg := &Config{}

	// Synthetic provider
	cfg.SyntheticAPIKey = os.Getenv("SYNTHETIC_API_KEY")

	// Z.ai provider
	cfg.ZaiAPIKey = os.Getenv("ZAI_API_KEY")
	cfg.ZaiBaseURL = os.Getenv("ZAI_BASE_URL")
	cfg.ZaiRegion = strings.ToLower(strings.TrimSpace(os.Getenv("ZAI_REGION")))
	if cfg.ZaiRegion == "" {
		cfg.ZaiRegion = "global"
	}

	// Anthropic provider
	cfg.AnthropicToken = os.Getenv("ANTHROPIC_TOKEN")
	cfg.AnthropicSource = strings.ToLower(strings.TrimSpace(os.Getenv("ANTHROPIC_SOURCE")))
	if cfg.AnthropicSource == "" {
		cfg.AnthropicSource = "auto"
	}

	// Copilot provider
	cfg.CopilotToken = os.Getenv("COPILOT_TOKEN")

	// Codex provider
	cfg.CodexToken = strings.TrimSpace(os.Getenv("CODEX_TOKEN"))
	cfg.CodexShowAvailable = strings.ToLower(strings.TrimSpace(os.Getenv("CODEX_SHOW_AVAILABLE")))
	if cfg.CodexShowAvailable == "" {
		cfg.CodexShowAvailable = "usage"
	}
	// Validate display mode - only accept "usage" or "available"
	if cfg.CodexShowAvailable != "usage" && cfg.CodexShowAvailable != "available" {
		cfg.CodexShowAvailable = "usage"
	}

	// Antigravity provider (auto-detection, or manual via env vars for Docker)
	cfg.AntigravityBaseURL = os.Getenv("ANTIGRAVITY_BASE_URL")
	cfg.AntigravityCSRFToken = os.Getenv("ANTIGRAVITY_CSRF_TOKEN")
	// Enable Antigravity if: (1) manual config provided, or (2) ANTIGRAVITY_ENABLED=true, or (3) auto-detect
	if cfg.AntigravityBaseURL != "" || os.Getenv("ANTIGRAVITY_ENABLED") == "true" {
		cfg.AntigravityEnabled = true
	}

	// MiniMax provider
	cfg.MiniMaxAPIKey = strings.TrimSpace(os.Getenv("MINIMAX_API_KEY"))
	cfg.MiniMaxRegion = strings.ToLower(strings.TrimSpace(os.Getenv("MINIMAX_REGION")))
	if cfg.MiniMaxRegion == "" {
		cfg.MiniMaxRegion = "global"
	}

	// OpenRouter provider
	cfg.OpenRouterAPIKey = strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY"))

	// Gemini provider (auto-detected, env vars, or opt-out via GEMINI_ENABLED=false)
	cfg.GeminiRefreshToken = strings.TrimSpace(os.Getenv("GEMINI_REFRESH_TOKEN"))
	cfg.GeminiAccessToken = strings.TrimSpace(os.Getenv("GEMINI_ACCESS_TOKEN"))
	if os.Getenv("GEMINI_ENABLED") == "false" {
		cfg.GeminiEnabled = false
	} else if os.Getenv("GEMINI_ENABLED") == "true" || cfg.GeminiRefreshToken != "" || cfg.GeminiAccessToken != "" {
		cfg.GeminiEnabled = true
	}
	// File-based auto-detection is done later in main.go

	// Poll Interval (seconds) - ONWATCH_* first, SYNTRACK_* fallback
	if flags.interval > 0 {
		cfg.PollInterval = time.Duration(flags.interval) * time.Second
	} else if env := envWithFallback("ONWATCH_POLL_INTERVAL", "SYNTRACK_POLL_INTERVAL"); env != "" {
		if v, err := strconv.Atoi(env); err == nil {
			cfg.PollInterval = time.Duration(v) * time.Second
		}
	}

	// Port
	if flags.port > 0 {
		cfg.Port = flags.port
	} else if env := envWithFallback("ONWATCH_PORT", "SYNTRACK_PORT"); env != "" {
		if v, err := strconv.Atoi(env); err == nil {
			cfg.Port = v
		}
	}

	// Admin credentials
	cfg.AdminUser = envWithFallback("ONWATCH_ADMIN_USER", "SYNTRACK_ADMIN_USER")
	cfg.AdminPass = envWithFallback("ONWATCH_ADMIN_PASS", "SYNTRACK_ADMIN_PASS")

	// DB Path
	if flags.db != "" {
		cfg.DBPath = expandTilde(flags.db)
		cfg.DBPathExplicit = true
	} else if envDB := envWithFallback("ONWATCH_DB_PATH", "SYNTRACK_DB_PATH"); envDB != "" {
		cfg.DBPath = expandTilde(envDB)
		cfg.DBPathExplicit = true
	}

	// Log Level
	cfg.LogLevel = envWithFallback("ONWATCH_LOG_LEVEL", "SYNTRACK_LOG_LEVEL")

	// Host (bind address)
	cfg.Host = envWithFallback("ONWATCH_HOST", "SYNTRACK_HOST")

	// Secure Cookies
	if env := envWithFallback("ONWATCH_SECURE_COOKIES", "SYNTRACK_SECURE_COOKIES"); env != "" {
		cfg.SecureCookies = strings.ToLower(env) == "true" || env == "1"
	}

	// Base Path (subdirectory hosting, e.g. "/onwatch")
	cfg.BasePath = strings.TrimSpace(os.Getenv("ONWATCH_BASE_PATH"))

	// Session Idle Timeout (seconds)
	if env := envWithFallback("ONWATCH_SESSION_IDLE_TIMEOUT", "SYNTRACK_SESSION_IDLE_TIMEOUT"); env != "" {
		if v, err := strconv.Atoi(env); err == nil {
			cfg.SessionIdleTimeout = time.Duration(v) * time.Second
		}
	}

	// Debug mode (CLI flag only)
	cfg.DebugMode = flags.debug
	cfg.DebugStdout = flags.debugStdout

	// Test mode (CLI flag only)
	cfg.TestMode = flags.test

	// Apply defaults
	cfg.applyDefaults()

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// applyDefaults sets default values for empty config fields.
func (c *Config) applyDefaults() {
	if c.PollInterval == 0 {
		c.PollInterval = 120 * time.Second
	}
	if c.Port == 0 {
		c.Port = 9211
	}
	if c.AdminUser == "" {
		c.AdminUser = "admin"
	}
	if c.AdminPass == "" {
		c.AdminPass = "changeme"
	}
	if c.DBPath == "" {
		// Check if running in Docker and use /data/onwatch.db as default
		if c.IsDockerEnvironment() {
			c.DBPath = "/data/onwatch.db"
		} else {
			home, err := os.UserHomeDir()
			if err != nil || home == "" {
				c.DBPath = "./onwatch.db"
			} else {
				c.DBPath = filepath.Join(home, ".onwatch", "data", "onwatch.db")
			}
		}
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	// Normalize base path: ensure leading slash, no trailing slash, empty means root
	c.BasePath = strings.TrimRight(c.BasePath, "/")
	if c.BasePath != "" && !strings.HasPrefix(c.BasePath, "/") {
		c.BasePath = "/" + c.BasePath
	}
	if c.ZaiBaseURL == "" {
		if c.ZaiRegion == "cn" {
			c.ZaiBaseURL = "https://open.bigmodel.cn/api"
		} else {
			c.ZaiBaseURL = "https://api.z.ai/api"
		}
	}
	if c.SessionIdleTimeout == 0 {
		c.SessionIdleTimeout = 600 * time.Second
	}
}

// Validate checks the configuration for errors.
func (c *Config) Validate() error {
	// Validate Synthetic API key if provided
	if c.SyntheticAPIKey != "" && !strings.HasPrefix(c.SyntheticAPIKey, "syn_") {
		return fmt.Errorf("SYNTHETIC_API_KEY must start with 'syn_'")
	}

	// Poll interval bounds
	minInterval := 10 * time.Second
	maxInterval := 3600 * time.Second
	if c.PollInterval < minInterval {
		return fmt.Errorf("poll interval must be at least %v", minInterval)
	}
	if c.PollInterval > maxInterval {
		return fmt.Errorf("poll interval must be at most %v", maxInterval)
	}

	// Port range
	if c.Port < 1024 || c.Port > 65535 {
		return fmt.Errorf("port must be between 1024 and 65535")
	}

	return nil
}

// AvailableProviders returns which providers are configured.
func (c *Config) AvailableProviders() []string {
	var providers []string
	if c.AnthropicToken != "" {
		providers = append(providers, "anthropic")
	}
	if c.SyntheticAPIKey != "" {
		providers = append(providers, "synthetic")
	}
	if c.ZaiAPIKey != "" {
		providers = append(providers, "zai")
	}
	if c.CopilotToken != "" {
		providers = append(providers, "copilot")
	}
	if c.CodexToken != "" || c.CodexHasProfiles {
		providers = append(providers, "codex")
	}
	if c.AntigravityEnabled {
		providers = append(providers, "antigravity")
	}
	if c.MiniMaxAPIKey != "" {
		providers = append(providers, "minimax")
	}
	if c.OpenRouterAPIKey != "" {
		providers = append(providers, "openrouter")
	}
	if c.GeminiEnabled {
		providers = append(providers, "gemini")
	}
	return providers
}

// HasProvider returns true if the given provider is configured.
func (c *Config) HasProvider(name string) bool {
	switch name {
	case "synthetic":
		return c.SyntheticAPIKey != ""
	case "zai":
		return c.ZaiAPIKey != ""
	case "anthropic":
		return c.AnthropicToken != ""
	case "copilot":
		return c.CopilotToken != ""
	case "codex":
		return c.CodexToken != "" || c.CodexHasProfiles
	case "antigravity":
		return c.AntigravityEnabled
	case "minimax":
		return c.MiniMaxAPIKey != ""
	case "openrouter":
		return c.OpenRouterAPIKey != ""
	case "gemini":
		return c.GeminiEnabled
	}
	return false
}

// HasMultipleProviders returns true if more than one provider is configured.
func (c *Config) HasMultipleProviders() bool {
	count := 0
	if c.SyntheticAPIKey != "" {
		count++
	}
	if c.ZaiAPIKey != "" {
		count++
	}
	if c.AnthropicToken != "" {
		count++
	}
	if c.CopilotToken != "" {
		count++
	}
	if c.CodexToken != "" || c.CodexHasProfiles {
		count++
	}
	if c.AntigravityEnabled {
		count++
	}
	if c.MiniMaxAPIKey != "" {
		count++
	}
	if c.OpenRouterAPIKey != "" {
		count++
	}
	if c.GeminiEnabled {
		count++
	}
	return count > 1
}

// HasBothProviders is an alias for HasMultipleProviders (backward compat).
func (c *Config) HasBothProviders() bool {
	return c.HasMultipleProviders()
}

// String returns a redacted string representation of the config.
func (c *Config) String() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Config{\n")

	// Providers section
	fmt.Fprintf(&sb, "  Providers: %v,\n", c.AvailableProviders())

	// Redact Synthetic API key
	syntheticKeyDisplay := redactAPIKey(c.SyntheticAPIKey, "syn_")
	fmt.Fprintf(&sb, "  SyntheticAPIKey: %s,\n", syntheticKeyDisplay)

	// Redact Z.ai API key
	zaiKeyDisplay := redactAPIKey(c.ZaiAPIKey, "")
	fmt.Fprintf(&sb, "  ZaiAPIKey: %s,\n", zaiKeyDisplay)
	fmt.Fprintf(&sb, "  ZaiBaseURL: %s,\n", c.ZaiBaseURL)

	// Redact Anthropic token
	anthropicDisplay := redactAPIKey(c.AnthropicToken, "")
	fmt.Fprintf(&sb, "  AnthropicToken: %s,\n", anthropicDisplay)
	if c.AnthropicAutoToken {
		fmt.Fprintf(&sb, "  AnthropicAutoToken: true,\n")
	}

	// Redact Copilot token
	copilotDisplay := redactAPIKey(c.CopilotToken, "ghp_")
	fmt.Fprintf(&sb, "  CopilotToken: %s,\n", copilotDisplay)

	// Redact MiniMax token
	minimaxDisplay := redactAPIKey(c.MiniMaxAPIKey, "")
	fmt.Fprintf(&sb, "  MiniMaxAPIKey: %s,\n", minimaxDisplay)

	fmt.Fprintf(&sb, "  PollInterval: %v,\n", c.PollInterval)
	fmt.Fprintf(&sb, "  SessionIdleTimeout: %v,\n", c.SessionIdleTimeout)
	fmt.Fprintf(&sb, "  Port: %d,\n", c.Port)
	if c.BasePath != "" {
		fmt.Fprintf(&sb, "  BasePath: %s,\n", c.BasePath)
	}
	fmt.Fprintf(&sb, "  AdminUser: %s,\n", c.AdminUser)
	fmt.Fprintf(&sb, "  AdminPass: ****,\n")
	fmt.Fprintf(&sb, "  DBPath: %s,\n", c.DBPath)
	fmt.Fprintf(&sb, "  LogLevel: %s,\n", c.LogLevel)
	fmt.Fprintf(&sb, "  DebugMode: %v,\n", c.DebugMode)
	fmt.Fprintf(&sb, "  DebugStdout: %v,\n", c.DebugStdout)
	fmt.Fprintf(&sb, "}")

	return sb.String()
}

// redactAPIKey masks the API key for display.
func redactAPIKey(key string, expectedPrefix string) string {
	if key == "" {
		return "(not set)"
	}

	if expectedPrefix != "" && !strings.HasPrefix(key, expectedPrefix) {
		return "***...***"
	}

	prefixLen := len(expectedPrefix)
	if len(key) <= prefixLen+7 {
		return expectedPrefix + "***...***"
	}

	// Show first 4 chars after prefix and last 3 chars
	return key[:prefixLen+4] + "***...***" + key[len(key)-3:]
}

// OpenRotatingLogFile opens a log file with size-based rotation.
// If the active file reaches 50MB, the chain is rotated before opening:
// path.2 -> path.3, path.1 -> path.2, path -> path.1.
func OpenRotatingLogFile(path string) (*os.File, error) {
	if info, err := os.Stat(path); err == nil {
		if info.Size() >= maxLogFileBytes {
			oldest := fmt.Sprintf("%s.%d", path, maxLogBackups)
			if err := os.Remove(oldest); err != nil && !os.IsNotExist(err) {
				return nil, fmt.Errorf("failed to remove oldest log backup %s: %w", oldest, err)
			}
			for i := maxLogBackups - 1; i >= 1; i-- {
				src := fmt.Sprintf("%s.%d", path, i)
				dst := fmt.Sprintf("%s.%d", path, i+1)
				if err := os.Rename(src, dst); err != nil && !os.IsNotExist(err) {
					return nil, fmt.Errorf("failed to rotate log backup %s to %s: %w", src, dst, err)
				}
			}
			if err := os.Rename(path, path+".1"); err != nil && !os.IsNotExist(err) {
				return nil, fmt.Errorf("failed to rotate active log file %s: %w", path, err)
			}
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to stat log file %s: %w", path, err)
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}
	return file, nil
}

// LogWriter returns the appropriate log destination based on mode.
//
// --debugstdout: returns os.Stdout (everything to stdout for manual debugging)
// --debug:      returns .onwatch.log file (stdout gets only warn/error via separate handler)
// Docker:       returns os.Stdout (containers should log to stdout)
// Background:   returns .onwatch.log file
func (c *Config) LogWriter() (io.Writer, error) {
	// --debugstdout: all logs to stdout
	if c.DebugStdout {
		return os.Stdout, nil
	}

	// Docker mode: always log to stdout
	if c.IsDockerEnvironment() {
		return os.Stdout, nil
	}

	// All other modes (--debug, background): log to file
	return c.openLogFile()
}

// openLogFile opens the rotating log file for writing.
func (c *Config) openLogFile() (io.Writer, error) {
	logName := ".onwatch.log"
	if c.TestMode {
		logName = ".onwatch-test.log"
	}
	logPath := filepath.Join(filepath.Dir(c.DBPath), logName)

	file, err := OpenRotatingLogFile(logPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}

	return file, nil
}

// IsDockerEnvironment detects if running inside a container (Docker, Kubernetes, etc.).
// Checks for Docker indicators (/.dockerenv, DOCKER_CONTAINER env var) and
// Kubernetes indicators (KUBERNETES_SERVICE_HOST env var, service account mount).
func (c *Config) IsDockerEnvironment() bool {
	// Check for /.dockerenv file (Docker-specific indicator)
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	// Check for DOCKER_CONTAINER environment variable
	if os.Getenv("DOCKER_CONTAINER") != "" {
		return true
	}
	// Check for Kubernetes environment (containerd/CRI-O don't create /.dockerenv)
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return true
	}
	// Check for Kubernetes service account mount (present in all k8s pods)
	if _, err := os.Stat("/var/run/secrets/kubernetes.io/serviceaccount"); err == nil {
		return true
	}
	return false
}

// IsDefaultPassword returns true if the default password "changeme" is being used.
func (c *Config) IsDefaultPassword() bool {
	return c.AdminPass == "changeme"
}
