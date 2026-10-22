package store

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// TestStoreRangeTiming is a manual timing harness against a real database
// copy. It is skipped unless ONWATCH_BENCH_DB points at the file. It never
// runs in CI or in the normal suite.
func TestStoreRangeTiming(t *testing.T) {
	dbPath := os.Getenv("ONWATCH_BENCH_DB")
	if dbPath == "" {
		t.Skip("set ONWATCH_BENCH_DB to a database copy to time range loads")
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("benchmark database: %v", err)
	}

	s, err := New(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	// The database copy ends at various past dates, so anchor each provider's
	// window to the last row in its own snapshot table.
	latest := func(table string) time.Time {
		var raw string
		if err := s.db.QueryRow(`SELECT MAX(captured_at) FROM ` + table).Scan(&raw); err != nil || raw == "" {
			return time.Now().UTC()
		}
		end, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return time.Now().UTC()
		}
		return end
	}

	type loadCase struct {
		name  string
		table string
		load  func(start, end time.Time) error
	}
	cases := []loadCase{
		{"anthropic", "anthropic_snapshots", func(start, end time.Time) error {
			snaps, err := s.QueryAnthropicRange(start, end)
			if err != nil {
				return err
			}
			t.Logf("  anthropic: %d snapshots", len(snaps))
			return nil
		}},
		{"codex", "codex_snapshots", func(start, end time.Time) error {
			snaps, err := s.QueryCodexRange(DefaultCodexAccountID, start, end)
			if err != nil {
				return err
			}
			t.Logf("  codex: %d snapshots", len(snaps))
			return nil
		}},
		{"copilot", "copilot_snapshots", func(start, end time.Time) error {
			snaps, err := s.QueryCopilotRange(start, end)
			if err != nil {
				return err
			}
			t.Logf("  copilot: %d snapshots", len(snaps))
			return nil
		}},
		{"gemini", "gemini_snapshots", func(start, end time.Time) error {
			snaps, err := s.QueryGeminiRange(start, end)
			if err != nil {
				return err
			}
			t.Logf("  gemini: %d snapshots", len(snaps))
			return nil
		}},
		{"minimax", "minimax_snapshots", func(start, end time.Time) error {
			snaps, err := s.QueryMiniMaxRange(start, end, 6)
			if err != nil {
				return err
			}
			t.Logf("  minimax: %d snapshots", len(snaps))
			return nil
		}},
		{"antigravity", "antigravity_snapshots", func(start, end time.Time) error {
			snaps, err := s.QueryAntigravityRange(start, end)
			if err != nil {
				return err
			}
			t.Logf("  antigravity: %d snapshots", len(snaps))
			return nil
		}},
		{"synthetic", "quota_snapshots", func(start, end time.Time) error {
			snaps, err := s.QueryRange(start, end)
			if err != nil {
				return err
			}
			t.Logf("  synthetic: %d snapshots", len(snaps))
			return nil
		}},
		{"grok", "grok_snapshots", func(start, end time.Time) error {
			snaps, err := s.QueryGrokRange(DefaultGrokAccountID, start, end)
			if err != nil {
				return err
			}
			t.Logf("  grok: %d snapshots", len(snaps))
			return nil
		}},
	}

	for _, days := range []int{7, 30} {
		for _, tc := range cases {
			now := latest(tc.table)
			start := now.Add(-time.Duration(days) * 24 * time.Hour)
			// Warm the page cache, then report the best of three.
			var best time.Duration
			for run := 0; run < 3; run++ {
				began := time.Now()
				if err := tc.load(start, now); err != nil {
					t.Fatalf("%s: %v", tc.name, err)
				}
				elapsed := time.Since(began)
				if best == 0 || elapsed < best {
					best = elapsed
				}
			}
			fmt.Printf("TIMING %-12s %2dd %s\n", tc.name, days, best.Round(time.Millisecond))
		}
	}
}
