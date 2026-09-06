package store

import (
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

// TestZaiSnapshot_CreditLimitRoundTrip verifies the credit-window metadata
// survives storage, so the dashboard can label the cards correctly.
func TestZaiSnapshot_CreditLimitRoundTrip(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	reset := time.UnixMilli(1788351145586).UTC()
	weekly := time.UnixMilli(1788784466996).UTC()
	in := &api.ZaiSnapshot{
		CapturedAt:          time.Now().UTC(),
		TimeUsage:           2000,
		TimeCurrentValue:    402,
		TimeRemaining:       1597,
		TimePercentage:      20,
		TimeLimitType:       api.ZaiLimitTypeCredit,
		TimeNextResetTime:   &reset,
		TokensUsage:         10000,
		TokensCurrentValue:  5207,
		TokensRemaining:     4792,
		TokensPercentage:    52,
		TokensLimitType:     api.ZaiLimitTypeCredit,
		TokensNextResetTime: &weekly,
	}
	if _, err := s.InsertZaiSnapshot(in); err != nil {
		t.Fatalf("InsertZaiSnapshot: %v", err)
	}

	out, err := s.QueryLatestZai()
	if err != nil {
		t.Fatalf("QueryLatestZai: %v", err)
	}
	if out == nil {
		t.Fatal("QueryLatestZai returned nil")
	}
	if out.TimeLimitType != api.ZaiLimitTypeCredit || out.TokensLimitType != api.ZaiLimitTypeCredit {
		t.Errorf("limit types = %q / %q, want CREDIT_LIMIT for both", out.TimeLimitType, out.TokensLimitType)
	}
	if out.TimePercentage != 20 || out.TokensPercentage != 52 {
		t.Errorf("percentages = %d / %d, want 20 / 52", out.TimePercentage, out.TokensPercentage)
	}
	if out.TimeNextResetTime == nil || !out.TimeNextResetTime.Equal(reset) {
		t.Errorf("TimeNextResetTime = %v, want %v", out.TimeNextResetTime, reset)
	}
	if !out.IsCreditBased() {
		t.Error("IsCreditBased() = false after round-trip, want true")
	}

	ranged, err := s.QueryZaiRange(in.CapturedAt.Add(-time.Hour), in.CapturedAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("QueryZaiRange: %v", err)
	}
	if len(ranged) != 1 {
		t.Fatalf("QueryZaiRange returned %d snapshots, want 1", len(ranged))
	}
	if ranged[0].TimeLimitType != api.ZaiLimitTypeCredit || ranged[0].TimeNextResetTime == nil {
		t.Errorf("range query lost credit metadata: type=%q reset=%v",
			ranged[0].TimeLimitType, ranged[0].TimeNextResetTime)
	}
}

// TestZaiSnapshot_LegacyRowsSurviveUpgrade pins the compatibility requirement:
// a database written before the credit columns existed must keep serving its
// rows unchanged after the schema migration adds them.
func TestZaiSnapshot_LegacyRowsSurviveUpgrade(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	// Recreate zai_snapshots at the pre-credit shape and seed a legacy row.
	if _, err := s.db.Exec(`DROP TABLE zai_snapshots`); err != nil {
		t.Fatalf("drop zai_snapshots: %v", err)
	}
	if _, err := s.db.Exec(`
		CREATE TABLE zai_snapshots (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			provider TEXT NOT NULL DEFAULT 'zai',
			captured_at TEXT NOT NULL,
			time_limit INTEGER NOT NULL,
			time_unit INTEGER NOT NULL,
			time_number INTEGER NOT NULL,
			time_usage REAL NOT NULL,
			time_current_value REAL NOT NULL,
			time_remaining REAL NOT NULL,
			time_percentage INTEGER NOT NULL,
			time_usage_details TEXT NOT NULL DEFAULT '',
			tokens_limit INTEGER NOT NULL,
			tokens_unit INTEGER NOT NULL,
			tokens_number INTEGER NOT NULL,
			tokens_usage REAL NOT NULL,
			tokens_current_value REAL NOT NULL,
			tokens_remaining REAL NOT NULL,
			tokens_percentage INTEGER NOT NULL,
			tokens_next_reset TEXT
		)
	`); err != nil {
		t.Fatalf("create legacy zai_snapshots: %v", err)
	}

	captured := time.Now().UTC().Add(-time.Minute)
	if _, err := s.db.Exec(`
		INSERT INTO zai_snapshots
		(provider, captured_at, time_limit, time_unit, time_number, time_usage,
		 time_current_value, time_remaining, time_percentage, time_usage_details,
		 tokens_limit, tokens_unit, tokens_number, tokens_usage,
		 tokens_current_value, tokens_remaining, tokens_percentage, tokens_next_reset)
		VALUES ('zai', ?, 5, 5, 1, 1000, 19, 981, 1, '', 5, 5, 1, 300000000, 200112618, 99887382, 66, NULL)`,
		captured.Format(time.RFC3339Nano),
	); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	if err := s.migrateSchema(); err != nil {
		t.Fatalf("migrateSchema: %v", err)
	}

	out, err := s.QueryLatestZai()
	if err != nil {
		t.Fatalf("QueryLatestZai after migration: %v", err)
	}
	if out == nil {
		t.Fatal("legacy row disappeared after migration")
	}
	if out.TimePercentage != 1 || out.TimeCurrentValue != 19 {
		t.Errorf("legacy time values changed: percentage=%d currentValue=%v, want 1 / 19",
			out.TimePercentage, out.TimeCurrentValue)
	}
	if out.TokensPercentage != 66 || out.TokensCurrentValue != 200112618 {
		t.Errorf("legacy tokens values changed: percentage=%d currentValue=%v, want 66 / 200112618",
			out.TokensPercentage, out.TokensCurrentValue)
	}
	// Rows written before the columns existed carry no limit type, which the
	// dashboard reads as the legacy TIME_LIMIT / TOKENS_LIMIT labelling.
	if out.TimeLimitType != "" || out.TokensLimitType != "" {
		t.Errorf("legacy row gained limit types %q / %q, want empty", out.TimeLimitType, out.TokensLimitType)
	}
	if out.TimeNextResetTime != nil {
		t.Errorf("legacy row gained a time reset: %v", out.TimeNextResetTime)
	}
	if out.IsCreditBased() {
		t.Error("IsCreditBased() = true for a legacy row, want false")
	}

	// New writes against the migrated table must work too.
	if _, err := s.InsertZaiSnapshot(&api.ZaiSnapshot{
		CapturedAt:     time.Now().UTC(),
		TimePercentage: 20,
		TimeLimitType:  api.ZaiLimitTypeCredit,
	}); err != nil {
		t.Fatalf("InsertZaiSnapshot after migration: %v", err)
	}
}
