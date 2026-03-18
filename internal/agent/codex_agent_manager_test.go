package agent

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/notify"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

type codexManagerFixture struct {
	manager     *CodexAgentManager
	store       *store.Store
	logger      *slog.Logger
	profilesDir string
}

func newCodexManagerFixture(t *testing.T) *codexManagerFixture {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")

	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { str.Close() })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tr := tracker.NewCodexTracker(str, logger)
	manager := NewCodexAgentManager(str, tr, time.Hour, logger)
	manager.profilesDir = filepath.Join(home, ".onwatch", "codex-profiles")
	if err := os.MkdirAll(manager.profilesDir, 0o700); err != nil {
		t.Fatalf("mkdir profiles dir: %v", err)
	}
	manager.SetPollingCheck(func() bool { return false })
	manager.ctx, manager.cancel = context.WithCancel(context.Background())
	t.Cleanup(func() {
		manager.stopAllAgents()
		if manager.cancel != nil {
			manager.cancel()
		}
	})

	return &codexManagerFixture{
		manager:     manager,
		store:       str,
		logger:      logger,
		profilesDir: manager.profilesDir,
	}
}

func (f *codexManagerFixture) writeProfile(t *testing.T, profile CodexProfile) string {
	t.Helper()

	filename := profile.Name
	if filename == "" {
		filename = "unnamed"
	}
	path := filepath.Join(f.profilesDir, filename+".json")
	data, err := json.Marshal(profile)
	if err != nil {
		t.Fatalf("marshal profile: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write profile: %v", err)
	}
	return path
}

func (f *codexManagerFixture) instance(profile string) *CodexAgentInstance {
	f.manager.mu.RLock()
	defer f.manager.mu.RUnlock()
	return f.manager.instances[profile]
}

func TestNewCodexAgentManager_Defaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	manager := NewCodexAgentManager(nil, nil, 15*time.Second, nil)
	if manager.logger == nil {
		t.Fatal("expected default logger")
	}
	if manager.interval != 15*time.Second {
		t.Fatalf("interval = %v, want 15s", manager.interval)
	}
	// profilesDir is no longer set by constructor - must be set via SetProfilesDir
	if manager.profilesDir != "" {
		t.Fatalf("profilesDir = %q, want empty (set via SetProfilesDir)", manager.profilesDir)
	}
	if manager.scanInterval != 30*time.Second {
		t.Fatalf("scanInterval = %v, want 30s", manager.scanInterval)
	}
	if manager.instances == nil || manager.lastScanProfiles == nil {
		t.Fatal("expected manager maps to be initialized")
	}
}

func TestCodexAgentManager_LoadAndStartProfiles(t *testing.T) {
	fx := newCodexManagerFixture(t)

	work := CodexProfile{Name: "work", AccountID: "acct-work", SavedAt: time.Now().UTC()}
	work.Tokens.AccessToken = "work-token"
	personal := CodexProfile{Name: "personal", AccountID: "acct-personal", SavedAt: time.Now().UTC()}
	personal.Tokens.AccessToken = "personal-token"
	fx.writeProfile(t, work)
	fx.writeProfile(t, personal)

	if err := fx.manager.loadAndStartProfiles(); err != nil {
		t.Fatalf("loadAndStartProfiles: %v", err)
	}

	waitUntil(t, time.Second, func() bool {
		return fx.instance("work") != nil && fx.instance("personal") != nil
	}, "profiles to start")

	if len(fx.manager.GetRunningProfiles()) != 2 {
		t.Fatalf("running profiles = %d, want 2", len(fx.manager.GetRunningProfiles()))
	}
	if len(fx.manager.lastScanProfiles) != 2 {
		t.Fatalf("tracked scan profiles = %d, want 2", len(fx.manager.lastScanProfiles))
	}

	accounts, err := fx.store.QueryProviderAccounts("codex")
	if err != nil {
		t.Fatalf("QueryProviderAccounts: %v", err)
	}
	if len(accounts) != 2 {
		t.Fatalf("provider account count = %d, want 2", len(accounts))
	}
}

