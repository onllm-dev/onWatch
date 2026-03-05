package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

// ANSI colors
const (
	colorRed    = "\033[0;31m"
	colorGreen  = "\033[0;32m"
	colorYellow = "\033[1;33m"
	colorBlue   = "\033[0;34m"
	colorCyan   = "\033[0;36m"
	colorBold   = "\033[1m"
	colorDim    = "\033[2m"
	colorReset  = "\033[0m"
)

type setupConfig struct {
	syntheticKey       string
	zaiKey             string
	zaiBaseURL         string
	anthropicToken     string
	codexToken         string
	antigravityEnabled bool
	adminUser          string
	adminPass          string
	port               int
	pollInterval       int
}

func runSetup() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}

	installDir := filepath.Join(homeDir, ".onwatch")
	envFile := filepath.Join(installDir, ".env")
	reader := bufio.NewReader(os.Stdin)

	fmt.Println()
	fmt.Printf("  %sonWatch Setup%s\n", colorBold, colorReset)
	fmt.Printf("  %shttps://github.com/onllm-dev/onwatch%s\n", colorDim, colorReset)
	fmt.Println()

	// Create directories
	if err := os.MkdirAll(filepath.Join(installDir, "data"), 0755); err != nil {
		return fmt.Errorf("failed to create directories: %w", err)
	}

	// Check for existing .env
	if _, err := os.Stat(envFile); err == nil {
		existing := loadExistingEnv(envFile)
		if allProvidersConfigured(existing) {
			fmt.Printf("  %sinfo%s  Existing .env found -- all providers configured\n", colorBlue, colorReset)
			fmt.Printf("  %sTo reconfigure, delete %s and run setup again.%s\n", colorDim, envFile, colorReset)
			return nil
		}
		if anyProviderConfigured(existing) {
			return addMissingProviders(reader, envFile, existing)
		}
		// .env exists but no providers — remove and start fresh
		fmt.Printf("  %swarn%s  Existing .env found but no API keys configured\n", colorYellow, colorReset)
		os.Remove(envFile)
	}

	// Fresh setup
	cfg, err := freshSetup(reader)
	if err != nil {
		return err
	}

	if err := writeEnvFile(envFile, cfg); err != nil {
		return err
	}

	printSummary(cfg)
	printNextSteps()

	return nil
}

