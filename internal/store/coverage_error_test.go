package store

import (
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

// closedStore returns a Store whose underlying DB connection is closed,
// so every SQL call returns an error. This lets us exercise error-handling
// branches that are unreachable with a healthy database.
func closedStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	s.db.Close()
	return s
}

// --- store.go error paths ---

func TestClosedDB_InsertSnapshot(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	now := time.Now().UTC()
	_, err := s.InsertSnapshot(&api.Snapshot{
		CapturedAt: now,
		Sub:        api.QuotaInfo{RenewsAt: now},
		Search:     api.QuotaInfo{RenewsAt: now},
		ToolCall:   api.QuotaInfo{RenewsAt: now},
	})
	if err == nil {
		t.Fatal("Expected error from InsertSnapshot on closed DB")
	}
}

func TestClosedDB_QueryLatest(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.QueryLatest()
	if err == nil {
		t.Fatal("Expected error from QueryLatest on closed DB")
	}
}

func TestClosedDB_QueryRange(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	now := time.Now().UTC()
	_, err := s.QueryRange(now.Add(-time.Hour), now)
	if err == nil {
		t.Fatal("Expected error from QueryRange on closed DB")
	}
}

func TestClosedDB_CreateSession(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	err := s.CreateSession("s1", time.Now().UTC(), 60, "synthetic")
	if err == nil {
		t.Fatal("Expected error from CreateSession on closed DB")
	}
}

func TestClosedDB_CloseOrphanedSessions(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.CloseOrphanedSessions()
	if err == nil {
		t.Fatal("Expected error from CloseOrphanedSessions on closed DB")
	}
}

func TestClosedDB_CloseSession(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	err := s.CloseSession("s1", time.Now().UTC())
	if err == nil {
		t.Fatal("Expected error from CloseSession on closed DB")
	}
}

func TestClosedDB_UpdateSessionMaxRequests(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	err := s.UpdateSessionMaxRequests("s1", 1.0, 1.0, 1.0)
	if err == nil {
		t.Fatal("Expected error from UpdateSessionMaxRequests on closed DB")
	}
}

func TestClosedDB_IncrementSnapshotCount(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	err := s.IncrementSnapshotCount("s1")
	if err == nil {
		t.Fatal("Expected error from IncrementSnapshotCount on closed DB")
	}
}

func TestClosedDB_QueryActiveSession(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.QueryActiveSession()
	if err == nil {
		t.Fatal("Expected error from QueryActiveSession on closed DB")
	}
}

func TestClosedDB_QuerySessionHistory(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.QuerySessionHistory("synthetic")
	if err == nil {
		t.Fatal("Expected error from QuerySessionHistory on closed DB")
	}
}

func TestClosedDB_CreateCycle(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.CreateCycle("sub", time.Now().UTC(), time.Now().UTC())
	if err == nil {
		t.Fatal("Expected error from CreateCycle on closed DB")
	}
}

func TestClosedDB_CloseCycle(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	err := s.CloseCycle("sub", time.Now().UTC(), 0.5, 0.1)
	if err == nil {
		t.Fatal("Expected error from CloseCycle on closed DB")
	}
}

func TestClosedDB_UpdateCycle(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	err := s.UpdateCycle("sub", 0.5, 0.1)
	if err == nil {
		t.Fatal("Expected error from UpdateCycle on closed DB")
	}
}

func TestClosedDB_QueryActiveCycle(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.QueryActiveCycle("sub")
	if err == nil {
		t.Fatal("Expected error from QueryActiveCycle on closed DB")
	}
}

func TestClosedDB_QueryCycleHistory(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.QueryCycleHistory("sub")
	if err == nil {
		t.Fatal("Expected error from QueryCycleHistory on closed DB")
	}
}

func TestClosedDB_QueryCyclesSince(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.QueryCyclesSince("sub", time.Now().UTC())
	if err == nil {
		t.Fatal("Expected error from QueryCyclesSince on closed DB")
	}
}

