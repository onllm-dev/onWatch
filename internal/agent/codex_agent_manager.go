package agent

import (
	"context"
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

	home, _ := os.UserHomeDir()
	profilesDir := ""
	if home != "" {
		profilesDir = filepath.Join(home, ".onwatch", "codex-profiles")
	}

	return &CodexAgentManager{
		store:            store,
		tracker:          tracker,
		interval:         interval,
		logger:           logger,
		instances:        make(map[string]*CodexAgentInstance),
		profilesDir:      profilesDir,
		scanInterval:     30 * time.Second, // Check for new profiles every 30 seconds
		lastScanProfiles: make(map[string]time.Time),
	}
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

	// Check if we already have this profile running
	m.mu.RLock()
	_, exists := m.instances[profile.Name]
	m.mu.RUnlock()

	if exists {
		return nil // Already running
	}

	return m.startAgentForProfile(profile)
}

// startAgentForProfile creates and starts an agent for a specific profile.
func (m *CodexAgentManager) startAgentForProfile(profile CodexProfile) error {
	// Get or create the database account ID for this profile
	dbAccount, err := m.store.GetOrCreateProviderAccount("codex", profile.Name)
	if err != nil {
		return fmt.Errorf("failed to get/create provider account: %w", err)
	}

	m.logger.Info("starting Codex agent for profile",
		"profile", profile.Name,
		"db_account_id", dbAccount.ID,
		"codex_account_id", profile.AccountID)

	// Create credentials from profile
	creds := &api.CodexCredentials{
		AccessToken:  profile.Tokens.AccessToken,
		RefreshToken: profile.Tokens.RefreshToken,
		IDToken:      profile.Tokens.IDToken,
		APIKey:       profile.APIKey,
		AccountID:    profile.AccountID,
	}

	// Create client for this profile
	client := api.NewCodexClient(creds.AccessToken, nil)

	// Create session manager for this profile
	sm := NewSessionManager(m.store, fmt.Sprintf("codex:%d", dbAccount.ID), 5*time.Minute, m.logger)

	// Create agent with account ID
	agent := NewCodexAgentWithAccount(client, m.store, m.tracker, m.interval, m.logger, sm, dbAccount.ID)

	// Set token refresh function that reads from profile
	profilePath := filepath.Join(m.profilesDir, profile.Name+".json")
	agent.SetTokenRefresh(func() string {
		// Re-read profile to get potentially updated tokens
		if data, err := os.ReadFile(profilePath); err == nil {
			var p CodexProfile
			if json.Unmarshal(data, &p) == nil {
				return p.Tokens.AccessToken
			}
		}
		return profile.Tokens.AccessToken
	})

	// Set credentials refresh function for proactive OAuth token refresh
	isDefaultProfile := profile.Name == "default"
	agent.SetCredentialsRefresh(func() *api.CodexCredentials {
		if isDefaultProfile {
			// Default profile reads from system credentials (~/.codex/auth.json)
			return api.DetectCodexCredentials(m.logger)
		}
		// Profile-specific credentials - re-read from profile file
		if data, err := os.ReadFile(profilePath); err == nil {
			var p CodexProfile
			if json.Unmarshal(data, &p) == nil {
				idToken := p.Tokens.IDToken
				expiresAt := api.ParseIDTokenExpiry(idToken)
				var expiresIn time.Duration
				if !expiresAt.IsZero() {
					expiresIn = time.Until(expiresAt)
				}
				return &api.CodexCredentials{
					AccessToken:  p.Tokens.AccessToken,
					RefreshToken: p.Tokens.RefreshToken,
					IDToken:      idToken,
					APIKey:       p.APIKey,
					AccountID:    p.AccountID,
					ExpiresAt:    expiresAt,
					ExpiresIn:    expiresIn,
				}
			}
		}
		return nil
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
