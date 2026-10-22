package store

import (
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

// batchedRangeCase describes one provider's range loader in terms that let the
// shared test compare every snapshot after a batched load. Each snapshot is
// inserted with two quotas ("alpha", "beta") whose utilization is derived from
// the snapshot index, so a row attached to the wrong snapshot is detectable.
type batchedRangeCase struct {
	name   string
	insert func(s *Store, i int, base time.Time) error
	query  func(s *Store, start, end time.Time) ([]rangeRow, error)
	// want overrides the expected values for providers whose stored values
	// are integers rather than the fractional default.
	want func(i int) map[string]float64
}

type rangeRow struct {
	at     time.Time
	values map[string]float64
	order  []string
}

func expectedValue(i int, quota string) float64 {
	if quota == "alpha" {
		return float64(i) + 0.25
	}
	return float64(i) + 0.75
}

func twoQuotaValues(i int) map[string]float64 {
	return map[string]float64{
		"alpha": expectedValue(i, "alpha"),
		"beta":  expectedValue(i, "beta"),
	}
}

func TestStoreRangeBatchingSpansChunks(t *testing.T) {
	t.Parallel()

	// One past two full 400-id batches: the third batch holds the remainder.
	const snapshotCount = 801

	cases := []batchedRangeCase{
		{
			name: "anthropic",
			insert: func(s *Store, i int, base time.Time) error {
				_, err := s.InsertAnthropicSnapshot(&api.AnthropicSnapshot{
					CapturedAt: base.Add(time.Duration(i) * time.Minute),
					RawJSON:    "{}",
					Quotas: []api.AnthropicQuota{
						{Name: "five_hour", Utilization: expectedValue(i, "alpha")},
						{Name: "seven_day", Utilization: expectedValue(i, "beta")},
					},
				})
				return err
			},
			query: func(s *Store, start, end time.Time) ([]rangeRow, error) {
				snaps, err := s.QueryAnthropicRange(start, end)
				if err != nil {
					return nil, err
				}
				rows := make([]rangeRow, len(snaps))
				for i, snap := range snaps {
					row := rangeRow{at: snap.CapturedAt, values: map[string]float64{}}
					for _, q := range snap.Quotas {
						switch q.Name {
						case "five_hour":
							row.values["alpha"] = q.Utilization
							row.order = append(row.order, "alpha")
						case "seven_day":
							row.values["beta"] = q.Utilization
							row.order = append(row.order, "beta")
						}
					}
					rows[i] = row
				}
				return rows, nil
			},
		},
		{
			name: "codex",
			insert: func(s *Store, i int, base time.Time) error {
				_, err := s.InsertCodexSnapshot(&api.CodexSnapshot{
					CapturedAt: base.Add(time.Duration(i) * time.Minute),
					RawJSON:    "{}",
					Quotas: []api.CodexQuota{
						{Name: "alpha", Utilization: expectedValue(i, "alpha")},
						{Name: "beta", Utilization: expectedValue(i, "beta")},
					},
				})
				return err
			},
			query: func(s *Store, start, end time.Time) ([]rangeRow, error) {
				snaps, err := s.QueryCodexRange(DefaultCodexAccountID, start, end)
				if err != nil {
					return nil, err
				}
				rows := make([]rangeRow, len(snaps))
				for i, snap := range snaps {
					row := rangeRow{at: snap.CapturedAt, values: map[string]float64{}}
					for _, q := range snap.Quotas {
						row.values[q.Name] = q.Utilization
						row.order = append(row.order, q.Name)
					}
					rows[i] = row
				}
				return rows, nil
			},
		},
		{
			name: "commandcode",
			insert: func(s *Store, i int, base time.Time) error {
				_, err := s.InsertCommandCodeSnapshot(&api.CommandCodeSnapshot{
					CapturedAt: base.Add(time.Duration(i) * time.Minute),
					RawJSON:    "{}",
					Quotas: []api.CommandCodeQuota{
						{Name: "alpha", Utilization: expectedValue(i, "alpha")},
						{Name: "beta", Utilization: expectedValue(i, "beta")},
					},
				})
				return err
			},
			query: func(s *Store, start, end time.Time) ([]rangeRow, error) {
				snaps, err := s.QueryCommandCodeRange(start, end)
				if err != nil {
					return nil, err
				}
				rows := make([]rangeRow, len(snaps))
				for i, snap := range snaps {
					row := rangeRow{at: snap.CapturedAt, values: map[string]float64{}}
					for _, q := range snap.Quotas {
						row.values[q.Name] = q.Utilization
						row.order = append(row.order, q.Name)
					}
					rows[i] = row
				}
				return rows, nil
			},
		},
		{
			name: "copilot",
			insert: func(s *Store, i int, base time.Time) error {
				_, err := s.InsertCopilotSnapshot(&api.CopilotSnapshot{
					CapturedAt: base.Add(time.Duration(i) * time.Minute),
					RawJSON:    "{}",
					Quotas: []api.CopilotQuota{
						{Name: "alpha", Entitlement: 1000000, Remaining: 1000000 - i},
						{Name: "beta", Entitlement: 1000000, Remaining: 1000000 - i},
					},
				})
				return err
			},
			query: func(s *Store, start, end time.Time) ([]rangeRow, error) {
				snaps, err := s.QueryCopilotRange(start, end)
				if err != nil {
					return nil, err
				}
				rows := make([]rangeRow, len(snaps))
				for i, snap := range snaps {
					row := rangeRow{at: snap.CapturedAt, values: map[string]float64{}}
					for _, q := range snap.Quotas {
						row.values[q.Name] = float64(q.Entitlement - q.Remaining)
						row.order = append(row.order, q.Name)
					}
					rows[i] = row
				}
				return rows, nil
			},
			// Entitlement/remaining are integers in this table.
			want: func(i int) map[string]float64 {
				return map[string]float64{"alpha": float64(i), "beta": float64(i)}
			},
		},
		{
			name: "cursor",
			insert: func(s *Store, i int, base time.Time) error {
				_, err := s.InsertCursorSnapshot(&api.CursorSnapshot{
					CapturedAt: base.Add(time.Duration(i) * time.Minute),
					RawJSON:    "{}",
					Quotas: []api.CursorQuota{
						{Name: "alpha", Utilization: expectedValue(i, "alpha")},
						{Name: "beta", Utilization: expectedValue(i, "beta")},
					},
				})
				return err
			},
			query: func(s *Store, start, end time.Time) ([]rangeRow, error) {
				snaps, err := s.QueryCursorRange(start, end)
				if err != nil {
					return nil, err
				}
				rows := make([]rangeRow, len(snaps))
				for i, snap := range snaps {
					row := rangeRow{at: snap.CapturedAt, values: map[string]float64{}}
					for _, q := range snap.Quotas {
						row.values[q.Name] = q.Utilization
						row.order = append(row.order, q.Name)
					}
					rows[i] = row
				}
				return rows, nil
			},
		},
		{
			name: "gemini",
			insert: func(s *Store, i int, base time.Time) error {
				_, err := s.InsertGeminiSnapshot(&api.GeminiSnapshot{
					CapturedAt: base.Add(time.Duration(i) * time.Minute),
					RawJSON:    "{}",
					Quotas: []api.GeminiQuota{
						{ModelID: "alpha", UsagePercent: expectedValue(i, "alpha")},
						{ModelID: "beta", UsagePercent: expectedValue(i, "beta")},
					},
				})
				return err
			},
			query: func(s *Store, start, end time.Time) ([]rangeRow, error) {
				snaps, err := s.QueryGeminiRange(start, end)
				if err != nil {
					return nil, err
				}
				rows := make([]rangeRow, len(snaps))
				for i, snap := range snaps {
					row := rangeRow{at: snap.CapturedAt, values: map[string]float64{}}
					for _, q := range snap.Quotas {
						row.values[q.ModelID] = q.UsagePercent
						row.order = append(row.order, q.ModelID)
					}
					rows[i] = row
				}
				return rows, nil
			},
		},
		{
			name: "minimax",
			insert: func(s *Store, i int, base time.Time) error {
				_, err := s.InsertMiniMaxSnapshot(&api.MiniMaxSnapshot{
					CapturedAt: base.Add(time.Duration(i) * time.Minute),
					RawJSON:    "{}",
					Models: []api.MiniMaxModelQuota{
						{ModelName: "alpha", UsedPercent: expectedValue(i, "alpha")},
						{ModelName: "beta", UsedPercent: expectedValue(i, "beta")},
					},
				}, 1)
				return err
			},
			query: func(s *Store, start, end time.Time) ([]rangeRow, error) {
				snaps, err := s.QueryMiniMaxRange(start, end, 1)
				if err != nil {
					return nil, err
				}
				rows := make([]rangeRow, len(snaps))
				for i, snap := range snaps {
					row := rangeRow{at: snap.CapturedAt, values: map[string]float64{}}
					for _, m := range snap.Models {
						row.values[m.ModelName] = m.UsedPercent
						row.order = append(row.order, m.ModelName)
					}
					rows[i] = row
				}
				return rows, nil
			},
		},
		{
			name: "muse",
			insert: func(s *Store, i int, base time.Time) error {
				_, err := s.InsertMuseSnapshot(&api.MuseSnapshot{
					CapturedAt: base.Add(time.Duration(i) * time.Minute),
					RawJSON:    "{}",
					Quotas: []api.MuseQuota{
						{Name: "alpha", Utilization: expectedValue(i, "alpha")},
						{Name: "beta", Utilization: expectedValue(i, "beta")},
					},
				})
				return err
			},
			query: func(s *Store, start, end time.Time) ([]rangeRow, error) {
				snaps, err := s.QueryMuseRange(start, end)
				if err != nil {
					return nil, err
				}
				rows := make([]rangeRow, len(snaps))
				for i, snap := range snaps {
					row := rangeRow{at: snap.CapturedAt, values: map[string]float64{}}
					for _, q := range snap.Quotas {
						row.values[q.Name] = q.Utilization
						row.order = append(row.order, q.Name)
					}
					rows[i] = row
				}
				return rows, nil
			},
		},
		{
			name: "ollama",
			insert: func(s *Store, i int, base time.Time) error {
				_, err := s.InsertOllamaSnapshot(&api.OllamaSnapshot{
					CapturedAt: base.Add(time.Duration(i) * time.Minute),
					RawJSON:    "{}",
					Quotas: []api.OllamaQuota{
						{Name: "alpha", Utilization: expectedValue(i, "alpha")},
						{Name: "beta", Utilization: expectedValue(i, "beta")},
					},
				})
				return err
			},
			query: func(s *Store, start, end time.Time) ([]rangeRow, error) {
				snaps, err := s.QueryOllamaRange(start, end)
				if err != nil {
					return nil, err
				}
				rows := make([]rangeRow, len(snaps))
				for i, snap := range snaps {
					row := rangeRow{at: snap.CapturedAt, values: map[string]float64{}}
					for _, q := range snap.Quotas {
						row.values[q.Name] = q.Utilization
						row.order = append(row.order, q.Name)
					}
					rows[i] = row
				}
				return rows, nil
			},
		},
		{
			name: "opencode",
			insert: func(s *Store, i int, base time.Time) error {
				_, err := s.InsertOpenCodeSnapshot(&api.OpenCodeSnapshot{
					CapturedAt: base.Add(time.Duration(i) * time.Minute),
					RawJSON:    "{}",
					Quotas: []api.OpenCodeQuota{
						{Name: "alpha", Utilization: expectedValue(i, "alpha")},
						{Name: "beta", Utilization: expectedValue(i, "beta")},
					},
				})
				return err
			},
			query: func(s *Store, start, end time.Time) ([]rangeRow, error) {
				snaps, err := s.QueryOpenCodeRange(start, end)
				if err != nil {
					return nil, err
				}
				rows := make([]rangeRow, len(snaps))
				for i, snap := range snaps {
					row := rangeRow{at: snap.CapturedAt, values: map[string]float64{}}
					for _, q := range snap.Quotas {
						row.values[q.Name] = q.Utilization
						row.order = append(row.order, q.Name)
					}
					rows[i] = row
				}
				return rows, nil
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s, err := New(":memory:")
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			defer s.Close()

			base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			for i := 0; i < snapshotCount; i++ {
				if err := tc.insert(s, i, base); err != nil {
					t.Fatalf("insert snapshot %d: %v", i, err)
				}
			}

			rows, err := tc.query(s, base.Add(-time.Minute), base.Add(snapshotCount*time.Minute))
			if err != nil {
				t.Fatalf("query range: %v", err)
			}
			if len(rows) != snapshotCount {
				t.Fatalf("got %d snapshots, want %d", len(rows), snapshotCount)
			}
			prev := time.Time{}
			for i, row := range rows {
				if i > 0 && !row.at.After(prev) {
					t.Fatalf("snapshot %d not ascending: %v <= %v", i, row.at, prev)
				}
				prev = row.at
				want := twoQuotaValues(i)
				if tc.want != nil {
					want = tc.want(i)
				}
				if len(row.values) != len(want) {
					t.Fatalf("snapshot %d has %d values, want %d", i, len(row.values), len(want))
				}
				for name, val := range want {
					if got := row.values[name]; got != val {
						t.Fatalf("snapshot %d quota %q = %v, want %v", i, name, got, val)
					}
				}
				if !sort.StringsAreSorted(row.order) {
					t.Fatalf("snapshot %d quotas out of order: %v", i, row.order)
				}
			}
		})
	}
}

// TestStoreSampledRangeKeepsEnds verifies the SQL window sampler trims a
// large range to chart size while keeping the first and last snapshots.
func TestStoreSampledRangeKeepsEnds(t *testing.T) {
	t.Parallel()

	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 1000; i++ {
		at := base.Add(time.Duration(i) * time.Minute)
		if _, err := s.InsertAnthropicSnapshot(&api.AnthropicSnapshot{
			CapturedAt: at,
			RawJSON:    "{}",
			Quotas:     []api.AnthropicQuota{{Name: "five_hour", Utilization: float64(i)}},
		}); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
		if _, err := s.InsertGrokSnapshot(&api.GrokSnapshot{
			CapturedAt:  at,
			RawJSON:     "{}",
			AccountID:   DefaultGrokAccountID,
			LoginMethod: "test",
			Quotas:      []api.GrokQuota{{Name: "credits", Utilization: float64(i)}},
		}); err != nil {
			t.Fatalf("insert grok %d: %v", i, err)
		}
	}

	snaps, err := s.QueryAnthropicRangeSampled(base, base.Add(1000*time.Minute))
	if err != nil {
		t.Fatalf("QueryAnthropicRangeSampled: %v", err)
	}
	if len(snaps) == 0 || len(snaps) > 200 {
		t.Fatalf("sampled %d snapshots, want 1..200", len(snaps))
	}
	if !snaps[0].CapturedAt.Equal(base) {
		t.Errorf("first sampled snapshot %v, want %v", snaps[0].CapturedAt, base)
	}
	if last := snaps[len(snaps)-1]; !last.CapturedAt.Equal(base.Add(999 * time.Minute)) {
		t.Errorf("last sampled snapshot %v, want %v", last.CapturedAt, base.Add(999*time.Minute))
	}
	for _, snap := range snaps {
		if len(snap.Quotas) != 1 {
			t.Fatalf("snapshot %v has %d quotas, want 1", snap.CapturedAt, len(snap.Quotas))
		}
	}

	// The joined sampler keeps each sampled snapshot's quota values.
	grokSnaps, err := s.QueryGrokRangeSampled(DefaultGrokAccountID, base, base.Add(1000*time.Minute))
	if err != nil {
		t.Fatalf("QueryGrokRangeSampled: %v", err)
	}
	if len(grokSnaps) == 0 || len(grokSnaps) > 200 {
		t.Fatalf("sampled %d grok snapshots, want 1..200", len(grokSnaps))
	}
	for _, snap := range grokSnaps {
		if len(snap.Quotas) != 1 || snap.Quotas[0].Name != "credits" {
			t.Fatalf("grok snapshot %v quotas = %+v, want one credits row", snap.CapturedAt, snap.Quotas)
		}
	}
}

// TestStoreSampledRangeFiltersAccount verifies account-scoped samplers do not
// sample across accounts: every returned snapshot belongs to the request.
func TestStoreSampledRangeFiltersAccount(t *testing.T) {
	t.Parallel()

	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	const otherAccount = int64(2)
	for i := 0; i < 600; i++ {
		at := base.Add(time.Duration(i) * time.Minute)
		accountID := DefaultCodexAccountID
		if i%2 == 1 {
			accountID = otherAccount
		}
		if _, err := s.InsertCodexSnapshot(&api.CodexSnapshot{
			CapturedAt: at,
			AccountID:  accountID,
			RawJSON:    "{}",
			Quotas:     []api.CodexQuota{{Name: "five_hour", Utilization: float64(i)}},
		}); err != nil {
			t.Fatalf("insert codex %d: %v", i, err)
		}
		if _, err := s.InsertMiniMaxSnapshot(&api.MiniMaxSnapshot{
			CapturedAt: at,
			RawJSON:    "{}",
			Models:     []api.MiniMaxModelQuota{{ModelName: "alpha", UsedPercent: float64(i)}},
		}, accountID); err != nil {
			t.Fatalf("insert minimax %d: %v", i, err)
		}
	}

	codexSnaps, err := s.QueryCodexRangeSampled(otherAccount, base, base.Add(600*time.Minute))
	if err != nil {
		t.Fatalf("QueryCodexRangeSampled: %v", err)
	}
	if len(codexSnaps) == 0 || len(codexSnaps) > 200 {
		t.Fatalf("sampled %d codex snapshots, want 1..200", len(codexSnaps))
	}
	for _, snap := range codexSnaps {
		if snap.AccountID != otherAccount {
			t.Fatalf("codex sample leaked account %d", snap.AccountID)
		}
		if len(snap.Quotas) != 1 {
			t.Fatalf("codex snapshot %v has %d quotas, want 1", snap.CapturedAt, len(snap.Quotas))
		}
	}

	minimaxSnaps, err := s.QueryMiniMaxRangeSampled(base, base.Add(600*time.Minute), otherAccount)
	if err != nil {
		t.Fatalf("QueryMiniMaxRangeSampled: %v", err)
	}
	if len(minimaxSnaps) == 0 || len(minimaxSnaps) > 200 {
		t.Fatalf("sampled %d minimax snapshots, want 1..200", len(minimaxSnaps))
	}
	for _, snap := range minimaxSnaps {
		if len(snap.Models) != 1 {
			t.Fatalf("minimax snapshot %v has %d models, want 1", snap.CapturedAt, len(snap.Models))
		}
	}
}

func TestWindowChartQueryWhereShape(t *testing.T) {
	t.Parallel()
	q := windowChartQueryWhere("t", "id", "account_id = ?")
	if want := "captured_at BETWEEN ? AND ? AND account_id = ?"; !strings.Contains(q, want) {
		t.Fatalf("query missing %q:\n%s", want, q)
	}
	q = windowChartQuery("t", "id")
	if strings.Contains(q, "account_id") {
		t.Fatalf("plain window query grew a filter:\n%s", q)
	}
}