func TestClosedDB_GetSetting(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.GetSetting("key")
	if err == nil {
		t.Fatal("Expected error from GetSetting on closed DB")
	}
}

func TestClosedDB_SetSetting(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	err := s.SetSetting("key", "val")
	if err == nil {
		t.Fatal("Expected error from SetSetting on closed DB")
	}
}

func TestClosedDB_SaveAuthToken(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	err := s.SaveAuthToken("token", time.Now().UTC())
	if err == nil {
		t.Fatal("Expected error from SaveAuthToken on closed DB")
	}
}

func TestClosedDB_GetAuthTokenExpiry(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, _, err := s.GetAuthTokenExpiry("token")
	if err == nil {
		t.Fatal("Expected error from GetAuthTokenExpiry on closed DB")
	}
}

func TestClosedDB_DeleteAuthToken(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	err := s.DeleteAuthToken("token")
	if err == nil {
		t.Fatal("Expected error from DeleteAuthToken on closed DB")
	}
}

func TestClosedDB_CleanExpiredAuthTokens(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	err := s.CleanExpiredAuthTokens()
	if err == nil {
		t.Fatal("Expected error from CleanExpiredAuthTokens on closed DB")
	}
}

func TestClosedDB_GetUser(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.GetUser("admin")
	if err == nil {
		t.Fatal("Expected error from GetUser on closed DB")
	}
}

func TestClosedDB_UpsertUser(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	err := s.UpsertUser("admin", "hash")
	if err == nil {
		t.Fatal("Expected error from UpsertUser on closed DB")
	}
}

func TestClosedDB_DeleteAllAuthTokens(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	err := s.DeleteAllAuthTokens()
	if err == nil {
		t.Fatal("Expected error from DeleteAllAuthTokens on closed DB")
	}
}

func TestClosedDB_UpsertNotificationLog(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	err := s.UpsertNotificationLog("anthropic", "five_hour", "threshold", 0.8)
	if err == nil {
		t.Fatal("Expected error from UpsertNotificationLog on closed DB")
	}
}

func TestClosedDB_GetLastNotification(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, _, err := s.GetLastNotification("anthropic", "five_hour", "threshold")
	if err == nil {
		t.Fatal("Expected error from GetLastNotification on closed DB")
	}
}

func TestClosedDB_ClearNotificationLog(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	err := s.ClearNotificationLog("anthropic", "five_hour")
	if err == nil {
		t.Fatal("Expected error from ClearNotificationLog on closed DB")
	}
}

func TestClosedDB_SavePushSubscription(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	err := s.SavePushSubscription("https://push.example.com", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "AAAAAAAAAAAAAAAAAAAAAAAAA=")
	if err == nil {
		t.Fatal("Expected error from SavePushSubscription on closed DB")
	}
}

func TestClosedDB_DeletePushSubscription(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	err := s.DeletePushSubscription("https://push.example.com")
	if err == nil {
		t.Fatal("Expected error from DeletePushSubscription on closed DB")
	}
}

func TestClosedDB_GetPushSubscriptions(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.GetPushSubscriptions()
	if err == nil {
		t.Fatal("Expected error from GetPushSubscriptions on closed DB")
	}
}

// --- Anthropic error paths ---

func TestClosedDB_InsertAnthropicSnapshot(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.InsertAnthropicSnapshot(&api.AnthropicSnapshot{
		CapturedAt: time.Now().UTC(),
		RawJSON:    "{}",
	})
	if err == nil {
		t.Fatal("Expected error from InsertAnthropicSnapshot on closed DB")
	}
}

func TestClosedDB_QueryLatestAnthropic(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.QueryLatestAnthropic()
	if err == nil {
		t.Fatal("Expected error from QueryLatestAnthropic on closed DB")
	}
}

func TestClosedDB_QueryAnthropicRange(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	now := time.Now().UTC()
	_, err := s.QueryAnthropicRange(now.Add(-time.Hour), now)
	if err == nil {
		t.Fatal("Expected error from QueryAnthropicRange on closed DB")
	}
}

