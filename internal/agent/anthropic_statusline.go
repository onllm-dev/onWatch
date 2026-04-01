package agent

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

// statuslineStalenessDefault is how old the statusline file can be before
// falling back to OAuth polling. Claude Code updates this on every API call,
// so 5 minutes of no updates indicates CC is idle or not running.
const statuslineStalenessDefault = 5 * time.Minute

// bridgeCheckIntervalDefault is how often the agent verifies the bridge is
// still configured in Claude Code's settings.json.
const bridgeCheckIntervalDefault = 30 * time.Minute

// statuslineFileName is the name of the shared file that the bridge script writes.
const statuslineFileName = "anthropic-statusline.json"

// bridgeSnippet is a minimal inline bash snippet prepended to the user's
// statusline command. It saves stdin to a file, then pipes stdin through
// to the original command unchanged. No separate script files needed.
//
// How it works:
//   1. Reads all of stdin into $I
//   2. Saves $I to ~/.onwatch/data/anthropic-statusline.json (atomic via temp+mv)
//   3. Pipes $I to stdout (so the next command in the pipe gets it)
const bridgeSnippet = `bash -c 'I=$(cat);D=$HOME/.onwatch/data;mkdir -p "$D" 2>/dev/null;T="$D/.sl-$$";printf "%s" "$I">"$T"&&mv -f "$T" "$D/anthropic-statusline.json" 2>/dev/null||rm -f "$T" 2>/dev/null;printf "%s" "$I"'`

// bridgeMarker is a substring used to detect if the bridge snippet is already
// present in the user's statusline command.
const bridgeMarker = "anthropic-statusline.json"

// StatuslineRateLimits is the rate_limits portion of the Claude Code statusline JSON.
type StatuslineRateLimits struct {
	FiveHour *StatuslineWindow `json:"five_hour,omitempty"`
	SevenDay *StatuslineWindow `json:"seven_day,omitempty"`
}

// StatuslineWindow represents a single rate limit window from the statusline.
type StatuslineWindow struct {
	UsedPercentage float64 `json:"used_percentage"`
	ResetsAt       int64   `json:"resets_at"` // Unix epoch seconds
}

// statuslinePayload is the subset of the Claude Code statusline JSON we parse.
type statuslinePayload struct {
	RateLimits StatuslineRateLimits `json:"rate_limits"`
}

// readStatuslineData reads and parses the statusline JSON file.
// Returns nil, nil if the file doesn't exist.
func readStatuslineData(path string) (*StatuslineRateLimits, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read statusline file: %w", err)
	}
	if len(data) == 0 {
		return nil, nil
	}

	var payload statuslinePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("parse statusline JSON: %w", err)
	}

	return &payload.RateLimits, nil
}

// isStatuslineFresh returns true if the statusline file exists and was modified
// within the given maximum age.
func isStatuslineFresh(path string, maxAge time.Duration) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) < maxAge
}

// isValidStatuslineWindow checks if a rate limit window has plausible values.
// Invalid data triggers fallback to API polling.
func isValidStatuslineWindow(w *StatuslineWindow) bool {
	if w == nil {
		return false
	}
	// used_percentage must be 0-100
	if w.UsedPercentage < 0 || w.UsedPercentage > 100 {
		return false
	}
	// resets_at must be a plausible Unix timestamp (after 2024, before 2030)
	if w.ResetsAt != 0 {
		if w.ResetsAt < 1704067200 || w.ResetsAt > 1893456000 {
			return false
		}
	}
	return true
}

// isValidStatuslineData checks if the rate limit data is plausible.
// Returns false if the data looks corrupted or nonsensical, triggering API fallback.
func isValidStatuslineData(rl *StatuslineRateLimits) bool {
	if rl == nil {
		return false
	}
	hasValid := false
	if rl.FiveHour != nil {
		if !isValidStatuslineWindow(rl.FiveHour) {
			return false
		}
		hasValid = true
	}
	if rl.SevenDay != nil {
		if !isValidStatuslineWindow(rl.SevenDay) {
			return false
		}
		hasValid = true
	}
	return hasValid
}

