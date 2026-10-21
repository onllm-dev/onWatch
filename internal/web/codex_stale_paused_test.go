package web

import (
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/agent"
	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

// A polling-disabled or long-paused account has a very old snapshot: the codex
// payload must carry the age so the UI can render its existing stale treatment
// instead of looking live (issue #147 review, item 3).
func TestHandler_CodexCurrent_EmitsAgeForStaleSnapshot(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	resetsAt := time.Now().Add(2 * time.Hour)
	capturedAt := time.Now().Add(-48 * time.Hour)
	if _, err := s.InsertCodexSnapshot(&api.CodexSnapshot{
		CapturedAt: capturedAt,
		AccountID:  DefaultCodexAccountID,
		PlanType:   "plus",
		Quotas: []api.CodexQuota{
			{Name: "seven_day", Utilization: 40, ResetsAt: &resetsAt},
		},
	}); err != nil {
		t.Fatalf("InsertCodexSnapshot: %v", err)
	}

	h := NewHandler(s, nil, nil, nil, createTestConfigWithCodex())
	result := h.buildCodexCurrent(DefaultCodexAccountID)

	if result["isStale"] != true {
		t.Errorf("isStale = %v, want true for a 48h-old snapshot", result["isStale"])
	}
	ageSeconds, ok := result["ageSeconds"].(int64)
	if !ok {
		t.Fatalf("ageSeconds missing or wrong type: %T", result["ageSeconds"])
	}
	if ageSeconds < 47*3600 || ageSeconds > 49*3600 {
		t.Errorf("ageSeconds = %d, want ~48h", ageSeconds)
	}
}

// A fresh snapshot must not be flagged stale and must still carry age.
func TestHandler_CodexCurrent_FreshSnapshotNotStale(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	if _, err := s.InsertCodexSnapshot(&api.CodexSnapshot{
		CapturedAt: time.Now(),
		AccountID:  DefaultCodexAccountID,
		PlanType:   "plus",
		Quotas:     []api.CodexQuota{{Name: "five_hour", Utilization: 5}},
	}); err != nil {
		t.Fatalf("InsertCodexSnapshot: %v", err)
	}

	h := NewHandler(s, nil, nil, nil, createTestConfigWithCodex())
	result := h.buildCodexCurrent(DefaultCodexAccountID)
	if result["isStale"] == true {
		t.Error("a fresh snapshot must not be marked stale")
	}
}

// A paused account at 100% utilization must still surface as critical: the
// pause is a warning floor, not a ceiling (issue #147 review, item 6).
func TestBuildMenubarSnapshot_PausedCodexKeepsCriticalSeverity(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	resetsAt := time.Now().Add(3 * time.Hour)
	if _, err := s.InsertCodexSnapshot(&api.CodexSnapshot{
		CapturedAt: time.Now(),
		AccountID:  DefaultCodexAccountID,
		PlanType:   "plus",
		Quotas: []api.CodexQuota{
			{Name: "seven_day", Utilization: 100, ResetsAt: &resetsAt},
		},
	}); err != nil {
		t.Fatalf("InsertCodexSnapshot: %v", err)
	}

	h := NewHandler(s, nil, nil, nil, createTestConfigWithCodex())
	h.SetCodexPauseReporter(&fakeCodexPauseReporter{paused: map[int64]agent.CodexPauseState{
		DefaultCodexAccountID: {Paused: true, Since: time.Now().Add(-time.Hour)},
	}})

	snapshot, err := h.BuildMenubarSnapshot()
	if err != nil {
		t.Fatalf("BuildMenubarSnapshot: %v", err)
	}
	card := findMenubarProviderCard(t, snapshot, "codex:1")
	if card.Status != "critical" {
		t.Errorf("paused codex card at 100%% status = %q, want critical", card.Status)
	}
	if card.ConnectionStatus != "paused" {
		t.Errorf("connection_status = %q, want paused (tooltip still signals re-auth)", card.ConnectionStatus)
	}
}

// A paused account below the critical threshold stays warning (the pause floor).
func TestBuildMenubarSnapshot_PausedCodexLowUsageIsWarning(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	resetsAt := time.Now().Add(3 * time.Hour)
	if _, err := s.InsertCodexSnapshot(&api.CodexSnapshot{
		CapturedAt: time.Now(),
		AccountID:  DefaultCodexAccountID,
		PlanType:   "plus",
		Quotas: []api.CodexQuota{
			{Name: "five_hour", Utilization: 5, ResetsAt: &resetsAt},
		},
	}); err != nil {
		t.Fatalf("InsertCodexSnapshot: %v", err)
	}

	h := NewHandler(s, nil, nil, nil, createTestConfigWithCodex())
	h.SetCodexPauseReporter(&fakeCodexPauseReporter{paused: map[int64]agent.CodexPauseState{
		DefaultCodexAccountID: {Paused: true, Since: time.Now().Add(-time.Hour)},
	}})

	snapshot, err := h.BuildMenubarSnapshot()
	if err != nil {
		t.Fatalf("BuildMenubarSnapshot: %v", err)
	}
	card := findMenubarProviderCard(t, snapshot, "codex:1")
	if card.Status != "warning" {
		t.Errorf("paused low-usage codex card status = %q, want warning", card.Status)
	}
}

// A polling-disabled (not paused) account serves a very old snapshot: the
// menubar card must flag it stale in connection_status, but staleness is shown
// only through the age label. Severity must come from usage alone, so a
// low-usage card stays healthy rather than turning amber just because of age.
func TestBuildMenubarSnapshot_CodexStaleAccountFlagged(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	resetsAt := time.Now().Add(3 * time.Hour)
	if _, err := s.InsertCodexSnapshot(&api.CodexSnapshot{
		CapturedAt: time.Now().Add(-30 * 24 * time.Hour),
		AccountID:  DefaultCodexAccountID,
		PlanType:   "plus",
		Quotas: []api.CodexQuota{
			{Name: "five_hour", Utilization: 5, ResetsAt: &resetsAt},
		},
	}); err != nil {
		t.Fatalf("InsertCodexSnapshot: %v", err)
	}

	// No pause reporter: this account is simply not being polled.
	h := NewHandler(s, nil, nil, nil, createTestConfigWithCodex())
	snapshot, err := h.BuildMenubarSnapshot()
	if err != nil {
		t.Fatalf("BuildMenubarSnapshot: %v", err)
	}
	card := findMenubarProviderCard(t, snapshot, "codex:1")
	if card.ConnectionStatus != "stale" {
		t.Errorf("stale codex card connection_status = %q, want stale", card.ConnectionStatus)
	}
	// Severity comes from usage alone: 5% stays healthy even when the snapshot
	// is a month old. Staleness is carried by the age label.
	if card.Status != "healthy" {
		t.Errorf("stale codex card status = %q, want healthy (age alone must not raise severity)", card.Status)
	}
}

// The codex summary path must not render a past reset as "Resetting...".
func TestBuildCodexSummaryResponse_PastResetNotResetting(t *testing.T) {
	t.Parallel()
	resetsAt := time.Now().Add(-90 * time.Minute)
	summary := &tracker.CodexSummary{
		QuotaName:      "five_hour",
		ResetsAt:       &resetsAt,
		TimeUntilReset: -90 * time.Minute,
	}

	result := buildCodexSummaryResponse(summary)
	if result["timeUntilReset"] == "Resetting..." {
		t.Fatalf("timeUntilReset = %q, want a non-negative label", result["timeUntilReset"])
	}
	if result["timeUntilReset"] != "reset due" {
		t.Errorf("timeUntilReset = %v, want \"reset due\"", result["timeUntilReset"])
	}
}
