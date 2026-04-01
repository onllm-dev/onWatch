package agent

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/notify"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

// CodexProfile represents a saved Codex credential profile.
type CodexProfile struct {
	Name      string    `json:"name"`
	AccountID string    `json:"account_id"` // Codex's account ID (string from API)
	UserID    string    `json:"user_id,omitempty"`
	SavedAt   time.Time `json:"saved_at"`
	Tokens    struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
	} `json:"tokens"`
	APIKey string `json:"api_key,omitempty"`
}

// CodexAgentInstance represents a running agent for a specific profile.
type CodexAgentInstance struct {
	Profile     CodexProfile
	DBAccountID int64 // Integer ID from provider_accounts table
	Agent       *CodexAgent
	Cancel      context.CancelFunc
}

// CodexAgentManager manages multiple CodexAgent instances for multi-account support.
type CodexAgentManager struct {
	store               *store.Store
	tracker             *tracker.CodexTracker
	interval            time.Duration
	logger              *slog.Logger
	notifier            *notify.NotificationEngine
	pollingCheck        func() bool                // Global Codex polling check
	accountPollingCheck func(accountID int64) bool // Per-account polling check

	mu        sync.RWMutex
	instances map[string]*CodexAgentInstance // profile name -> instance
	ctx       context.Context
	cancel    context.CancelFunc

	// For detecting new profiles
	profilesDir      string
	scanInterval     time.Duration
	lastScanProfiles map[string]time.Time // profile name -> modified time
}

// NewCodexAgentManager creates a new manager for multi-account Codex polling.
func NewCodexAgentManager(store *store.Store, tracker *tracker.CodexTracker, interval time.Duration, logger *slog.Logger) *CodexAgentManager {
	if logger == nil {
		logger = slog.Default()
	}

	return &CodexAgentManager{
		store:            store,
		tracker:          tracker,
		interval:         interval,
		logger:           logger,
		instances:        make(map[string]*CodexAgentInstance),
		scanInterval:     30 * time.Second, // Check for new profiles every 30 seconds
		lastScanProfiles: make(map[string]time.Time),
	}
}

// SetProfilesDir sets the directory to scan for Codex profile files.
func (m *CodexAgentManager) SetProfilesDir(dir string) {
	m.profilesDir = dir
}

// SetNotifier sets the notification engine for all agents.
func (m *CodexAgentManager) SetNotifier(n *notify.NotificationEngine) {
	m.notifier = n
}

// SetPollingCheck sets the global polling check function for all agents.
func (m *CodexAgentManager) SetPollingCheck(fn func() bool) {
	m.pollingCheck = fn
}

// SetAccountPollingCheck sets a per-account polling check function.
// This is called with the database account ID to check if polling is enabled for that specific account.
func (m *CodexAgentManager) SetAccountPollingCheck(fn func(accountID int64) bool) {
	m.accountPollingCheck = fn
}

// Run starts the manager and all profile agents.
func (m *CodexAgentManager) Run(ctx context.Context) error {
	m.ctx, m.cancel = context.WithCancel(ctx)
	defer m.cancel()

	m.logger.Info("Codex agent manager started", "interval", m.interval)

	// Load and start all existing profiles
	if err := m.loadAndStartProfiles(); err != nil {
		m.logger.Error("failed to load initial profiles", "error", err)
		// Continue anyway - we might have the default credentials
	}

	// If no profiles found, try to start with default credentials
	m.mu.RLock()
	hasProfiles := len(m.instances) > 0
	m.mu.RUnlock()

	if !hasProfiles {
		m.logger.Info("no saved profiles found, using current credentials as default")
		if err := m.startDefaultAgent(); err != nil {
			m.logger.Warn("failed to start default agent", "error", err)
		}
	}

	// Mark orphaned DB accounts as deleted - these are provider_accounts rows
	// that have no corresponding running agent (e.g., profile file was deleted
	// while daemon was not running). This ensures they don't show on the dashboard.
	m.markOrphanedAccountsDeleted()

	// Deduplicate provider accounts that share the same Codex account_id
	// (merges telemetry data, never deletes it)
	if merged, err := m.store.DeduplicateProviderAccounts("codex"); err != nil {
		m.logger.Warn("failed to deduplicate Codex accounts", "error", err)
	} else if merged > 0 {
		m.logger.Info("deduplicated Codex accounts", "merged", merged)
	}

	// Start profile scanner in background
	go m.profileScanner()

	// Wait for context cancellation
	<-m.ctx.Done()

	// Stop all agents
	m.stopAllAgents()

	return nil
}