// statuslineToSnapshot converts statusline rate limit data to an AnthropicSnapshot
// compatible with the existing store and tracker pipeline.
// Both statusline and the OAuth API report utilization as 0-100 percentage values.
func statuslineToSnapshot(rl *StatuslineRateLimits, capturedAt time.Time) *api.AnthropicSnapshot {
	snapshot := &api.AnthropicSnapshot{
		CapturedAt: capturedAt,
	}

	if rl.FiveHour != nil {
		q := api.AnthropicQuota{
			Name:        "five_hour",
			Utilization: rl.FiveHour.UsedPercentage,
		}
		if rl.FiveHour.ResetsAt > 0 {
			t := time.Unix(rl.FiveHour.ResetsAt, 0).UTC()
			q.ResetsAt = &t
		}
		snapshot.Quotas = append(snapshot.Quotas, q)
	}

	if rl.SevenDay != nil {
		q := api.AnthropicQuota{
			Name:        "seven_day",
			Utilization: rl.SevenDay.UsedPercentage,
		}
		if rl.SevenDay.ResetsAt > 0 {
			t := time.Unix(rl.SevenDay.ResetsAt, 0).UTC()
			q.ResetsAt = &t
		}
		snapshot.Quotas = append(snapshot.Quotas, q)
	}

	// Generate synthetic raw JSON for audit trail
	if raw, err := json.Marshal(rl); err == nil {
		snapshot.RawJSON = `{"_source":"statusline","rate_limits":` + string(raw) + `}`
	}

	return snapshot
}

// --- Bridge Setup (Minimal Inline Approach) ---

// bridgeSetup holds mutable state for the bridge auto-configuration.
var bridgeSetup struct {
	mu            sync.Mutex
	lastCheck     time.Time
	checkInterval time.Duration
}

func init() {
	bridgeSetup.checkInterval = bridgeCheckIntervalDefault
}

// onwatchDataDir returns the onWatch data directory path.
func onwatchDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".onwatch", "data")
}

// claudeSettingsPath returns the path to Claude Code's user settings.json.
func claudeSettingsPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".claude", "settings.json")
}

// isClaudeCodeInstalled checks if Claude Code appears to be installed
// by looking for the ~/.claude/ directory.
func isClaudeCodeInstalled() bool {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(home, ".claude"))
	return err == nil && info.IsDir()
}

// isBridgeDisabled checks if the user has disabled the statusline bridge.
func isBridgeDisabled() bool {
	dataDir := onwatchDataDir()
	if dataDir == "" {
		return false
	}
	data, err := os.ReadFile(filepath.Join(dataDir, "..", "config.json"))
	if err != nil {
		return false
	}
	var cfg map[string]interface{}
	if json.Unmarshal(data, &cfg) != nil {
		return false
	}
	if v, ok := cfg["statusline_bridge"]; ok {
		if b, ok := v.(bool); ok {
			return !b
		}
		if s, ok := v.(string); ok {
			return s == "off" || s == "false" || s == "disabled"
		}
	}
	return false
}

// hasBridgeSnippet returns true if the command already contains our bridge.
func hasBridgeSnippet(command string) bool {
	return strings.Contains(command, bridgeMarker)
}

// addBridgeSnippet prepends the save snippet to the user's command via a pipe.
// If the user has no command, returns just the save snippet (no pipe).
func addBridgeSnippet(userCommand string) string {
	if userCommand == "" {
		// No user command - standalone: save data, no display output
		return bridgeSnippet + " > /dev/null"
	}
	// Prepend: save stdin to file, then pipe original stdin to user's command
	return bridgeSnippet + " | " + userCommand
}

// removeBridgeSnippet strips our snippet from the command, returning the
// user's original command. Returns empty string if nothing remains.
func removeBridgeSnippet(command string) string {
	// Remove "snippet | user-cmd" → "user-cmd"
	if idx := strings.Index(command, bridgeSnippet+" | "); idx == 0 {
		return strings.TrimSpace(command[len(bridgeSnippet+" | "):])
	}
	// Remove "snippet > /dev/null" → "" (standalone mode)
	if command == bridgeSnippet+" > /dev/null" {
		return ""
	}
	// Not our command
	return command
}

// readClaudeSettings reads and parses ~/.claude/settings.json.
func readClaudeSettings() (map[string]interface{}, error) {
	path := claudeSettingsPath()
	if path == "" {
		return nil, fmt.Errorf("cannot determine settings path")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]interface{}), nil
		}
		return nil, fmt.Errorf("read settings: %w", err)
	}
	if len(data) == 0 {
		return make(map[string]interface{}), nil
	}
	var settings map[string]interface{}
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("parse settings: %w", err)
	}
	return settings, nil
}

// writeClaudeSettings writes settings to ~/.claude/settings.json atomically.
func writeClaudeSettings(settings map[string]interface{}) error {
	path := claudeSettingsPath()
	if path == "" {
		return fmt.Errorf("cannot determine settings path")
	}

	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create settings dir: %w", err)
	}

	tmpFile, err := os.CreateTemp(dir, ".onwatch-settings-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp settings file: %w", err)
	}
	tmpPath := tmpFile.Name()
	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write temp settings: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp settings: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("chmod temp settings: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename settings: %w", err)
	}

	return nil
}

