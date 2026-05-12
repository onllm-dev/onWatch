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

// GeminiProfile represents a saved Gemini credential profile.
type GeminiProfile struct {
	Name      string    `json:"name"`
	ProjectID string    `json:"project_id"`
	UserID    string    `json:"user_id,omitempty"`
	SavedAt   time.Time `json:"saved_at"`
	Tokens    struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token,omitempty"`
	} `json:"tokens"`
}

// GeminiAgentInstance represents a running agent for a specific profile.
type GeminiAgentInstance struct {
	Profile     GeminiProfile
	DBAccountID int64
	Agent       *GeminiAgent
	Cancel      context.CancelFunc
}

// GeminiAgentManager manages multiple GeminiAgent instances for multi-account support.
type GeminiAgentManager struct {
	store               *store.Store
	tracker             *tracker.GeminiTracker
	interval            time.Duration
	logger              *slog.Logger
	notifier            *notify.NotificationEngine
	pollingCheck        func() bool
	accountPollingCheck func(accountID int64) bool

	mu        sync.RWMutex
	instances map[string]*GeminiAgentInstance
	ctx       context.Context
	cancel    context.CancelFunc

	profilesDir      string
	scanInterval     time.Duration
	lastScanProfiles map[string]time.Time
}

// NewGeminiAgentManager creates a new manager for multi-account Gemini polling.
func NewGeminiAgentManager(store *store.Store, tracker *tracker.GeminiTracker, interval time.Duration, logger *slog.Logger) *GeminiAgentManager {
	if logger == nil {
		logger = slog.Default()
	}

	return &GeminiAgentManager{
		store:            store,
		tracker:          tracker,
		interval:         interval,
		logger:           logger,
		instances:        make(map[string]*GeminiAgentInstance),
		scanInterval:     30 * time.Second,
		lastScanProfiles: make(map[string]time.Time),
	}
}

// SetProfilesDir sets the directory to scan for Gemini profile files.
func (m *GeminiAgentManager) SetProfilesDir(dir string) {
	m.profilesDir = dir
}

// SetNotifier sets the notification engine for all agents.
func (m *GeminiAgentManager) SetNotifier(n *notify.NotificationEngine) {
	m.notifier = n
}

// SetPollingCheck sets the global polling check function for all agents.
func (m *GeminiAgentManager) SetPollingCheck(fn func() bool) {
	m.pollingCheck = fn
}

// SetAccountPollingCheck sets a per-account polling check function.
func (m *GeminiAgentManager) SetAccountPollingCheck(fn func(accountID int64) bool) {
	m.accountPollingCheck = fn
}

// Run starts the manager and all profile agents.
func (m *GeminiAgentManager) Run(ctx context.Context) error {
	m.ctx, m.cancel = context.WithCancel(ctx)
	defer m.cancel()

	m.logger.Info("Gemini agent manager started", "interval", m.interval)

	if err := m.loadAndStartProfiles(); err != nil {
		m.logger.Error("failed to load initial Gemini profiles", "error", err)
	}

	m.mu.RLock()
	hasProfiles := len(m.instances) > 0
	m.mu.RUnlock()

	if !hasProfiles {
		m.logger.Info("no saved Gemini profiles found, using current credentials as default")
		if err := m.startDefaultAgent(); err != nil {
			m.logger.Warn("failed to start default Gemini agent", "error", err)
		}
	}

	m.markOrphanedAccountsDeleted()

	if merged, err := m.store.DeduplicateProviderAccounts("gemini"); err != nil {
		m.logger.Warn("failed to deduplicate Gemini accounts", "error", err)
	} else if merged > 0 {
		m.logger.Info("deduplicated Gemini accounts", "merged", merged)
	}

	go m.profileScanner()

	<-m.ctx.Done()
	m.stopAllAgents()
	return nil
}

func (m *GeminiAgentManager) loadAndStartProfiles() error {
	if m.profilesDir == "" {
		return fmt.Errorf("profiles directory not set")
	}

	entries, err := os.ReadDir(m.profilesDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to read Gemini profiles directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		profilePath := filepath.Join(m.profilesDir, entry.Name())
		if err := m.loadAndStartProfile(profilePath); err != nil {
			m.logger.Warn("failed to load Gemini profile", "path", profilePath, "error", err)
			continue
		}

		if info, err := entry.Info(); err == nil {
			profileName := strings.TrimSuffix(entry.Name(), ".json")
			m.lastScanProfiles[profileName] = info.ModTime()
		}
	}

	return nil
}

