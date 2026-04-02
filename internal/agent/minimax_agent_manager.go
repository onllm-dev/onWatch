package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/notify"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

// MiniMaxAgentInstance represents a running agent for a specific MiniMax account.
type MiniMaxAgentInstance struct {
	DBAccountID int64
	AccountName string
	Agent       *MiniMaxAgent
	Cancel      context.CancelFunc
}

// MiniMaxAgentManager manages multiple MiniMaxAgent instances for multi-account support.
// Unlike CodexAgentManager, this is entirely DB-driven (no file-based profile scanning)
// and supports hot-reload via the Reload() method when accounts are added/removed via UI.
type MiniMaxAgentManager struct {
	store               *store.Store
	tracker             *tracker.MiniMaxTracker
	interval            time.Duration
	logger              *slog.Logger
	notifier            *notify.NotificationEngine
	pollingCheck        func() bool                // Global MiniMax polling check
	accountPollingCheck func(accountID int64) bool // Per-account polling check
	region              string                      // Default region for API base URL

	mu        sync.RWMutex
	instances map[int64]*MiniMaxAgentInstance // db account id -> instance
	wg        sync.WaitGroup                  // tracks running agent goroutines
	reloadMu  sync.Mutex                      // prevents concurrent Reload() calls
	ctx       context.Context
	cancel    context.CancelFunc
}

// NewMiniMaxAgentManager creates a new manager for multi-account MiniMax polling.
func NewMiniMaxAgentManager(store *store.Store, tracker *tracker.MiniMaxTracker, interval time.Duration, logger *slog.Logger) *MiniMaxAgentManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &MiniMaxAgentManager{
		store:     store,
		tracker:   tracker,
		interval:  interval,
		logger:    logger,
		instances: make(map[int64]*MiniMaxAgentInstance),
		region:    "global",
	}
}

// SetNotifier sets the notification engine for all agents.
func (m *MiniMaxAgentManager) SetNotifier(n *notify.NotificationEngine) {
	m.notifier = n
}

// SetPollingCheck sets the global polling check function.
func (m *MiniMaxAgentManager) SetPollingCheck(fn func() bool) {
	m.pollingCheck = fn
}

// SetAccountPollingCheck sets a per-account polling check function.
func (m *MiniMaxAgentManager) SetAccountPollingCheck(fn func(accountID int64) bool) {
	m.accountPollingCheck = fn
}

// SetRegion sets the default region for MiniMax API endpoints.
func (m *MiniMaxAgentManager) SetRegion(region string) {
	m.region = region
}

// minimaxAccountMeta holds the JSON metadata stored in provider_accounts.metadata.
type minimaxAccountMeta struct {
	APIKey string `json:"api_key,omitempty"`
	Region string `json:"region,omitempty"`
}

func parseMinimaxAccountMeta(raw string) minimaxAccountMeta {
	var meta minimaxAccountMeta
	if raw != "" {
		json.Unmarshal([]byte(raw), &meta)
	}
	return meta
}

func minimaxBaseURL(region string) string {
	if region == "cn" {
		return "https://www.minimaxi.com/v1/api/openplatform/coding_plan/remains"
	}
	return "https://api.minimax.io/v1/api/openplatform/coding_plan/remains"
}

// Run starts the manager, loads all accounts, and starts agents.
func (m *MiniMaxAgentManager) Run(ctx context.Context) error {
	m.ctx, m.cancel = context.WithCancel(ctx)
	defer m.cancel()

	m.logger.Info("MiniMax agent manager started", "interval", m.interval)

	if err := m.loadAndStartAccounts(); err != nil {
		m.logger.Error("failed to load initial MiniMax accounts", "error", err)
	}

	// Wait for context cancellation
	<-m.ctx.Done()
	m.stopAllAgents()
	return nil
}

