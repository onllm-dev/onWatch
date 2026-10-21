package agent

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/testutil/testhome"
)

// --- readStatuslineData tests ---

func TestReadStatuslineData_ValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "statusline.json")

	payload := `{
		"rate_limits": {
			"five_hour": {"used_percentage": 42.5, "resets_at": 1766000000},
			"seven_day": {"used_percentage": 15.2, "resets_at": 1766500000}
		},
		"session_id": "test-session"
	}`
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	rl, err := readStatuslineData(path)
	if err != nil {
		t.Fatalf("readStatuslineData: %v", err)
	}
	if rl == nil {
		t.Fatal("expected non-nil result")
	}
	if rl.FiveHour == nil {
		t.Fatal("expected FiveHour to be set")
	}
	if rl.FiveHour.UsedPercentage != 42.5 {
		t.Errorf("FiveHour.UsedPercentage = %f, want 42.5", rl.FiveHour.UsedPercentage)
	}
	if rl.FiveHour.ResetsAt != 1766000000 {
		t.Errorf("FiveHour.ResetsAt = %d, want 1766000000", rl.FiveHour.ResetsAt)
	}
	if rl.SevenDay == nil {
		t.Fatal("expected SevenDay to be set")
	}
	if rl.SevenDay.UsedPercentage != 15.2 {
		t.Errorf("SevenDay.UsedPercentage = %f, want 15.2", rl.SevenDay.UsedPercentage)
	}
}

func TestReadStatuslineData_FileNotFound(t *testing.T) {
	rl, err := readStatuslineData("/nonexistent/path/statusline.json")
	if err != nil {
		t.Fatalf("expected nil error for missing file, got: %v", err)
	}
	if rl != nil {
		t.Fatal("expected nil result for missing file")
	}
}

func TestReadStatuslineData_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "statusline.json")
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	rl, err := readStatuslineData(path)
	if err != nil {
		t.Fatalf("expected nil error for empty file, got: %v", err)
	}
	if rl != nil {
		t.Fatal("expected nil result for empty file")
	}
}