func (m *GeminiAgentManager) loadAndStartProfile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var profile GeminiProfile
	if err := json.Unmarshal(data, &profile); err != nil {
		return err
	}

	// Derive name from filename if not set
	if profile.Name == "" {
		base := filepath.Base(path)
		profile.Name = strings.TrimSuffix(base, ".json")
	}

	if profile.UserID == "" {
		profile.UserID = api.ParseGeminiIDTokenUserID(profile.Tokens.IDToken)
	}

	// Check if we already have this profile running
	m.mu.RLock()
	_, exists := m.instances[profile.Name]
	m.mu.RUnlock()

	if exists {
		return nil
	}

	return m.startAgentForProfile(profile)
}

func geminiCredentialsFromProfile(profile GeminiProfile) *api.GeminiCredentials {
	idToken := strings.TrimSpace(profile.Tokens.IDToken)
	userID := strings.TrimSpace(profile.UserID)
	if userID == "" {
		userID = api.ParseGeminiIDTokenUserID(idToken)
	}

	return &api.GeminiCredentials{
		AccessToken:  strings.TrimSpace(profile.Tokens.AccessToken),
		RefreshToken: strings.TrimSpace(profile.Tokens.RefreshToken),
		IDToken:      idToken,
		UserID:       userID,
	}
}

func readGeminiProfileCredentials(profilePath string) *api.GeminiCredentials {
	data, err := os.ReadFile(profilePath)
	if err != nil {
		return nil
	}

	var profile GeminiProfile
	if err := json.Unmarshal(data, &profile); err != nil {
		return nil
	}

	return geminiCredentialsFromProfile(profile)
}

func geminiCompositeExternalID(projectID, userID string) string {
	if strings.TrimSpace(projectID) == "" {
		return userID
	}
	creds := &api.GeminiCredentials{UserID: userID}
	return creds.CompositeExternalID(projectID)
}

func geminiProfileCompositeExternalID(profile GeminiProfile) string {
	userID := strings.TrimSpace(profile.UserID)
	if userID == "" {
		userID = api.ParseGeminiIDTokenUserID(profile.Tokens.IDToken)
	}
	return geminiCompositeExternalID(profile.ProjectID, userID)
}