// loadAndStartProfiles loads all profiles from disk and starts agents for each.
func (m *CodexAgentManager) loadAndStartProfiles() error {
	if m.profilesDir == "" {
		return fmt.Errorf("profiles directory not set")
	}

	entries, err := os.ReadDir(m.profilesDir)
	if os.IsNotExist(err) {
		return nil // No profiles directory yet
	}
	if err != nil {
		return fmt.Errorf("failed to read profiles directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		profilePath := filepath.Join(m.profilesDir, entry.Name())
		if err := m.loadAndStartProfile(profilePath); err != nil {
			m.logger.Warn("failed to load profile", "path", profilePath, "error", err)
			continue
		}

		// Track file modification time
		if info, err := entry.Info(); err == nil {
			profileName := strings.TrimSuffix(entry.Name(), ".json")
			m.lastScanProfiles[profileName] = info.ModTime()
		}
	}

	return nil
}

// loadAndStartProfile loads a single profile and starts an agent for it.
func (m *CodexAgentManager) loadAndStartProfile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var profile CodexProfile
	if err := json.Unmarshal(data, &profile); err != nil {
		return err
	}

	// Derive name from filename if not set
	if profile.Name == "" {
		base := filepath.Base(path)
		profile.Name = strings.TrimSuffix(base, ".json")
	}

	if profile.UserID == "" {
		profile.UserID = api.ParseIDTokenUserID(profile.Tokens.IDToken)
	}

	// Check if we already have this profile running
	m.mu.RLock()
	_, exists := m.instances[profile.Name]
	m.mu.RUnlock()

	if exists {
		return nil // Already running
	}

	return m.startAgentForProfile(profile)
}

func codexCredentialsFromProfile(profile CodexProfile) *api.CodexCredentials {
	idToken := strings.TrimSpace(profile.Tokens.IDToken)
	expiresAt := api.ParseIDTokenExpiry(idToken)
	var expiresIn time.Duration
	if !expiresAt.IsZero() {
		expiresIn = time.Until(expiresAt)
	}

	userID := strings.TrimSpace(profile.UserID)
	if userID == "" {
		userID = api.ParseIDTokenUserID(idToken)
	}

	return &api.CodexCredentials{
		AccessToken:  strings.TrimSpace(profile.Tokens.AccessToken),
		RefreshToken: strings.TrimSpace(profile.Tokens.RefreshToken),
		IDToken:      idToken,
		APIKey:       strings.TrimSpace(profile.APIKey),
		AccountID:    strings.TrimSpace(profile.AccountID),
		UserID:       userID,
		ExpiresAt:    expiresAt,
		ExpiresIn:    expiresIn,
	}
}

func readCodexProfileCredentials(profilePath string) *api.CodexCredentials {
	data, err := os.ReadFile(profilePath)
	if err != nil {
		return nil
	}

	var profile CodexProfile
	if err := json.Unmarshal(data, &profile); err != nil {
		return nil
	}

	return codexCredentialsFromProfile(profile)
}

func shouldUseSystemCredsForProfile(profileCreds, systemCreds *api.CodexCredentials, expectedAccountID string) bool {
	if systemCreds == nil {
		return false
	}

	systemAccountID := strings.TrimSpace(systemCreds.AccountID)
	if systemAccountID == "" {
		return false
	}

	if accountID := strings.TrimSpace(expectedAccountID); accountID != "" && accountID != systemAccountID {
		return false
	}
	if profileCreds != nil {
		if accountID := strings.TrimSpace(profileCreds.AccountID); accountID != "" && accountID != systemAccountID {
			return false
		}
	}

	if profileCreds == nil {
		return systemCreds.AccessToken != "" || systemCreds.APIKey != ""
	}

	if profileCreds.AccessToken == "" && systemCreds.AccessToken != "" {
		return true
	}

	if !profileCreds.ExpiresAt.IsZero() && !systemCreds.ExpiresAt.IsZero() {
		if systemCreds.ExpiresAt.After(profileCreds.ExpiresAt) {
			return true
		}
	}

	if profileCreds.IsExpired() && !systemCreds.IsExpired() {
		return true
	}

	return systemCreds.AccessToken != "" && subtle.ConstantTimeCompare([]byte(systemCreds.AccessToken), []byte(profileCreds.AccessToken)) == 0
}

