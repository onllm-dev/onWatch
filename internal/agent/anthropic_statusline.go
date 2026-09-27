package agent

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
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
//  1. Reads all of stdin into $I
//  2. Saves $I to ~/.onwatch/data/anthropic-statusline.json (atomic via temp+mv)
//  3. Pipes $I to stdout (so the next command in the pipe gets it)
//
// This is the exact text written on macOS and Linux. It must stay byte-for-byte
// stable: existing installs are recognised (and removed) by matching it.
const bridgeSnippet = bridgeSnippetHead + `$HOME/.onwatch/data` + bridgeSnippetTail

// bridgeSnippetHead and bridgeSnippetTail surround the data directory in the
// bridge snippet. Splitting the snippet here lets Windows embed an absolute
// data directory while every variant stays recognisable for removal.
const (
	bridgeSnippetHead = `bash -c 'I=$(cat);D=`
	bridgeSnippetTail = `;mkdir -p "$D" 2>/dev/null;T="$D/.sl-$$";printf "%s" "$I">"$T"&&mv -f "$T" "$D/anthropic-statusline.json" 2>/dev/null||rm -f "$T" 2>/dev/null;printf "%s" "$I"'`
)

// bridgeStandaloneSuffix discards the snippet's stdout when the user has no
// statusline command of their own.
const bridgeStandaloneSuffix = " > /dev/null"

