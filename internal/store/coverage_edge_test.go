package store

import (
	"database/sql"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

// droppedTableStore creates a store then drops a specific table to trigger
// secondary error paths (errors that occur after the first SQL succeeds).
func droppedTableStore(t *testing.T, tableToDrop string) *Store {
	t.Helper()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	_, err = s.db.Exec("DROP TABLE IF EXISTS " + tableToDrop)
	if err != nil {
		t.Fatalf("Failed to drop table %s: %v", tableToDrop, err)
	}
	return s
}

// --- Test error paths when quota value tables are dropped ---
// These trigger errors in the "load quota values" loops that happen
// after the main snapshot query succeeds.

func TestDroppedTable_QueryLatestAnthropic_QuotaValuesError(t *testing.T) {
	t.Parallel()
	s := droppedTableStore(t, "anthropic_quota_values")
	defer s.Close()

	// Insert a snapshot directly (bypassing quota values insert)
	_, err := s.db.Exec(
		`INSERT INTO anthropic_snapshots (captured_at, raw_json, quota_count) VALUES (?, '{}', 0)`,
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		t.Fatalf("Direct insert: %v", err)
	}

	_, err = s.QueryLatestAnthropic()
	if err == nil {
		t.Fatal("Expected error when anthropic_quota_values table is missing")
	}
}

func TestDroppedTable_QueryAnthropicRange_QuotaValuesError(t *testing.T) {
	t.Parallel()
	s := droppedTableStore(t, "anthropic_quota_values")
	defer s.Close()

	now := time.Now().UTC()
	_, err := s.db.Exec(
		`INSERT INTO anthropic_snapshots (captured_at, raw_json, quota_count) VALUES (?, '{}', 0)`,
		now.Format(time.RFC3339Nano),
	)
	if err != nil {
		t.Fatalf("Direct insert: %v", err)
	}

	_, err = s.QueryAnthropicRange(now.Add(-time.Hour), now.Add(time.Hour))
	if err == nil {
		t.Fatal("Expected error when anthropic_quota_values table is missing")
	}
}

func TestDroppedTable_QueryLatestCopilot_QuotaValuesError(t *testing.T) {
	t.Parallel()
	s := droppedTableStore(t, "copilot_quota_values")
	defer s.Close()

	_, err := s.db.Exec(
		`INSERT INTO copilot_snapshots (captured_at, quota_count) VALUES (?, 0)`,
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		t.Fatalf("Direct insert: %v", err)
	}

	_, err = s.QueryLatestCopilot()
	if err == nil {
		t.Fatal("Expected error when copilot_quota_values table is missing")
	}
}

func TestDroppedTable_QueryCopilotRange_QuotaValuesError(t *testing.T) {
	t.Parallel()
	s := droppedTableStore(t, "copilot_quota_values")
	defer s.Close()

	now := time.Now().UTC()
	_, err := s.db.Exec(
		`INSERT INTO copilot_snapshots (captured_at, quota_count) VALUES (?, 0)`,
		now.Format(time.RFC3339Nano),
	)
	if err != nil {
		t.Fatalf("Direct insert: %v", err)
	}

	_, err = s.QueryCopilotRange(now.Add(-time.Hour), now.Add(time.Hour))
	if err == nil {
		t.Fatal("Expected error when copilot_quota_values table is missing")
	}
}

func TestDroppedTable_QueryLatestCodex_QuotaValuesError(t *testing.T) {
	t.Parallel()
	s := droppedTableStore(t, "codex_quota_values")
	defer s.Close()

	_, err := s.db.Exec(
		`INSERT INTO codex_snapshots (captured_at, quota_count) VALUES (?, 0)`,
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		t.Fatalf("Direct insert: %v", err)
	}

	_, err = s.QueryLatestCodex(DefaultCodexAccountID)
	if err == nil {
		t.Fatal("Expected error when codex_quota_values table is missing")
	}
}

func TestDroppedTable_QueryCodexRange_QuotaValuesError(t *testing.T) {
	t.Parallel()
	s := droppedTableStore(t, "codex_quota_values")
	defer s.Close()

	now := time.Now().UTC()
	_, err := s.db.Exec(
		`INSERT INTO codex_snapshots (captured_at, quota_count) VALUES (?, 0)`,
		now.Format(time.RFC3339Nano),
	)
	if err != nil {
		t.Fatalf("Direct insert: %v", err)
	}

	_, err = s.QueryCodexRange(DefaultCodexAccountID, now.Add(-time.Hour), now.Add(time.Hour))
	if err == nil {
		t.Fatal("Expected error when codex_quota_values table is missing")
	}
}

func TestDroppedTable_QueryLatestAntigravity_ModelValuesError(t *testing.T) {
	t.Parallel()
	s := droppedTableStore(t, "antigravity_model_values")
	defer s.Close()

	_, err := s.db.Exec(
		`INSERT INTO antigravity_snapshots (captured_at, model_count) VALUES (?, 0)`,
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		t.Fatalf("Direct insert: %v", err)
	}

	_, err = s.QueryLatestAntigravity()
	if err == nil {
		t.Fatal("Expected error when antigravity_model_values table is missing")
	}
}

func TestDroppedTable_QueryAntigravityRange_ModelValuesError(t *testing.T) {
	t.Parallel()
	s := droppedTableStore(t, "antigravity_model_values")
	defer s.Close()

	now := time.Now().UTC()
	_, err := s.db.Exec(
		`INSERT INTO antigravity_snapshots (captured_at, model_count) VALUES (?, 0)`,
		now.Format(time.RFC3339Nano),
	)
	if err != nil {
		t.Fatalf("Direct insert: %v", err)
	}

	_, err = s.QueryAntigravityRange(now.Add(-time.Hour), now.Add(time.Hour))
	if err == nil {
		t.Fatal("Expected error when antigravity_model_values table is missing")
	}
}

func TestDroppedTable_QueryAntigravitySnapshotAtOrBefore_ModelValuesError(t *testing.T) {
	t.Parallel()
	s := droppedTableStore(t, "antigravity_model_values")
	defer s.Close()

	now := time.Now().UTC()
	_, err := s.db.Exec(
		`INSERT INTO antigravity_snapshots (captured_at, model_count) VALUES (?, 0)`,
		now.Format(time.RFC3339Nano),
	)
	if err != nil {
		t.Fatalf("Direct insert: %v", err)
	}

	_, err = s.QueryAntigravitySnapshotAtOrBefore(now.Add(time.Hour))
	if err == nil {
		t.Fatal("Expected error when antigravity_model_values table is missing")
	}
}

// --- Test Codex parseCodexTime error branches ---
// Insert data with invalid time formats to trigger parse errors.

func TestCodexStore_QueryCodexCycleHistory_ParseError(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	// Insert a cycle with an invalid cycle_start time format
	_, err = s.db.Exec(
		`INSERT INTO codex_reset_cycles (quota_name, cycle_start, cycle_end, peak_utilization, total_delta)
		VALUES ('five_hour', 'not-a-time', ?, 0.5, 0.1)`,
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		t.Fatalf("Direct insert: %v", err)
	}

	_, err = s.QueryCodexCycleHistory(DefaultCodexAccountID, "five_hour")
	if err == nil {
		t.Fatal("Expected error from invalid time format in cycle_start")
	}
}

func TestCodexStore_QueryCodexCyclesSince_ParseError(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	// Insert a cycle with an invalid cycle_start time format
	_, err = s.db.Exec(
		`INSERT INTO codex_reset_cycles (quota_name, cycle_start, cycle_end, peak_utilization, total_delta)
		VALUES ('five_hour', 'bad-time', ?, 0.5, 0.1)`,
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		t.Fatalf("Direct insert: %v", err)
	}

	_, err = s.QueryCodexCyclesSince(DefaultCodexAccountID, "five_hour", time.Now().Add(-time.Hour))
	if err == nil {
		t.Fatal("Expected error from invalid time format")
	}
}

func TestCodexStore_QueryActiveCodexCycle_ParseErrorCycleStart(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	// Insert an active cycle with an invalid cycle_start
	_, err = s.db.Exec(
		`INSERT INTO codex_reset_cycles (quota_name, cycle_start, peak_utilization, total_delta)
		VALUES ('five_hour', 'bad-time', 0.5, 0.1)`,
	)
	if err != nil {
		t.Fatalf("Direct insert: %v", err)
	}

	_, err = s.QueryActiveCodexCycle(DefaultCodexAccountID, "five_hour")
	if err == nil {
		t.Fatal("Expected error from invalid cycle_start time format")
	}
}

// --- Test notification log migration path ---
// Create a store with the OLD notification_log schema (without provider column),
// then verify the migration runs correctly.

func TestMigrateNotificationLogProviderScope(t *testing.T) {
	t.Parallel()
	dbPath := t.TempDir() + "/migrate_notif.db"

	// Create DB with old schema (notification_log WITHOUT provider column)
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("Failed to open db: %v", err)
	}
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA synchronous=NORMAL;",
		"PRAGMA foreign_keys=ON;",
		"PRAGMA busy_timeout=5000;",
	} {
		db.Exec(pragma)
	}

	// Create the old notification_log schema (without provider column)
	_, err = db.Exec(`
		CREATE TABLE notification_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			quota_key TEXT NOT NULL,
			notification_type TEXT NOT NULL,
			sent_at TEXT NOT NULL,
			utilization REAL,
			UNIQUE(quota_key, notification_type)
		)
	`)
	if err != nil {
		t.Fatalf("Failed to create old notification_log: %v", err)
	}

	// Insert old-format data
	_, err = db.Exec(
		`INSERT INTO notification_log (quota_key, notification_type, sent_at, utilization) VALUES ('five_hour', 'threshold', ?, 0.8)`,
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		t.Fatalf("Failed to insert old notification: %v", err)
	}

	// Also create other required tables that New() expects (minimum schema)
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL);
		CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
		CREATE TABLE IF NOT EXISTS auth_tokens (token TEXT PRIMARY KEY, expires_at TEXT NOT NULL);
		CREATE TABLE IF NOT EXISTS users (username TEXT PRIMARY KEY, password_hash TEXT NOT NULL, updated_at TEXT NOT NULL);
		CREATE TABLE IF NOT EXISTS push_subscriptions (id INTEGER PRIMARY KEY AUTOINCREMENT, endpoint TEXT NOT NULL UNIQUE, p256dh TEXT NOT NULL, auth TEXT NOT NULL, created_at TEXT NOT NULL);
	`)
	if err != nil {
		t.Fatalf("Failed to create supporting tables: %v", err)
	}

	db.Close()

	// Now open with New() which should run migration
	s, err := New(dbPath)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	defer s.Close()

	// Verify migration happened: check notification_log now has provider column
	hasProvider, err := s.tableHasColumn("notification_log", "provider")
	if err != nil {
		t.Fatalf("tableHasColumn: %v", err)
	}
	if !hasProvider {
		t.Fatal("Expected notification_log to have provider column after migration")
	}

	// Verify migrated data
	sentAt, util, err := s.GetLastNotification("legacy", "five_hour", "threshold")
	if err != nil {
		t.Fatalf("GetLastNotification: %v", err)
	}
	if sentAt.IsZero() {
		t.Fatal("Expected migrated notification data")
	}
	if util != 0.8 {
		t.Fatalf("Expected utilization 0.8, got %f", util)
	}
}

// --- Test Codex overview with cross-quota values ---

func TestCodexStore_QueryCodexCycleOverview_WithCrossQuotas(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	resetsAt := now.Add(5 * time.Hour)

	// Create cycle
	_, err = s.CreateCodexCycle(DefaultCodexAccountID, "five_hour", now, &resetsAt)
	if err != nil {
		t.Fatalf("CreateCodexCycle: %v", err)
	}

	// Insert snapshots
	for i := 0; i < 3; i++ {
		snap := &api.CodexSnapshot{
			CapturedAt: now.Add(time.Duration(i) * time.Hour),
			PlanType:   "pro",
			RawJSON:    "{}",
			Quotas: []api.CodexQuota{
				{Name: "five_hour", Utilization: float64(i) * 0.2, ResetsAt: &resetsAt, Status: "active"},
			},
		}
		_, err = s.InsertCodexSnapshot(snap)
		if err != nil {
			t.Fatalf("InsertCodexSnapshot[%d]: %v", i, err)
		}
	}

	// Close the cycle
	err = s.CloseCodexCycle(DefaultCodexAccountID, "five_hour", now.Add(5*time.Hour), 0.4, 0.4)
	if err != nil {
		t.Fatalf("CloseCodexCycle: %v", err)
	}

	overview, err := s.QueryCodexCycleOverview(DefaultCodexAccountID, "five_hour", 10)
	if err != nil {
		t.Fatalf("QueryCodexCycleOverview: %v", err)
	}
	if len(overview) == 0 {
		t.Fatal("Expected at least 1 overview row")
	}
}

// --- Test Antigravity overview: getAntigravityGroupedCrossQuotasAt fallback ---

func TestAntigravityStore_getAntigravityGroupedCrossQuotasAt_Fallback(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	resetTime := now.Add(24 * time.Hour)

	// Insert a snapshot
	snap := &api.AntigravitySnapshot{
		CapturedAt: now,
		Models: []api.AntigravityModelQuota{
			{ModelID: "model-a", Label: "Model A", RemainingFraction: 0.5, RemainingPercent: 50.0, ResetTime: &resetTime},
		},
		RawJSON: "{}",
	}
	_, err = s.InsertAntigravitySnapshot(snap)
	if err != nil {
		t.Fatalf("InsertAntigravitySnapshot: %v", err)
	}

	// Query with referenceTime BEFORE the snapshot - forces fallback to QueryLatestAntigravity
	entries, err := s.getAntigravityGroupedCrossQuotasAt(now.Add(-24 * time.Hour))
	if err != nil {
		t.Fatalf("getAntigravityGroupedCrossQuotasAt: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("Expected at least 1 entry from fallback path")
	}
}

// --- Test CopilotCycleOverview with cross-quota data ---

func TestCopilotStore_QueryCopilotCycleOverview_WithCrossQuotas(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	resetDate := now.Add(30 * 24 * time.Hour)

	// Create cycle
	_, err = s.CreateCopilotCycle("premium_interactions", now, &resetDate)
	if err != nil {
		t.Fatalf("CreateCopilotCycle: %v", err)
	}

	// Insert snapshots
	for i := 0; i < 3; i++ {
		snap := &api.CopilotSnapshot{
			CapturedAt: now.Add(time.Duration(i) * time.Hour),
			RawJSON:    "{}",
			Quotas: []api.CopilotQuota{
				{Name: "premium_interactions", Entitlement: 1000, Remaining: 900 - i*100, PercentRemaining: float64(90 - i*10)},
			},
		}
		_, err = s.InsertCopilotSnapshot(snap)
		if err != nil {
			t.Fatalf("InsertCopilotSnapshot[%d]: %v", i, err)
		}
	}

	// Close cycle
	err = s.CloseCopilotCycle("premium_interactions", now.Add(24*time.Hour), 500, 100)
	if err != nil {
		t.Fatalf("CloseCopilotCycle: %v", err)
	}

	overview, err := s.QueryCopilotCycleOverview("premium_interactions", 10)
	if err != nil {
		t.Fatalf("QueryCopilotCycleOverview: %v", err)
	}
	if len(overview) == 0 {
		t.Fatal("Expected at least 1 overview row")
	}
}

// --- Test Anthropic overview with cross-quota data ---

func TestAnthropicStore_QueryAnthropicCycleOverview_WithCrossQuotas(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	resetsAt := now.Add(5 * time.Hour)

	// Create cycle
	_, err = s.CreateAnthropicCycle("five_hour", now, &resetsAt)
	if err != nil {
		t.Fatalf("CreateAnthropicCycle: %v", err)
	}

	// Insert snapshots
	for i := 0; i < 3; i++ {
		snap := &api.AnthropicSnapshot{
			CapturedAt: now.Add(time.Duration(i) * time.Hour),
			RawJSON:    "{}",
			Quotas: []api.AnthropicQuota{
				{Name: "five_hour", Utilization: float64(i) * 0.2, ResetsAt: &resetsAt},
			},
		}
		_, err = s.InsertAnthropicSnapshot(snap)
		if err != nil {
			t.Fatalf("InsertAnthropicSnapshot[%d]: %v", i, err)
		}
	}

	// Close cycle
	err = s.CloseAnthropicCycle("five_hour", now.Add(5*time.Hour), 0.4, 0.4)
	if err != nil {
		t.Fatalf("CloseAnthropicCycle: %v", err)
	}

	overview, err := s.QueryAnthropicCycleOverview("five_hour", 10)
	if err != nil {
		t.Fatalf("QueryAnthropicCycleOverview: %v", err)
	}
	if len(overview) == 0 {
		t.Fatal("Expected at least 1 overview row")
	}
}