func TestCodexAgentManager_LoadAndStartProfile_DerivesNameAndSkipsDuplicate(t *testing.T) {
	fx := newCodexManagerFixture(t)

	path := filepath.Join(fx.profilesDir, "derived.json")
	if err := os.WriteFile(path, []byte(`{"account_id":"acct-derived","tokens":{"access_token":"first-token"}}`), 0o600); err != nil {
		t.Fatalf("write derived profile: %v", err)
	}

	if err := fx.manager.loadAndStartProfile(path); err != nil {
		t.Fatalf("loadAndStartProfile: %v", err)
	}
	waitUntil(t, time.Second, func() bool { return fx.instance("derived") != nil }, "derived profile to start")

	if got := fx.instance("derived").Profile.Name; got != "derived" {
		t.Fatalf("derived profile name = %q, want derived", got)
	}

	if err := fx.manager.loadAndStartProfile(path); err != nil {
		t.Fatalf("second loadAndStartProfile: %v", err)
	}
	if len(fx.manager.GetRunningProfiles()) != 1 {
		t.Fatalf("running profiles after duplicate load = %d, want 1", len(fx.manager.GetRunningProfiles()))
	}
}

func TestCodexAgentManager_StartAgentForProfile_WiresNotifierChecksAndRefresh(t *testing.T) {
	fx := newCodexManagerFixture(t)

	defaultAccount, err := fx.store.GetOrCreateProviderAccount("codex", "default")
	if err != nil {
		t.Fatalf("GetOrCreateProviderAccount(default): %v", err)
	}

	profile := CodexProfile{Name: "work", AccountID: "acct-work", SavedAt: time.Now().UTC()}
	profile.Tokens.AccessToken = "token-one"
	fx.writeProfile(t, profile)

	notifier := notify.New(fx.store, fx.logger)
	fx.manager.SetNotifier(notifier)

	var globalEnabled atomic.Bool
	var accountEnabled atomic.Bool
	globalEnabled.Store(true)
	accountEnabled.Store(true)
	fx.manager.SetPollingCheck(func() bool { return globalEnabled.Load() })
	fx.manager.SetAccountPollingCheck(func(accountID int64) bool {
		return accountEnabled.Load() && accountID == defaultAccount.ID
	})

	if err := fx.manager.startAgentForProfile(profile); err != nil {
		t.Fatalf("startAgentForProfile: %v", err)
	}
	waitUntil(t, time.Second, func() bool { return fx.instance("work") != nil }, "work profile to start")

	instance := fx.instance("work")
	if instance.DBAccountID != defaultAccount.ID {
		t.Fatalf("db account id = %d, want %d", instance.DBAccountID, defaultAccount.ID)
	}
	if instance.Agent.notifier != notifier {
		t.Fatal("expected notifier to be propagated to agent")
	}
	if !instance.Agent.pollingCheck() {
		t.Fatal("expected polling to be enabled when both gates allow it")
	}

	globalEnabled.Store(false)
	if instance.Agent.pollingCheck() {
		t.Fatal("expected global polling check to disable polling")
	}
	globalEnabled.Store(true)
	accountEnabled.Store(false)
	if instance.Agent.pollingCheck() {
		t.Fatal("expected per-account polling check to disable polling")
	}
	accountEnabled.Store(true)

	profile.Tokens.AccessToken = "token-two"
	fx.writeProfile(t, profile)
	if got := instance.Agent.tokenRefresh(); got != "token-two" {
		t.Fatalf("tokenRefresh() = %q, want token-two", got)
	}

	accounts, err := fx.store.QueryProviderAccounts("codex")
	if err != nil {
		t.Fatalf("QueryProviderAccounts: %v", err)
	}
	if len(accounts) != 1 || accounts[0].Name != "work" || accounts[0].ID != defaultAccount.ID {
		t.Fatalf("provider accounts = %+v, want renamed default account", accounts)
	}
}