func updateProfileFromSystemCreds(profilePath string, creds *api.CodexCredentials, logger *slog.Logger) error {
	if creds == nil {
		return fmt.Errorf("nil credentials")
	}
	if logger == nil {
		logger = slog.Default()
	}

	data, err := os.ReadFile(profilePath)
	if err != nil {
		return fmt.Errorf("read profile: %w", err)
	}

	var profile CodexProfile
	if err := json.Unmarshal(data, &profile); err != nil {
		return fmt.Errorf("parse profile: %w", err)
	}

	if creds.AccessToken != "" {
		profile.Tokens.AccessToken = creds.AccessToken
	}
	if creds.RefreshToken != "" {
		profile.Tokens.RefreshToken = creds.RefreshToken
	}
	if creds.IDToken != "" {
		profile.Tokens.IDToken = creds.IDToken
	}
	if profile.AccountID == "" {
		profile.AccountID = creds.AccountID
	}
	if profile.UserID == "" {
		profile.UserID = creds.UserID
	}
	profile.SavedAt = time.Now().UTC()

	updated, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal profile: %w", err)
	}

	tempPath := profilePath + ".tmp"
	if err := os.WriteFile(tempPath, updated, 0o600); err != nil {
		return fmt.Errorf("write temp profile: %w", err)
	}
	if err := os.Rename(tempPath, profilePath); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("rename profile: %w", err)
	}

	logger.Info("updated Codex profile tokens from auth.json", "path", profilePath)
	return nil
}

