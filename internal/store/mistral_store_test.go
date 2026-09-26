package store

import (
	"context"
	"database/sql"
	"github.com/onllm-dev/onwatch/v2/internal/api"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMistralPartitionsAndStale(t *testing.T) {
	s, e := New(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	for _, id := range []string{"a", "b"} {
		if e = s.SaveMistral(ctx, &api.MistralSnapshot{Identity: id, CapturedAt: now, Status: "ok", Quotas: []api.MistralQuota{{Name: "api_included", Used: 1, Limit: 10, CapturedAt: now}}}); e != nil {
			t.Fatal(e)
		}
	}
	if e = s.SaveMistral(ctx, &api.MistralSnapshot{Identity: "b", CapturedAt: now.Add(time.Minute), Status: "partial"}); e != nil {
		t.Fatal(e)
	}
	snap, e := s.LatestMistral(ctx)
	if e != nil || snap.Identity != "b" || len(snap.Quotas) != 1 || !snap.Quotas[0].CapturedAt.Equal(now) {
		t.Fatalf("%+v %v", snap, e)
	}
	rows, e := s.MistralHistory(ctx, now.Add(-time.Hour), now.Add(time.Hour), 99999)
	if e != nil || len(rows) != 2 {
		t.Fatalf("%d %v", len(rows), e)
	}
}

func TestMistralResetsCorrectionsAndRestart(t *testing.T) {
	s, e := New(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	now := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	reset := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	q := api.MistralQuota{Name: "api_included", Used: 5, Limit: 10, Utilization: 50, CapturedAt: now, ResetsAt: &reset}
	for i, used := range []float64{5, 4, 6} {
		q.Used = used
		q.CapturedAt = now.Add(time.Duration(i) * time.Minute)
		did, e := s.TrackMistral(ctx, "a", q)
		if e != nil || did {
			t.Fatalf("false reset: %v %v", did, e)
		}
	}
	q.Limit = 20
	q.Utilization = 30
	q.CapturedAt = now.Add(4 * time.Minute)
	did, e := s.TrackMistral(ctx, "a", q)
	if e != nil || did {
		t.Fatal("plan change reset")
	}
	next := reset.AddDate(0, 1, 0)
	q.ResetsAt = &next
	q.CapturedAt = reset.Add(time.Minute)
	q.Used = 1
	q.Utilization = 5
	did, e = s.TrackMistral(ctx, "a", q)
	if e != nil || !did {
		t.Fatalf("missing reset %v %v", did, e)
	}
	if e = s.SetSetting("mistral_identity", "a"); e != nil {
		t.Fatal(e)
	}
	cycles, e := s.MistralCycles(ctx, q.Name, 99999)
	if e != nil || len(cycles) != 2 {
		t.Fatalf("cycles=%+v err=%v", cycles, e)
	}
	if cycles[0].TotalDelta != 0 || cycles[1].TotalDelta != 20 {
		t.Fatalf("incorrect deltas %+v", cycles)
	}
	// Reprocessing stale data after restart cannot add deltas.
	q.Used = 9
	did, e = s.TrackMistral(ctx, "a", q)
	if e != nil || did {
		t.Fatal(e)
	}
	cycles, _ = s.MistralCycles(ctx, q.Name, 50)
	if cycles[0].TotalDelta != 0 {
		t.Fatal("stale delta")
	}
}

func TestMistralBillingHistory(t *testing.T) {
	s, e := New(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	now := time.Now().UTC()
	amount := -1.25
	snap := &api.MistralSnapshot{Identity: "billing", CapturedAt: now, Status: "partial", Billing: &api.MistralBilling{Amount: &amount, Currency: "GBP", CapturedAt: now, PeriodStart: now, Status: "ok"}}
	if e = s.SaveMistral(context.Background(), snap); e != nil {
		t.Fatal(e)
	}
	rows, e := s.MistralHistory(context.Background(), now.Add(-time.Hour), now.Add(time.Hour), 5)
	if e != nil || len(rows) != 1 || rows[0].Billing == nil || *rows[0].Billing.Amount != amount {
		t.Fatalf("missing billing history: %+v %v", rows, e)
	}
}

// Mirrors TestGrokStore_QueryRangeLoadsQuotas. Uses a file-based store on
// purpose: ":memory:" forces a single connection, which serialises reads and
// hides the split-read-snapshot problem a per-row query loop can hit.
func TestMistralHistoryLoadsChildrenFileBacked(t *testing.T) {
	db, e := New(filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ctx := context.Background()
	base := time.Now().UTC().Add(-time.Hour)
	amount := 2.5
	for i := 0; i < 5; i++ {
		at := base.Add(time.Duration(i) * time.Minute)
		snap := &api.MistralSnapshot{Identity: "id", CapturedAt: at, Status: "ok", Quotas: []api.MistralQuota{
			{Name: "api_included", Used: float64(i), Limit: 10, Utilization: float64(i) * 10, Currency: "EUR", CapturedAt: at},
			{Name: "vibe_included", Used: float64(i) * 2, Limit: 100, Utilization: float64(i), Currency: "EUR", CapturedAt: at},
		}, Billing: &api.MistralBilling{Amount: &amount, Currency: "EUR", Status: "ok", CapturedAt: at, PeriodStart: at, PeriodEnd: at.Add(time.Hour)}}
		if e = db.SaveMistral(ctx, snap); e != nil {
			t.Fatal(e)
		}
	}
	rows, e := db.MistralHistory(ctx, base.Add(-time.Minute), time.Now().UTC().Add(time.Minute), 200)
	if e != nil {
		t.Fatal(e)
	}
	if len(rows) != 5 {
		t.Fatalf("snapshots=%d, want 5", len(rows))
	}
	for i, r := range rows {
		// Each snapshot must carry both quotas and its billing row.
		if len(r.Quotas) != 2 {
			t.Fatalf("row %d quotas=%d, want 2", i, len(r.Quotas))
		}
		if r.Quotas[0].Name != "api_included" || r.Quotas[1].Name != "vibe_included" {
			t.Fatalf("row %d quota order/names wrong: %+v", i, r.Quotas)
		}
		if r.Quotas[0].Used != float64(i) || r.Quotas[0].Currency != "EUR" {
			t.Fatalf("row %d quota mismatched to its snapshot: %+v", i, r.Quotas[0])
		}
		if r.Quotas[0].CapturedAt.IsZero() {
			t.Fatalf("row %d quota lost capturedAt", i)
		}
		if r.Billing == nil || r.Billing.Amount == nil || *r.Billing.Amount != amount {
			t.Fatalf("row %d billing=%+v", i, r.Billing)
		}
	}
	// Ascending by capture time.
	for i := 1; i < len(rows); i++ {
		if rows[i].CapturedAt.Before(rows[i-1].CapturedAt) {
			t.Fatalf("history not ascending at %d", i)
		}
	}
}

// The clamp is the only upper bound in internal/store; a refactor must not drop it.
func TestMistralHistoryClampsLimit(t *testing.T) {
	db, e := New(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ctx := context.Background()
	base := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < 5; i++ {
		at := base.Add(time.Duration(i) * time.Second)
		if e = db.SaveMistral(ctx, &api.MistralSnapshot{Identity: "id", CapturedAt: at, Status: "ok", Quotas: []api.MistralQuota{{Name: "api_included", CapturedAt: at}}}); e != nil {
			t.Fatal(e)
		}
	}
	for _, limit := range []int{0, -1, 1, 100000} {
		rows, e := db.MistralHistory(ctx, base.Add(-time.Minute), time.Now().UTC().Add(time.Minute), limit)
		if e != nil {
			t.Fatalf("limit %d: %v", limit, e)
		}
		if len(rows) == 0 || len(rows) > 5 {
			t.Fatalf("limit %d returned %d rows", limit, len(rows))
		}
	}
}

// Quotas and billing are stored in typed columns, like every other provider's
// *_quota_values table, not as JSON. A percentage-only quota must keep its
// unknown amounts as NULL rather than zero, and every read path must rebuild
// the same values.
func TestMistralQuotasStoredAsTypedColumns(t *testing.T) {
	s, e := New(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Millisecond)
	reset := at.Add(72 * time.Hour)
	amount := 2.5
	if e = s.SaveMistral(ctx, &api.MistralSnapshot{Identity: "id", CapturedAt: at, Status: "ok",
		Quotas: []api.MistralQuota{
			{Name: "api_included", Used: 1.5, Limit: 10, Utilization: 15, Currency: "EUR", CapturedAt: at, ResetsAt: &reset},
			{Name: "vibe_included", Utilization: 8, PercentOnly: true, CapturedAt: at},
		},
		Billing: &api.MistralBilling{Amount: &amount, Currency: "EUR", Status: "ok", CapturedAt: at, PeriodStart: at, PeriodEnd: at.Add(24 * time.Hour)},
	}); e != nil {
		t.Fatal(e)
	}

	var used, limit sql.NullFloat64
	var currency string
	if e = s.db.QueryRowContext(ctx, `SELECT used,limit_value,currency FROM mistral_quota_values WHERE quota_name='api_included'`).Scan(&used, &limit, &currency); e != nil {
		t.Fatal(e)
	}
	if !used.Valid || used.Float64 != 1.5 || !limit.Valid || limit.Float64 != 10 || currency != "EUR" {
		t.Fatalf("api quota columns: used=%v limit=%v currency=%q", used, limit, currency)
	}
	if e = s.db.QueryRowContext(ctx, `SELECT used,limit_value FROM mistral_quota_values WHERE quota_name='vibe_included'`).Scan(&used, &limit); e != nil {
		t.Fatal(e)
	}
	if used.Valid || limit.Valid {
		t.Fatalf("percentage-only quota stored amounts instead of NULL: used=%v limit=%v", used, limit)
	}
	var billed float64
	if e = s.db.QueryRowContext(ctx, `SELECT amount FROM mistral_billing`).Scan(&billed); e != nil || billed != amount {
		t.Fatalf("billing amount=%v err=%v", billed, e)
	}

	check := func(label string, qs []api.MistralQuota, b *api.MistralBilling) {
		t.Helper()
		if len(qs) != 2 {
			t.Fatalf("%s: quotas=%d", label, len(qs))
		}
		for _, q := range qs {
			if q.Name == "" || !q.CapturedAt.Equal(at) {
				t.Fatalf("%s: name/capturedAt lost: %+v", label, q)
			}
		}
		if qs[0].Used != 1.5 || qs[0].Limit != 10 || qs[0].Utilization != 15 || qs[0].Currency != "EUR" || qs[0].ResetsAt == nil || !qs[0].ResetsAt.Equal(reset) {
			t.Fatalf("%s: api quota mangled: %+v", label, qs[0])
		}
		if !qs[1].PercentOnly || qs[1].Utilization != 8 || qs[1].ResetsAt != nil {
			t.Fatalf("%s: vibe quota mangled: %+v", label, qs[1])
		}
		if b == nil || b.Amount == nil || *b.Amount != amount || b.Currency != "EUR" || b.Status != "ok" || !b.PeriodEnd.Equal(at.Add(24*time.Hour)) {
			t.Fatalf("%s: billing mangled: %+v", label, b)
		}
	}
	latest, e := s.LatestMistral(ctx)
	if e != nil || latest == nil {
		t.Fatalf("latest=%v err=%v", latest, e)
	}
	check("LatestMistral", latest.Quotas, latest.Billing)

	rows, e := s.MistralHistory(ctx, at.Add(-time.Hour), at.Add(time.Hour), 200)
	if e != nil || len(rows) != 1 {
		t.Fatalf("history rows=%d err=%v", len(rows), e)
	}
	check("MistralHistory", rows[0].Quotas, rows[0].Billing)
}

// LatestMistral runs on every dashboard refresh, menubar refresh and metrics
// scrape. Ordering by id alone made SQLite sort every snapshot the identity
// had - about 28ms a call at 90 days of history, growing with retention. Each
// statement must walk an index and stop at the first row instead.
func TestMistralHotQueriesAvoidFullSort(t *testing.T) {
	s, e := New(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	for name, c := range map[string]struct {
		sql  string
		args []any
	}{
		"latest snapshot": {mistralLatestSnapshotSQL, nil},
		"latest billing":  {mistralLatestBillingSQL, []any{"id"}},
		"history window":  {mistralHistoryWindowSQL, []any{now.Add(-time.Hour).Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), 200}},
	} {
		rows, e := s.db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+c.sql, c.args...)
		if e != nil {
			t.Fatalf("%s: %v", name, e)
		}
		var plan []string
		for rows.Next() {
			var id, parent, notused int
			var detail string
			if e = rows.Scan(&id, &parent, &notused, &detail); e != nil {
				t.Fatal(e)
			}
			plan = append(plan, detail)
		}
		rows.Close()
		if strings.Contains(strings.Join(plan, " | "), "TEMP B-TREE FOR ORDER BY") {
			t.Fatalf("%s sorts every candidate row instead of walking an index: %v", name, plan)
		}
	}
}

// foreign_keys is a per-connection setting in SQLite, and the pool only
// enables it on one of its two connections. Pruning must therefore remove
// child rows itself: a cascade that silently does not fire leaves quota and
// billing rows behind forever, so retention stops bounding storage.
func TestMistralPruneRemovesChildrenWithoutForeignKeys(t *testing.T) {
	s, e := New(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	// The in-memory store has one connection, so this reproduces the second
	// pool connection's defaults exactly.
	if _, e = s.db.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	amount := 1.0
	for _, at := range []time.Time{now.AddDate(0, 0, -400), now} {
		if e = s.SaveMistral(ctx, &api.MistralSnapshot{Identity: "id", CapturedAt: at, Status: "ok",
			Quotas:  []api.MistralQuota{{Name: "api_included", Used: 1, Limit: 10, CapturedAt: at}},
			Billing: &api.MistralBilling{Amount: &amount, Currency: "EUR", Status: "ok", CapturedAt: at, PeriodStart: at, PeriodEnd: at.Add(time.Hour)}}); e != nil {
			t.Fatal(e)
		}
	}
	if e = s.PruneMistral(ctx, now.AddDate(0, 0, -90)); e != nil {
		t.Fatal(e)
	}
	count := func(q string) int {
		var n int
		if e := s.db.QueryRowContext(ctx, q).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	if n := count(`SELECT COUNT(*) FROM mistral_snapshots`); n != 1 {
		t.Fatalf("snapshots=%d, want the old one pruned and the latest kept", n)
	}
	if n := count(`SELECT COUNT(*) FROM mistral_quota_values WHERE snapshot_id NOT IN (SELECT id FROM mistral_snapshots)`); n != 0 {
		t.Fatalf("%d orphaned quota rows survived the prune", n)
	}
	if n := count(`SELECT COUNT(*) FROM mistral_billing WHERE snapshot_id NOT IN (SELECT id FROM mistral_snapshots)`); n != 0 {
		t.Fatalf("%d orphaned billing rows survived the prune", n)
	}
}
