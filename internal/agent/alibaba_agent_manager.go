package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/notify"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

// defaultAlibabaRegion is the console region used when an account has none.
const defaultAlibabaRegion = "ap-southeast-1"

// AlibabaAgentInstance represents a running agent for a specific Alibaba account.
type AlibabaAgentInstance struct {
	DBAccountID int64
	AccountName string
	Agent       *AlibabaAgent
	Cancel      context.CancelFunc
}

// AlibabaAgentManager manages multiple AlibabaAgent instances for multi-account
// support. It is entirely DB-driven and supports hot-reload via Reload() when
// accounts are added/removed via the UI.
type AlibabaAgentManager struct {
	store               *store.Store
	tracker             *tracker.AlibabaTracker
	interval            time.Duration
	logger              *slog.Logger
	notifier            *notify.NotificationEngine
	pollingCheck        func() bool                // Global Alibaba polling check
	accountPollingCheck func(accountID int64) bool // Per-account polling check
	region              string                     // Default console region
	clientOpts          []api.AlibabaOption        // Extra client options (testing)

	mu        sync.RWMutex
	instances map[int64]*AlibabaAgentInstance // db account id -> instance
	wg        sync.WaitGroup                  // tracks running agent goroutines
	reloadMu  sync.Mutex                      // prevents concurrent Reload() calls
	ctx       context.Context
	cancel    context.CancelFunc
}

// NewAlibabaAgentManager creates a new manager for multi-account Alibaba polling.
func NewAlibabaAgentManager(store *store.Store, tracker *tracker.AlibabaTracker, interval time.Duration, logger *slog.Logger) *AlibabaAgentManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &AlibabaAgentManager{
		store:     store,
		tracker:   tracker,
		interval:  interval,
		logger:    logger,
		instances: make(map[int64]*AlibabaAgentInstance),
		region:    defaultAlibabaRegion,
	}
}

// SetNotifier sets the notification engine for all agents.
func (m *AlibabaAgentManager) SetNotifier(n *notify.NotificationEngine) {
	m.notifier = n
}

// SetPollingCheck sets the global polling check function.
func (m *AlibabaAgentManager) SetPollingCheck(fn func() bool) {
	m.pollingCheck = fn
}

// SetAccountPollingCheck sets a per-account polling check function.
func (m *AlibabaAgentManager) SetAccountPollingCheck(fn func(accountID int64) bool) {
	m.accountPollingCheck = fn
}

// SetRegion sets the default console region for accounts without one.
func (m *AlibabaAgentManager) SetRegion(region string) {
	m.region = region
}

// SetClientOptions sets extra options applied to every account client. It is
// meant for tests that point the clients at a local server.
func (m *AlibabaAgentManager) SetClientOptions(opts ...api.AlibabaOption) {
	m.clientOpts = opts
}

// alibabaAccountMeta holds the JSON metadata stored in provider_accounts.metadata.
type alibabaAccountMeta struct {
	Cookies string `json:"cookies,omitempty"`
	Region  string `json:"region,omitempty"`
}

func parseAlibabaAccountMeta(raw string) alibabaAccountMeta {
	var meta alibabaAccountMeta
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &meta)
	}
	return meta
}

// Run starts the manager, loads all accounts, and starts agents.
func (m *AlibabaAgentManager) Run(ctx context.Context) error {
	m.ctx, m.cancel = context.WithCancel(ctx)
	defer m.cancel()

	m.logger.Info("Alibaba agent manager started", "interval", m.interval)

	if err := m.loadAndStartAccounts(); err != nil {
		m.logger.Error("failed to load initial Alibaba accounts", "error", err)
	}

	<-m.ctx.Done()
	m.stopAllAgents()
	return nil
}

// Reload stops all agents and restarts from the current DB state.
// Called by the web handler when accounts are added/updated/deleted.
// Uses reloadMu to prevent concurrent reloads from racing.
func (m *AlibabaAgentManager) Reload() {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()

	if m.ctx == nil || m.ctx.Err() != nil {
		return
	}
	m.logger.Info("Alibaba agent manager reloading accounts")
	m.stopAllAgents()
	if err := m.loadAndStartAccounts(); err != nil {
		m.logger.Error("failed to reload Alibaba accounts", "error", err)
	}
}

// loadAndStartAccounts reads active Alibaba accounts from the DB and starts an
// agent for each account that has cookies.
func (m *AlibabaAgentManager) loadAndStartAccounts() error {
	accounts, err := m.store.QueryActiveProviderAccounts("alibaba")
	if err != nil {
		return fmt.Errorf("failed to query Alibaba accounts: %w", err)
	}

	for _, acc := range accounts {
		meta := parseAlibabaAccountMeta(acc.Metadata)
		if strings.TrimSpace(meta.Cookies) == "" {
			m.logger.Debug("skipping Alibaba account without cookies", "account", acc.Name, "id", acc.ID)
			continue
		}
		if err := m.startAgentForAccount(acc.ID, acc.Name, meta); err != nil {
			m.logger.Warn("failed to start Alibaba agent for account", "account", acc.Name, "error", err)
		}
	}

	m.mu.RLock()
	count := len(m.instances)
	m.mu.RUnlock()
	m.logger.Info("Alibaba accounts loaded", "count", count)

	return nil
}

// startAgentForAccount creates and starts an agent for a specific account.
func (m *AlibabaAgentManager) startAgentForAccount(accountID int64, name string, meta alibabaAccountMeta) error {
	m.mu.RLock()
	if _, exists := m.instances[accountID]; exists {
		m.mu.RUnlock()
		return nil // Already running
	}
	m.mu.RUnlock()

	region := strings.TrimSpace(meta.Region)
	if region == "" {
		region = m.region
	}

	opts := []api.AlibabaOption{api.WithAlibabaRegion(region), api.WithAlibabaAccountLabel(name)}
	opts = append(opts, m.clientOpts...)
	client := api.NewAlibabaClient(meta.Cookies, m.logger, opts...)
	sm := NewSessionManager(m.store, fmt.Sprintf("alibaba:%d", accountID), 5*time.Minute, m.logger)
	agent := NewAlibabaAgentWithAccount(client, m.store, m.tracker, m.interval, m.logger, sm, accountID)

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

	instance := &AlibabaAgentInstance{
		DBAccountID: accountID,
		AccountName: name,
		Agent:       agent,
		Cancel:      agentCancel,
	}

	m.mu.Lock()
	m.instances[accountID] = instance
	m.mu.Unlock()

	m.logger.Info("starting Alibaba agent for account", "account", name, "id", accountID, "region", region)

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				m.logger.Error("Alibaba agent panicked", "account", name, "panic", r)
			}
		}()
		if err := agent.Run(agentCtx); err != nil && agentCtx.Err() == nil {
			m.logger.Error("Alibaba agent error", "account", name, "error", err)
		}
	}()

	return nil
}

// stopAllAgents stops all running agents and waits for goroutines to finish.
func (m *AlibabaAgentManager) stopAllAgents() {
	m.mu.Lock()
	instances := make([]*AlibabaAgentInstance, 0, len(m.instances))
	for _, inst := range m.instances {
		instances = append(instances, inst)
	}
	m.instances = make(map[int64]*AlibabaAgentInstance)
	m.mu.Unlock()

	for _, inst := range instances {
		if inst.Cancel != nil {
			inst.Cancel()
		}
	}
	m.wg.Wait()
}

// GetRunningAccounts returns information about currently running account agents.
func (m *AlibabaAgentManager) GetRunningAccounts() []map[string]interface{} {
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
