package testutil

import (
	"testing"
	"time"
)

func TestDiscardLogger_ReturnsLogger(t *testing.T) {
	logger := DiscardLogger()
	if logger == nil {
		t.Fatal("expected non-nil logger")
	}
	logger.Info("discarded log entry")
}

func TestInMemoryStore_CreatesEmptyQueryableStore(t *testing.T) {
	s := InMemoryStore(t)
	latest, err := s.QueryLatest()
	if err != nil {
		t.Fatalf("query latest synthetic: %v", err)
	}
	if latest != nil {
		t.Fatalf("expected empty store to have no latest synthetic snapshot, got %+v", latest)
	}
}

func TestInMemoryStoreWithSnapshots_PopulatesAllProviders(t *testing.T) {
	s := InMemoryStoreWithSnapshots(t, 3, 2, 2, 2)

	now := time.Now().UTC()
	start := now.Add(-10 * time.Minute)
	end := now.Add(10 * time.Minute)

	synthetic, err := s.QueryRange(start, end)
	if err != nil {
		t.Fatalf("query synthetic range: %v", err)
	}
	if len(synthetic) != 3 {
		t.Fatalf("expected 3 synthetic snapshots, got %d", len(synthetic))
	}
	if synthetic[0].Sub.Requests != 100 || synthetic[2].Sub.Requests != 120 {
		t.Fatalf("unexpected synthetic values: first=%v last=%v", synthetic[0].Sub.Requests, synthetic[2].Sub.Requests)
	}

	zai, err := s.QueryZaiRange(start, end)
	if err != nil {
		t.Fatalf("query zai range: %v", err)
	}
	if len(zai) != 2 {
		t.Fatalf("expected 2 zai snapshots, got %d", len(zai))
	}
	if zai[0].TimeCurrentValue != 10 || zai[1].TokensCurrentValue != 15000000 {
		t.Fatalf("unexpected zai values: first time=%v last tokens=%v", zai[0].TimeCurrentValue, zai[1].TokensCurrentValue)
	}

	anthropic, err := s.QueryAnthropicRange(start, end)
	if err != nil {
		t.Fatalf("query anthropic range: %v", err)
	}
	if len(anthropic) != 2 {
		t.Fatalf("expected 2 anthropic snapshots, got %d", len(anthropic))
	}
	if len(anthropic[0].Quotas) != 2 {
		t.Fatalf("expected anthropic quotas to be loaded, got %d", len(anthropic[0].Quotas))
	}

	copilot, err := s.QueryCopilotRange(start, end)
	if err != nil {
		t.Fatalf("query copilot range: %v", err)
	}
	if len(copilot) != 2 {
		t.Fatalf("expected 2 copilot snapshots, got %d", len(copilot))
	}
	if copilot[0].CopilotPlan != "individual_pro" {
		t.Fatalf("expected copilot plan individual_pro, got %q", copilot[0].CopilotPlan)
	}
	if len(copilot[0].Quotas) != 2 {
		t.Fatalf("expected copilot quotas to be loaded, got %d", len(copilot[0].Quotas))
	}
}

func TestInMemoryStoreWithSnapshots_AllowsOmittingOptionalCopilotSnapshots(t *testing.T) {
	s := InMemoryStoreWithSnapshots(t, 1, 1, 1)

	start := time.Now().UTC().Add(-10 * time.Minute)
	end := time.Now().UTC().Add(10 * time.Minute)
	copilot, err := s.QueryCopilotRange(start, end)
	if err != nil {
		t.Fatalf("query copilot range: %v", err)
	}
	if len(copilot) != 0 {
		t.Fatalf("expected no copilot snapshots by default, got %d", len(copilot))
	}
}

func TestTestConfig_UsesBaseURLAndTestingDefaults(t *testing.T) {
	cfg := TestConfig("http://example.test")

	if cfg.ZaiBaseURL != "http://example.test" {
		t.Fatalf("expected ZaiBaseURL to use provided base URL, got %q", cfg.ZaiBaseURL)
	}
	if cfg.AdminUser != "admin" || cfg.AdminPass != "testpass" {
		t.Fatalf("unexpected admin credentials: %q/%q", cfg.AdminUser, cfg.AdminPass)
	}
	if !cfg.DebugMode || !cfg.TestMode {
		t.Fatalf("expected debug and test mode enabled, got debug=%v test=%v", cfg.DebugMode, cfg.TestMode)
	}
}

func TestTestHandler_ReturnsConfiguredHandlerAndStore(t *testing.T) {
	h, s := TestHandler(t)
	if h == nil {
		t.Fatal("expected handler")
	}
	if s == nil {
		t.Fatal("expected store")
	}

	latest, err := s.QueryLatest()
	if err != nil {
		t.Fatalf("query latest on handler store: %v", err)
	}
	if latest != nil {
		t.Fatalf("expected fresh handler store to be empty, got %+v", latest)
	}
}