func TestReadStatuslineData_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "statusline.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := readStatuslineData(path)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestReadStatuslineData_NoRateLimits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "statusline.json")
	if err := os.WriteFile(path, []byte(`{"session_id":"test"}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	rl, err := readStatuslineData(path)
	if err != nil {
		t.Fatalf("readStatuslineData: %v", err)
	}
	if rl == nil {
		t.Fatal("expected non-nil result")
	}
	if rl.FiveHour != nil || rl.SevenDay != nil {
		t.Error("expected nil windows when not present")
	}
}

func TestReadStatuslineData_PartialRateLimits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "statusline.json")
	payload := `{"rate_limits": {"five_hour": {"used_percentage": 50.0, "resets_at": 1766000000}}}`
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	rl, err := readStatuslineData(path)
	if err != nil {
		t.Fatalf("readStatuslineData: %v", err)
	}
	if rl.FiveHour == nil {
		t.Fatal("expected FiveHour to be set")
	}
	if rl.SevenDay != nil {
		t.Error("expected SevenDay to be nil when not present")
	}
}

// --- isStatuslineFresh tests ---

func TestIsStatuslineFresh_RecentFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "statusline.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !isStatuslineFresh(path, 5*time.Minute) {
		t.Error("expected fresh for just-written file")
	}
}

func TestIsStatuslineFresh_StaleFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "statusline.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	oldTime := time.Now().Add(-10 * time.Minute)
	if err := os.Chtimes(path, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	if isStatuslineFresh(path, 5*time.Minute) {
		t.Error("expected stale for old file")
	}
}

func TestIsStatuslineFresh_MissingFile(t *testing.T) {
	if isStatuslineFresh("/nonexistent/file", 5*time.Minute) {
		t.Error("expected not fresh for missing file")
	}
}

// --- Validation tests ---

func TestIsValidStatuslineData_Valid(t *testing.T) {
	rl := &StatuslineRateLimits{
		FiveHour: &StatuslineWindow{UsedPercentage: 42.5, ResetsAt: 1766000000},
	}
	if !isValidStatuslineData(rl) {
		t.Error("expected valid")
	}
}

func TestIsValidStatuslineData_OutOfRange(t *testing.T) {
	rl := &StatuslineRateLimits{
		FiveHour: &StatuslineWindow{UsedPercentage: 150, ResetsAt: 1766000000},
	}
	if isValidStatuslineData(rl) {
		t.Error("expected invalid for percentage > 100")
	}
}

func TestIsValidStatuslineData_NegativePercent(t *testing.T) {
	rl := &StatuslineRateLimits{
		FiveHour: &StatuslineWindow{UsedPercentage: -5, ResetsAt: 1766000000},
	}
	if isValidStatuslineData(rl) {
		t.Error("expected invalid for negative percentage")
	}
}

func TestIsValidStatuslineData_Nil(t *testing.T) {
	if isValidStatuslineData(nil) {
		t.Error("expected invalid for nil")
	}
}

func TestIsValidStatuslineData_EmptyWindows(t *testing.T) {
	rl := &StatuslineRateLimits{}
	if isValidStatuslineData(rl) {
		t.Error("expected invalid when no windows present")
	}
}

// --- statuslineToSnapshot tests ---

func TestStatuslineToSnapshot_FullData(t *testing.T) {
	rl := &StatuslineRateLimits{
		FiveHour: &StatuslineWindow{UsedPercentage: 42.5, ResetsAt: 1766000000},
		SevenDay: &StatuslineWindow{UsedPercentage: 15.0, ResetsAt: 1766500000},
	}

	now := time.Now().UTC()
	snap := statuslineToSnapshot(rl, now)

	if snap.CapturedAt != now {
		t.Errorf("CapturedAt = %v, want %v", snap.CapturedAt, now)
	}
	if len(snap.Quotas) != 2 {
		t.Fatalf("quota count = %d, want 2", len(snap.Quotas))
	}

	quotaMap := map[string]float64{}
	for _, q := range snap.Quotas {
		quotaMap[q.Name] = q.Utilization
	}

	// Verify percentage is passed through as-is (API also uses 0-100)
	if v, ok := quotaMap["five_hour"]; !ok || v != 42.5 {
		t.Errorf("five_hour utilization = %f, want 42.5", v)
	}
	if v, ok := quotaMap["seven_day"]; !ok || v != 15.0 {
		t.Errorf("seven_day utilization = %f, want 15.0", v)
	}

	for _, q := range snap.Quotas {
		if q.ResetsAt == nil {
			t.Errorf("%s: ResetsAt should be set", q.Name)
		}
	}

	if snap.RawJSON == "" {
		t.Error("expected RawJSON to be set")
	}
	if !strings.Contains(snap.RawJSON, `"_source":"statusline"`) {
		t.Errorf("RawJSON should contain source marker, got: %s", snap.RawJSON)
	}
}

func TestStatuslineToSnapshot_OnlyFiveHour(t *testing.T) {
	rl := &StatuslineRateLimits{
		FiveHour: &StatuslineWindow{UsedPercentage: 80.0, ResetsAt: 1766000000},
	}
	snap := statuslineToSnapshot(rl, time.Now().UTC())
	if len(snap.Quotas) != 1 {
		t.Fatalf("quota count = %d, want 1", len(snap.Quotas))
	}
	if snap.Quotas[0].Name != "five_hour" {
		t.Errorf("quota name = %s, want five_hour", snap.Quotas[0].Name)
	}
	if snap.Quotas[0].Utilization != 80.0 {
		t.Errorf("utilization = %f, want 80.0", snap.Quotas[0].Utilization)
	}
}

func TestStatuslineToSnapshot_NoResetsAt(t *testing.T) {
	rl := &StatuslineRateLimits{
		FiveHour: &StatuslineWindow{UsedPercentage: 50.0, ResetsAt: 0},
	}
	snap := statuslineToSnapshot(rl, time.Now().UTC())
	if snap.Quotas[0].ResetsAt != nil {
		t.Error("ResetsAt should be nil when epoch is 0")
	}
}

func TestStatuslineToSnapshot_Empty(t *testing.T) {
	rl := &StatuslineRateLimits{}
	snap := statuslineToSnapshot(rl, time.Now().UTC())
	if len(snap.Quotas) != 0 {
		t.Errorf("quota count = %d, want 0 for empty rate limits", len(snap.Quotas))
	}
}

// --- Bridge snippet tests ---

func TestHasBridgeSnippet(t *testing.T) {
	tests := []struct {
		command string
		want    bool
	}{
		{"", false},
		{"/usr/local/bin/my-statusline.sh", false},
		{bridgeSnippet + " | ~/.claude/statusline.sh", true},
		{bridgeSnippet + " > /dev/null", true},
		{"something with anthropic-statusline.json in it", true},
	}
	for _, tt := range tests {
		if got := hasBridgeSnippet(tt.command); got != tt.want {
			t.Errorf("hasBridgeSnippet(%q) = %v, want %v", tt.command[:min(len(tt.command), 40)], got, tt.want)
		}
	}
}

func TestAddBridgeSnippet_NoExistingCommand(t *testing.T) {
	result := addBridgeSnippet("")
	if !strings.Contains(result, bridgeMarker) {
		t.Error("expected bridge marker in result")
	}
	if !strings.HasSuffix(result, "> /dev/null") {
		t.Error("expected > /dev/null suffix for standalone mode")
	}
}

func TestAddBridgeSnippet_WithExistingCommand(t *testing.T) {
	result := addBridgeSnippet("~/.claude/statusline.sh")
	if !strings.Contains(result, bridgeMarker) {
		t.Error("expected bridge marker in result")
	}
	if !strings.HasSuffix(result, "~/.claude/statusline.sh") {
		t.Errorf("expected user command at end, got: %s", result)
	}
	if !strings.Contains(result, " | ") {
		t.Error("expected pipe between snippet and user command")
	}
}

func TestRemoveBridgeSnippet_WithUserCommand(t *testing.T) {
	cmd := bridgeSnippet + " | ~/.claude/statusline.sh"
	result := removeBridgeSnippet(cmd)
	if result != "~/.claude/statusline.sh" {
		t.Errorf("removeBridgeSnippet() = %q, want ~/.claude/statusline.sh", result)
	}
}

func TestRemoveBridgeSnippet_Standalone(t *testing.T) {
	cmd := bridgeSnippet + " > /dev/null"
	result := removeBridgeSnippet(cmd)
	if result != "" {
		t.Errorf("removeBridgeSnippet() = %q, want empty", result)
	}
}

func TestRemoveBridgeSnippet_NotOurCommand(t *testing.T) {
	result := removeBridgeSnippet("/usr/local/bin/my-statusline.sh")
	if result != "/usr/local/bin/my-statusline.sh" {
		t.Errorf("removeBridgeSnippet() = %q, want original", result)
	}
}

// --- Bridge setup tests ---

func TestSetupStatuslineBridge_CCNotInstalled(t *testing.T) {
	home := t.TempDir()
	testhome.SetTestHome(t, home)
	logger := slog.Default()
	if err := SetupStatuslineBridge(logger); err != nil {
		t.Fatalf("expected no error when CC not installed: %v", err)
	}
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	if _, err := os.Stat(settingsPath); !os.IsNotExist(err) {
		t.Error("settings.json should not be created when CC not installed")
	}
}

func TestSetupStatuslineBridge_NoExistingStatusline(t *testing.T) {
	home := t.TempDir()
	testhome.SetTestHome(t, home)
	stubBridgeShell(t, true)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	logger := slog.Default()
	if err := SetupStatuslineBridge(logger); err != nil {
		t.Fatalf("SetupStatuslineBridge: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings map[string]interface{}
	json.Unmarshal(data, &settings)
	cmd := getCurrentStatusLineCommand(settings)
	if !hasBridgeSnippet(cmd) {
		t.Errorf("expected bridge snippet in command, got: %s", cmd)
	}
	if !strings.HasSuffix(cmd, "> /dev/null") {
		t.Error("expected standalone mode (> /dev/null)")
	}
}

func TestSetupStatuslineBridge_PrependsToExistingCommand(t *testing.T) {
	home := t.TempDir()
	testhome.SetTestHome(t, home)
	stubBridgeShell(t, true)
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Write existing settings with user's statusline
	existingSettings := map[string]interface{}{
		"theme": "dark",
		"statusLine": map[string]interface{}{
			"type":    "command",
			"command": "~/.claude/statusline.sh",
		},
	}
	data, _ := json.MarshalIndent(existingSettings, "", "  ")
	os.WriteFile(filepath.Join(claudeDir, "settings.json"), data, 0o600)

	logger := slog.Default()
	if err := SetupStatuslineBridge(logger); err != nil {
		t.Fatalf("SetupStatuslineBridge: %v", err)
	}

	updatedData, _ := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	var settings map[string]interface{}
	json.Unmarshal(updatedData, &settings)
	cmd := getCurrentStatusLineCommand(settings)

	// Should have bridge snippet + pipe + original command
	if !hasBridgeSnippet(cmd) {
		t.Errorf("expected bridge snippet, got: %s", cmd)
	}
	if !strings.HasSuffix(cmd, "~/.claude/statusline.sh") {
		t.Errorf("expected user command preserved at end, got: %s", cmd)
	}

	// Other settings preserved
	if settings["theme"] != "dark" {
		t.Error("existing settings should be preserved")
	}
}

func TestSetupStatuslineBridge_Idempotent(t *testing.T) {
	home := t.TempDir()
	testhome.SetTestHome(t, home)
	stubBridgeShell(t, true)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	logger := slog.Default()
	SetupStatuslineBridge(logger)

	data1, _ := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))

	SetupStatuslineBridge(logger)

	data2, _ := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))

	if string(data1) != string(data2) {
		t.Error("setup not idempotent")
	}
}

func TestSetupStatuslineBridge_Disabled(t *testing.T) {
	home := t.TempDir()
	testhome.SetTestHome(t, home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	configDir := filepath.Join(home, ".onwatch")
	os.MkdirAll(configDir, 0o700)
	os.WriteFile(filepath.Join(configDir, "config.json"), []byte(`{"statusline_bridge": false}`), 0o600)

	logger := slog.Default()
	SetupStatuslineBridge(logger)

	settingsPath := filepath.Join(home, ".claude", "settings.json")
	if _, err := os.Stat(settingsPath); !os.IsNotExist(err) {
		t.Error("settings.json should not exist when bridge is disabled")
	}
}

func TestSetupStatuslineBridge_MalformedSettings(t *testing.T) {
	home := t.TempDir()
	testhome.SetTestHome(t, home)
	claudeDir := filepath.Join(home, ".claude")
	os.MkdirAll(claudeDir, 0o700)
	os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte("{bad json"), 0o600)

	logger := slog.Default()
	if err := SetupStatuslineBridge(logger); err != nil {
		t.Fatalf("expected graceful skip, got error: %v", err)
	}
}

// --- Disable tests ---

func TestDisableStatuslineBridge_RestoresOriginalCommand(t *testing.T) {
	home := t.TempDir()
	testhome.SetTestHome(t, home)
	stubBridgeShell(t, true)
	claudeDir := filepath.Join(home, ".claude")
	os.MkdirAll(claudeDir, 0o700)

	// Setup with existing user command
	existingSettings := map[string]interface{}{
		"statusLine": map[string]interface{}{
			"type":    "command",
			"command": "~/.claude/statusline.sh",
		},
	}
	data, _ := json.MarshalIndent(existingSettings, "", "  ")
	os.WriteFile(filepath.Join(claudeDir, "settings.json"), data, 0o600)

	logger := slog.Default()
	SetupStatuslineBridge(logger)
	DisableStatuslineBridge(logger)

	updatedData, _ := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	var settings map[string]interface{}
	json.Unmarshal(updatedData, &settings)
	cmd := getCurrentStatusLineCommand(settings)
	if cmd != "~/.claude/statusline.sh" {
		t.Errorf("expected restored command, got: %s", cmd)
	}
}

func TestDisableStatuslineBridge_RemovesStandalone(t *testing.T) {
	home := t.TempDir()
	testhome.SetTestHome(t, home)
	stubBridgeShell(t, true)
	claudeDir := filepath.Join(home, ".claude")
	os.MkdirAll(claudeDir, 0o700)

	logger := slog.Default()
	SetupStatuslineBridge(logger)
	DisableStatuslineBridge(logger)

	updatedData, _ := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	var settings map[string]interface{}
	json.Unmarshal(updatedData, &settings)
	if _, exists := settings["statusLine"]; exists {
		t.Error("statusLine should be removed when standalone was disabled")
	}
}

// --- Health check tests ---

func TestEnsureStatuslineBridge_DetectsUserChange(t *testing.T) {
	home := t.TempDir()
	testhome.SetTestHome(t, home)
	stubBridgeShell(t, true)
	claudeDir := filepath.Join(home, ".claude")
	os.MkdirAll(claudeDir, 0o700)

	logger := slog.Default()
	SetupStatuslineBridge(logger)

	// Simulate user changing their statusline (removes our snippet)
	newSettings := map[string]interface{}{
		"statusLine": map[string]interface{}{
			"type":    "command",
			"command": "/usr/local/bin/new-statusline.sh",
		},
	}
	data, _ := json.MarshalIndent(newSettings, "", "  ")
	os.WriteFile(filepath.Join(claudeDir, "settings.json"), data, 0o600)

	// Reset timer
	bridgeSetup.mu.Lock()
	bridgeSetup.lastCheck = time.Time{}
	bridgeSetup.mu.Unlock()

	EnsureStatuslineBridge(logger)

	updatedData, _ := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	var settings map[string]interface{}
	json.Unmarshal(updatedData, &settings)
	cmd := getCurrentStatusLineCommand(settings)

	if !hasBridgeSnippet(cmd) {
		t.Errorf("expected bridge re-prepended, got: %s", cmd)
	}
	if !strings.HasSuffix(cmd, "/usr/local/bin/new-statusline.sh") {
		t.Errorf("expected user's new command preserved, got: %s", cmd)
	}
}

// --- captured-at tests ---

// The statusline payload has no timestamp, so the snapshot must be stamped
// with the file's mtime. Re-stamping it with the poll time makes data up to
// the staleness window old display as "now" (stale-data audit).
func TestStatuslineCapturedAtUsesFileMtime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "anthropic-statusline.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	mtime := time.Now().Add(-3 * time.Minute).Truncate(time.Second)
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	now := time.Now().UTC()
	got := readStatuslineCapturedAt(path, now)
	if !got.Equal(mtime.UTC()) {
		t.Fatalf("capturedAt = %v, want file mtime %v", got, mtime.UTC())
	}

	snap := statuslineToSnapshot(&StatuslineRateLimits{
		FiveHour: &StatuslineWindow{UsedPercentage: 10},
	}, got)
	if !snap.CapturedAt.Equal(mtime.UTC()) {
		t.Fatalf("snapshot CapturedAt = %v, want %v", snap.CapturedAt, mtime.UTC())
	}
}

func TestStatuslineCapturedAtFallsBackToNow(t *testing.T) {
	now := time.Now().UTC()
	if got := readStatuslineCapturedAt("/nonexistent/statusline.json", now); !got.Equal(now) {
		t.Fatalf("missing file capturedAt = %v, want %v", got, now)
	}
}

// A file whose mtime is in the future (clock skew) must not produce a card
// that is "fresh" for hours. Clamp it to now.
func TestStatuslineCapturedAtClampsFutureMtime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "anthropic-statusline.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	future := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	now := time.Now().UTC()
	if got := readStatuslineCapturedAt(path, now); !got.Equal(now) {
		t.Fatalf("future mtime capturedAt = %v, want clamped %v", got, now)
	}
}

// Review item 5: an unchanged statusline file must not insert another row on
// every poll. The bridge rewrites the file only when Claude Code refreshes, so
// the same mtime is the same data.
func TestStatuslinePollSkipsUnchangedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "anthropic-statusline.json")
	payload := `{"rate_limits":{"five_hour":{"used_percentage":42},"seven_day":{"used_percentage":15}}}`
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	mtime := time.Now().Add(-2 * time.Minute).Truncate(time.Second)
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer st.Close()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := NewAnthropicAgent(nil, st, nil, 120*time.Second, logger, nil)
	a.statuslinePath = path
	a.statuslineStaleness = time.Hour
	a.apiPollingDisabled = true

	if !a.pollStatuslineOnly() {
		t.Fatal("first poll must insert the statusline snapshot")
	}
	if a.pollStatuslineOnly() {
		t.Fatal("an unchanged statusline file must be skipped")
	}
	before, err := st.QueryAnthropicRange(mtime.Add(-time.Hour).UTC(), time.Now().Add(time.Hour).UTC())
	if err != nil {
		t.Fatalf("range: %v", err)
	}
	if len(before) != 1 {
		t.Fatalf("stored rows = %d, want exactly one after two polls", len(before))
	}

	// A new mtime is new data and must insert.
	later := mtime.Add(5 * time.Minute)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatalf("chtimes again: %v", err)
	}
	if !a.pollStatuslineOnly() {
		t.Fatal("a rewritten statusline file must insert")
	}
	after, err := st.QueryAnthropicRange(mtime.Add(-time.Hour).UTC(), time.Now().Add(time.Hour).UTC())
	if err != nil {
		t.Fatalf("range: %v", err)
	}
	if len(after) != 2 {
		t.Fatalf("stored rows = %d, want two after the file changed", len(after))
	}
}