func freshSetup(reader *bufio.Reader) (*setupConfig, error) {
	fmt.Printf("\n  %s--- Provider Selection ---%s\n\n", colorBold, colorReset)

	providerOptions := []string{
		"Synthetic only",
		"Z.ai only",
		"Anthropic (Claude Code) only",
		"Codex only",
		"Antigravity (Windsurf) only",
		"Multiple (choose one at a time)",
		"All available",
	}

	choice := promptChoice(reader, "Which providers do you want to track?", providerOptions)

	cfg := &setupConfig{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	switch choice {
	case 1: // Synthetic only
		cfg.syntheticKey = collectSyntheticKey(reader)
	case 2: // Z.ai only
		cfg.zaiKey, cfg.zaiBaseURL = collectZaiConfig(reader)
	case 3: // Anthropic only
		cfg.anthropicToken = collectAnthropicToken(reader, logger)
	case 4: // Codex only
		cfg.codexToken = collectCodexToken(reader, logger)
	case 5: // Antigravity only
		cfg.antigravityEnabled = true
		fmt.Printf("  %s ok %s  Antigravity enabled (auto-detects running Windsurf process)\n", colorGreen, colorReset)
	case 6: // Multiple
		cfg.syntheticKey, cfg.zaiKey, cfg.zaiBaseURL, cfg.anthropicToken, cfg.codexToken, cfg.antigravityEnabled = collectMultipleProviders(reader, logger)
	case 7: // All
		cfg.syntheticKey = collectSyntheticKey(reader)
		cfg.zaiKey, cfg.zaiBaseURL = collectZaiConfig(reader)
		cfg.anthropicToken = collectAnthropicToken(reader, logger)
		cfg.codexToken = collectCodexToken(reader, logger)
		cfg.antigravityEnabled = true
		fmt.Printf("\n  %s ok %s  Antigravity enabled (auto-detects running Windsurf process)\n", colorGreen, colorReset)
	}

	// Validate at least one provider
	if cfg.syntheticKey == "" && cfg.zaiKey == "" && cfg.anthropicToken == "" && cfg.codexToken == "" && !cfg.antigravityEnabled {
		return nil, fmt.Errorf("at least one provider is required")
	}

	// Dashboard credentials
	fmt.Printf("\n  %s--- Dashboard Credentials ---%s\n\n", colorBold, colorReset)

	cfg.adminUser = promptWithDefault(reader, "Dashboard username", "admin")

	fmt.Printf("  Dashboard password %s[Enter = auto-generate]%s: ", colorDim, colorReset)
	passInput := readLine(reader)
	if passInput == "" {
		cfg.adminPass = generatePassword()
		fmt.Printf("  %s ok %s  Generated password: %s%s%s\n", colorGreen, colorReset, colorBold, cfg.adminPass, colorReset)
		fmt.Printf("  %sSave this password -- it won't be shown again%s\n", colorYellow, colorReset)
	} else {
		cfg.adminPass = passInput
		fmt.Printf("  %s ok %s  Password set\n", colorGreen, colorReset)
	}

	// Optional settings
	fmt.Printf("\n  %s--- Optional Settings ---%s\n\n", colorBold, colorReset)

	for {
		portStr := promptWithDefault(reader, "Dashboard port", "9211")
		port, err := strconv.Atoi(portStr)
		if err == nil && port >= 1 && port <= 65535 {
			cfg.port = port
			break
		}
		fmt.Printf("  %sMust be a number between 1 and 65535%s\n", colorRed, colorReset)
	}

	for {
		intervalStr := promptWithDefault(reader, "Polling interval in seconds", "120")
		interval, err := strconv.Atoi(intervalStr)
		if err == nil && interval >= 10 && interval <= 3600 {
			cfg.pollInterval = interval
			break
		}
		fmt.Printf("  %sMust be a number between 10 and 3600%s\n", colorRed, colorReset)
	}

	return cfg, nil
}

func collectMultipleProviders(reader *bufio.Reader, logger *slog.Logger) (synKey, zaiKey, zaiURL, anthToken, codexToken string, antiEnabled bool) {
	if promptYesNo(reader, "Add Synthetic provider?", false) {
		synKey = collectSyntheticKey(reader)
	}
	if promptYesNo(reader, "Add Z.ai provider?", false) {
		zaiKey, zaiURL = collectZaiConfig(reader)
	}
	if promptYesNo(reader, "Add Anthropic (Claude Code) provider?", false) {
		anthToken = collectAnthropicToken(reader, logger)
	}
	if promptYesNo(reader, "Add Codex provider?", false) {
		codexToken = collectCodexToken(reader, logger)
	}
	if promptYesNo(reader, "Add Antigravity (Windsurf) provider?", false) {
		antiEnabled = true
		fmt.Printf("  %sAntigravity auto-detects the running Windsurf process%s\n", colorDim, colorReset)
	}
	return
}

func collectSyntheticKey(reader *bufio.Reader) string {
	fmt.Printf("\n  %sGet your key: https://synthetic.new/settings/api%s\n", colorDim, colorReset)
	for {
		fmt.Print("  Synthetic API key (syn_...): ")
		key := readLine(reader)
		if strings.HasPrefix(key, "syn_") {
			fmt.Printf("  %s ok %s  %s%s...%s%s\n", colorGreen, colorReset, colorDim, key[:6], key[len(key)-4:], colorReset)
			return key
		}
		if key == "" {
			fmt.Printf("  %sCannot be empty%s\n", colorRed, colorReset)
		} else {
			fmt.Printf("  %sKey must start with 'syn_'%s\n", colorRed, colorReset)
		}
	}
}

func collectZaiConfig(reader *bufio.Reader) (string, string) {
	fmt.Printf("\n  %sGet your key: https://www.z.ai/api-keys%s\n", colorDim, colorReset)

	var key string
	for {
		fmt.Print("  Z.ai API key: ")
		key = readLine(reader)
		if key != "" {
			masked := maskValue(key)
			fmt.Printf("  %s ok %s  %s%s%s\n", colorGreen, colorReset, colorDim, masked, colorReset)
			break
		}
		fmt.Printf("  %sCannot be empty%s\n", colorRed, colorReset)
	}

	fmt.Println()
	defaultURL := "https://api.z.ai/api"
	if promptYesNo(reader, fmt.Sprintf("Use default Z.ai base URL (%s)?", defaultURL), true) {
		return key, defaultURL
	}

	for {
		baseURL := promptWithDefault(reader, "Z.ai base URL", "https://open.bigmodel.cn/api")
		if strings.HasPrefix(baseURL, "https://") {
			return key, baseURL
		}
		fmt.Printf("  %sURL must start with 'https://'%s\n", colorRed, colorReset)
	}
}

func collectAnthropicToken(reader *bufio.Reader, logger *slog.Logger) string {
	fmt.Printf("\n  %sAnthropic (Claude Code) Token Setup%s\n", colorBold, colorReset)
	fmt.Printf("  %sonWatch can auto-detect your Claude Code credentials.%s\n\n", colorDim, colorReset)

	// Try auto-detection
	if token := api.DetectAnthropicToken(logger); token != "" {
		masked := maskValue(token)
		fmt.Printf("  %s ok %s  Auto-detected Claude Code token\n", colorGreen, colorReset)
		fmt.Printf("  %sToken: %s%s\n", colorDim, masked, colorReset)
		if promptYesNo(reader, "Use auto-detected token?", true) {
			return token
		}
	} else {
		fmt.Printf("  %s!%s Could not auto-detect Claude Code credentials\n", colorYellow, colorReset)
	}

	return promptSecret(reader, "Anthropic token")
}

func collectCodexToken(reader *bufio.Reader, logger *slog.Logger) string {
	fmt.Printf("\n  %sCodex Token Setup%s\n", colorBold, colorReset)
	fmt.Printf("  %sonWatch can auto-detect your Codex OAuth token from auth.json.%s\n\n", colorDim, colorReset)

	// Try auto-detection
	if token := api.DetectCodexToken(logger); token != "" {
		masked := maskValue(token)
		fmt.Printf("  %s ok %s  Auto-detected Codex token\n", colorGreen, colorReset)
		fmt.Printf("  %sToken: %s%s\n", colorDim, masked, colorReset)
		if promptYesNo(reader, "Use auto-detected token?", true) {
			return token
		}
	} else {
		fmt.Printf("  %s!%s Could not auto-detect Codex token from auth.json\n", colorYellow, colorReset)
	}

	return promptSecret(reader, "Codex token")
}

func writeEnvFile(path string, cfg *setupConfig) error {
	var b strings.Builder

	b.WriteString("# ===============================================================\n")
	b.WriteString("# onWatch Configuration\n")
	b.WriteString(fmt.Sprintf("# Generated by 'onwatch setup' on %s\n", time.Now().UTC().Format("2006-01-02 15:04:05 UTC")))
	b.WriteString("# ===============================================================\n\n")

	if cfg.syntheticKey != "" {
		b.WriteString("# Synthetic API key (https://synthetic.new/settings/api)\n")
		b.WriteString(fmt.Sprintf("SYNTHETIC_API_KEY=%s\n\n", cfg.syntheticKey))
	}

	if cfg.zaiKey != "" {
		b.WriteString("# Z.ai API key (https://www.z.ai/api-keys)\n")
		b.WriteString(fmt.Sprintf("ZAI_API_KEY=%s\n\n", cfg.zaiKey))
		b.WriteString("# Z.ai base URL\n")
		b.WriteString(fmt.Sprintf("ZAI_BASE_URL=%s\n\n", cfg.zaiBaseURL))
	}

	if cfg.anthropicToken != "" {
		b.WriteString("# Anthropic token (Claude Code)\n")
		b.WriteString(fmt.Sprintf("ANTHROPIC_TOKEN=%s\n\n", cfg.anthropicToken))
	}

	if cfg.codexToken != "" {
		b.WriteString("# Codex OAuth token\n")
		b.WriteString(fmt.Sprintf("CODEX_TOKEN=%s\n\n", cfg.codexToken))
	}

	if cfg.antigravityEnabled {
		b.WriteString("# Antigravity (Windsurf) - auto-detected from local process\n")
		b.WriteString("ANTIGRAVITY_ENABLED=true\n\n")
	}

	b.WriteString("# Dashboard credentials\n")
	b.WriteString(fmt.Sprintf("ONWATCH_ADMIN_USER=%s\n", cfg.adminUser))
	b.WriteString(fmt.Sprintf("ONWATCH_ADMIN_PASS=%s\n\n", cfg.adminPass))

	b.WriteString("# Polling interval in seconds (10-3600)\n")
	b.WriteString(fmt.Sprintf("ONWATCH_POLL_INTERVAL=%d\n\n", cfg.pollInterval))

	b.WriteString("# Dashboard port\n")
	b.WriteString(fmt.Sprintf("ONWATCH_PORT=%d\n", cfg.port))

	if err := os.WriteFile(path, []byte(b.String()), 0600); err != nil {
		return fmt.Errorf("failed to write .env: %w", err)
	}

	fmt.Printf("  %s ok %s  Created %s\n", colorGreen, colorReset, path)
	return nil
}

func printSummary(cfg *setupConfig) {
	providers := []string{}
	if cfg.syntheticKey != "" {
		providers = append(providers, "Synthetic")
	}
	if cfg.zaiKey != "" {
		providers = append(providers, "Z.ai")
	}
	if cfg.anthropicToken != "" {
		providers = append(providers, "Anthropic")
	}
	if cfg.codexToken != "" {
		providers = append(providers, "Codex")
	}
	if cfg.antigravityEnabled {
		providers = append(providers, "Antigravity")
	}
	providerLabel := strings.Join(providers, ", ")

	maskedPass := strings.Repeat("*", len(cfg.adminPass))

	fmt.Println()
	fmt.Printf("  %s+- Configuration Summary ------------------+%s\n", colorBold, colorReset)
	fmt.Printf("  %s|%s  Provider:  %-29s%s|%s\n", colorBold, colorReset, providerLabel, colorBold, colorReset)
	fmt.Printf("  %s|%s  Dashboard: %-29s%s|%s\n", colorBold, colorReset, fmt.Sprintf("http://localhost:%d", cfg.port), colorBold, colorReset)
	fmt.Printf("  %s|%s  Username:  %-29s%s|%s\n", colorBold, colorReset, cfg.adminUser, colorBold, colorReset)
	fmt.Printf("  %s|%s  Password:  %-29s%s|%s\n", colorBold, colorReset, maskedPass, colorBold, colorReset)
	fmt.Printf("  %s|%s  Interval:  %-29s%s|%s\n", colorBold, colorReset, fmt.Sprintf("%ds", cfg.pollInterval), colorBold, colorReset)
	fmt.Printf("  %s+-------------------------------------------+%s\n", colorBold, colorReset)
}

func printNextSteps() {
	fmt.Println()
	fmt.Printf("  %sNext steps:%s\n", colorBold, colorReset)
	fmt.Printf("    %sonwatch%s           # Start (runs in background)\n", colorCyan, colorReset)
	fmt.Printf("    %sonwatch stop%s      # Stop\n", colorCyan, colorReset)
	fmt.Printf("    %sonwatch status%s    # Status\n", colorCyan, colorReset)
	fmt.Printf("    %sonwatch --debug%s   # Run in foreground\n", colorCyan, colorReset)
	fmt.Println()
}

// --- Existing .env handling ---

type existingEnv struct {
	syntheticKey       string
	zaiKey             string
	anthropicToken     string
	codexToken         string
	antigravityEnabled bool
}

func loadExistingEnv(path string) *existingEnv {
	data, err := os.ReadFile(path)
	if err != nil {
		return &existingEnv{}
	}
	env := &existingEnv{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key, val := parts[0], parts[1]
		switch key {
		case "SYNTHETIC_API_KEY":
			env.syntheticKey = val
		case "ZAI_API_KEY":
			env.zaiKey = val
		case "ANTHROPIC_TOKEN":
			env.anthropicToken = val
		case "CODEX_TOKEN":
			env.codexToken = val
		case "ANTIGRAVITY_ENABLED":
			env.antigravityEnabled = val == "true"
		}
	}
	return env
}

func allProvidersConfigured(env *existingEnv) bool {
	return env.syntheticKey != "" && env.zaiKey != "" && env.anthropicToken != "" && env.codexToken != "" && env.antigravityEnabled
}

func anyProviderConfigured(env *existingEnv) bool {
	return env.syntheticKey != "" || env.zaiKey != "" || env.anthropicToken != "" || env.codexToken != "" || env.antigravityEnabled
}

func addMissingProviders(reader *bufio.Reader, envFile string, existing *existingEnv) error {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	configured := []string{}
	if existing.syntheticKey != "" {
		configured = append(configured, "Synthetic")
	}
	if existing.zaiKey != "" {
		configured = append(configured, "Z.ai")
	}
	if existing.anthropicToken != "" {
		configured = append(configured, "Anthropic")
	}
	if existing.codexToken != "" {
		configured = append(configured, "Codex")
	}
	if existing.antigravityEnabled {
		configured = append(configured, "Antigravity")
	}
	fmt.Printf("  %sinfo%s  Existing .env found -- configured: %s\n\n", colorBlue, colorReset, strings.Join(configured, ", "))

	f, err := os.OpenFile(envFile, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("failed to open .env: %w", err)
	}
	defer f.Close()

	if existing.syntheticKey == "" {
		if promptYesNo(reader, "Add Synthetic provider?", false) {
			key := collectSyntheticKey(reader)
			fmt.Fprintf(f, "\n# Synthetic API key (https://synthetic.new/settings/api)\nSYNTHETIC_API_KEY=%s\n", key)
			fmt.Printf("  %s ok %s  Added Synthetic provider to .env\n", colorGreen, colorReset)
		}
	}

	if existing.zaiKey == "" {
		if promptYesNo(reader, "Add Z.ai provider?", false) {
			key, baseURL := collectZaiConfig(reader)
			fmt.Fprintf(f, "\n# Z.ai API key (https://www.z.ai/api-keys)\nZAI_API_KEY=%s\n\n# Z.ai base URL\nZAI_BASE_URL=%s\n", key, baseURL)
			fmt.Printf("  %s ok %s  Added Z.ai provider to .env\n", colorGreen, colorReset)
		}
	}

	if existing.anthropicToken == "" {
		// Try auto-detection
		if token := api.DetectAnthropicToken(logger); token != "" {
			fmt.Printf("  %s ok %s  Claude Code credentials detected on this system\n", colorGreen, colorReset)
			if promptYesNo(reader, "Enable Anthropic tracking?", true) {
				fmt.Fprintf(f, "\n# Anthropic token (Claude Code -- auto-detected)\nANTHROPIC_TOKEN=%s\n", token)
				fmt.Printf("  %s ok %s  Added Anthropic provider to .env (auto-detected)\n", colorGreen, colorReset)
			}
		} else if promptYesNo(reader, "Add Anthropic (Claude Code) provider?", false) {
			token := collectAnthropicToken(reader, logger)
			fmt.Fprintf(f, "\n# Anthropic token (Claude Code)\nANTHROPIC_TOKEN=%s\n", token)
			fmt.Printf("  %s ok %s  Added Anthropic provider to .env\n", colorGreen, colorReset)
		}
	}

	if existing.codexToken == "" {
		if token := api.DetectCodexToken(logger); token != "" {
			fmt.Printf("  %s ok %s  Codex auth token detected on this system\n", colorGreen, colorReset)
			if promptYesNo(reader, "Enable Codex tracking?", true) {
				fmt.Fprintf(f, "\n# Codex OAuth token\nCODEX_TOKEN=%s\n", token)
				fmt.Printf("  %s ok %s  Added Codex provider to .env (auto-detected)\n", colorGreen, colorReset)
			}
		} else if promptYesNo(reader, "Add Codex provider?", false) {
			token := collectCodexToken(reader, logger)
			fmt.Fprintf(f, "\n# Codex OAuth token\nCODEX_TOKEN=%s\n", token)
			fmt.Printf("  %s ok %s  Added Codex provider to .env\n", colorGreen, colorReset)
		}
	}

	if !existing.antigravityEnabled {
		if promptYesNo(reader, "Add Antigravity (Windsurf) provider?", false) {
			fmt.Fprintf(f, "\n# Antigravity (Windsurf) - auto-detected from local process\nANTIGRAVITY_ENABLED=true\n")
			fmt.Printf("  %s ok %s  Added Antigravity provider to .env\n", colorGreen, colorReset)
		}
	}

	return nil
}

// --- Input helpers ---

func readLine(reader *bufio.Reader) string {
	line, _ := reader.ReadString('\n')
	return strings.TrimSpace(line)
}

func promptWithDefault(reader *bufio.Reader, prompt, defaultVal string) string {
	fmt.Printf("  %s %s[%s]%s: ", prompt, colorDim, defaultVal, colorReset)
	input := readLine(reader)
	if input == "" {
		return defaultVal
	}
	return input
}

func promptYesNo(reader *bufio.Reader, prompt string, defaultYes bool) bool {
	suffix := "(y/N)"
	if defaultYes {
		suffix = "(Y/n)"
	}
	fmt.Printf("  %s %s: ", prompt, suffix)
	input := strings.ToLower(readLine(reader))
	if input == "" {
		return defaultYes
	}
	return strings.HasPrefix(input, "y")
}

func promptSecret(reader *bufio.Reader, prompt string) string {
	for {
		fmt.Printf("  %s: ", prompt)
		val := readLine(reader)
		if val != "" {
			masked := maskValue(val)
			fmt.Printf("  %s ok %s  %s%s%s\n", colorGreen, colorReset, colorDim, masked, colorReset)
			return val
		}
		fmt.Printf("  %sCannot be empty%s\n", colorRed, colorReset)
	}
}

func promptChoice(reader *bufio.Reader, prompt string, options []string) int {
	fmt.Printf("  %s%s%s\n", colorBold, prompt, colorReset)
	for i, opt := range options {
		fmt.Printf("    %s%d)%s %s\n", colorCyan, i+1, colorReset, opt)
	}
	for {
		fmt.Printf("  %s>%s ", colorBold, colorReset)
		input := readLine(reader)
		n, err := strconv.Atoi(input)
		if err == nil && n >= 1 && n <= len(options) {
			return n
		}
		fmt.Printf("  %sPlease enter 1-%d%s\n", colorRed, len(options), colorReset)
	}
}

func maskValue(val string) string {
	if len(val) > 10 {
		return val[:6] + "..." + val[len(val)-4:]
	}
	if len(val) > 3 {
		return val[:3] + "..."
	}
	return "***"
}

func generatePassword() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based
		return fmt.Sprintf("onwatch%d", time.Now().UnixNano()%100000)
	}
	return hex.EncodeToString(b)
}
