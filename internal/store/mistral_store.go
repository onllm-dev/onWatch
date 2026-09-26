package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

// The Mistral tables are declared with every other provider's in store.go.
const mistralActiveIdentity = `(SELECT value FROM settings WHERE key='mistral_identity')`

// Hot-path reads. LatestMistral runs on every dashboard refresh, menubar
// refresh and metrics scrape, so each statement is ordered to let SQLite walk
// an index backwards and stop at the first row. idx_mistral_snapshots is
// (identity, captured_at) and carries the rowid, so ordering by captured_at
// then id matches it; ordering by id alone forced a sort of every snapshot the
// identity had - about 28ms a call at 90 days, growing with retention.
const (
	mistralLatestSnapshotSQL = `SELECT id,identity,captured_at,status FROM mistral_snapshots WHERE identity=` + mistralActiveIdentity + ` ORDER BY captured_at DESC, id DESC LIMIT 1`
	// Driven from the billing table, which only has rows when pay-as-you-go
	// data exists, instead of from every snapshot of the identity.
	mistralLatestBillingSQL = `SELECT b.amount,b.currency,b.period_start,b.period_end,b.captured_at,b.status FROM mistral_billing b WHERE (SELECT identity FROM mistral_snapshots WHERE id=b.snapshot_id)=? ORDER BY b.snapshot_id DESC LIMIT 1`
	mistralHistoryWindowSQL = `SELECT id,identity,captured_at,status FROM mistral_snapshots WHERE identity=` + mistralActiveIdentity + ` AND captured_at BETWEEN ? AND ? ORDER BY captured_at DESC, id DESC LIMIT ?`
)

// mistralQuotaFromRow rebuilds a quota from its typed columns. NULL amounts mean
// Mistral reported only a percentage, which the API type marks as PercentOnly
// so the unknown amounts are never shown as zero.
func mistralQuotaFromRow(name string, used, limit sql.NullFloat64, utilization float64, currency string, resetsAt sql.NullString) api.MistralQuota {
	q := api.MistralQuota{Name: name, Utilization: utilization, Currency: currency}
	if used.Valid && limit.Valid {
		q.Used, q.Limit = used.Float64, limit.Float64
	} else {
		q.PercentOnly = true
	}
	if resetsAt.Valid && resetsAt.String != "" {
		if t, e := time.Parse(time.RFC3339Nano, resetsAt.String); e == nil {
			q.ResetsAt = &t
		}
	}
	return q
}

func mistralBillingFromRow(amount float64, currency, periodStart, periodEnd, capturedAt, status string) *api.MistralBilling {
	b := &api.MistralBilling{Amount: &amount, Currency: currency, Status: status}
	b.PeriodStart, _ = time.Parse(time.RFC3339Nano, periodStart)
	b.PeriodEnd, _ = time.Parse(time.RFC3339Nano, periodEnd)
	b.CapturedAt, _ = time.Parse(time.RFC3339Nano, capturedAt)
	return b
}

