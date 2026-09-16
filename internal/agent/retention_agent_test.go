package agent

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

func newRetentionTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestRetentionAgent_NoOpWhenPolicyUnset asserts the agent is inert on an
// install that has not chosen a retention period. This is the property that
// makes the feature safe to ship to existing users.
func TestRetentionAgent_NoOpWhenPolicyUnset(t *testing.T) {
	t.Parallel()
	s := newRetentionTestStore(t)

	a := NewRetentionAgent(s, store.RetentionPolicy{}, slog.Default())
	res, err := a.RunOnce(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if res.TotalScrubbed() != 0 || res.TotalDeleted() != 0 {
		t.Errorf("agent acted with no policy set: scrubbed=%d deleted=%d", res.TotalScrubbed(), res.TotalDeleted())
	}
}

// TestRetentionAgent_ReadsPolicyFromSettingsEachPass asserts a change made in
// the dashboard takes effect without restarting the daemon. A privacy control
// that needs a restart is not much of a control.
func TestRetentionAgent_ReadsPolicyFromSettingsEachPass(t *testing.T) {
	t.Parallel()
	s := newRetentionTestStore(t)

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	if _, err := s.InsertGrokSnapshot(&api.GrokSnapshot{
		CapturedAt:  now.AddDate(0, 0, -100),
		AccountID:   1,
		Email:       "dev@example.com",
		TeamID:      "team-42",
		LoginMethod: "sso",
		RawJSON:     `{"email":"dev@example.com"}`,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	a := NewRetentionAgent(s, store.RetentionPolicy{}, slog.Default())

	// Nothing set yet.
	res, err := a.RunOnce(context.Background(), now)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if res.TotalScrubbed() != 0 {
		t.Fatalf("scrubbed %d rows before a policy was chosen", res.TotalScrubbed())
	}

	// Operator sets a 30-day scrub in the dashboard.
	if err := s.SetSetting(store.SettingRetentionScrubDays, "30"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	res, err = a.RunOnce(context.Background(), now)
	if err != nil {
		t.Fatalf("RunOnce after settings change: %v", err)
	}
	if res.TotalScrubbed() == 0 {
		t.Error("agent did not pick up the new retention setting")
	}
}

// TestRetentionAgent_EnvDefaultAppliesUntilOperatorChooses covers the
// precedence rule: the env var seeds the period for a deployment that has no
// dashboard access yet, and the dashboard wins once it has been used.
func TestRetentionAgent_EnvDefaultAppliesUntilOperatorChooses(t *testing.T) {
	t.Parallel()
	s := newRetentionTestStore(t)

	envPolicy := store.RetentionPolicy{ScrubAfter: 30 * 24 * time.Hour}
	a := NewRetentionAgent(s, envPolicy, slog.Default())

	if got := a.EffectivePolicy().ScrubAfter; got != 30*24*time.Hour {
		t.Errorf("ScrubAfter = %v, want the env default to apply while the setting is unset", got)
	}

	if err := s.SetSetting(store.SettingRetentionScrubDays, "90"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if got := a.EffectivePolicy().ScrubAfter; got != 90*24*time.Hour {
		t.Errorf("ScrubAfter = %v, want the dashboard setting to win once chosen", got)
	}

	// An explicit 0 in the dashboard means "keep everything" and must override
	// the env default rather than falling back to it.
	if err := s.SetSetting(store.SettingRetentionScrubDays, "0"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if got := a.EffectivePolicy().ScrubAfter; got != 0 {
		t.Errorf("ScrubAfter = %v, want 0 - an explicit choice of \"keep everything\" must stick", got)
	}
}

// TestRetentionAgent_RunStopsOnContextCancel asserts the loop is well-behaved
// at shutdown.
func TestRetentionAgent_RunStopsOnContextCancel(t *testing.T) {
	t.Parallel()
	s := newRetentionTestStore(t)

	a := NewRetentionAgent(s, store.RetentionPolicy{}, slog.Default())
	a.SetInterval(10 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()

	time.Sleep(30 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil && err != context.Canceled {
			t.Errorf("Run returned %v, want nil or context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}
