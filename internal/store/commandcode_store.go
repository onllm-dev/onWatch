package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

// CommandCodeResetCycle is one Command Code quota window cycle.
type CommandCodeResetCycle struct {
	ID              int64
	QuotaName       string
	CycleStart      time.Time
	CycleEnd        *time.Time
	ResetsAt        *time.Time
	PeakUtilization float64
	TotalDelta      float64
}

// CommandCodeLatestQuota is the newest stored value per quota window.
type CommandCodeLatestQuota struct {
	Name        string
	Used        float64
	Limit       float64
	Utilization float64
	Format      string
	ResetsAt    *time.Time
	Remaining   float64
	CapturedAt  time.Time
	Plan        string
	AccountName string
}

// InsertCommandCodeSnapshot stores a snapshot and its per-quota values
// atomically.
func (s *Store) InsertCommandCodeSnapshot(snapshot *api.CommandCodeSnapshot) (int64, error) {
	if snapshot == nil {
		return 0, fmt.Errorf("nil commandcode snapshot")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	var periodStart, periodEnd interface{}
	if snapshot.PeriodStart != nil {
		periodStart = snapshot.PeriodStart.Format(time.RFC3339Nano)
	}
	if snapshot.PeriodEnd != nil {
		periodEnd = snapshot.PeriodEnd.Format(time.RFC3339Nano)
	}

	result, err := tx.Exec(
		`INSERT INTO commandcode_snapshots (
			captured_at, raw_json, account_name, account_id, org_id, plan, status,
			monthly_credits, purchased_credits, free_credits, remaining_credits,
			period_start, period_end, period_cost, period_requests, period_tokens, quota_count
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		snapshot.CapturedAt.Format(time.RFC3339Nano),
		snapshot.RawJSON,
		snapshot.AccountName,
		snapshot.AccountID,
		snapshot.OrgID,
		snapshot.Plan,
		snapshot.Status,
		snapshot.MonthlyCredits,
		snapshot.PurchasedCredits,
		snapshot.FreeCredits,
		snapshot.RemainingCredits,
		periodStart,
		periodEnd,
		snapshot.PeriodCostUSD,
		snapshot.PeriodReqs,
		snapshot.PeriodTokens,
		len(snapshot.Quotas),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to insert commandcode snapshot: %w", err)
	}

	snapshotID, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to get snapshot ID: %w", err)
	}

	for _, q := range snapshot.Quotas {
		var resetsAt interface{}
		if q.ResetsAt != nil {
			resetsAt = q.ResetsAt.Format(time.RFC3339Nano)
		}
		if _, err := tx.Exec(
			`INSERT INTO commandcode_quota_values (snapshot_id, quota_name, used, limit_value, utilization, format, resets_at, remaining) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			snapshotID, q.Name, q.Used, q.Limit, q.Utilization, string(q.Format), resetsAt, q.Remaining,
		); err != nil {
			return 0, fmt.Errorf("failed to insert quota value %s: %w", q.Name, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit: %w", err)
	}

	return snapshotID, nil
}

func scanCommandCodePeriod(capturedAt string, periodStart, periodEnd sql.NullString) (time.Time, *time.Time, *time.Time) {
	at, _ := time.Parse(time.RFC3339Nano, capturedAt)
	var start, end *time.Time
	if periodStart.Valid && periodStart.String != "" {
		if t, err := time.Parse(time.RFC3339Nano, periodStart.String); err == nil {
			start = &t
		}
	}
	if periodEnd.Valid && periodEnd.String != "" {
		if t, err := time.Parse(time.RFC3339Nano, periodEnd.String); err == nil {
			end = &t
		}
	}
	return at, start, end
}

func (s *Store) queryCommandCodeQuotaValues(snapshotID int64) ([]api.CommandCodeQuota, error) {
	rows, err := s.db.Query(
		`SELECT quota_name, used, limit_value, utilization, format, resets_at, remaining FROM commandcode_quota_values WHERE snapshot_id = ? ORDER BY quota_name`,
		snapshotID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query commandcode quota values: %w", err)
	}
	defer rows.Close()

	var quotas []api.CommandCodeQuota
	for rows.Next() {
		var q api.CommandCodeQuota
		var format string
		var resetsAt sql.NullString
		if err := rows.Scan(&q.Name, &q.Used, &q.Limit, &q.Utilization, &format, &resetsAt, &q.Remaining); err != nil {
			return nil, fmt.Errorf("failed to scan commandcode quota value: %w", err)
		}
		q.Format = api.CommandCodeQuotaFormat(format)
		if resetsAt.Valid && resetsAt.String != "" {
			if t, err := time.Parse(time.RFC3339Nano, resetsAt.String); err == nil {
				q.ResetsAt = &t
			}
		}
		quotas = append(quotas, q)
	}
	return quotas, rows.Err()
}

// QueryLatestCommandCode returns the most recent snapshot with its quotas.
func (s *Store) QueryLatestCommandCode() (*api.CommandCodeSnapshot, error) {
	var snapshot api.CommandCodeSnapshot
	var capturedAt string
	var periodStart, periodEnd sql.NullString

	err := s.db.QueryRow(
		`SELECT id, captured_at, account_name, account_id, org_id, plan, status,
		        monthly_credits, purchased_credits, free_credits, remaining_credits,
		        period_start, period_end, period_cost, period_requests, period_tokens
		FROM commandcode_snapshots ORDER BY captured_at DESC LIMIT 1`,
	).Scan(
		&snapshot.ID, &capturedAt, &snapshot.AccountName, &snapshot.AccountID, &snapshot.OrgID,
		&snapshot.Plan, &snapshot.Status,
		&snapshot.MonthlyCredits, &snapshot.PurchasedCredits, &snapshot.FreeCredits,
		&snapshot.RemainingCredits, &periodStart, &periodEnd,
		&snapshot.PeriodCostUSD, &snapshot.PeriodReqs, &snapshot.PeriodTokens,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query latest commandcode: %w", err)
	}

	snapshot.CapturedAt, snapshot.PeriodStart, snapshot.PeriodEnd = scanCommandCodePeriod(capturedAt, periodStart, periodEnd)

	quotas, err := s.queryCommandCodeQuotaValues(snapshot.ID)
	if err != nil {
		return nil, err
	}
	snapshot.Quotas = quotas

	return &snapshot, nil
}

// QueryCommandCodeRange returns snapshots within a time range with an optional
// row cap. The cap keeps the newest rows, like the other providers.
func (s *Store) QueryCommandCodeRange(start, end time.Time, limit ...int) ([]*api.CommandCodeSnapshot, error) {
	const columns = `id, captured_at, account_name, account_id, org_id, plan, status,
		monthly_credits, purchased_credits, free_credits, remaining_credits,
		period_start, period_end, period_cost, period_requests, period_tokens`

	query := `SELECT ` + columns + ` FROM commandcode_snapshots
		WHERE captured_at BETWEEN ? AND ? ORDER BY captured_at ASC`
	args := []interface{}{start.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano)}
	if len(limit) > 0 && limit[0] > 0 {
		query = `SELECT ` + columns + ` FROM (
				SELECT ` + columns + ` FROM commandcode_snapshots
				WHERE captured_at BETWEEN ? AND ?
				ORDER BY captured_at DESC
				LIMIT ?
			) recent
			ORDER BY captured_at ASC`
		args = append(args, limit[0])
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query commandcode range: %w", err)
	}
	defer rows.Close()

	var snapshots []*api.CommandCodeSnapshot
	for rows.Next() {
		var snap api.CommandCodeSnapshot
		var capturedAt string
		var periodStart, periodEnd sql.NullString
		if err := rows.Scan(
			&snap.ID, &capturedAt, &snap.AccountName, &snap.AccountID, &snap.OrgID,
			&snap.Plan, &snap.Status,
			&snap.MonthlyCredits, &snap.PurchasedCredits, &snap.FreeCredits,
			&snap.RemainingCredits, &periodStart, &periodEnd,
			&snap.PeriodCostUSD, &snap.PeriodReqs, &snap.PeriodTokens,
		); err != nil {
			return nil, fmt.Errorf("failed to scan commandcode snapshot: %w", err)
		}
		snap.CapturedAt, snap.PeriodStart, snap.PeriodEnd = scanCommandCodePeriod(capturedAt, periodStart, periodEnd)
		snapshots = append(snapshots, &snap)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, snap := range snapshots {
		quotas, err := s.queryCommandCodeQuotaValues(snap.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to query quota values for snapshot %d: %w", snap.ID, err)
		}
		snap.Quotas = quotas
	}

	return snapshots, nil
}

// CreateCommandCodeCycle opens a new reset cycle for a quota window.
func (s *Store) CreateCommandCodeCycle(quotaName string, cycleStart time.Time, resetsAt *time.Time) (int64, error) {
	var resetsAtVal interface{}
	if resetsAt != nil {
		resetsAtVal = resetsAt.Format(time.RFC3339Nano)
	}

	result, err := s.db.Exec(
		`INSERT INTO commandcode_reset_cycles (quota_name, cycle_start, resets_at) VALUES (?, ?, ?)`,
		quotaName, cycleStart.Format(time.RFC3339Nano), resetsAtVal,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to create commandcode cycle: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to get cycle ID: %w", err)
	}
	return id, nil
}

// CloseCommandCodeCycle closes the active cycle for a quota window.
func (s *Store) CloseCommandCodeCycle(quotaName string, cycleEnd time.Time, peak, delta float64) error {
	_, err := s.db.Exec(
		`UPDATE commandcode_reset_cycles SET cycle_end = ?, peak_utilization = ?, total_delta = ?
		WHERE quota_name = ? AND cycle_end IS NULL`,
		cycleEnd.Format(time.RFC3339Nano), peak, delta, quotaName,
	)
	if err != nil {
		return fmt.Errorf("failed to close commandcode cycle: %w", err)
	}
	return nil
}

// UpdateCommandCodeCycle updates peak and delta of the active cycle.
func (s *Store) UpdateCommandCodeCycle(quotaName string, peak, delta float64) error {
	_, err := s.db.Exec(
		`UPDATE commandcode_reset_cycles SET peak_utilization = ?, total_delta = ?
		WHERE quota_name = ? AND cycle_end IS NULL`,
		peak, delta, quotaName,
	)
	if err != nil {
		return fmt.Errorf("failed to update commandcode cycle: %w", err)
	}
	return nil
}

func scanCommandCodeCycle(rows *sql.Rows) (*CommandCodeResetCycle, error) {
	var cycle CommandCodeResetCycle
	var cycleStart string
	var cycleEnd, resetsAt sql.NullString

	if err := rows.Scan(&cycle.ID, &cycle.QuotaName, &cycleStart, &cycleEnd, &resetsAt,
		&cycle.PeakUtilization, &cycle.TotalDelta); err != nil {
		return nil, fmt.Errorf("failed to scan commandcode cycle: %w", err)
	}

	cycle.CycleStart, _ = time.Parse(time.RFC3339Nano, cycleStart)
	if cycleEnd.Valid && cycleEnd.String != "" {
		if t, err := time.Parse(time.RFC3339Nano, cycleEnd.String); err == nil {
			cycle.CycleEnd = &t
		}
	}
	if resetsAt.Valid && resetsAt.String != "" {
		if t, err := time.Parse(time.RFC3339Nano, resetsAt.String); err == nil {
			cycle.ResetsAt = &t
		}
	}
	return &cycle, nil
}

// QueryActiveCommandCodeCycle returns the open cycle for a quota window.
func (s *Store) QueryActiveCommandCodeCycle(quotaName string) (*CommandCodeResetCycle, error) {
	rows, err := s.db.Query(
		`SELECT id, quota_name, cycle_start, cycle_end, resets_at, peak_utilization, total_delta
		FROM commandcode_reset_cycles WHERE quota_name = ? AND cycle_end IS NULL`,
		quotaName,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query active commandcode cycle: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, rows.Err()
	}
	cycle, err := scanCommandCodeCycle(rows)
	if err != nil {
		return nil, err
	}
	return cycle, rows.Err()
}

// QueryCommandCodeCycleHistory returns closed cycles, newest first.
func (s *Store) QueryCommandCodeCycleHistory(quotaName string, limit ...int) ([]*CommandCodeResetCycle, error) {
	query := `SELECT id, quota_name, cycle_start, cycle_end, resets_at, peak_utilization, total_delta
		FROM commandcode_reset_cycles WHERE quota_name = ? AND cycle_end IS NOT NULL ORDER BY cycle_start DESC`
	args := []interface{}{quotaName}
	if len(limit) > 0 && limit[0] > 0 {
		query += ` LIMIT ?`
		args = append(args, limit[0])
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query commandcode cycles: %w", err)
	}
	defer rows.Close()

	var cycles []*CommandCodeResetCycle
	for rows.Next() {
		cycle, err := scanCommandCodeCycle(rows)
		if err != nil {
			return nil, err
		}
		cycles = append(cycles, cycle)
	}

	return cycles, rows.Err()
}

// QueryCommandCodeUtilizationSeries returns utilization points since a time.
func (s *Store) QueryCommandCodeUtilizationSeries(quotaName string, since time.Time) ([]UtilizationPoint, error) {
	rows, err := s.db.Query(
		`SELECT s.captured_at, qv.utilization
		FROM commandcode_quota_values qv
		JOIN commandcode_snapshots s ON s.id = qv.snapshot_id
		WHERE qv.quota_name = ? AND s.captured_at >= ?
		ORDER BY s.captured_at ASC`,
		quotaName, since.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query commandcode utilization series: %w", err)
	}
	defer rows.Close()

	var points []UtilizationPoint
	for rows.Next() {
		var capturedAt string
		var util float64
		if err := rows.Scan(&capturedAt, &util); err != nil {
			return nil, fmt.Errorf("failed to scan utilization point: %w", err)
		}
		t, _ := time.Parse(time.RFC3339Nano, capturedAt)
		points = append(points, UtilizationPoint{CapturedAt: t, Utilization: util})
	}

	return points, rows.Err()
}

// QueryCommandCodeLatestPerQuota returns the newest stored value per window,
// so one window that stops being reported does not blank the others.
func (s *Store) QueryCommandCodeLatestPerQuota() ([]CommandCodeLatestQuota, error) {
	rows, err := s.db.Query(`
		SELECT qv.quota_name, qv.used, qv.limit_value, qv.utilization, qv.format, qv.resets_at,
		       qv.remaining, s.captured_at, s.plan, s.account_name
		FROM commandcode_quota_values qv
		JOIN commandcode_snapshots s ON s.id = qv.snapshot_id
		WHERE s.id = (SELECT MAX(id) FROM commandcode_snapshots)
		ORDER BY qv.quota_name ASC`)
	if err != nil {
		return nil, fmt.Errorf("failed to query latest commandcode per-quota: %w", err)
	}
	defer rows.Close()

	var results []CommandCodeLatestQuota
	for rows.Next() {
		var q CommandCodeLatestQuota
		var resetsAt sql.NullString
		var capturedAt string
		if err := rows.Scan(&q.Name, &q.Used, &q.Limit, &q.Utilization, &q.Format,
			&resetsAt, &q.Remaining, &capturedAt, &q.Plan, &q.AccountName); err != nil {
			return nil, fmt.Errorf("failed to scan latest commandcode quota: %w", err)
		}
		q.CapturedAt, _ = time.Parse(time.RFC3339Nano, capturedAt)
		if resetsAt.Valid && resetsAt.String != "" {
			if t, err := time.Parse(time.RFC3339Nano, resetsAt.String); err == nil {
				q.ResetsAt = &t
			}
		}
		results = append(results, q)
	}
	return results, rows.Err()
}

// QueryAllCommandCodeQuotaNames returns every quota window seen in cycles.
func (s *Store) QueryAllCommandCodeQuotaNames() ([]string, error) {
	rows, err := s.db.Query(
		`SELECT DISTINCT quota_name FROM commandcode_reset_cycles ORDER BY quota_name`,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query commandcode quota names: %w", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("failed to scan commandcode quota name: %w", err)
		}
		names = append(names, name)
	}

	return names, rows.Err()
}

// QueryCommandCodeCycleOverview returns active plus recent cycles with the
// peak snapshot of each.
func (s *Store) QueryCommandCodeCycleOverview(groupBy string, limit int) ([]CycleOverviewRow, error) {
	if limit <= 0 {
		limit = 50
	}

	var cycles []*CommandCodeResetCycle
	activeCycle, err := s.QueryActiveCommandCodeCycle(groupBy)
	if err != nil {
		return nil, fmt.Errorf("store.QueryCommandCodeCycleOverview: active: %w", err)
	}
	if activeCycle != nil {
		cycles = append(cycles, activeCycle)
		limit--
	}

	// A limit fully consumed by the active cycle means no history at all.
	// Passing 0 through would drop the LIMIT clause and return every cycle ever
	// recorded, which is the opposite of the cap requested.
	if limit > 0 {
		completedCycles, err := s.QueryCommandCodeCycleHistory(groupBy, limit)
		if err != nil {
			return nil, fmt.Errorf("store.QueryCommandCodeCycleOverview: %w", err)
		}
		cycles = append(cycles, completedCycles...)
	}

	var overviewRows []CycleOverviewRow
	for _, c := range cycles {
		row := CycleOverviewRow{
			CycleID:    c.ID,
			QuotaType:  c.QuotaName,
			CycleStart: c.CycleStart,
			CycleEnd:   c.CycleEnd,
			PeakValue:  c.PeakUtilization,
			TotalDelta: c.TotalDelta,
		}

		var endBoundary time.Time
		if c.CycleEnd != nil {
			endBoundary = *c.CycleEnd
		} else {
			endBoundary = time.Now().Add(time.Minute)
		}

		var snapshotID int64
		var capturedAt string
		err := s.db.QueryRow(
			`SELECT s.id, s.captured_at FROM commandcode_snapshots s
			JOIN commandcode_quota_values qv ON qv.snapshot_id = s.id
			WHERE qv.quota_name = ? AND s.captured_at >= ? AND s.captured_at < ?
			ORDER BY qv.utilization DESC LIMIT 1`,
			groupBy,
			c.CycleStart.Format(time.RFC3339Nano),
			endBoundary.Format(time.RFC3339Nano),
		).Scan(&snapshotID, &capturedAt)

		if err == sql.ErrNoRows {
			overviewRows = append(overviewRows, row)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("store.QueryCommandCodeCycleOverview: peak snapshot: %w", err)
		}

		row.PeakTime, _ = time.Parse(time.RFC3339Nano, capturedAt)

		qRows, err := s.db.Query(
			`SELECT quota_name, utilization, used, limit_value FROM commandcode_quota_values WHERE snapshot_id = ? ORDER BY quota_name`,
			snapshotID,
		)
		if err != nil {
			return nil, fmt.Errorf("store.QueryCommandCodeCycleOverview: quota values: %w", err)
		}
		for qRows.Next() {
			var entry CrossQuotaEntry
			if err := qRows.Scan(&entry.Name, &entry.Percent, &entry.Value, new(float64)); err != nil {
				qRows.Close()
				return nil, fmt.Errorf("store.QueryCommandCodeCycleOverview: scan quota: %w", err)
			}
			row.CrossQuotas = append(row.CrossQuotas, entry)
		}
		qRows.Close()

		overviewRows = append(overviewRows, row)
	}

	return overviewRows, nil
}