func TestClosedDB_CreateAnthropicCycle(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.CreateAnthropicCycle("five_hour", time.Now().UTC(), nil)
	if err == nil {
		t.Fatal("Expected error from CreateAnthropicCycle on closed DB")
	}
}

func TestClosedDB_CloseAnthropicCycle(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	err := s.CloseAnthropicCycle("five_hour", time.Now().UTC(), 0.5, 0.1)
	if err == nil {
		t.Fatal("Expected error from CloseAnthropicCycle on closed DB")
	}
}

func TestClosedDB_UpdateAnthropicCycle(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	err := s.UpdateAnthropicCycle("five_hour", 0.5, 0.1)
	if err == nil {
		t.Fatal("Expected error from UpdateAnthropicCycle on closed DB")
	}
}

func TestClosedDB_QueryActiveAnthropicCycle(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.QueryActiveAnthropicCycle("five_hour")
	if err == nil {
		t.Fatal("Expected error from QueryActiveAnthropicCycle on closed DB")
	}
}

func TestClosedDB_QueryAnthropicCycleHistory(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.QueryAnthropicCycleHistory("five_hour")
	if err == nil {
		t.Fatal("Expected error from QueryAnthropicCycleHistory on closed DB")
	}
}

func TestClosedDB_QueryAnthropicCyclesSince(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.QueryAnthropicCyclesSince("five_hour", time.Now().UTC())
	if err == nil {
		t.Fatal("Expected error from QueryAnthropicCyclesSince on closed DB")
	}
}

func TestClosedDB_QueryAnthropicUtilizationSeries(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.QueryAnthropicUtilizationSeries("five_hour", time.Now().UTC())
	if err == nil {
		t.Fatal("Expected error from QueryAnthropicUtilizationSeries on closed DB")
	}
}

func TestClosedDB_QueryAnthropicCycleOverview(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.QueryAnthropicCycleOverview("sub", 10)
	if err == nil {
		t.Fatal("Expected error from QueryAnthropicCycleOverview on closed DB")
	}
}

func TestClosedDB_QueryAllAnthropicQuotaNames(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.QueryAllAnthropicQuotaNames()
	if err == nil {
		t.Fatal("Expected error from QueryAllAnthropicQuotaNames on closed DB")
	}
}

// --- Codex error paths ---

func TestClosedDB_InsertCodexSnapshot(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.InsertCodexSnapshot(&api.CodexSnapshot{
		CapturedAt: time.Now().UTC(),
		RawJSON:    "{}",
	})
	if err == nil {
		t.Fatal("Expected error from InsertCodexSnapshot on closed DB")
	}
}

func TestClosedDB_QueryLatestCodex(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.QueryLatestCodex(DefaultCodexAccountID)
	if err == nil {
		t.Fatal("Expected error from QueryLatestCodex on closed DB")
	}
}

func TestClosedDB_QueryCodexRange(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	now := time.Now().UTC()
	_, err := s.QueryCodexRange(DefaultCodexAccountID, now.Add(-time.Hour), now)
	if err == nil {
		t.Fatal("Expected error from QueryCodexRange on closed DB")
	}
}

func TestClosedDB_CreateCodexCycle(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.CreateCodexCycle(DefaultCodexAccountID, "five_hour", time.Now().UTC(), nil)
	if err == nil {
		t.Fatal("Expected error from CreateCodexCycle on closed DB")
	}
}

func TestClosedDB_CloseCodexCycle(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	err := s.CloseCodexCycle(DefaultCodexAccountID, "five_hour", time.Now().UTC(), 0.5, 0.1)
	if err == nil {
		t.Fatal("Expected error from CloseCodexCycle on closed DB")
	}
}

func TestClosedDB_UpdateCodexCycle(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	err := s.UpdateCodexCycle(DefaultCodexAccountID, "five_hour", 0.5, 0.1)
	if err == nil {
		t.Fatal("Expected error from UpdateCodexCycle on closed DB")
	}
}