func TestCodexAgentManager_StartDefaultAgent(t *testing.T) {
	fx := newCodexManagerFixture(t)

	authDir := filepath.Join(os.Getenv("HOME"), ".codex")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatalf("mkdir .codex: %v", err)
	}
	authPath := filepath.Join(authDir, "auth.json")
	if err := os.WriteFile(authPath, []byte(`{"tokens":{"access_token":"default-token","refresh_token":"refresh","id_token":"id","account_id":"acct-default"}}`), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	if err := fx.manager.startDefaultAgent(); err != nil {
		t.Fatalf("startDefaultAgent: %v", err)
	}
	waitUntil(t, time.Second, func() bool { return fx.instance("default") != nil }, "default profile to start")

	instance := fx.instance("default")
	if instance.Profile.Name != "default" {
		t.Fatalf("default profile name = %q, want default", instance.Profile.Name)
	}
	if instance.Profile.AccountID != "acct-default" {
		t.Fatalf("default profile account = %q, want acct-default", instance.Profile.AccountID)
	}
}

func TestCodexAgentManager_ErrorAndFallbackPaths(t *testing.T) {
	fx := newCodexManagerFixture(t)

	t.Run("loadAndStartProfiles without configured dir", func(t *testing.T) {
		fx.manager.profilesDir = ""
		err := fx.manager.loadAndStartProfiles()
		if err == nil || !strings.Contains(err.Error(), "profiles directory not set") {
			t.Fatalf("loadAndStartProfiles() error = %v", err)
		}
		fx.manager.profilesDir = fx.profilesDir
	})

	t.Run("loadAndStartProfiles missing directory returns nil", func(t *testing.T) {
		missingDir := filepath.Join(t.TempDir(), "missing")
		fx.manager.profilesDir = missingDir
		if err := fx.manager.loadAndStartProfiles(); err != nil {
			t.Fatalf("loadAndStartProfiles(missing dir) = %v", err)
		}
		fx.manager.profilesDir = fx.profilesDir
	})

	t.Run("loadAndStartProfile invalid json", func(t *testing.T) {
		badPath := filepath.Join(fx.profilesDir, "broken.json")
		if err := os.WriteFile(badPath, []byte("{invalid"), 0o600); err != nil {
			t.Fatalf("write broken profile: %v", err)
		}
		if err := fx.manager.loadAndStartProfile(badPath); err == nil {
			t.Fatal("expected invalid JSON profile to fail")
		}
	})

	t.Run("startDefaultAgent without credentials", func(t *testing.T) {
		if err := fx.manager.startDefaultAgent(); err == nil || !strings.Contains(err.Error(), "no Codex credentials found") {
			t.Fatalf("startDefaultAgent() error = %v", err)
		}
	})
}

func TestCodexAgentManager_Run_LoadsProfilesAndStopsOnCancel(t *testing.T) {
	fx := newCodexManagerFixture(t)

	profile := CodexProfile{Name: "work", AccountID: "acct-work", SavedAt: time.Now().UTC()}
	profile.Tokens.AccessToken = "run-token"
	fx.writeProfile(t, profile)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- fx.manager.Run(ctx)
	}()

	waitUntil(t, time.Second, func() bool { return fx.instance("work") != nil }, "run() to start work profile")

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit after cancellation")
	}

	if len(fx.manager.GetRunningProfiles()) != 0 {
		t.Fatalf("running profiles after Run exit = %d, want 0", len(fx.manager.GetRunningProfiles()))
	}
}

func TestCodexAgentManager_Run_UsesDefaultCredentialsWhenNoProfiles(t *testing.T) {
	fx := newCodexManagerFixture(t)

	authDir := filepath.Join(os.Getenv("HOME"), ".codex")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatalf("mkdir .codex: %v", err)
	}
	if err := os.WriteFile(filepath.Join(authDir, "auth.json"), []byte(`{"tokens":{"access_token":"default-run-token","account_id":"acct-run-default"}}`), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- fx.manager.Run(ctx)
	}()

	waitUntil(t, time.Second, func() bool { return fx.instance("default") != nil }, "default profile to start from Run")
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit after default-profile cancellation")
	}
}

