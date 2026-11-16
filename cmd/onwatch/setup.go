package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/service"
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
	syntheticKey          string
	zaiKey                string
	zaiBaseURL            string
	anthropicToken        string
	codexToken            string
	openCodeEnabled       bool
	antigravityEnabled    bool
	antigravitySource     string
	geminiEnabled         bool
	grokEnabled           bool
	ollamaKey             string
	museEnabled           bool
	museKey               string
	commandCodeEnabled    bool
	commandCodeKey        string
	zenmuxKey             string
	alibabaEnabled        bool
	alibabaCookiesFile    string
	alibabaRegion         string
	openCodeGoConfigured  bool
	openCodeGoKey         string
	openCodeGoWorkspaceID string
	openCodeGoAuthCookie  string
	adminUser             string
	adminPass             string
	port                  int
	pollInterval          int
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
			offerAutostart(reader)
			offerGitHubStar(reader, runGH, starMarkerPath(installDir))
			return nil
		}
		if anyProviderConfigured(existing) {
			err := addMissingProviders(reader, envFile, existing)
			if err == nil {
				offerAutostart(reader)
				offerGitHubStar(reader, runGH, starMarkerPath(installDir))
			}
			return err
		}
		// .env exists but no providers - remove and start fresh
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
	offerAutostart(reader)
	offerGitHubStar(reader, runGH, starMarkerPath(installDir))
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
		"OpenCode (opencode-codex) only",
		"Antigravity (Windsurf) only",
		"Gemini CLI only",
		"Grok (xAI) only",
		"Ollama Cloud only",
		"Muse (Meta) only",
		"Command Code only",
		"Zenmux only",
		"Alibaba Token Plan only",
		"OpenCode Go only",
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
	case 5: // OpenCode only
		cfg.openCodeEnabled = collectOpenCode(reader)
	case 6: // Antigravity only
		cfg.antigravityEnabled = true
		fmt.Printf("  %s ok %s  Antigravity enabled (auto-detects running Windsurf process)\n", colorGreen, colorReset)
	case 7: // Gemini only
		cfg.geminiEnabled = true
		fmt.Printf("  %s ok %s  Gemini enabled (auto-detects from ~/.gemini/oauth_creds.json)\n", colorGreen, colorReset)
	case 8: // Grok only
		cfg.grokEnabled = collectGrok(reader, logger)
	case 9: // Ollama Cloud only
		cfg.ollamaKey = collectOllamaKey(reader)
	case 10: // Muse (Meta) only
		cfg.museEnabled, cfg.museKey = collectMuse(reader, logger)
	case 11: // Command Code only
		cfg.commandCodeEnabled, cfg.commandCodeKey = collectCommandCode(reader, logger)
	case 12: // Zenmux only
		cfg.zenmuxKey = collectZenmuxKey(reader)
	case 13: // Alibaba Token Plan only
		cfg.alibabaEnabled, cfg.alibabaCookiesFile, cfg.alibabaRegion = collectAlibaba(reader)
	case 14: // OpenCode Go only
		applyOpenCodeGoConfig(cfg, collectOpenCodeGo(reader, logger))
	case 15: // Multiple
		cfg.syntheticKey, cfg.zaiKey, cfg.zaiBaseURL, cfg.anthropicToken, cfg.codexToken, cfg.openCodeEnabled, cfg.antigravityEnabled, cfg.geminiEnabled, cfg.grokEnabled, cfg.ollamaKey, cfg.museEnabled, cfg.museKey, cfg.commandCodeEnabled, cfg.commandCodeKey, cfg.zenmuxKey = collectMultipleProviders(reader, logger)
		applyOpenCodeGoConfig(cfg, collectMultipleOpenCodeGo(reader, logger))
		if promptYesNo(reader, "Add Alibaba Token Plan provider?", false) {
			cfg.alibabaEnabled, cfg.alibabaCookiesFile, cfg.alibabaRegion = collectAlibaba(reader)
		}
	case 16: // All
		cfg.syntheticKey = collectSyntheticKey(reader)
		cfg.zaiKey, cfg.zaiBaseURL = collectZaiConfig(reader)
		cfg.anthropicToken = collectAnthropicToken(reader, logger)
		cfg.codexToken = collectCodexToken(reader, logger)
		cfg.openCodeEnabled = collectOpenCode(reader)
		cfg.antigravityEnabled = true
		fmt.Printf("\n  %s ok %s  Antigravity enabled (auto-detects running Windsurf process)\n", colorGreen, colorReset)
		cfg.geminiEnabled = true
		fmt.Printf("  %s ok %s  Gemini enabled (auto-detects from ~/.gemini/oauth_creds.json)\n", colorGreen, colorReset)
		cfg.grokEnabled = collectGrok(reader, logger)
		cfg.ollamaKey = collectOllamaKey(reader)
		cfg.museEnabled, cfg.museKey = collectMuse(reader, logger)
		cfg.commandCodeEnabled, cfg.commandCodeKey = collectCommandCode(reader, logger)
		cfg.zenmuxKey = collectZenmuxKey(reader)
		applyOpenCodeGoConfig(cfg, collectOpenCodeGo(reader, logger))
		cfg.alibabaEnabled, cfg.alibabaCookiesFile, cfg.alibabaRegion = collectAlibaba(reader)
	}

	// Validate at least one provider
	if cfg.syntheticKey == "" && cfg.zaiKey == "" && cfg.anthropicToken == "" && cfg.codexToken == "" && !cfg.openCodeEnabled && !cfg.antigravityEnabled && !cfg.geminiEnabled && !cfg.grokEnabled && cfg.ollamaKey == "" && !cfg.museEnabled && cfg.museKey == "" && !cfg.commandCodeEnabled && cfg.commandCodeKey == "" && cfg.zenmuxKey == "" && !cfg.openCodeGoConfigured && !cfg.alibabaEnabled && cfg.alibabaCookiesFile == "" {
		return nil, fmt.Errorf("at least one provider is required")
	}

	// Antigravity data source preference (all variants share one quota).
	if cfg.antigravityEnabled {
		cfg.antigravitySource = collectAntigravitySource(reader)
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

func collectMultipleProviders(reader *bufio.Reader, logger *slog.Logger) (synKey, zaiKey, zaiURL, anthToken, codexToken string, openCodeEnabled, antiEnabled, geminiEnabled, grokEnabled bool, ollamaKey string, museEnabled bool, museKey string, commandCodeEnabled bool, commandCodeKey string, zenmuxKey string) {
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
	if promptYesNo(reader, "Add OpenCode (opencode-codex) provider?", false) {
		openCodeEnabled = collectOpenCode(reader)
	}
	if promptYesNo(reader, "Add Antigravity (Windsurf) provider?", false) {
		antiEnabled = true
		fmt.Printf("  %sAntigravity auto-detects the running Windsurf process%s\n", colorDim, colorReset)
	}
	if promptYesNo(reader, "Add Gemini CLI provider?", false) {
		geminiEnabled = true
		fmt.Printf("  %sGemini auto-detects from ~/.gemini/oauth_creds.json%s\n", colorDim, colorReset)
	}
	if promptYesNo(reader, "Add Grok (xAI) provider?", false) {
		grokEnabled = collectGrok(reader, logger)
	}
	if promptYesNo(reader, "Add Ollama Cloud provider?", false) {
		ollamaKey = collectOllamaKey(reader)
	}
	if promptYesNo(reader, "Add Muse (Meta) provider?", false) {
		museEnabled, museKey = collectMuse(reader, logger)
	}
	if promptYesNo(reader, "Add Command Code provider?", false) {
		commandCodeEnabled, commandCodeKey = collectCommandCode(reader, logger)
	}
	if promptYesNo(reader, "Add Zenmux provider?", false) {
		zenmuxKey = collectZenmuxKey(reader)
	}
	return
}

// collectMultipleOpenCodeGo is a thin wrapper so collectMultipleProviders can
// offer OpenCode Go alongside the other providers.
func collectMultipleOpenCodeGo(reader *bufio.Reader, logger *slog.Logger) openCodeGoConfig {
	if promptYesNo(reader, "Add OpenCode Go provider?", false) {
		return collectOpenCodeGo(reader, logger)
	}
	return openCodeGoConfig{}
}

// collectAntigravitySource asks which Antigravity data source to use. All
// variants share one Google-account quota, so this is a preference, not a
// separate provider. Returns "both" (default), "cli", or "ide".
func collectAntigravitySource(reader *bufio.Reader) string {
	fmt.Printf("\n  %sAntigravity Data Source%s\n", colorBold, colorReset)
	fmt.Printf("  %sAll Antigravity variants share one Google-account quota.%s\n", colorDim, colorReset)
	options := []string{
		"Both (prefer agy CLI, fall back to IDE)",
		"agy CLI only (richer weekly + 5h data; auto-launches agy)",
		"IDE only (desktop language server)",
	}
	switch promptChoice(reader, "Which source should onWatch use?", options) {
	case 2:
		return "cli"
	case 3:
		return "ide"
	default:
		return "both"
	}
}

// collectGrok enables xAI Grok credit tracking. onWatch auto-detects the bearer
// from ~/.grok/auth.json (or $GROK_HOME) and refreshes it at runtime, so no
// token is stored in .env. Returns true if the user opts in.
func collectGrok(reader *bufio.Reader, logger *slog.Logger) bool {
	fmt.Printf("\n  %sGrok (xAI) Setup%s\n", colorBold, colorReset)
	fmt.Printf("  %sonWatch reads your Grok login from %s.%s\n", colorDim, api.GrokAuthPath(), colorReset)

	if creds := api.DetectGrokCredentials(logger); creds != nil && creds.AccessToken != "" {
		fmt.Printf("  %s ok %s  Detected Grok credentials at %s\n", colorGreen, colorReset, api.GrokAuthPath())
		return promptYesNo(reader, "Enable Grok tracking?", true)
	}

	fmt.Printf("  %s!%s No Grok auth.json found at %s\n", colorYellow, colorReset, api.GrokAuthPath())
	fmt.Printf("  %sRun 'grok login' (or set GROK_TOKEN), then re-run setup.%s\n", colorDim, colorReset)
	return promptYesNo(reader, "Enable Grok tracking anyway?", false)
}

// collectOpenCode enables ChatGPT tracking via OpenCode's auth.json. onWatch
// reads, refreshes, and writes back the token at runtime, so no token is stored
// in .env. Returns true if the user opts in.
func collectOpenCode(reader *bufio.Reader) bool {
	fmt.Printf("\n  %sOpenCode (opencode-codex) Setup%s\n", colorBold, colorReset)
	fmt.Printf("  %sonWatch reads your ChatGPT OAuth login from the opencode-codex auth.json.%s\n", colorDim, colorReset)

	if path := api.OpenCodeAuthPath(); path != "" {
		if _, err := os.Stat(path); err == nil {
			fmt.Printf("  %s ok %s  Detected opencode-codex credentials at %s\n", colorGreen, colorReset, path)
			return promptYesNo(reader, "Enable OpenCode (opencode-codex) tracking?", true)
		}
		fmt.Printf("  %s!%s No opencode-codex auth.json found at %s\n", colorYellow, colorReset, path)
		fmt.Printf("  %sRun 'opencode auth login' and choose ChatGPT, then re-run setup.%s\n", colorDim, colorReset)
	}
	return promptYesNo(reader, "Enable OpenCode (opencode-codex) tracking anyway?", false)
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

// collectOllamaKey prompts for an Ollama Cloud API key. The key comes from
// ollama.com/settings/keys and is stored as OLLAMA_API_KEY in .env. onWatch uses
// it to read included monthly usage and plan details from the ollama.com API.
// verifyOllamaKey checks a key against ollama.com and returns the plan tier.
// A package variable so tests can stub it and never touch the network.
var verifyOllamaKey = func(key string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	snap, err := api.NewOllamaClient(key, slog.New(slog.NewTextHandler(io.Discard, nil)), api.WithOllamaTimeout(10*time.Second)).FetchSnapshot(ctx)
	if err != nil {
		return "", err
	}
	return snap.Plan, nil
}

func collectOllamaKey(reader *bufio.Reader) string {
	fmt.Printf("\n  %sOllama Cloud Setup%s\n", colorBold, colorReset)
	fmt.Printf("  %sCreate an API key at https://ollama.com/settings/keys%s\n", colorDim, colorReset)
	for {
		fmt.Print("  Ollama Cloud API key: ")
		key := readLine(reader)
		if key == "" {
			fmt.Printf("  %sCannot be empty%s\n", colorRed, colorReset)
			continue
		}
		plan, err := verifyOllamaKey(key)
		switch {
		case err == nil:
			if plan == "" {
				plan = "unknown"
			}
			fmt.Printf("  %s ok %s  Key verified with ollama.com (plan: %s)  %s%s%s\n", colorGreen, colorReset, plan, colorDim, maskValue(key), colorReset)
			return key
		case api.IsOllamaAuthError(err):
			fmt.Printf("  %sollama.com rejected this key (401). Check it at https://ollama.com/settings/keys and try again.%s\n", colorRed, colorReset)
			continue
		default:
			fmt.Printf("  %s!%s Could not reach ollama.com to verify the key (%v) - saving it anyway\n", colorYellow, colorReset, err)
			fmt.Printf("  %s ok %s  %s%s%s\n", colorGreen, colorReset, colorDim, maskValue(key), colorReset)
			return key
		}
	}
}

// detectMuseCredentialsFunc resolves local Muse credentials for the setup
// wizard. A package variable so tests can stub it and never touch the
// developer's keychain.
var detectMuseCredentialsFunc = api.DetectMuseCredentials

// verifyMuseKey checks a key against the Meta Model API with one minimal
// usage probe and returns a "5h x% / weekly y%" summary.
// A package variable so tests can stub it and never touch the network.
var verifyMuseKey = func(key, model string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	snap, err := api.NewMuseClient(key, model, slog.New(slog.NewTextHandler(io.Discard, nil)), api.WithMuseTimeout(30*time.Second)).FetchSnapshot(ctx)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("5h %.1f%% used / weekly %.1f%% used", snap.WindowUsedPct, snap.WeeklyUsedPct), nil
}

// collectMuse enables Meta Muse coding-plan tracking. onWatch prefers the
// existing `muse login` session (keychain / login file) and only stores an
// explicit META_API_KEY in .env when the user provides one. Returns the
// enabled flag and the explicit key ("" when auto-detect is used).
func collectMuse(reader *bufio.Reader, logger *slog.Logger) (bool, string) {
	fmt.Printf("\n  %sMuse (Meta) Setup%s\n", colorBold, colorReset)
	fmt.Printf("  %sonWatch reads your Muse coding-plan quota from the Meta Model API.%s\n", colorDim, colorReset)

	if creds := detectMuseCredentialsFunc(logger); creds != nil && creds.APIKey != "" {
		fmt.Printf("  %s ok %s  Detected Muse credentials (%s)\n", colorGreen, colorReset, creds.Source)
		if promptYesNo(reader, "Enable Muse tracking?", true) {
			return true, ""
		}
		return false, ""
	}

	fmt.Printf("  %s!%s No Muse login found (run 'muse login' first, or paste a key)%s\n", colorYellow, colorReset, colorReset)
	fmt.Printf("  %sLeave empty to enable with auto-detect later.%s\n", colorDim, colorReset)
	for {
		fmt.Print("  Meta API key [Enter = auto-detect]: ")
		key := readLine(reader)
		if key == "" {
			return promptYesNo(reader, "Enable Muse tracking anyway?", false), ""
		}
		// One tiny probe verifies the key; the same probe powers `muse /usage`.
		summary, err := verifyMuseKey(key, api.ResolveMuseModel())
		switch {
		case err == nil:
			fmt.Printf("  %s ok %s  Key verified (%s)  %s%s%s\n", colorGreen, colorReset, summary, colorDim, maskValue(key), colorReset)
			return true, key
		case api.IsMuseAuthError(err):
			fmt.Printf("  %sMeta rejected this key (401). Run 'muse login' and try again.%s\n", colorRed, colorReset)
			continue
		default:
			fmt.Printf("  %s!%s Could not reach api.meta.ai to verify the key (%v) - saving it anyway\n", colorYellow, colorReset, err)
			fmt.Printf("  %s ok %s  %s%s%s\n", colorGreen, colorReset, colorDim, maskValue(key), colorReset)
			return true, key
		}
	}
}

// detectCommandCodeCredentialsFunc resolves local Command Code credentials for
// the setup wizard. A package variable so tests can stub it and never read the
// developer's real auth files.
var detectCommandCodeCredentialsFunc = api.DetectCommandCodeCredentials

// verifyCommandCodeKey checks a key against the Command Code API with one
// read-only GET and returns a short credit summary.
// A package variable so tests can stub it and never touch the network.
var verifyCommandCodeKey = func(key, baseURL string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	opts := []api.CommandCodeClientOption{api.WithCommandCodeTimeout(20 * time.Second)}
	if baseURL != "" {
		opts = append(opts, api.WithCommandCodeBaseURL(baseURL))
	}
	snap, err := api.NewCommandCodeClient(key, slog.New(slog.NewTextHandler(io.Discard, nil)), opts...).FetchSnapshot(ctx)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("$%.2f credits remaining, %d requests this period", snap.RemainingCredits, snap.PeriodReqs), nil
}

// collectCommandCode enables Command Code credit tracking. onWatch prefers the
// key saved by the `cmd` CLI - the Command Code binary, which most onWatch
// users get here through - and only stores an explicit key in .env when the
// user provides one. Every poll is a read-only GET, so unlike Muse there is no
// per-poll cost to warn about. Returns the enabled flag and the explicit key
// ("" when auto-detect is used).
func collectCommandCode(reader *bufio.Reader, logger *slog.Logger) (bool, string) {
	fmt.Printf("\n  %sCommand Code Setup%s\n", colorBold, colorReset)
	fmt.Printf("  %sonWatch reads your Command Code credit balance and rate-limit windows from the Command Code API.%s\n", colorDim, colorReset)

	if creds := detectCommandCodeCredentialsFunc(logger); creds != nil && creds.APIKey != "" {
		fmt.Printf("  %s ok %s  Detected Command Code credentials (%s)\n", colorGreen, colorReset, creds.Source)
		return promptYesNo(reader, "Enable Command Code tracking?", true), ""
	}

	fmt.Printf("  %s!%s No Command Code login found (log in with the 'cmd' CLI first, or paste a key)%s\n", colorYellow, colorReset, colorReset)
	fmt.Printf("  %sLeave empty to enable with auto-detect later.%s\n", colorDim, colorReset)
	for {
		fmt.Print("  Command Code API key [Enter = auto-detect]: ")
		key := readLine(reader)
		if key == "" {
			return promptYesNo(reader, "Enable Command Code tracking anyway?", false), ""
		}
		summary, err := verifyCommandCodeKey(key, "")
		switch {
		case err == nil:
			fmt.Printf("  %s ok %s  Key verified (%s)  %s%s%s\n", colorGreen, colorReset, summary, colorDim, maskValue(key), colorReset)
			return true, key
		case api.IsCommandCodeAuthError(err):
			fmt.Printf("  %sCommand Code rejected this key (401). Log in again with the 'cmd' CLI or paste a fresh key.%s\n", colorRed, colorReset)
			continue
		default:
			fmt.Printf("  %s!%s Could not reach api.commandcode.ai to verify the key (%v) - saving it anyway\n", colorYellow, colorReset, err)
			fmt.Printf("  %s ok %s  %s%s%s\n", colorGreen, colorReset, colorDim, maskValue(key), colorReset)
			return true, key
		}
	}
}

// collectZenmuxKey prompts for a Zenmux Management API Key. Zenmux has no
// local CLI login to auto-detect, so the key is always entered by hand and
// stored as ZENMUX_MANAGEMENT_API_KEY. Setup never contacts zenmux.ai: the
// first poll verifies the key.
func collectZenmuxKey(reader *bufio.Reader) string {
	fmt.Printf("\n  %sZenmux Setup%s\n", colorBold, colorReset)
	fmt.Printf("  %sCreate a Management API Key in the Zenmux Console.%s\n", colorDim, colorReset)
	fmt.Printf("  %sAccount usage needs a personal account management key, not an inference key.%s\n", colorDim, colorReset)
	for {
		fmt.Print("  Zenmux Management API Key: ")
		key := readLine(reader)
		if key == "" {
			fmt.Printf("  %sCannot be empty%s\n", colorRed, colorReset)
			continue
		}
		fmt.Printf("  %s ok %s  %s%s%s\n", colorGreen, colorReset, colorDim, maskValue(key), colorReset)
		return key
	}
}

// collectAlibaba enables the Alibaba Token Plan provider. Its credential is a
// multi-line cookies.txt export, which does not fit a one-line secret prompt,
// so the wizard asks for a path to a cookie file instead and records it as
// ALIBABA_COOKIES_FILE. An empty path is allowed: the provider stays enabled so
// an account can be added from the dashboard after install. The file path is
// validated but its contents are never read into .env or echoed.
// Returns (enabled, cookiesFile, region).
func collectAlibaba(reader *bufio.Reader) (bool, string, string) {
	fmt.Printf("\n  %sAlibaba Token Plan Setup%s\n", colorBold, colorReset)
	fmt.Printf("  %sonWatch reads the console with your browser session cookies.%s\n", colorDim, colorReset)
	fmt.Printf("  %sExport them per docs/ALIBABA_SETUP.md, then give the path to the export.%s\n", colorDim, colorReset)
	fmt.Printf("  %sPress Enter to skip the file and add an account from the dashboard after install.%s\n", colorDim, colorReset)

	var cookiesFile string
	for {
		fmt.Print("  Alibaba cookies.txt path [Enter = configure from dashboard]: ")
		input := readLine(reader)
		if input == "" {
			break
		}
		expanded := expandHomePath(input)
		info, err := os.Stat(expanded)
		if err != nil || info.IsDir() {
			fmt.Printf("  %s!%s Cannot read %s\n", colorYellow, colorReset, expanded)
			continue
		}
		// Only the path is stored. Reject an export that clearly holds no
		// Alibaba cookies now instead of saving a file the daemon will skip.
		if data, readErr := os.ReadFile(expanded); readErr == nil {
			if _, filterErr := api.FilterAlibabaCookies(string(data)); filterErr != nil {
				fmt.Printf("  %s!%s That export has no alibabacloud.com cookies - export cookies for the Alibaba console site only\n", colorYellow, colorReset)
				continue
			}
		}
		if _, ok := alibabaEnvValue(expanded); !ok {
			fmt.Printf("  %s!%s That path contains a quote or line break, or ends in a backslash, and cannot be stored in .env - move the export somewhere else\n", colorYellow, colorReset)
			continue
		}
		cookiesFile = expanded
		fmt.Printf("  %s ok %s  Using cookie export %s\n", colorGreen, colorReset, expanded)
		break
	}

	if cookiesFile == "" {
		fmt.Printf("  %sAdd the Alibaba account in Settings -> Providers after install; see docs/ALIBABA_SETUP.md%s\n", colorDim, colorReset)
	}

	region := ""
	for {
		region = promptWithDefault(reader, "Alibaba console region", "ap-southeast-1")
		if alibabaSetupRegionPattern.MatchString(region) {
			break
		}
		fmt.Printf("  %sRegion must be a lowercase slug such as ap-southeast-1%s\n", colorRed, colorReset)
	}

	return true, cookiesFile, region
}

// alibabaSetupRegionPattern mirrors the validation in internal/config and
// internal/web so the wizard never writes a region those reject.
var alibabaSetupRegionPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,31}$`)

// expandHomePath expands a leading ~ to the user's home directory (the shell
// does this for a terminal, but not for a value typed into the wizard).
func expandHomePath(path string) string {
	path = strings.TrimSpace(path)
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	return filepath.Join(home, path[2:])
}

// alibabaEnvValue renders a cookie file path as a godotenv value. The file is
// read line by line through godotenv, where an unquoted value is cut at " #"
// and has "$" expanded; a stray quote can make the whole file fail to load.
// Single-quoted values are taken literally by godotenv, so the path is wrapped
// in single quotes when that round-trips. ok is false for a path that cannot be
// represented (it contains the quote character or a line break, or ends in a
// backslash, which godotenv treats as escaping the closing quote and then
// fails the whole file with "unterminated quoted value"). Backslashes elsewhere
// are literal inside single quotes, so Windows paths still round-trip.
func alibabaEnvValue(path string) (value string, ok bool) {
	if strings.ContainsAny(path, "'\n\r") || strings.HasSuffix(path, `\`) {
		return "", false
	}
	return "'" + path + "'", true
}

// alibabaEnvValueOrRaw is alibabaEnvValue for values that may predate this
// quoting, where an unrepresentable path is still better written than dropped.
// New input is rejected before it reaches here, so this only guards a re-run.
func alibabaEnvValueOrRaw(path string) string {
	if value, ok := alibabaEnvValue(path); ok {
		return value
	}
	return path
}

// decodeEnvValue unquotes a single- or double-quoted .env value so the
// detection pass compares the path the user typed and the path godotenv reads.
func decodeEnvValue(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) >= 2 {
		if (raw[0] == '\'' && raw[len(raw)-1] == '\'') || (raw[0] == '"' && raw[len(raw)-1] == '"') {
			return raw[1 : len(raw)-1]
		}
	}
	return raw
}

// openCodeGoConfig is the result of the OpenCode Go prompt: either a Usage API
// key, or a workspace ID plus session cookie.
type openCodeGoConfig struct {
	key         string
	workspaceID string
	cookie      string
}

// verifyOpenCodeGoKey checks a key against the Go usage API with one read-only
// GET and returns a short window summary. A package variable so tests can stub
// it and never touch the network.
var verifyOpenCodeGoKey = func(key string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	snap, err := api.NewOpenCodeClient(slog.New(slog.NewTextHandler(io.Discard, nil)), api.WithOpenCodeTimeout(15*time.Second)).FetchUsageSnapshot(ctx, key)
	if err != nil {
		return "", err
	}
	parts := make([]string, 0, len(snap.Quotas))
	for _, q := range snap.Quotas {
		parts = append(parts, fmt.Sprintf("%s %.1f%%", q.Name, q.Utilization))
	}
	return strings.Join(parts, " / ") + " used", nil
}

// collectOpenCodeGo prompts for OpenCode Go credentials. A Go Usage API key
// (oc_sk_...) is preferred; a workspace ID (wrk_...) switches to session-cookie
// mode, which needs the __Host-console_session cookie as well. Returns an empty
// config when the user skips, so the provider stays off.
func collectOpenCodeGo(reader *bufio.Reader, logger *slog.Logger) openCodeGoConfig {
	_ = logger
	fmt.Printf("\n  %sOpenCode Go Setup%s\n", colorBold, colorReset)
	fmt.Printf("  %sonWatch reads your Go plan's 5-hour, weekly and monthly meters.%s\n", colorDim, colorReset)
	fmt.Printf("  %sCreate a key in the OpenCode console: Keys -> Add Service Account (usage read).%s\n", colorDim, colorReset)

	for {
		fmt.Print("  OpenCode Go Usage API key (oc_sk_...), or workspace ID (wrk_...) for cookie mode [Enter = skip]: ")
		value := readLine(reader)
		if value == "" {
			fmt.Printf("  %sSkipped OpenCode Go (enable it later in Settings -> Providers)%s\n", colorDim, colorReset)
			return openCodeGoConfig{}
		}
		if strings.HasPrefix(value, "wrk_") {
			return collectOpenCodeGoCookieMode(reader, value)
		}
		if !strings.HasPrefix(value, "oc_sk_") {
			fmt.Printf("  %sKey must start with 'oc_sk_' (or a 'wrk_' workspace ID for cookie mode)%s\n", colorRed, colorReset)
			continue
		}
		summary, err := verifyOpenCodeGoKey(value)
		switch {
		case err == nil:
			fmt.Printf("  %s ok %s  Key verified (%s)  %s%s%s\n", colorGreen, colorReset, summary, colorDim, maskValue(value), colorReset)
			return openCodeGoConfig{key: value}
		case api.IsOpenCodeAuthError(err):
			fmt.Printf("  %sOpenCode rejected this key. Check it has usage read access in the console.%s\n", colorRed, colorReset)
			continue
		default:
			fmt.Printf("  %s!%s Could not reach opencode.ai to verify the key (%v) - saving it anyway\n", colorYellow, colorReset, err)
			fmt.Printf("  %s ok %s  %s%s%s\n", colorGreen, colorReset, colorDim, maskValue(value), colorReset)
			return openCodeGoConfig{key: value}
		}
	}
}

// collectOpenCodeGoCookieMode asks for the session cookie when the user gave a
// workspace ID. Both values are required together.
func collectOpenCodeGoCookieMode(reader *bufio.Reader, workspaceID string) openCodeGoConfig {
	for {
		fmt.Print("  __Host-console_session cookie value (from opencode.ai/console) [Enter = skip]: ")
		cookie := readLine(reader)
		if cookie == "" {
			fmt.Printf("  %sCookie mode needs the session cookie; skipping OpenCode Go%s\n", colorDim, colorReset)
			return openCodeGoConfig{}
		}
		fmt.Printf("  %s ok %s  Workspace %s%s%s with a session cookie\n", colorGreen, colorReset, colorDim, workspaceID, colorReset)
		return openCodeGoConfig{workspaceID: workspaceID, cookie: cookie}
	}
}

// applyOpenCodeGoConfig copies a collected OpenCode Go config into the setup
// config, keeping the configured flag in step with the credential.
func applyOpenCodeGoConfig(cfg *setupConfig, oc openCodeGoConfig) {
	cfg.openCodeGoKey = oc.key
	cfg.openCodeGoWorkspaceID = oc.workspaceID
	cfg.openCodeGoAuthCookie = oc.cookie
	cfg.openCodeGoConfigured = oc.key != "" || (oc.workspaceID != "" && oc.cookie != "")
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

	if cfg.openCodeEnabled {
		b.WriteString("# OpenCode (opencode-codex) - reads ~/.local/share/opencode/auth.json (feeds Codex)\n")
		b.WriteString("OPENCODE_ENABLED=true\n\n")
	}

	if cfg.antigravityEnabled {
		b.WriteString("# Antigravity (Windsurf) - auto-detected from local process\n")
		b.WriteString("ANTIGRAVITY_ENABLED=true\n")
		source := cfg.antigravitySource
		if source == "" {
			source = "both"
		}
		b.WriteString("# Data source: both | cli (agy) | ide\n")
		b.WriteString(fmt.Sprintf("ANTIGRAVITY_SOURCE=%s\n\n", source))
	}

	if cfg.geminiEnabled {
		b.WriteString("# Gemini CLI - auto-detected from ~/.gemini/oauth_creds.json\n")
		b.WriteString("GEMINI_ENABLED=true\n\n")
	}

	if cfg.grokEnabled {
		b.WriteString("# Grok (xAI) - auto-detected from ~/.grok/auth.json (or $GROK_HOME)\n")
		b.WriteString("GROK_ENABLED=true\n\n")
	}

	if cfg.ollamaKey != "" {
		b.WriteString("# Ollama Cloud API key (https://ollama.com/settings/keys)\n")
		b.WriteString(fmt.Sprintf("OLLAMA_API_KEY=%s\n", cfg.ollamaKey))
		b.WriteString("# Optional: included usage cap in USD (0 = derive from plan)\n")
		b.WriteString("# OLLAMA_MONTHLY_LIMIT=\n")
		b.WriteString("# Optional: reset day of month 1-31 (0 = account anniversary)\n")
		b.WriteString("# OLLAMA_RESET_DAY=\n\n")
	}

	if cfg.museEnabled || cfg.museKey != "" {
		b.WriteString("# Muse (Meta) coding plan - auto-detected from `muse login` when no key is set\n")
		if cfg.museKey != "" {
			b.WriteString(fmt.Sprintf("META_API_KEY=%s\n", cfg.museKey))
		} else {
			b.WriteString("MUSE_ENABLED=true\n")
		}
		b.WriteString("# Optional: usage-probe model (default: your Muse settings model, else muse-spark-1.3)\n")
		b.WriteString("# META_MUSE_MODEL=\n\n")
	}

	if cfg.commandCodeEnabled || cfg.commandCodeKey != "" {
		b.WriteString("# Command Code - auto-detected from the `cmd` CLI login when no key is set\n")
		if cfg.commandCodeKey != "" {
			b.WriteString(fmt.Sprintf("COMMAND_CODE_API_KEY=%s\n", cfg.commandCodeKey))
		} else {
			b.WriteString("COMMANDCODE_ENABLED=true\n")
		}
		b.WriteString("# Optional: override the API base URL (proxy or compatible endpoint)\n")
		b.WriteString("# COMMANDCODE_BASE_URL=\n\n")
	}

	if cfg.zenmuxKey != "" {
		b.WriteString("# Zenmux Management API Key (https://zenmux.ai) - see docs/ZENMUX_SETUP.md\n")
		b.WriteString(fmt.Sprintf("ZENMUX_MANAGEMENT_API_KEY=%s\n", cfg.zenmuxKey))
		b.WriteString("# Optional: set false to disable Zenmux polling even with a key present\n")
		b.WriteString("# ZENMUX_ENABLED=\n\n")
	}

	if cfg.openCodeGoConfigured || cfg.openCodeGoKey != "" || cfg.openCodeGoWorkspaceID != "" {
		b.WriteString("# OpenCode Go - reads your Go plan's 5-hour/weekly/monthly meters\n")
		if cfg.openCodeGoKey != "" {
			b.WriteString(fmt.Sprintf("OPENCODE_GO_API_KEY=%s\n\n", cfg.openCodeGoKey))
		} else {
			b.WriteString(fmt.Sprintf("OPENCODE_GO_WORKSPACE_ID=%s\n", cfg.openCodeGoWorkspaceID))
			b.WriteString(fmt.Sprintf("OPENCODE_GO_AUTH_COOKIE=%s\n\n", cfg.openCodeGoAuthCookie))
		}
	}

	if cfg.alibabaEnabled || cfg.alibabaCookiesFile != "" {
		b.WriteString("# Alibaba Token Plan - see docs/ALIBABA_SETUP.md\n")
		if cfg.alibabaCookiesFile != "" {
			b.WriteString("# Path to a cookies.txt export (cookies stay in the file, never in .env)\n")
			b.WriteString(fmt.Sprintf("ALIBABA_COOKIES_FILE=%s\n", alibabaEnvValueOrRaw(cfg.alibabaCookiesFile)))
		} else {
			b.WriteString("# No cookies file yet: add an account in Settings -> Providers after install\n")
			b.WriteString("ALIBABA_ENABLED=true\n")
		}
		b.WriteString(fmt.Sprintf("ALIBABA_REGION=%s\n\n", cfg.alibabaRegion))
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
	if cfg.openCodeEnabled {
		providers = append(providers, "OpenCode")
	}
	if cfg.antigravityEnabled {
		providers = append(providers, "Antigravity")
	}
	if cfg.geminiEnabled {
		providers = append(providers, "Gemini")
	}
	if cfg.grokEnabled {
		providers = append(providers, "Grok")
	}
	if cfg.ollamaKey != "" {
		providers = append(providers, "Ollama Cloud")
	}
	if cfg.museEnabled || cfg.museKey != "" {
		providers = append(providers, "Muse")
	}
	if cfg.commandCodeEnabled || cfg.commandCodeKey != "" {
		providers = append(providers, "Command Code")
	}
	if cfg.zenmuxKey != "" {
		providers = append(providers, "Zenmux")
	}
	if cfg.openCodeGoConfigured || cfg.openCodeGoKey != "" || cfg.openCodeGoWorkspaceID != "" {
		providers = append(providers, "OpenCode Go")
	}
	if cfg.alibabaEnabled || cfg.alibabaCookiesFile != "" {
		providers = append(providers, "Alibaba Token Plan")
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
	if service.Supported() {
		fmt.Printf("    %sonwatch service status%s  # Auto-start at login\n", colorCyan, colorReset)
	}
	fmt.Println()
}

// --- Existing .env handling ---

type existingEnv struct {
	syntheticKey          string
	zaiKey                string
	anthropicToken        string
	codexToken            string
	openCodeEnabled       bool
	antigravityEnabled    bool
	geminiEnabled         bool
	grokEnabled           bool
	ollamaKey             string
	commandCodeKey        string
	commandCodeEnabled    bool
	zenmuxKey             string
	alibabaConfigured     bool
	alibabaCookiesFile    string
	openCodeGoConfigured  bool
	openCodeGoWorkspaceID string
	openCodeGoAuthCookie  string
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
		case "OPENCODE_ENABLED":
			env.openCodeEnabled = val == "true"
		case "ANTIGRAVITY_ENABLED":
			env.antigravityEnabled = val == "true"
		case "GEMINI_ENABLED":
			env.geminiEnabled = val == "true"
		case "GROK_ENABLED":
			env.grokEnabled = val == "true"
		case "GROK_TOKEN":
			if val != "" {
				env.grokEnabled = true
			}
		case "OLLAMA_API_KEY":
			env.ollamaKey = val
		case "COMMAND_CODE_API_KEY", "COMMANDCODE_API_KEY":
			if val != "" {
				env.commandCodeKey = val
			}
		case "COMMANDCODE_ENABLED":
			if val == "true" {
				env.commandCodeEnabled = true
			}
		case "ZENMUX_MANAGEMENT_API_KEY":
			env.zenmuxKey = val
		case "OPENCODE_GO_API_KEY":
			if val != "" {
				env.openCodeGoConfigured = true
			}
		case "OPENCODE_GO_WORKSPACE_ID":
			if val != "" {
				env.openCodeGoWorkspaceID = val
			}
		case "OPENCODE_GO_AUTH_COOKIE":
			if val != "" {
				env.openCodeGoAuthCookie = val
			}
		case "ALIBABA_COOKIES_FILE", "ALIBABA_COOKIES":
			// The value may be single- or double-quoted so a path with spaces,
			// "#" or "$" survives godotenv; compare the unquoted text.
			if value := decodeEnvValue(val); value != "" {
				env.alibabaConfigured = true
				if key == "ALIBABA_COOKIES_FILE" {
					env.alibabaCookiesFile = value
				}
			}
		case "ALIBABA_ENABLED":
			if val == "true" {
				env.alibabaConfigured = true
			}
		}
	}
	// Session mode needs both the workspace ID and the cookie.
	if env.openCodeGoWorkspaceID != "" && env.openCodeGoAuthCookie != "" {
		env.openCodeGoConfigured = true
	}
	if !env.geminiEnabled {
		if home, err := os.UserHomeDir(); err == nil {
			if _, err := os.Stat(filepath.Join(home, ".gemini", "oauth_creds.json")); err == nil {
				env.geminiEnabled = true
			}
		}
	}
	if !env.grokEnabled {
		if _, err := os.Stat(api.GrokAuthPath()); err == nil {
			env.grokEnabled = true
		}
	}
	return env
}

func allProvidersConfigured(env *existingEnv) bool {
	return env.syntheticKey != "" && env.zaiKey != "" && env.anthropicToken != "" && env.codexToken != "" && env.openCodeEnabled && env.antigravityEnabled && env.geminiEnabled && env.grokEnabled && env.ollamaKey != ""
}

func anyProviderConfigured(env *existingEnv) bool {
	return env.syntheticKey != "" || env.zaiKey != "" || env.anthropicToken != "" || env.codexToken != "" || env.openCodeEnabled || env.antigravityEnabled || env.geminiEnabled || env.grokEnabled || env.ollamaKey != "" || env.commandCodeKey != "" || env.commandCodeEnabled || env.zenmuxKey != "" || env.openCodeGoConfigured || env.alibabaConfigured
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
	if existing.openCodeEnabled {
		configured = append(configured, "OpenCode")
	}
	if existing.antigravityEnabled {
		configured = append(configured, "Antigravity")
	}
	if existing.geminiEnabled {
		configured = append(configured, "Gemini")
	}
	if existing.grokEnabled {
		configured = append(configured, "Grok")
	}
	if existing.ollamaKey != "" {
		configured = append(configured, "Ollama Cloud")
	}
	if existing.commandCodeEnabled || existing.commandCodeKey != "" {
		configured = append(configured, "Command Code")
	}
	if existing.zenmuxKey != "" {
		configured = append(configured, "Zenmux")
	}
	if existing.openCodeGoConfigured {
		configured = append(configured, "OpenCode Go")
	}
	if existing.alibabaConfigured {
		configured = append(configured, "Alibaba Token Plan")
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

	if !existing.openCodeEnabled {
		path := api.OpenCodeAuthPath()
		detected := false
		if path != "" {
			if _, statErr := os.Stat(path); statErr == nil {
				detected = true
			}
		}
		if detected {
			fmt.Printf("  %s ok %s  OpenCode (opencode-codex) credentials detected on this system\n", colorGreen, colorReset)
			if promptYesNo(reader, "Enable OpenCode (opencode-codex) tracking?", true) {
				fmt.Fprintf(f, "\n# OpenCode (opencode-codex) - reads ~/.local/share/opencode/auth.json (feeds Codex)\nOPENCODE_ENABLED=true\n")
				fmt.Printf("  %s ok %s  Added OpenCode provider to .env (auto-detected)\n", colorGreen, colorReset)
			}
		} else if promptYesNo(reader, "Add OpenCode (opencode-codex) provider?", false) {
			fmt.Fprintf(f, "\n# OpenCode (opencode-codex) - reads ~/.local/share/opencode/auth.json (feeds Codex)\nOPENCODE_ENABLED=true\n")
			fmt.Printf("  %s ok %s  Added OpenCode provider to .env\n", colorGreen, colorReset)
			fmt.Printf("  %sNote: run 'opencode auth login' and choose ChatGPT to authenticate%s\n", colorDim, colorReset)
		}
	}

	if !existing.antigravityEnabled {
		if promptYesNo(reader, "Add Antigravity (Windsurf) provider?", false) {
			source := collectAntigravitySource(reader)
			fmt.Fprintf(f, "\n# Antigravity (Windsurf) - auto-detected from local process\nANTIGRAVITY_ENABLED=true\n# Data source: both | cli (agy) | ide\nANTIGRAVITY_SOURCE=%s\n", source)
			fmt.Printf("  %s ok %s  Added Antigravity provider to .env (source: %s)\n", colorGreen, colorReset, source)
		}
	}

	if !existing.geminiEnabled {
		// Try to detect Gemini CLI credentials. Use os.UserHomeDir, not $HOME:
		// Windows keeps the profile in USERPROFILE.
		geminiDetected := false
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			_, statErr := os.Stat(filepath.Join(home, ".gemini", "oauth_creds.json"))
			geminiDetected = statErr == nil
		}
		if geminiDetected {
			fmt.Printf("  %s ok %s  Gemini CLI credentials detected on this system\n", colorGreen, colorReset)
			if promptYesNo(reader, "Enable Gemini tracking?", true) {
				fmt.Fprintf(f, "\n# Gemini CLI - auto-detected from ~/.gemini/oauth_creds.json\nGEMINI_ENABLED=true\n")
				fmt.Printf("  %s ok %s  Added Gemini provider to .env (auto-detected)\n", colorGreen, colorReset)
			}
		} else if promptYesNo(reader, "Add Gemini CLI provider?", false) {
			fmt.Fprintf(f, "\n# Gemini CLI - auto-detected from ~/.gemini/oauth_creds.json\nGEMINI_ENABLED=true\n")
			fmt.Printf("  %s ok %s  Added Gemini provider to .env\n", colorGreen, colorReset)
			fmt.Printf("  %sNote: Install Gemini CLI and run 'gemini' to authenticate%s\n", colorDim, colorReset)
		}
	}

	if !existing.grokEnabled {
		if creds := api.DetectGrokCredentials(logger); creds != nil && creds.AccessToken != "" {
			fmt.Printf("  %s ok %s  Grok credentials detected on this system\n", colorGreen, colorReset)
			if promptYesNo(reader, "Enable Grok tracking?", true) {
				fmt.Fprintf(f, "\n# Grok (xAI) - auto-detected from ~/.grok/auth.json (or $GROK_HOME)\nGROK_ENABLED=true\n")
				fmt.Printf("  %s ok %s  Added Grok provider to .env (auto-detected)\n", colorGreen, colorReset)
			}
		} else if promptYesNo(reader, "Add Grok (xAI) provider?", false) {
			fmt.Fprintf(f, "\n# Grok (xAI) - auto-detected from ~/.grok/auth.json (or $GROK_HOME)\nGROK_ENABLED=true\n")
			fmt.Printf("  %s ok %s  Added Grok provider to .env\n", colorGreen, colorReset)
			fmt.Printf("  %sNote: run 'grok login' to authenticate (or set GROK_TOKEN)%s\n", colorDim, colorReset)
		}
	}

	if existing.ollamaKey == "" {
		if promptYesNo(reader, "Add Ollama Cloud provider?", false) {
			key := collectOllamaKey(reader)
			fmt.Fprintf(f, "\n# Ollama Cloud API key (https://ollama.com/settings/keys)\nOLLAMA_API_KEY=%s\n", key)
			fmt.Printf("  %s ok %s  Added Ollama Cloud provider to .env\n", colorGreen, colorReset)
		}
	}

	if !existing.commandCodeEnabled && existing.commandCodeKey == "" {
		if promptYesNo(reader, "Add Command Code provider?", false) {
			enabled, key := collectCommandCode(reader, logger)
			if key != "" {
				fmt.Fprintf(f, "\n# Command Code\nCOMMAND_CODE_API_KEY=%s\n", key)
			} else if enabled {
				fmt.Fprintf(f, "\n# Command Code - auto-detected from the `cmd` CLI login\nCOMMANDCODE_ENABLED=true\n")
			}
			fmt.Printf("  %s ok %s  Added Command Code provider to .env\n", colorGreen, colorReset)
		}
	}

	if existing.zenmuxKey == "" {
		if promptYesNo(reader, "Add Zenmux provider?", false) {
			key := collectZenmuxKey(reader)
			fmt.Fprintf(f, "\n# Zenmux Management API Key (https://zenmux.ai) - see docs/ZENMUX_SETUP.md\nZENMUX_MANAGEMENT_API_KEY=%s\n", key)
			fmt.Printf("  %s ok %s  Added Zenmux provider to .env\n", colorGreen, colorReset)
		}
	}

	if !existing.alibabaConfigured {
		if promptYesNo(reader, "Add Alibaba Token Plan provider?", false) {
			_, cookiesFile, region := collectAlibaba(reader)
			fmt.Fprintf(f, "\n# Alibaba Token Plan - see docs/ALIBABA_SETUP.md\n")
			if cookiesFile != "" {
				fmt.Fprintf(f, "ALIBABA_COOKIES_FILE=%s\n", alibabaEnvValueOrRaw(cookiesFile))
			} else {
				fmt.Fprintf(f, "ALIBABA_ENABLED=true\n")
			}
			fmt.Fprintf(f, "ALIBABA_REGION=%s\n", region)
			fmt.Printf("  %s ok %s  Added Alibaba Token Plan provider to .env\n", colorGreen, colorReset)
		}
	}

	if !existing.openCodeGoConfigured {
		if promptYesNo(reader, "Add OpenCode Go provider?", false) {
			oc := collectOpenCodeGo(reader, logger)
			if oc.key != "" {
				fmt.Fprintf(f, "\n# OpenCode Go - reads your Go plan's 5-hour/weekly/monthly meters\nOPENCODE_GO_API_KEY=%s\n", oc.key)
			} else if oc.workspaceID != "" && oc.cookie != "" {
				fmt.Fprintf(f, "\n# OpenCode Go - browser session mode\nOPENCODE_GO_WORKSPACE_ID=%s\nOPENCODE_GO_AUTH_COOKIE=%s\n", oc.workspaceID, oc.cookie)
			}
			if oc.key != "" || oc.workspaceID != "" {
				fmt.Printf("  %s ok %s  Added OpenCode Go provider to .env\n", colorGreen, colorReset)
			}
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