func TestClosedDB_QueryActiveCodexCycle(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.QueryActiveCodexCycle(DefaultCodexAccountID, "five_hour")
	if err == nil {
		t.Fatal("Expected error from QueryActiveCodexCycle on closed DB")
	}
}

func TestClosedDB_QueryCodexCycleHistory(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.QueryCodexCycleHistory(DefaultCodexAccountID, "five_hour")
	if err == nil {
		t.Fatal("Expected error from QueryCodexCycleHistory on closed DB")
	}
}

func TestClosedDB_QueryCodexCyclesSince(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.QueryCodexCyclesSince(DefaultCodexAccountID, "five_hour", time.Now().UTC())
	if err == nil {
		t.Fatal("Expected error from QueryCodexCyclesSince on closed DB")
	}
}

func TestClosedDB_QueryCodexUtilizationSeries(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.QueryCodexUtilizationSeries(DefaultCodexAccountID, "five_hour", time.Now().UTC())
	if err == nil {
		t.Fatal("Expected error from QueryCodexUtilizationSeries on closed DB")
	}
}

func TestClosedDB_QueryCodexCycleOverview(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.QueryCodexCycleOverview(DefaultCodexAccountID, "sub", 10)
	if err == nil {
		t.Fatal("Expected error from QueryCodexCycleOverview on closed DB")
	}
}

func TestClosedDB_QueryAllCodexQuotaNames(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.QueryAllCodexQuotaNames()
	if err == nil {
		t.Fatal("Expected error from QueryAllCodexQuotaNames on closed DB")
	}
}

func TestClosedDB_UpdateCodexCycleResetsAt(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	now := time.Now().UTC()
	err := s.UpdateCodexCycleResetsAt(DefaultCodexAccountID, "five_hour", &now)
	if err == nil {
		t.Fatal("Expected error from UpdateCodexCycleResetsAt on closed DB")
	}
}

// --- Copilot error paths ---

func TestClosedDB_InsertCopilotSnapshot(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.InsertCopilotSnapshot(&api.CopilotSnapshot{
		CapturedAt: time.Now().UTC(),
		RawJSON:    "{}",
	})
	if err == nil {
		t.Fatal("Expected error from InsertCopilotSnapshot on closed DB")
	}
}

func TestClosedDB_QueryLatestCopilot(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.QueryLatestCopilot()
	if err == nil {
		t.Fatal("Expected error from QueryLatestCopilot on closed DB")
	}
}

func TestClosedDB_QueryCopilotRange(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	now := time.Now().UTC()
	_, err := s.QueryCopilotRange(now.Add(-time.Hour), now)
	if err == nil {
		t.Fatal("Expected error from QueryCopilotRange on closed DB")
	}
}

func TestClosedDB_CreateCopilotCycle(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.CreateCopilotCycle("premium_interactions", time.Now().UTC(), nil)
	if err == nil {
		t.Fatal("Expected error from CreateCopilotCycle on closed DB")
	}
}

func TestClosedDB_CloseCopilotCycle(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	err := s.CloseCopilotCycle("premium_interactions", time.Now().UTC(), 500, 100)
	if err == nil {
		t.Fatal("Expected error from CloseCopilotCycle on closed DB")
	}
}

func TestClosedDB_UpdateCopilotCycle(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	err := s.UpdateCopilotCycle("premium_interactions", 500, 100)
	if err == nil {
		t.Fatal("Expected error from UpdateCopilotCycle on closed DB")
	}
}

func TestClosedDB_QueryActiveCopilotCycle(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.QueryActiveCopilotCycle("premium_interactions")
	if err == nil {
		t.Fatal("Expected error from QueryActiveCopilotCycle on closed DB")
	}
}

func TestClosedDB_QueryCopilotCycleHistory(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.QueryCopilotCycleHistory("premium_interactions")
	if err == nil {
		t.Fatal("Expected error from QueryCopilotCycleHistory on closed DB")
	}
}

func TestClosedDB_QueryCopilotCyclesSince(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.QueryCopilotCyclesSince("premium_interactions", time.Now().UTC())
	if err == nil {
		t.Fatal("Expected error from QueryCopilotCyclesSince on closed DB")
	}
}