// bridgeSnippetFor returns the bridge snippet for the given platform and
// onWatch data directory.
//
// On Windows, Claude Code runs statusline commands through Git Bash, where
// $HOME is not guaranteed to match the directory onWatch reads from:
// os.UserHomeDir uses %USERPROFILE%, while Git Bash derives HOME from an
// existing HOME variable or %HOMEDRIVE%%HOMEPATH% first. The snippet therefore
// embeds the absolute data directory, with forward slashes because Git Bash
// treats backslashes as escapes.
func bridgeSnippetFor(goos, dataDir string) string {
	if goos != "windows" || dataDir == "" {
		return bridgeSnippet
	}
	dir := strings.ReplaceAll(dataDir, `\`, "/")
	// Double-quote for the inner bash, then escape for the outer single quotes.
	quoted := `"` + bashDoubleQuoteEscaper.Replace(dir) + `"`
	quoted = strings.ReplaceAll(quoted, "'", `'\''`)
	return bridgeSnippetHead + quoted + bridgeSnippetTail
}

// bashDoubleQuoteEscaper escapes the characters that stay special inside a
// bash double-quoted string.
var bashDoubleQuoteEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "`", "\\`")

// bridgeGOOS is the platform whose bridge snippet this process writes. It is a
// variable only so tests can exercise the Windows and Unix handling on any OS.
var bridgeGOOS = runtime.GOOS

// currentBridgeSnippet returns the bridge snippet for this platform.
func currentBridgeSnippet() string {
	return bridgeSnippetFor(bridgeGOOS, onwatchDataDir())
}

// bridgeMarker is a substring used to detect if the bridge snippet is already
// present in the user's statusline command.
const bridgeMarker = "anthropic-statusline.json"

// StatuslineRateLimits is the rate_limits portion of the Claude Code statusline JSON.
//
// five_hour and seven_day are the two windows observed in practice, so they get
// named fields. Anything else Claude Code reports - a per-model weekly window,
// say - lands in Extra under the key it used. Decoding into a fixed struct
// would drop those silently, which is the same hardcoded-shape failure that
// hid Anthropic's per-model weekly limits in issue #121.
type StatuslineRateLimits struct {
	FiveHour *StatuslineWindow
	SevenDay *StatuslineWindow
	Extra    map[string]*StatuslineWindow
}

// statuslineWindowKeyFiveHour and statuslineWindowKeySevenDay are the two
// window names with dedicated fields; every other key flows through Extra.
const (
	statuslineWindowKeyFiveHour = "five_hour"
	statuslineWindowKeySevenDay = "seven_day"
)

// UnmarshalJSON keeps every entry that structurally looks like a rate-limit
// window, whatever it is called. Membership is decided by shape rather than by
// a list of known names: a name list is exactly what makes a new window
// invisible until someone ships a release.
func (rl *StatuslineRateLimits) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	for key, val := range raw {
		w := decodeStatuslineWindow(val)
		if w == nil {
			continue
		}
		switch key {
		case statuslineWindowKeyFiveHour:
			rl.FiveHour = w
		case statuslineWindowKeySevenDay:
			rl.SevenDay = w
		default:
			if rl.Extra == nil {
				rl.Extra = make(map[string]*StatuslineWindow)
			}
			rl.Extra[key] = w
		}
	}
	return nil
}

// MarshalJSON writes the windows back out under their original keys. Without
// it the audit trail in AnthropicSnapshot.RawJSON would lose everything in
// Extra - the same way the API path's stored "raw" response loses limits[].
func (rl StatuslineRateLimits) MarshalJSON() ([]byte, error) {
	out := make(map[string]*StatuslineWindow, len(rl.Extra)+2)
	for key, w := range rl.Extra {
		out[key] = w
	}
	if rl.FiveHour != nil {
		out[statuslineWindowKeyFiveHour] = rl.FiveHour
	}
	if rl.SevenDay != nil {
		out[statuslineWindowKeySevenDay] = rl.SevenDay
	}
	return json.Marshal(out)
}

// decodeStatuslineWindow returns a window only if the value is an object
// carrying used_percentage. A string, a number or an unrelated nested object
// under rate_limits must not become a quota card.
func decodeStatuslineWindow(val json.RawMessage) *StatuslineWindow {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(val, &probe); err != nil {
		return nil
	}
	if _, ok := probe["used_percentage"]; !ok {
		return nil
	}
	var w StatuslineWindow
	if err := json.Unmarshal(val, &w); err != nil {
		return nil
	}
	return &w
}

// orderedWindows returns every window sorted by name, with five_hour and
// seven_day first so the familiar cards keep their positions. Map iteration is
// randomised, so without this the quota order would change between polls.
func (rl *StatuslineRateLimits) orderedWindows() []statuslineNamedWindow {
	out := make([]statuslineNamedWindow, 0, len(rl.Extra)+2)
	if rl.FiveHour != nil {
		out = append(out, statuslineNamedWindow{statuslineWindowKeyFiveHour, rl.FiveHour})
	}
	if rl.SevenDay != nil {
		out = append(out, statuslineNamedWindow{statuslineWindowKeySevenDay, rl.SevenDay})
	}
	extra := make([]string, 0, len(rl.Extra))
	for key := range rl.Extra {
		extra = append(extra, key)
	}
	sort.Strings(extra)
	for _, key := range extra {
		out = append(out, statuslineNamedWindow{key, rl.Extra[key]})
	}
	return out
}

// statuslineNamedWindow pairs a window with the key Claude Code reported it under.
type statuslineNamedWindow struct {
	Name   string
	Window *StatuslineWindow
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
	for _, nw := range rl.orderedWindows() {
		if !isValidStatuslineWindow(nw.Window) {
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

	for _, nw := range rl.orderedWindows() {
		q := api.AnthropicQuota{
			Name:        nw.Name,
			Utilization: nw.Window.UsedPercentage,
		}
		if nw.Window.ResetsAt > 0 {
			t := time.Unix(nw.Window.ResetsAt, 0).UTC()
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
	snippet := currentBridgeSnippet()
	if userCommand == "" {
		// No user command - standalone: save data, no display output
		return snippet + bridgeStandaloneSuffix
	}
	// Prepend: save stdin to file, then pipe original stdin to user's command
	return snippet + " | " + userCommand
}

// removeBridgeSnippet strips our snippet from the command, returning the
// user's original command. Returns empty string if nothing remains.
func removeBridgeSnippet(command string) string {
	userCmd, _ := stripBridgeSnippet(command)
	return userCmd
}

// stripBridgeSnippet removes a bridge snippet written by any onWatch version or
// platform variant. ok is false when the command does not start with a bridge
// snippet, in which case command is returned unchanged.
func stripBridgeSnippet(command string) (userCmd string, ok bool) {
	if !strings.HasPrefix(command, bridgeSnippetHead) {
		return command, false
	}
	end := strings.Index(command, bridgeSnippetTail)
	if end < 0 {
		return command, false
	}
	rest := command[end+len(bridgeSnippetTail):]
	switch {
	case strings.HasPrefix(rest, " | "):
		// "snippet | user-cmd" -> "user-cmd"
		return strings.TrimSpace(rest[len(" | "):]), true
	case rest == bridgeStandaloneSuffix:
		// "snippet > /dev/null" -> "" (standalone mode)
		return "", true
	}
	return command, false
}

// bridgedCommand returns the statusline command with the current bridge
// snippet in front, and whether it differs from currentCmd. A bridge that is
// outdated for this platform (written for another data directory, or the
// $HOME form older Windows builds wrote) is replaced in place. A command that
// mentions the bridge file but was not written by onWatch is left alone.
//
// A recognised bridge from the other platform family is also left alone: a
// settings.json synced between Windows and macOS/Linux would otherwise be
// rewritten by each machine in turn, forever. Only Windows writes the quoted
// absolute-path form, so macOS/Linux never rewrite it; Windows replaces the
// $HOME form, which older Windows builds wrote, after which neither side
// changes the synced command again.
func bridgedCommand(currentCmd string) (string, bool) {
	if !hasBridgeSnippet(currentCmd) {
		return addBridgeSnippet(currentCmd), true
	}
	userCmd, ok := stripBridgeSnippet(currentCmd)
	if !ok {
		return currentCmd, false
	}
	if bridgeGOOS != "windows" && isWindowsBridgeSnippet(currentCmd) {
		return currentCmd, false
	}
	newCmd := addBridgeSnippet(userCmd)
	return newCmd, newCmd != currentCmd
}

// isWindowsBridgeSnippet reports whether command starts with the Windows form
// of the bridge snippet, which embeds a quoted absolute data directory where
// the macOS/Linux form has $HOME/.onwatch/data.
func isWindowsBridgeSnippet(command string) bool {
	if !strings.HasPrefix(command, bridgeSnippetHead) {
		return false
	}
	return strings.HasPrefix(command[len(bridgeSnippetHead):], `"`)
}

// removeUnrunnableBridge strips an existing bridge snippet from Claude Code's
// settings when no shell that can run it is available (Windows without Git
// Bash, where Claude Code runs the statusline in PowerShell and the snippet
// takes the user's own statusline down with it). The user's command is
// restored; a standalone bridge leaves no statusline. Settings without a
// bridge are not touched.
func removeUnrunnableBridge(logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	settings, err := readClaudeSettings()
	if err != nil {
		return
	}
	userCmd, ok := stripBridgeSnippet(getCurrentStatusLineCommand(settings))
	if !ok {
		return
	}
	if userCmd == "" {
		delete(settings, "statusLine")
	} else {
		setStatusLineCommand(settings, userCmd)
	}
	if err := writeClaudeSettings(settings); err != nil {
		logger.Warn("Failed to remove statusline bridge that PowerShell cannot run", "error", err)
		return
	}
	logger.Info("Removed statusline bridge from Claude Code settings: Git Bash not found, so Claude Code runs the statusline in PowerShell, which cannot run it")
}

// bridgeShellAvailable reports whether Claude Code will run the statusline
// command in a shell that understands the bash snippet. On Windows, Claude
// Code uses Git Bash when it is installed and PowerShell otherwise; under
// PowerShell the snippet fails and takes the user's own statusline down with
// it, so the bridge is only configured when Git Bash is present.
var bridgeShellAvailable = func() bool {
	if runtime.GOOS != "windows" {
		return true
	}
	return findGitBash(os.Getenv, exec.LookPath, isRegularFile) != ""
}

// findGitBash locates Git for Windows' bash.exe the way a Windows user would
// have it installed: an explicit CLAUDE_CODE_GIT_BASH_PATH, next to git.exe on
// PATH, or a standard install location. Returns "" if none is found.
func findGitBash(getenv func(string) string, lookPath func(string) (string, error), exists func(string) bool) string {
	if p := strings.TrimSpace(getenv("CLAUDE_CODE_GIT_BASH_PATH")); p != "" && exists(p) {
		return p
	}
	// git.exe lives in <root>\cmd, <root>\bin or <root>\mingw64\bin.
	if gitPath, err := lookPath("git"); err == nil && gitPath != "" {
		dir := filepath.Dir(gitPath)
		for _, up := range []string{"..", filepath.Join("..", "..")} {
			if c := filepath.Join(dir, up, "bin", "bash.exe"); exists(c) {
				return c
			}
		}
	}
	var candidates []string
	for _, env := range []string{"ProgramFiles", "ProgramW6432", "ProgramFiles(x86)"} {
		if root := getenv(env); root != "" {
			candidates = append(candidates, filepath.Join(root, "Git", "bin", "bash.exe"))
		}
	}
	if root := getenv("LOCALAPPDATA"); root != "" {
		candidates = append(candidates, filepath.Join(root, "Programs", "Git", "bin", "bash.exe"))
	}
	for _, c := range candidates {
		if exists(c) {
			return c
		}
	}
	return ""
}

// isRegularFile reports whether path exists and is not a directory.
func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
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

	if !bridgeShellAvailable() {
		removeUnrunnableBridge(logger)
		logger.Info("Claude Code statusline bridge disabled: Git Bash not found; statusline data unavailable on this machine (API polling still runs unless ANTHROPIC_SOURCE=statusline)")
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

	// Prepend our snippet to whatever the user has (or standalone if empty).
	// An outdated snippet is replaced in place.
	newCmd, changed := bridgedCommand(currentCmd)
	if !changed {
		logger.Debug("Statusline bridge already configured")
		return nil
	}
	setStatusLineCommand(settings, newCmd)
	if err := writeClaudeSettings(settings); err != nil {
		logger.Warn("Failed to configure statusline bridge", "error", err)
		return nil
	}

	if hasBridgeSnippet(currentCmd) {
		logger.Info("Updated statusline bridge")
	} else if currentCmd == "" {
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
	if !bridgeShellAvailable() {
		removeUnrunnableBridge(logger)
		return
	}

	settings, err := readClaudeSettings()
	if err != nil {
		return
	}

	currentCmd := getCurrentStatusLineCommand(settings)
	newCmd, changed := bridgedCommand(currentCmd)
	if !changed {
		return // Still healthy
	}

	// Bridge was removed (user changed their statusline) or is outdated - re-prepend
	setStatusLineCommand(settings, newCmd)
	if err := writeClaudeSettings(settings); err == nil {
		if hasBridgeSnippet(currentCmd) {
			logger.Info("Statusline bridge updated")
		} else if currentCmd == "" {
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