func shouldUseSystemCredsForGeminiProfile(profileCreds, systemCreds *api.GeminiCredentials, expectedProjectID, expectedUserID string) bool {
	if systemCreds == nil {
		return false
	}

	if profileCreds == nil {
		return systemCreds.AccessToken != "" || systemCreds.RefreshToken != ""
	}

	// If system account doesn't match profile account identity, don't use it
	systemUserID := strings.TrimSpace(systemCreds.UserID)
	if systemUserID == "" {
		systemUserID = api.ParseGeminiIDTokenUserID(systemCreds.IDToken)
	}

	if expectedUserID != "" && systemUserID != "" && expectedUserID != systemUserID {
		return false
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

func updateGeminiProfileFromSystemCreds(profilePath string, creds *api.GeminiCredentials, logger *slog.Logger) error {
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

	var profile GeminiProfile
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

	logger.Info("updated Gemini profile tokens from oauth_creds.json", "path", profilePath)
	return nil
}

func saveGeminiTokensToProfile(profilePath, accessToken, refreshToken, idToken string, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}

	data, err := os.ReadFile(profilePath)
	if err != nil {
		return fmt.Errorf("read profile: %w", err)
	}

	var profile GeminiProfile
	if err := json.Unmarshal(data, &profile); err != nil {
		return fmt.Errorf("parse profile: %w", err)
	}

	if accessToken != "" {
		profile.Tokens.AccessToken = accessToken
	}
	if refreshToken != "" {
		profile.Tokens.RefreshToken = refreshToken
	}
	if idToken != "" {
		profile.Tokens.IDToken = idToken
		if uid := api.ParseGeminiIDTokenUserID(idToken); uid != "" {
			profile.UserID = uid
		}
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

	logger.Info("saved refreshed Gemini tokens to profile", "path", profilePath)
	return nil
}

func (m *GeminiAgentManager) startAgentForProfile(profile GeminiProfile) error {
	externalID := geminiProfileCompositeExternalID(profile)
	if externalID == "" {
		externalID = profile.ProjectID
	}

	dbAccount, err := m.store.GetOrCreateProviderAccountByExternalID("gemini", profile.Name, externalID)
	if err != nil {
		return fmt.Errorf("failed to get/create Gemini provider account: %w", err)
	}

	m.logger.Info("starting Gemini agent for profile",
		"profile", profile.Name,
		"db_account_id", dbAccount.ID,
		"project_id", profile.ProjectID)

	creds := geminiCredentialsFromProfile(profile)
	client := api.NewGeminiClient(creds.AccessToken, nil)
	if profile.ProjectID != "" {
		client.SetProjectID(profile.ProjectID)
	}

	sm := NewSessionManager(m.store, fmt.Sprintf("gemini:%d", dbAccount.ID), 5*time.Minute, m.logger)
	agent := NewGeminiAgentWithAccount(client, m.store, m.tracker, m.interval, m.logger, sm, dbAccount.ID)

	profilePath := filepath.Join(m.profilesDir, profile.Name+".json")
	isDefaultProfile := profile.Name == "default"

	agent.SetCredentialsRefresh(func() *api.GeminiCredentials {
		if isDefaultProfile {
			return api.DetectGeminiCredentials(m.logger, m.store)
		}

		profileCreds := readGeminiProfileCredentials(profilePath)
		systemCreds := api.DetectGeminiCredentials(m.logger, m.store)

		if shouldUseSystemCredsForGeminiProfile(profileCreds, systemCreds, profile.ProjectID, profile.UserID) {
			if err := updateGeminiProfileFromSystemCreds(profilePath, systemCreds, m.logger); err != nil {
				m.logger.Warn("failed to update Gemini profile from oauth_creds.json", "error", err, "profile", profile.Name)
			}
			return systemCreds
		}

		return profileCreds
	})

	agent.SetTokenSave(func(accessToken, refreshToken string, expiresIn int) error {
		// Google Gemini OAuth doesn't usually provide id_token on refresh unless
		// specifically requested and it's not always rotated. We'll handle it if it comes.
		// For now, RefreshGeminiToken doesn't return IDToken in the same way Codex does.
		// Wait, GeminiOAuthTokenResponse DOES have IDToken.

		if isDefaultProfile {
			// Save to both file and DB for default
			if err := api.WriteGeminiCredentials(accessToken, expiresIn); err != nil {
				m.logger.Debug("Failed to save default Gemini credentials to file", "error", err)
			}
			expiresAt := time.Now().Add(time.Duration(expiresIn) * time.Second).UnixMilli()
			return m.store.SaveGeminiTokens(accessToken, refreshToken, expiresAt)
		}

		// Named profiles: save refreshed tokens to the profile file only.
		// We don't have idToken here yet from the SetTokenSave signature.
		// Let's check GeminiAgent's SetTokenSave signature.
		if err := saveGeminiTokensToProfile(profilePath, accessToken, refreshToken, "", m.logger); err != nil {
			return err
		}

		if info, statErr := os.Stat(profilePath); statErr == nil {
			m.mu.Lock()
			m.lastScanProfiles[profile.Name] = info.ModTime()
			m.mu.Unlock()
		}
		return nil
	})

	agent.SetClientCredentials(api.DetectGeminiClientCredentials())

	if m.notifier != nil {
		agent.SetNotifier(m.notifier)
	}

	accountID := dbAccount.ID
	agent.SetPollingCheck(func() bool {
		if m.pollingCheck != nil && !m.pollingCheck() {
			return false
		}
		if m.accountPollingCheck != nil && !m.accountPollingCheck(accountID) {
			return false
		}
		return true
	})

	agentCtx, agentCancel := context.WithCancel(m.ctx)
	instance := &GeminiAgentInstance{
		Profile:     profile,
		DBAccountID: dbAccount.ID,
		Agent:       agent,
		Cancel:      agentCancel,
	}

	m.mu.Lock()
	m.instances[profile.Name] = instance
	m.mu.Unlock()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				m.logger.Error("Gemini agent panicked", "profile", profile.Name, "panic", r)
			}
		}()

		if err := agent.Run(agentCtx); err != nil && agentCtx.Err() == nil {
			m.logger.Error("Gemini agent error", "profile", profile.Name, "error", err)
		}

		m.mu.Lock()
		delete(m.instances, profile.Name)
		m.mu.Unlock()
	}()

	return nil
}