func TestClosedDB_QueryCopilotUsageSeries(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.QueryCopilotUsageSeries("premium_interactions", time.Now().UTC())
	if err == nil {
		t.Fatal("Expected error from QueryCopilotUsageSeries on closed DB")
	}
}

func TestClosedDB_QueryCopilotCycleOverview(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.QueryCopilotCycleOverview("sub", 10)
	if err == nil {
		t.Fatal("Expected error from QueryCopilotCycleOverview on closed DB")
	}
}

func TestClosedDB_QueryAllCopilotQuotaNames(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.QueryAllCopilotQuotaNames()
	if err == nil {
		t.Fatal("Expected error from QueryAllCopilotQuotaNames on closed DB")
	}
}

// --- Zai error paths ---

func TestClosedDB_InsertZaiSnapshot(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.InsertZaiSnapshot(&api.ZaiSnapshot{
		CapturedAt: time.Now().UTC(),
	})
	if err == nil {
		t.Fatal("Expected error from InsertZaiSnapshot on closed DB")
	}
}

func TestClosedDB_QueryLatestZai(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.QueryLatestZai()
	if err == nil {
		t.Fatal("Expected error from QueryLatestZai on closed DB")
	}
}

func TestClosedDB_QueryZaiRange(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	now := time.Now().UTC()
	_, err := s.QueryZaiRange(now.Add(-time.Hour), now)
	if err == nil {
		t.Fatal("Expected error from QueryZaiRange on closed DB")
	}
}

func TestClosedDB_CreateZaiCycle(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.CreateZaiCycle("tokens", time.Now().UTC(), nil)
	if err == nil {
		t.Fatal("Expected error from CreateZaiCycle on closed DB")
	}
}

func TestClosedDB_CloseZaiCycle(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	err := s.CloseZaiCycle("tokens", time.Now().UTC(), 500000, 200000)
	if err == nil {
		t.Fatal("Expected error from CloseZaiCycle on closed DB")
	}
}

func TestClosedDB_UpdateZaiCycle(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	err := s.UpdateZaiCycle("tokens", 500000, 200000)
	if err == nil {
		t.Fatal("Expected error from UpdateZaiCycle on closed DB")
	}
}

func TestClosedDB_QueryActiveZaiCycle(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.QueryActiveZaiCycle("tokens")
	if err == nil {
		t.Fatal("Expected error from QueryActiveZaiCycle on closed DB")
	}
}

func TestClosedDB_InsertZaiHourlyUsage(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	err := s.InsertZaiHourlyUsage("2026-03-04 12:00", 10, 500, 2, 3, 1)
	if err == nil {
		t.Fatal("Expected error from InsertZaiHourlyUsage on closed DB")
	}
}

func TestClosedDB_QueryZaiHourlyUsage(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	now := time.Now().UTC()
	_, err := s.QueryZaiHourlyUsage(now.Add(-time.Hour), now)
	if err == nil {
		t.Fatal("Expected error from QueryZaiHourlyUsage on closed DB")
	}
}

func TestClosedDB_QueryZaiCycleHistory(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.QueryZaiCycleHistory("tokens")
	if err == nil {
		t.Fatal("Expected error from QueryZaiCycleHistory on closed DB")
	}
}

func TestClosedDB_QueryZaiCycleOverview(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.QueryZaiCycleOverview("tokens", 10)
	if err == nil {
		t.Fatal("Expected error from QueryZaiCycleOverview on closed DB")
	}
}

func TestClosedDB_QueryZaiCyclesSince(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.QueryZaiCyclesSince("tokens", time.Now().UTC())
	if err == nil {
		t.Fatal("Expected error from QueryZaiCyclesSince on closed DB")
	}
}

// --- Antigravity error paths ---

func TestClosedDB_InsertAntigravitySnapshot(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.InsertAntigravitySnapshot(&api.AntigravitySnapshot{
		CapturedAt: time.Now().UTC(),
		RawJSON:    "{}",
	})
	if err == nil {
		t.Fatal("Expected error from InsertAntigravitySnapshot on closed DB")
	}
}