func TestCodexAgentManager_ProfileScanner_DetectsNewProfiles(t *testing.T) {
	fx := newCodexManagerFixture(t)
	fx.manager.scanInterval = 10 * time.Millisecond

	done := make(chan struct{})
	go func() {
		fx.manager.profileScanner()
		close(done)
	}()

	profile := CodexProfile{Name: "scanner", AccountID: "acct-scan", SavedAt: time.Now().UTC()}
	profile.Tokens.AccessToken = "scanner-token"
	fx.writeProfile(t, profile)

	waitUntil(t, time.Second, func() bool { return fx.instance("scanner") != nil }, "scanner profile to start")

	fx.manager.cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("profileScanner did not stop after cancellation")
	}
}

func TestCodexAgentManager_ScanForProfileChanges_RestartsAndDeletesProfiles(t *testing.T) {
	fx := newCodexManagerFixture(t)

	work := CodexProfile{Name: "work", AccountID: "acct-work", SavedAt: time.Now().UTC()}
	work.Tokens.AccessToken = "old-token"
	workPath := fx.writeProfile(t, work)

	if err := fx.manager.loadAndStartProfiles(); err != nil {
		t.Fatalf("loadAndStartProfiles: %v", err)
	}
	waitUntil(t, time.Second, func() bool { return fx.instance("work") != nil }, "initial work profile to start")
	oldInstance := fx.instance("work")

	personal := CodexProfile{Name: "personal", AccountID: "acct-personal", SavedAt: time.Now().UTC()}
	personal.Tokens.AccessToken = "personal-token"
	fx.writeProfile(t, personal)
	fx.manager.scanForProfileChanges()
	waitUntil(t, time.Second, func() bool { return fx.instance("personal") != nil }, "new profile to start")

	work.Tokens.AccessToken = "new-token"
	fx.writeProfile(t, work)
	modTime := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(workPath, modTime, modTime); err != nil {
		t.Fatalf("chtimes profile: %v", err)
	}
	fx.manager.scanForProfileChanges()
	waitUntil(t, time.Second, func() bool {
		instance := fx.instance("work")
		return instance != nil && instance != oldInstance
	}, "modified work profile to restart")
	if got := fx.instance("work").Agent.tokenRefresh(); got != "new-token" {
		t.Fatalf("restarted tokenRefresh() = %q, want new-token", got)
	}

	if err := os.Remove(filepath.Join(fx.profilesDir, "personal.json")); err != nil {
		t.Fatalf("remove personal profile: %v", err)
	}
	fx.manager.scanForProfileChanges()
	waitUntil(t, time.Second, func() bool { return fx.instance("personal") == nil }, "deleted profile to stop")

	if _, exists := fx.manager.lastScanProfiles["personal"]; exists {
		t.Fatal("expected deleted profile to be removed from scan tracking")
	}
}

func TestCodexAgentManager_StopAgentAndStopAllAgents(t *testing.T) {
	fx := newCodexManagerFixture(t)

	work := CodexProfile{Name: "work", AccountID: "acct-work", SavedAt: time.Now().UTC()}
	work.Tokens.AccessToken = "work-token"
	personal := CodexProfile{Name: "personal", AccountID: "acct-personal", SavedAt: time.Now().UTC()}
	personal.Tokens.AccessToken = "personal-token"

	if err := fx.manager.startAgentForProfile(work); err != nil {
		t.Fatalf("start work profile: %v", err)
	}
	if err := fx.manager.startAgentForProfile(personal); err != nil {
		t.Fatalf("start personal profile: %v", err)
	}
	waitUntil(t, time.Second, func() bool {
		return fx.instance("work") != nil && fx.instance("personal") != nil
	}, "profiles to start for stop tests")

	fx.manager.stopAgent("work")
	waitUntil(t, time.Second, func() bool { return fx.instance("work") == nil }, "work profile to stop")
	if fx.instance("personal") == nil {
		t.Fatal("expected personal profile to keep running after stopping work")
	}

	fx.manager.stopAllAgents()
	waitUntil(t, time.Second, func() bool { return len(fx.manager.GetRunningProfiles()) == 0 }, "all profiles to stop")
}