// Reload stops all agents and restarts from the current DB state.
// Called by the web handler when accounts are added/updated/deleted.
// Uses reloadMu to prevent concurrent reloads from racing.
func (m *MiniMaxAgentManager) Reload() {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()

	if m.ctx == nil || m.ctx.Err() != nil {
		return
	}
	m.logger.Info("MiniMax agent manager reloading accounts")
	m.stopAllAgents()
	if err := m.loadAndStartAccounts(); err != nil {
		m.logger.Error("failed to reload MiniMax accounts", "error", err)
	}
}

// loadAndStartAccounts reads active MiniMax accounts from the DB and starts an agent for each.
func (m *MiniMaxAgentManager) loadAndStartAccounts() error {
	accounts, err := m.store.QueryActiveProviderAccounts("minimax")
	if err != nil {
		return fmt.Errorf("failed to query MiniMax accounts: %w", err)
	}

	for _, acc := range accounts {
		meta := parseMinimaxAccountMeta(acc.Metadata)
		if meta.APIKey == "" {
			m.logger.Debug("skipping MiniMax account without API key", "account", acc.Name, "id", acc.ID)
			continue
		}
		if err := m.startAgentForAccount(acc.ID, acc.Name, meta); err != nil {
			m.logger.Warn("failed to start MiniMax agent for account", "account", acc.Name, "error", err)
		}
	}

	m.mu.RLock()
	count := len(m.instances)
	m.mu.RUnlock()
	m.logger.Info("MiniMax accounts loaded", "count", count)

	return nil
}

// startAgentForAccount creates and starts an agent for a specific account.
func (m *MiniMaxAgentManager) startAgentForAccount(accountID int64, name string, meta minimaxAccountMeta) error {
	m.mu.RLock()
	if _, exists := m.instances[accountID]; exists {
		m.mu.RUnlock()
		return nil // Already running
	}
	m.mu.RUnlock()

	region := meta.Region
	if region == "" {
		region = m.region
	}
	baseURL := minimaxBaseURL(region)

	client := api.NewMiniMaxClient(meta.APIKey, m.logger, api.WithMiniMaxBaseURL(baseURL))
	sm := NewSessionManager(m.store, fmt.Sprintf("minimax:%d", accountID), 5*time.Minute, m.logger)
	agent := NewMiniMaxAgentWithAccount(client, m.store, m.tracker, m.interval, m.logger, sm, accountID)

	if m.notifier != nil {
		agent.SetNotifier(m.notifier)
	}

	// Combine global + per-account polling checks
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

	instance := &MiniMaxAgentInstance{
		DBAccountID: accountID,
		AccountName: name,
		Agent:       agent,
		Cancel:      agentCancel,
	}

	m.mu.Lock()
	m.instances[accountID] = instance
	m.mu.Unlock()

	m.logger.Info("starting MiniMax agent for account", "account", name, "id", accountID, "region", region)

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				m.logger.Error("MiniMax agent panicked", "account", name, "panic", r)
			}
		}()
		if err := agent.Run(agentCtx); err != nil && agentCtx.Err() == nil {
			m.logger.Error("MiniMax agent error", "account", name, "error", err)
		}
	}()

	return nil
}

// stopAllAgents stops all running agents and waits for goroutines to finish.
func (m *MiniMaxAgentManager) stopAllAgents() {
	m.mu.Lock()
	instances := make([]*MiniMaxAgentInstance, 0, len(m.instances))
	for _, inst := range m.instances {
		instances = append(instances, inst)
	}
	m.instances = make(map[int64]*MiniMaxAgentInstance)
	m.mu.Unlock()

	for _, inst := range instances {
		if inst.Cancel != nil {
			inst.Cancel()
		}
	}
	m.wg.Wait()
}

// GetRunningAccounts returns information about currently running account agents.
func (m *MiniMaxAgentManager) GetRunningAccounts() []map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]map[string]interface{}, 0, len(m.instances))
	for _, inst := range m.instances {
		result = append(result, map[string]interface{}{
			"name":          inst.AccountName,
			"db_account_id": inst.DBAccountID,
		})
	}
	return result
}