func TestClosedDB_QueryLatestAntigravity(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.QueryLatestAntigravity()
	if err == nil {
		t.Fatal("Expected error from QueryLatestAntigravity on closed DB")
	}
}

func TestClosedDB_QueryAntigravityRange(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	now := time.Now().UTC()
	_, err := s.QueryAntigravityRange(now.Add(-time.Hour), now)
	if err == nil {
		t.Fatal("Expected error from QueryAntigravityRange on closed DB")
	}
}

func TestClosedDB_CreateAntigravityCycle(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.CreateAntigravityCycle("model-a", time.Now().UTC(), nil)
	if err == nil {
		t.Fatal("Expected error from CreateAntigravityCycle on closed DB")
	}
}

func TestClosedDB_CloseAntigravityCycle(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	err := s.CloseAntigravityCycle("model-a", time.Now().UTC(), 0.5, 0.1)
	if err == nil {
		t.Fatal("Expected error from CloseAntigravityCycle on closed DB")
	}
}

func TestClosedDB_UpdateAntigravityCycle(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	err := s.UpdateAntigravityCycle("model-a", 0.5, 0.1)
	if err == nil {
		t.Fatal("Expected error from UpdateAntigravityCycle on closed DB")
	}
}

func TestClosedDB_QueryActiveAntigravityCycle(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.QueryActiveAntigravityCycle("model-a")
	if err == nil {
		t.Fatal("Expected error from QueryActiveAntigravityCycle on closed DB")
	}
}

func TestClosedDB_QueryAntigravityCycleHistory(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.QueryAntigravityCycleHistory("model-a")
	if err == nil {
		t.Fatal("Expected error from QueryAntigravityCycleHistory on closed DB")
	}
}

func TestClosedDB_QueryAntigravityUsageSeries(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.QueryAntigravityUsageSeries("model-a", time.Now().UTC())
	if err == nil {
		t.Fatal("Expected error from QueryAntigravityUsageSeries on closed DB")
	}
}

func TestClosedDB_QueryAntigravityCycleOverview(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.QueryAntigravityCycleOverview("model-a", 10)
	if err == nil {
		t.Fatal("Expected error from QueryAntigravityCycleOverview on closed DB")
	}
}

func TestClosedDB_QueryAllAntigravityModelIDs(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.QueryAllAntigravityModelIDs()
	if err == nil {
		t.Fatal("Expected error from QueryAllAntigravityModelIDs on closed DB")
	}
}

func TestClosedDB_QueryAntigravityModelIDsForGroup(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.QueryAntigravityModelIDsForGroup("group-a")
	if err == nil {
		t.Fatal("Expected error from QueryAntigravityModelIDsForGroup on closed DB")
	}
}

func TestClosedDB_QueryAntigravitySnapshotAtOrBefore(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.QueryAntigravitySnapshotAtOrBefore(time.Now().UTC())
	if err == nil {
		t.Fatal("Expected error from QueryAntigravitySnapshotAtOrBefore on closed DB")
	}
}

// --- Synthetic CycleOverview ---

func TestClosedDB_QuerySyntheticCycleOverview(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	_, err := s.QuerySyntheticCycleOverview("sub", 10)
	if err == nil {
		t.Fatal("Expected error from QuerySyntheticCycleOverview on closed DB")
	}
}

// --- Migration error paths ---

func TestClosedDB_MigrateSessionsToUsageBased(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	err := s.MigrateSessionsToUsageBased(30 * time.Minute)
	if err == nil {
		t.Fatal("Expected error from MigrateSessionsToUsageBased on closed DB")
	}
}

func TestClosedDB_QueryAntigravityHistory(t *testing.T) {
	t.Parallel()
	s := closedStore(t)
	now := time.Now().UTC()
	_, err := s.QueryAntigravityHistory(now.Add(-time.Hour), now)
	if err == nil {
		t.Fatal("Expected error from QueryAntigravityHistory on closed DB")
	}
}