// startAgentForProfile creates and starts an agent for a specific profile.
func (m *CodexAgentManager) startAgentForProfile(profile CodexProfile) error {
	if profile.UserID == "" {
		profile.UserID = api.ParseIDTokenUserID(profile.Tokens.IDToken)
	}

	externalID := profile.AccountID
	if creds := codexCredentialsFromProfile(profile); creds != nil {
		if composite := creds.CompositeExternalID(); composite != "" {
			externalID = composite
		}
	}

	// Get or create the database account ID for this profile.
	// Uses external_id (Codex account_id:user_id for Team-safe dedup) to ensure
	// one DB row per real Codex identity, regardless of profile name.
	dbAccount, err := m.store.GetOrCreateProviderAccountByExternalID("codex", profile.Name, externalID)
	if err != nil {
		return fmt.Errorf("failed to get/create provider account: %w", err)
	}

	m.logger.Info("starting Codex agent for profile",
		"profile", profile.Name,
		"db_account_id", dbAccount.ID,
		"codex_account_id", profile.AccountID)

	// Create credentials from profile
	creds := codexCredentialsFromProfile(profile)

	// Create client for this profile
	client := api.NewCodexClient(creds.AccessToken, nil)

	// Create session manager for this profile
	sm := NewSessionManager(m.store, fmt.Sprintf("codex:%d", dbAccount.ID), 5*time.Minute, m.logger)

	// Create agent with account ID
	agent := NewCodexAgentWithAccount(client, m.store, m.tracker, m.interval, m.logger, sm, dbAccount.ID)

	// Set token refresh function that reads from profile
	profilePath := filepath.Join(m.profilesDir, profile.Name+".json")
	isDefaultProfile := profile.Name == "default"
	agent.SetTokenRefresh(func() string {
		if isDefaultProfile {
			if systemCreds := api.DetectCodexCredentials(m.logger); systemCreds != nil {
				return systemCreds.AccessToken
			}
			return profile.Tokens.AccessToken
		}

		profileCreds := readCodexProfileCredentials(profilePath)
		if profileCreds != nil {
			if !profileCreds.IsExpiringSoon(codexTokenRefreshThreshold) {
				if profileCreds.AccessToken != "" {
					return profileCreds.AccessToken
				}
			}
		}

		systemCreds := api.DetectCodexCredentials(m.logger)
		if shouldUseSystemCredsForProfile(profileCreds, systemCreds, profile.AccountID) {
			if err := updateProfileFromSystemCreds(profilePath, systemCreds, m.logger); err != nil {
				m.logger.Warn("failed to persist Codex profile token refresh from auth.json", "error", err, "profile", profile.Name)
			}
			if systemCreds.AccessToken != "" {
				return systemCreds.AccessToken
			}
		}

		if profileCreds != nil && profileCreds.AccessToken != "" {
			return profileCreds.AccessToken
		}
		return profile.Tokens.AccessToken
	})

	// Set credentials refresh function for proactive OAuth token refresh
	agent.SetCredentialsRefresh(func() *api.CodexCredentials {
		if isDefaultProfile {
			// Default profile reads from system credentials (~/.codex/auth.json)
			return api.DetectCodexCredentials(m.logger)
		}

		profileCreds := readCodexProfileCredentials(profilePath)
		if profileCreds != nil && !profileCreds.IsExpiringSoon(codexTokenRefreshThreshold) {
			return profileCreds
		}

		systemCreds := api.DetectCodexCredentials(m.logger)
		if shouldUseSystemCredsForProfile(profileCreds, systemCreds, profile.AccountID) {
			if err := updateProfileFromSystemCreds(profilePath, systemCreds, m.logger); err != nil {
				m.logger.Warn("failed to update Codex profile from auth.json", "error", err, "profile", profile.Name)
			}
			return systemCreds
		}

		return profileCreds
	})

	// Set notifier if available
	if m.notifier != nil {
		agent.SetNotifier(m.notifier)
	}

	// Set polling check - combines global and per-account checks
	accountID := dbAccount.ID
	agent.SetPollingCheck(func() bool {
		// Check global Codex polling first
		if m.pollingCheck != nil && !m.pollingCheck() {
			return false
		}
		// Check per-account polling
		if m.accountPollingCheck != nil && !m.accountPollingCheck(accountID) {
			return false
		}
		return true
	})

	// Create context for this agent
	agentCtx, agentCancel := context.WithCancel(m.ctx)

	instance := &CodexAgentInstance{
		Profile:     profile,
		DBAccountID: dbAccount.ID,
		Agent:       agent,
		Cancel:      agentCancel,
	}

	m.mu.Lock()
	m.instances[profile.Name] = instance
	m.mu.Unlock()

	// Start agent in goroutine
	go func() {
		defer func() {
			if r := recover(); r != nil {
				m.logger.Error("Codex agent panicked", "profile", profile.Name, "panic", r)
			}
		}()

		if err := agent.Run(agentCtx); err != nil && agentCtx.Err() == nil {
			m.logger.Error("Codex agent error", "profile", profile.Name, "error", err)
		}

		// Remove from instances when done
		m.mu.Lock()
		delete(m.instances, profile.Name)
		m.mu.Unlock()
	}()

	return nil
}

// startDefaultAgent starts an agent using current system credentials (no saved profile).
func (m *CodexAgentManager) startDefaultAgent() error {
	creds := api.DetectCodexCredentials(m.logger)
	if creds == nil || (creds.AccessToken == "" && creds.APIKey == "") {
		return fmt.Errorf("no Codex credentials found")
	}

	// Use "default" as the profile name for unsaved credentials
	profile := CodexProfile{
		Name:      "default",
		AccountID: creds.AccountID,
		UserID:    creds.UserID,
	}
	profile.Tokens.AccessToken = creds.AccessToken
	profile.Tokens.RefreshToken = creds.RefreshToken
	profile.Tokens.IDToken = creds.IDToken
	profile.APIKey = creds.APIKey

	return m.startAgentForProfile(profile)
}

// profileScanner periodically checks for new or modified profiles.
func (m *CodexAgentManager) profileScanner() {
	ticker := time.NewTicker(m.scanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.scanForProfileChanges()
		}
	}
}