func (s *Store) SaveMistral(ctx context.Context, snap *api.MistralSnapshot) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	result, e := tx.ExecContext(ctx, `INSERT INTO mistral_snapshots(identity,captured_at,status) VALUES(?,?,?)`, snap.Identity, snap.CapturedAt.Format(time.RFC3339Nano), snap.Status)
	if e != nil {
		return e
	}
	id, e := result.LastInsertId()
	if e != nil {
		return e
	}
	for _, q := range snap.Quotas {
		// Percentage-only console data leaves the amounts NULL, never zero.
		var used, limit, resetsAt any
		if !q.PercentOnly {
			used, limit = q.Used, q.Limit
		}
		if q.ResetsAt != nil {
			resetsAt = q.ResetsAt.Format(time.RFC3339Nano)
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO mistral_quota_values(snapshot_id,quota_name,used,limit_value,utilization,currency,resets_at) VALUES(?,?,?,?,?,?,?)`, id, q.Name, used, limit, q.Utilization, q.Currency, resetsAt); e != nil {
			return e
		}
	}
	if b := snap.Billing; b != nil && b.Amount != nil {
		if _, e = tx.ExecContext(ctx, `INSERT INTO mistral_billing(snapshot_id,amount,currency,period_start,period_end,captured_at,status) VALUES(?,?,?,?,?,?,?)`, id, *b.Amount, b.Currency, b.PeriodStart.Format(time.RFC3339Nano), b.PeriodEnd.Format(time.RFC3339Nano), b.CapturedAt.Format(time.RFC3339Nano), b.Status); e != nil {
			return e
		}
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES('mistral_identity',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, snap.Identity); e != nil {
		return e
	}
	if e = tx.Commit(); e == nil {
		snap.ID = id
	}
	return e
}
func (s *Store) LatestMistral(ctx context.Context) (*api.MistralSnapshot, error) {
	snap := &api.MistralSnapshot{Quotas: []api.MistralQuota{}}
	var captured string
	e := s.db.QueryRowContext(ctx, mistralLatestSnapshotSQL).Scan(&snap.ID, &snap.Identity, &captured, &snap.Status)
	if e == sql.ErrNoRows {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	snap.CapturedAt, _ = time.Parse(time.RFC3339Nano, captured)
	// Each quota comes from the newest snapshot that carried it, which may not
	// be the newest snapshot overall, so capturedAt is taken per quota rather
	// than inherited from the parent - the dashboard's staleness check needs it.
	rows, e := s.db.QueryContext(ctx, `SELECT q.quota_name,q.used,q.limit_value,q.utilization,q.currency,q.resets_at,qs.captured_at FROM (SELECT 'api_included' AS name UNION ALL SELECT 'vibe_included') keys JOIN mistral_quota_values q ON q.quota_name=keys.name AND q.snapshot_id=(SELECT MAX(q2.snapshot_id) FROM mistral_quota_values q2 JOIN mistral_snapshots s ON s.id=q2.snapshot_id WHERE s.identity=? AND q2.quota_name=keys.name) JOIN mistral_snapshots qs ON qs.id=q.snapshot_id ORDER BY q.quota_name`, snap.Identity)
	if e != nil {
		return nil, e
	}
	for rows.Next() {
		var name, currency, quotaAt string
		var used, limit sql.NullFloat64
		var utilization float64
		var resetsAt sql.NullString
		if e = rows.Scan(&name, &used, &limit, &utilization, &currency, &resetsAt, &quotaAt); e != nil {
			rows.Close()
			return nil, e
		}
		q := mistralQuotaFromRow(name, used, limit, utilization, currency, resetsAt)
		q.CapturedAt, _ = time.Parse(time.RFC3339Nano, quotaAt)
		snap.Quotas = append(snap.Quotas, q)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	var amount float64
	var currency, periodStart, periodEnd, billedAt, status string
	e = s.db.QueryRowContext(ctx, mistralLatestBillingSQL, snap.Identity).Scan(&amount, &currency, &periodStart, &periodEnd, &billedAt, &status)
	if e == nil {
		snap.Billing = mistralBillingFromRow(amount, currency, periodStart, periodEnd, billedAt, status)
	} else if e != sql.ErrNoRows {
		return nil, e
	}
	return snap, nil
}

// MistralHistory returns snapshots with their quota and billing rows in a
// single cursor. A per-snapshot query loop reads through up to 2N additional
// statements that are not inside a transaction, so on the file-backed store -
// which keeps two connections - they can straddle separate WAL read snapshots
// and return partial sets while the agent is writing.
func (s *Store) MistralHistory(ctx context.Context, start, end time.Time, limit int) ([]*api.MistralSnapshot, error) {
	// The limit bounds snapshots, not joined rows, so apply it in a subquery
	// before joining. The identity filter has to stay inside it too, or it no
	// longer bounds what LIMIT sees.
	rows, e := s.db.QueryContext(ctx, `SELECT r.id,r.identity,r.captured_at,r.status,q.quota_name,q.used,q.limit_value,q.utilization,q.currency,q.resets_at,b.amount,b.currency,b.period_start,b.period_end,b.captured_at,b.status FROM (`+mistralHistoryWindowSQL+`) r LEFT JOIN mistral_quota_values q ON q.snapshot_id=r.id LEFT JOIN mistral_billing b ON b.snapshot_id=r.id ORDER BY r.id, q.quota_name`,
		start.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano), max(1, min(limit, 200)))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	result := []*api.MistralSnapshot{}
	byID := map[int64]*api.MistralSnapshot{}
	for rows.Next() {
		var id int64
		var identity, capturedAt, status string
		// Every joined column is nullable: a snapshot can have no quota rows
		// and usually has no billing row.
		var quotaName, quotaCurrency, resetsAt sql.NullString
		var used, limit, utilization sql.NullFloat64
		var amount sql.NullFloat64
		var billCurrency, periodStart, periodEnd, billedAt, billStatus sql.NullString
		if e = rows.Scan(&id, &identity, &capturedAt, &status, &quotaName, &used, &limit, &utilization, &quotaCurrency, &resetsAt, &amount, &billCurrency, &periodStart, &periodEnd, &billedAt, &billStatus); e != nil {
			return nil, e
		}
		snap, ok := byID[id]
		if !ok {
			snap = &api.MistralSnapshot{ID: id, Identity: identity, Status: status, Quotas: []api.MistralQuota{}}
			snap.CapturedAt, _ = time.Parse(time.RFC3339Nano, capturedAt)
			byID[id] = snap
			result = append(result, snap)
		}
		// mistral_billing.snapshot_id is the primary key, so billing repeats
		// across a snapshot's quota rows rather than fanning them out.
		if amount.Valid && snap.Billing == nil {
			snap.Billing = mistralBillingFromRow(amount.Float64, billCurrency.String, periodStart.String, periodEnd.String, billedAt.String, billStatus.String)
		}
		if !quotaName.Valid {
			continue
		}
		q := mistralQuotaFromRow(quotaName.String, used, limit, utilization.Float64, quotaCurrency.String, resetsAt)
		q.CapturedAt = snap.CapturedAt
		snap.Quotas = append(snap.Quotas, q)
	}
	return result, rows.Err()
}

type MistralCycle struct {
	ID              int64      `json:"id"`
	QuotaName       string     `json:"quotaName"`
	CycleStart      time.Time  `json:"cycleStart"`
	CycleEnd        *time.Time `json:"cycleEnd"`
	ResetsAt        *time.Time `json:"resetsAt"`
	PeakUtilization float64    `json:"peakUtilization"`
	TotalDelta      float64    `json:"totalDelta"`
	IsActive        bool       `json:"isActive"`
}

// TrackMistral updates persisted cycle state atomically. Only an observed period
// advance after the prior reset creates a cycle. Corrections/plan changes do not.
func (s *Store) TrackMistral(ctx context.Context, identity string, q api.MistralQuota) (bool, error) {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return false, e
	}
	defer tx.Rollback()
	var id int64
	var reset sql.NullString
	var lastUsed, lastLimit float64
	var lastAt string
	e = tx.QueryRowContext(ctx, `SELECT id,resets_at,last_used,last_limit,last_at FROM mistral_cycles WHERE identity=? AND quota_name=? AND cycle_end IS NULL`, identity, q.Name).Scan(&id, &reset, &lastUsed, &lastLimit, &lastAt)
	if e != nil && e != sql.ErrNoRows {
		return false, e
	}
	fresh := e == sql.ErrNoRows
	didReset := false
	if !fresh {
		previousAt, _ := time.Parse(time.RFC3339Nano, lastAt)
		if !q.CapturedAt.After(previousAt) {
			return false, nil
		}
		oldReset, _ := time.Parse(time.RFC3339Nano, reset.String)
		didReset = reset.Valid && q.ResetsAt != nil && !q.CapturedAt.Before(oldReset) && q.ResetsAt.After(oldReset)
		if didReset {
			if _, e = tx.ExecContext(ctx, `UPDATE mistral_cycles SET cycle_end=? WHERE id=?`, oldReset.Format(time.RFC3339Nano), id); e != nil {
				return false, e
			}
			fresh = true
		}
	}
	var resetAt any
	if q.ResetsAt != nil {
		resetAt = q.ResetsAt.Format(time.RFC3339Nano)
	}
	if fresh {
		_, e = tx.ExecContext(ctx, `INSERT INTO mistral_cycles(identity,quota_name,cycle_start,resets_at,peak,delta,last_used,last_limit,last_at) VALUES(?,?,?,?,?,0,?,?,?)`, identity, q.Name, q.CapturedAt.Format(time.RFC3339Nano), resetAt, q.Utilization, q.Used, q.Limit, q.CapturedAt.Format(time.RFC3339Nano))
	} else {
		delta := 0.0
		if q.Limit == lastLimit && q.Limit > 0 && q.Used > lastUsed {
			delta = (q.Used - lastUsed) / q.Limit * 100
		}
		_, e = tx.ExecContext(ctx, `UPDATE mistral_cycles SET peak=MAX(peak,?),delta=delta+?,last_used=?,last_limit=?,last_at=?,resets_at=COALESCE(resets_at,?) WHERE id=?`, q.Utilization, delta, q.Used, q.Limit, q.CapturedAt.Format(time.RFC3339Nano), resetAt, id)
	}
	if e != nil {
		return false, e
	}
	return didReset, tx.Commit()
}
func (s *Store) MistralCycles(ctx context.Context, name string, limit int) ([]MistralCycle, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT id,quota_name,cycle_start,cycle_end,resets_at,peak,delta FROM mistral_cycles WHERE identity=`+mistralActiveIdentity+` AND quota_name=? ORDER BY id DESC LIMIT ?`, name, max(1, min(limit, 200)))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	result := []MistralCycle{}
	for rows.Next() {
		var c MistralCycle
		var start string
		var end, reset sql.NullString
		if e = rows.Scan(&c.ID, &c.QuotaName, &start, &end, &reset, &c.PeakUtilization, &c.TotalDelta); e != nil {
			return nil, e
		}
		c.CycleStart, _ = time.Parse(time.RFC3339Nano, start)
		c.IsActive = !end.Valid
		if end.Valid {
			t, _ := time.Parse(time.RFC3339Nano, end.String)
			c.CycleEnd = &t
		}
		if reset.Valid {
			t, _ := time.Parse(time.RFC3339Nano, reset.String)
			c.ResetsAt = &t
		}
		result = append(result, c)
	}
	return result, rows.Err()
}
func (s *Store) PruneMistral(ctx context.Context, before time.Time) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	// Child rows are deleted explicitly rather than through ON DELETE CASCADE:
	// foreign_keys is a per-connection setting and the pool only enables it on
	// one of its connections, so a cascade cannot be relied on to fire. Rows it
	// missed would outlive retention indefinitely.
	//
	// The doomed set is fixed first, because the "latest per source" rules below
	// read the very tables being pruned. It lives in a temp table, which the
	// transaction's single connection owns, so a large first prune does not hit
	// SQLite's bound-variable limit.
	for i, stmt := range []string{
		`DROP TABLE IF EXISTS temp.mistral_prune`,
		// Preserve latest values per source for reconnect, even beyond retention.
		`CREATE TEMP TABLE mistral_prune AS SELECT id FROM mistral_snapshots WHERE captured_at<? AND id NOT IN (SELECT MAX(id) FROM mistral_snapshots GROUP BY identity) AND id NOT IN (SELECT MAX(q.snapshot_id) FROM mistral_quota_values q JOIN mistral_snapshots s ON s.id=q.snapshot_id GROUP BY s.identity,q.quota_name) AND id NOT IN (SELECT MAX(b.snapshot_id) FROM mistral_billing b JOIN mistral_snapshots s ON s.id=b.snapshot_id GROUP BY s.identity)`,
		`DELETE FROM mistral_quota_values WHERE snapshot_id IN (SELECT id FROM temp.mistral_prune)`,
		`DELETE FROM mistral_billing WHERE snapshot_id IN (SELECT id FROM temp.mistral_prune)`,
		`DELETE FROM mistral_snapshots WHERE id IN (SELECT id FROM temp.mistral_prune)`,
		`DROP TABLE temp.mistral_prune`,
	} {
		var args []any
		if i == 1 {
			args = []any{before.Format(time.RFC3339Nano)}
		}
		if _, e = tx.ExecContext(ctx, stmt, args...); e != nil {
			return e
		}
	}
	_, e = tx.ExecContext(ctx, `DELETE FROM mistral_cycles WHERE cycle_end IS NOT NULL AND cycle_end<?`, before.Format(time.RFC3339Nano))
	if e != nil {
		return e
	}
	return tx.Commit()
}