func (m *GeminiAgentManager) startDefaultAgent() error {
	creds := api.DetectGeminiCredentials(m.logger, m.store)
	if creds == nil || (creds.AccessToken == "" && creds.RefreshToken == "") {
		return fmt.Errorf("no Gemini credentials found")
	}

	profile := GeminiProfile{
		Name: "default",
	}
	profile.Tokens.AccessToken = creds.AccessToken
	profile.Tokens.RefreshToken = creds.RefreshToken

	return m.startAgentForProfile(profile)
}

func (m *GeminiAgentManager) profileScanner() {
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

func (m *GeminiAgentManager) scanForProfileChanges() {
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
			profilePath := filepath.Join(m.profilesDir, entry.Name())
			if known {
				m.logger.Info("Gemini profile modified, restarting agent", "profile", profileName)
				m.stopAgent(profileName)
			} else {
				m.logger.Info("new Gemini profile detected", "profile", profileName)
			}

			if err := m.loadAndStartProfile(profilePath); err != nil {
				m.logger.Warn("failed to start agent for Gemini profile", "profile", profileName, "error", err)
			}
			m.lastScanProfiles[profileName] = info.ModTime()
		}
	}

	m.mu.RLock()
	profileNames := make([]string, 0, len(m.instances))
	for name := range m.instances {
		if name != "default" {
			profileNames = append(profileNames, name)
		}
	}
	m.mu.RUnlock()

	for _, name := range profileNames {
		profilePath := filepath.Join(m.profilesDir, name+".json")
		if _, err := os.Stat(profilePath); os.IsNotExist(err) {
			m.logger.Info("Gemini profile deleted, stopping agent", "profile", name)
			m.stopAgent(name)
			delete(m.lastScanProfiles, name)
			if m.store != nil {
				if err := m.store.MarkProviderAccountDeleted("gemini", name); err != nil {
					m.logger.Warn("failed to mark Gemini provider account deleted", "profile", name, "error", err)
				}
			}
		}
	}
}

func (m *GeminiAgentManager) markOrphanedAccountsDeleted() {
	accounts, err := m.store.QueryActiveProviderAccounts("gemini")
	if err != nil {
		m.logger.Warn("failed to query active Gemini accounts for orphan check", "error", err)
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
			continue
		}
		if m.profilesDir != "" {
			profilePath := filepath.Join(m.profilesDir, acc.Name+".json")
			if _, statErr := os.Stat(profilePath); statErr == nil {
				continue
			}
		}
		m.logger.Info("marking orphaned Gemini account as deleted", "name", acc.Name, "id", acc.ID)
		if err := m.store.MarkProviderAccountDeleted("gemini", acc.Name); err != nil {
			m.logger.Warn("failed to mark orphaned Gemini account deleted", "name", acc.Name, "error", err)
		}
	}
}

func (m *GeminiAgentManager) stopAgent(profileName string) {
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

func (m *GeminiAgentManager) stopAllAgents() {
	m.mu.Lock()
	instances := make([]*GeminiAgentInstance, 0, len(m.instances))
	for _, inst := range m.instances {
		instances = append(instances, inst)
	}
	m.instances = make(map[string]*GeminiAgentInstance)
	m.mu.Unlock()

	for _, inst := range instances {
		if inst.Cancel != nil {
			inst.Cancel()
		}
	}
}

func (m *GeminiAgentManager) GetRunningProfiles() []map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]map[string]interface{}, 0, len(m.instances))
	for _, inst := range m.instances {
		result = append(result, map[string]interface{}{
			"name":          inst.Profile.Name,
			"db_account_id": inst.DBAccountID,
			"project_id":    inst.Profile.ProjectID,
		})
	}
	return result
}

func (m *GeminiAgentManager) SaveProfile(name string) error {
	if m.profilesDir == "" {
		return fmt.Errorf("profiles directory not set")
	}
	profilePath := filepath.Join(m.profilesDir, name+".json")
	profile := map[string]string{"name": name}
	data, err := json.Marshal(profile)
	if err != nil {
		return err
	}
	return os.WriteFile(profilePath, data, 0644)
}

func (m *GeminiAgentManager) DeleteProfile(name string) error {
	if m.profilesDir == "" {
		return fmt.Errorf("profiles directory not set")
	}
	profilePath := filepath.Join(m.profilesDir, name+".json")
	return os.Remove(profilePath)
}