// scanForProfileChanges checks for new or modified profiles.
func (m *CodexAgentManager) scanForProfileChanges() {
	if m.profilesDir == "" {
		return
	}

	entries, err := os.ReadDir(m.profilesDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		profileName := strings.TrimSuffix(entry.Name(), ".json")
		info, err := entry.Info()
		if err != nil {
			continue
		}

		lastMod, known := m.lastScanProfiles[profileName]
		if !known || info.ModTime().After(lastMod) {
			// New or modified profile
			profilePath := filepath.Join(m.profilesDir, entry.Name())

			if known {
				// Profile was modified - stop old agent and restart
				m.logger.Info("profile modified, restarting agent", "profile", profileName)
				m.stopAgent(profileName)
			} else {
				m.logger.Info("new profile detected", "profile", profileName)
			}

			if err := m.loadAndStartProfile(profilePath); err != nil {
				m.logger.Warn("failed to start agent for profile", "profile", profileName, "error", err)
			}

			m.lastScanProfiles[profileName] = info.ModTime()
		}
	}

	// Check for deleted profiles
	m.mu.RLock()
	profileNames := make([]string, 0, len(m.instances))
	for name := range m.instances {
		if name != "default" { // Don't stop default agent based on file deletion
			profileNames = append(profileNames, name)
		}
	}
	m.mu.RUnlock()

	for _, name := range profileNames {
		profilePath := filepath.Join(m.profilesDir, name+".json")
		if _, err := os.Stat(profilePath); os.IsNotExist(err) {
			m.logger.Info("profile deleted, stopping agent", "profile", name)
			m.stopAgent(name)
			delete(m.lastScanProfiles, name)
			// Mark the provider account as deleted in the database
			if m.store != nil {
				if err := m.store.MarkProviderAccountDeleted("codex", name); err != nil {
					m.logger.Warn("failed to mark provider account deleted", "profile", name, "error", err)
				}
			}
		}
	}
}

// markOrphanedAccountsDeleted finds provider_accounts rows that have no
// corresponding running agent and marks them as deleted. This handles the case
// where profile files were deleted while the daemon was not running - the scanner
// never saw the deletion, so the DB rows remain active.
func (m *CodexAgentManager) markOrphanedAccountsDeleted() {
	accounts, err := m.store.QueryActiveProviderAccounts("codex")
	if err != nil {
		m.logger.Warn("failed to query active accounts for orphan check", "error", err)
		return
	}

	m.mu.RLock()
	running := make(map[string]bool, len(m.instances))
	for name := range m.instances {
		running[name] = true
	}
	m.mu.RUnlock()

	for _, acc := range accounts {
		if running[acc.Name] {
			continue // has a running agent, not orphaned
		}
		// Only mark as deleted if the profile file also doesn't exist on disk.
		// A profile file might exist but failed to load (bad JSON, etc.) -
		// in that case we don't want to mark it deleted prematurely.
		if m.profilesDir != "" {
			profilePath := filepath.Join(m.profilesDir, acc.Name+".json")
			if _, statErr := os.Stat(profilePath); statErr == nil {
				continue // file exists on disk, just failed to load - don't mark deleted
			}
		}
		m.logger.Info("marking orphaned Codex account as deleted", "name", acc.Name, "id", acc.ID)
		if err := m.store.MarkProviderAccountDeleted("codex", acc.Name); err != nil {
			m.logger.Warn("failed to mark orphaned account deleted", "name", acc.Name, "error", err)
		}
	}
}

// stopAgent stops a specific profile's agent.
func (m *CodexAgentManager) stopAgent(profileName string) {
	m.mu.Lock()
	instance, exists := m.instances[profileName]
	if exists {
		delete(m.instances, profileName)
	}
	m.mu.Unlock()

	if exists && instance.Cancel != nil {
		instance.Cancel()
	}
}

// stopAllAgents stops all running agents.
func (m *CodexAgentManager) stopAllAgents() {
	m.mu.Lock()
	instances := make([]*CodexAgentInstance, 0, len(m.instances))
	for _, inst := range m.instances {
		instances = append(instances, inst)
	}
	m.instances = make(map[string]*CodexAgentInstance)
	m.mu.Unlock()

	for _, inst := range instances {
		if inst.Cancel != nil {
			inst.Cancel()
		}
	}
}

// GetRunningProfiles returns information about currently running profile agents.
func (m *CodexAgentManager) GetRunningProfiles() []map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]map[string]interface{}, 0, len(m.instances))
	for _, inst := range m.instances {
		result = append(result, map[string]interface{}{
			"name":          inst.Profile.Name,
			"db_account_id": inst.DBAccountID,
			"codex_account": inst.Profile.AccountID,
		})
	}
	return result
}