func (s *Store) MistralCycleOverview(ctx context.Context, name string) ([]CycleOverviewRow, error) {
	cycles, e := s.MistralCycles(ctx, name, 50)
	if e != nil {
		return nil, e
	}
	result := []CycleOverviewRow{}
	for _, c := range cycles {
		row := CycleOverviewRow{CycleID: c.ID, QuotaType: name, CycleStart: c.CycleStart, CycleEnd: c.CycleEnd, PeakValue: c.PeakUtilization, TotalDelta: c.TotalDelta, CrossQuotas: []CrossQuotaEntry{}}
		end := time.Now().Add(time.Minute)
		if c.CycleEnd != nil {
			end = *c.CycleEnd
		}
		var id int64
		var at string
		e = s.db.QueryRowContext(ctx, `SELECT s.id,s.captured_at FROM mistral_snapshots s JOIN mistral_quota_values q ON q.snapshot_id=s.id WHERE s.identity=`+mistralActiveIdentity+` AND q.quota_name=? AND s.captured_at>=? AND s.captured_at<? ORDER BY q.utilization DESC LIMIT 1`, name, c.CycleStart.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano)).Scan(&id, &at)
		if e == sql.ErrNoRows {
			result = append(result, row)
			continue
		}
		if e != nil {
			return nil, e
		}
		row.PeakTime, _ = time.Parse(time.RFC3339Nano, at)
		rows, e := s.db.QueryContext(ctx, `SELECT quota_name,used,limit_value,utilization FROM mistral_quota_values WHERE snapshot_id=? ORDER BY quota_name`, id)
		if e != nil {
			return nil, e
		}
		for rows.Next() {
			var name string
			var used, limit sql.NullFloat64
			var utilization float64
			if e = rows.Scan(&name, &used, &limit, &utilization); e != nil {
				rows.Close()
				return nil, e
			}
			row.CrossQuotas = append(row.CrossQuotas, CrossQuotaEntry{Name: name, Value: used.Float64, Limit: limit.Float64, Percent: utilization})
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
		result = append(result, row)
	}
	return result, nil
}