// getCurrentStatusLineCommand extracts the current statusLine command from settings.
func getCurrentStatusLineCommand(settings map[string]interface{}) string {
	sl, ok := settings["statusLine"]
	if !ok || sl == nil {
		return ""
	}
	slMap, ok := sl.(map[string]interface{})
	if !ok {
		return ""
	}
	cmd, _ := slMap["command"].(string)
	return cmd
}

// setStatusLineCommand sets the statusLine command in settings.
func setStatusLineCommand(settings map[string]interface{}, command string) {
	settings["statusLine"] = map[string]interface{}{
		"type":    "command",
		"command": command,
	}
}

// SetupStatuslineBridge configures the Claude Code statusline bridge for zero-429
// Anthropic monitoring. Uses a minimal inline approach: prepends a small bash
// snippet to the user's existing statusline command that tees stdin to a file.
//
// - User's original command stays visible and functional in settings.json
// - No separate script files or passthrough files needed
// - Snippet is idempotent - safe to call multiple times
func SetupStatuslineBridge(logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}

	if !isClaudeCodeInstalled() {
		logger.Debug("Claude Code not detected, statusline bridge not configured")
		return nil
	}

	if isBridgeDisabled() {
		logger.Debug("Statusline bridge disabled by user configuration")
		return nil
	}

	// Ensure data directory exists for the statusline file
	dataDir := onwatchDataDir()
	if dataDir != "" {
		_ = os.MkdirAll(dataDir, 0o700)
	}

	settings, err := readClaudeSettings()
	if err != nil {
		logger.Warn("Cannot read Claude Code settings, skipping statusline bridge setup", "error", err)
		return nil
	}

	currentCmd := getCurrentStatusLineCommand(settings)

	if hasBridgeSnippet(currentCmd) {
		logger.Debug("Statusline bridge already configured")
		return nil
	}

	// Prepend our snippet to whatever the user has (or standalone if empty)
	newCmd := addBridgeSnippet(currentCmd)
	setStatusLineCommand(settings, newCmd)
	if err := writeClaudeSettings(settings); err != nil {
		logger.Warn("Failed to configure statusline bridge", "error", err)
		return nil
	}

	if currentCmd == "" {
		logger.Info("Configured statusline bridge (standalone)")
	} else {
		logger.Info("Configured statusline bridge (prepended to existing command)")
	}
	return nil
}

// EnsureStatuslineBridge performs a periodic health check to verify the bridge
// snippet is still present. If the user changed their statusline, re-prepends it.
func EnsureStatuslineBridge(logger *slog.Logger) {
	bridgeSetup.mu.Lock()
	defer bridgeSetup.mu.Unlock()

	if time.Since(bridgeSetup.lastCheck) < bridgeSetup.checkInterval {
		return
	}
	bridgeSetup.lastCheck = time.Now()

	if !isClaudeCodeInstalled() || isBridgeDisabled() {
		return
	}

	settings, err := readClaudeSettings()
	if err != nil {
		return
	}

	currentCmd := getCurrentStatusLineCommand(settings)
	if hasBridgeSnippet(currentCmd) {
		return // Still healthy
	}

	// Bridge was removed (user changed their statusline) - re-prepend
	newCmd := addBridgeSnippet(currentCmd)
	setStatusLineCommand(settings, newCmd)
	if err := writeClaudeSettings(settings); err == nil {
		if currentCmd == "" {
			logger.Info("Statusline bridge re-established (standalone)")
		} else {
			logger.Info("Statusline bridge re-prepended to user command")
		}
	}
}

// DisableStatuslineBridge removes the bridge snippet from Claude Code settings.
func DisableStatuslineBridge(logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}

	settings, err := readClaudeSettings()
	if err != nil {
		return fmt.Errorf("read settings: %w", err)
	}

	currentCmd := getCurrentStatusLineCommand(settings)
	if !hasBridgeSnippet(currentCmd) {
		logger.Info("Statusline bridge not configured, nothing to disable")
		return nil
	}

	// Strip our snippet, restore user's original command
	originalCmd := removeBridgeSnippet(currentCmd)
	if originalCmd == "" {
		delete(settings, "statusLine")
	} else {
		setStatusLineCommand(settings, originalCmd)
	}
	if err := writeClaudeSettings(settings); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}

	// Clean up statusline data file
	dataDir := onwatchDataDir()
	if dataDir != "" {
		_ = os.Remove(filepath.Join(dataDir, statuslineFileName))
	}

	logger.Info("Statusline bridge disabled and cleaned up")
	return nil
}

// StatuslineDataPath returns the path to the statusline data file.
func StatuslineDataPath() string {
	dataDir := onwatchDataDir()
	if dataDir == "" {
		return ""
	}
	return filepath.Join(dataDir, statuslineFileName)
}
