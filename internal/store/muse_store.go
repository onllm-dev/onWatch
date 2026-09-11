package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

// MuseResetCycle is one Muse quota window cycle.
type MuseResetCycle struct {
	ID              int64
	QuotaName       string
	CycleStart      time.Time
	CycleEnd        *time.Time
	ResetsAt        *time.Time
	PeakUtilization float64
	TotalDelta      float64
}

// MuseLatestQuota is the newest stored value per quota window.
type MuseLatestQuota struct {
	Name        string
	Used        float64
	Limit       float64
	Utilization float64
	Format      string
	ResetsAt    *time.Time
	CapturedAt  time.Time
	Tier        string
	Model       string
}

// InsertMuseSnapshot stores a snapshot and its per-quota values atomically.
func (s *Store) InsertMuseSnapshot(snapshot *api.MuseSnapshot) (int64, error) {
	if snapshot == nil {
		return 0, fmt.Errorf("nil muse snapshot")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	var windowResetsAt, weeklyResetsAt interface{}
	if snapshot.WindowResetsAt != nil {
		windowResetsAt = snapshot.WindowResetsAt.Format(time.RFC3339Nano)
	}
	if snapshot.WeeklyResetsAt != nil {
		weeklyResetsAt = snapshot.WeeklyResetsAt.Format(time.RFC3339Nano)
	}

	result, err := tx.Exec(
		`INSERT INTO muse_snapshots (captured_at, raw_json, tier, model, window_used, window_resets_at, window_duration_mins, weekly_used, weekly_resets_at, quota_count) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		snapshot.CapturedAt.Format(time.RFC3339Nano),
		snapshot.RawJSON,
		snapshot.Tier,
		snapshot.Model,
		snapshot.WindowUsedPct,
		windowResetsAt,
		snapshot.WindowDurationMins,
		snapshot.WeeklyUsedPct,
		weeklyResetsAt,
		len(snapshot.Quotas),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to insert muse snapshot: %w", err)
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
		_, err := tx.Exec(
			`INSERT INTO muse_quota_values (snapshot_id, quota_name, used, limit_value, utilization, format, resets_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			snapshotID, q.Name, q.Used, q.Limit, q.Utilization, string(q.Format), resetsAt,
		)
		if err != nil {
			return 0, fmt.Errorf("failed to insert quota value %s: %w", q.Name, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit: %w", err)
	}

	return snapshotID, nil
}

func scanMuseSnapshotRow(snapshot *api.MuseSnapshot, capturedAt, windowResetsAt, weeklyResetsAt string, windowResetsNull, weeklyResetsNull bool) {
	snapshot.CapturedAt, _ = time.Parse(time.RFC3339Nano, capturedAt)
	if !windowResetsNull && windowResetsAt != "" {
		t, _ := time.Parse(time.RFC3339Nano, windowResetsAt)
		snapshot.WindowResetsAt = &t
	}
	if !weeklyResetsNull && weeklyResetsAt != "" {
		t, _ := time.Parse(time.RFC3339Nano, weeklyResetsAt)
		snapshot.WeeklyResetsAt = &t
	}
}

func (s *Store) queryMuseQuotaValues(snapshotID int64) ([]api.MuseQuota, error) {
	rows, err := s.db.Query(
		`SELECT quota_name, used, limit_value, utilization, format, resets_at FROM muse_quota_values WHERE snapshot_id = ? ORDER BY quota_name`,
		snapshotID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query quota values: %w", err)
	}
	defer rows.Close()

	var quotas []api.MuseQuota
	for rows.Next() {
		var q api.MuseQuota
		var format string
		var resetsAt sql.NullString
		if err := rows.Scan(&q.Name, &q.Used, &q.Limit, &q.Utilization, &format, &resetsAt); err != nil {
			return nil, fmt.Errorf("failed to scan quota value: %w", err)
		}
		q.Format = api.MuseQuotaFormat(format)
		if resetsAt.Valid && resetsAt.String != "" {
			t, _ := time.Parse(time.RFC3339Nano, resetsAt.String)
			q.ResetsAt = &t
		}
		quotas = append(quotas, q)
	}
	return quotas, rows.Err()
}

// QueryLatestMuse returns the most recent Muse snapshot with its quotas.
func (s *Store) QueryLatestMuse() (*api.MuseSnapshot, error) {
	var snapshot api.MuseSnapshot
	var capturedAt string
	var windowResetsAt, weeklyResetsAt sql.NullString

	err := s.db.QueryRow(
		`SELECT id, captured_at, tier, model, window_used, window_resets_at, window_duration_mins, weekly_used, weekly_resets_at
		FROM muse_snapshots ORDER BY captured_at DESC LIMIT 1`,
	).Scan(
		&snapshot.ID, &capturedAt, &snapshot.Tier, &snapshot.Model,
		&snapshot.WindowUsedPct, &windowResetsAt, &snapshot.WindowDurationMins,
		&snapshot.WeeklyUsedPct, &weeklyResetsAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query latest muse: %w", err)
	}

	scanMuseSnapshotRow(&snapshot, capturedAt, windowResetsAt.String, weeklyResetsAt.String, !windowResetsAt.Valid, !weeklyResetsAt.Valid)

	quotas, err := s.queryMuseQuotaValues(snapshot.ID)
	if err != nil {
		return nil, err
	}
	snapshot.Quotas = quotas

	return &snapshot, nil
}

// QueryMuseRange returns Muse snapshots within a time range with optional limit.
func (s *Store) QueryMuseRange(start, end time.Time, limit ...int) ([]*api.MuseSnapshot, error) {
	query := `SELECT id, captured_at, tier, model, window_used, window_resets_at, window_duration_mins, weekly_used, weekly_resets_at FROM muse_snapshots
		WHERE captured_at BETWEEN ? AND ? ORDER BY captured_at ASC`
	args := []interface{}{start.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano)}
	if len(limit) > 0 && limit[0] > 0 {
		query = `SELECT id, captured_at, tier, model, window_used, window_resets_at, window_duration_mins, weekly_used, weekly_resets_at
			FROM (
				SELECT id, captured_at, tier, model, window_used, window_resets_at, window_duration_mins, weekly_used, weekly_resets_at
				FROM muse_snapshots
				WHERE captured_at BETWEEN ? AND ?
				ORDER BY captured_at DESC
				LIMIT ?
			) recent
			ORDER BY captured_at ASC`
		args = append(args, limit[0])
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query muse range: %w", err)
	}
	defer rows.Close()

	var snapshots []*api.MuseSnapshot
	for rows.Next() {
		var snap api.MuseSnapshot
		var capturedAt string
		var windowResetsAt, weeklyResetsAt sql.NullString
		if err := rows.Scan(&snap.ID, &capturedAt, &snap.Tier, &snap.Model, &snap.WindowUsedPct, &windowResetsAt, &snap.WindowDurationMins, &snap.WeeklyUsedPct, &weeklyResetsAt); err != nil {
			return nil, fmt.Errorf("failed to scan muse snapshot: %w", err)
		}
		scanMuseSnapshotRow(&snap, capturedAt, windowResetsAt.String, weeklyResetsAt.String, !windowResetsAt.Valid, !weeklyResetsAt.Valid)
		snapshots = append(snapshots, &snap)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, snap := range snapshots {
		quotas, err := s.queryMuseQuotaValues(snap.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to query quota values for snapshot %d: %w", snap.ID, err)
		}
		snap.Quotas = quotas
	}

	return snapshots, nil
}

// CreateMuseCycle opens a new reset cycle for a quota window.
func (s *Store) CreateMuseCycle(quotaName string, cycleStart time.Time, resetsAt *time.Time) (int64, error) {
	var resetsAtVal interface{}
	if resetsAt != nil {
		resetsAtVal = resetsAt.Format(time.RFC3339Nano)
	}

	result, err := s.db.Exec(
		`INSERT INTO muse_reset_cycles (quota_name, cycle_start, resets_at) VALUES (?, ?, ?)`,
		quotaName, cycleStart.Format(time.RFC3339Nano), resetsAtVal,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to create muse cycle: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to get cycle ID: %w", err)
	}
	return id, nil
}

// CloseMuseCycle closes the active cycle for a quota window.
func (s *Store) CloseMuseCycle(quotaName string, cycleEnd time.Time, peak, delta float64) error {
	_, err := s.db.Exec(
		`UPDATE muse_reset_cycles SET cycle_end = ?, peak_utilization = ?, total_delta = ?
		WHERE quota_name = ? AND cycle_end IS NULL`,
		cycleEnd.Format(time.RFC3339Nano), peak, delta, quotaName,
	)
	if err != nil {
		return fmt.Errorf("failed to close muse cycle: %w", err)
	}
	return nil
}

// UpdateMuseCycle updates peak and delta of the active cycle.
func (s *Store) UpdateMuseCycle(quotaName string, peak, delta float64) error {
	_, err := s.db.Exec(
		`UPDATE muse_reset_cycles SET peak_utilization = ?, total_delta = ?
		WHERE quota_name = ? AND cycle_end IS NULL`,
		peak, delta, quotaName,
	)
	if err != nil {
		return fmt.Errorf("failed to update muse cycle: %w", err)
	}
	return nil
}

func scanMuseCycle(rows *sql.Rows) (*MuseResetCycle, error) {
	var cycle MuseResetCycle
	var cycleStart string
	var cycleEnd, resetsAt sql.NullString

	if err := rows.Scan(&cycle.ID, &cycle.QuotaName, &cycleStart, &cycleEnd, &resetsAt,
		&cycle.PeakUtilization, &cycle.TotalDelta); err != nil {
		return nil, fmt.Errorf("failed to scan muse cycle: %w", err)
	}

	cycle.CycleStart, _ = time.Parse(time.RFC3339Nano, cycleStart)
	if cycleEnd.Valid && cycleEnd.String != "" {
		t, _ := time.Parse(time.RFC3339Nano, cycleEnd.String)
		cycle.CycleEnd = &t
	}
	if resetsAt.Valid {
		rt, _ := time.Parse(time.RFC3339Nano, resetsAt.String)
		cycle.ResetsAt = &rt
	}
	return &cycle, nil
}

// QueryActiveMuseCycle returns the open cycle for a quota window, if any.
func (s *Store) QueryActiveMuseCycle(quotaName string) (*MuseResetCycle, error) {
	rows, err := s.db.Query(
		`SELECT id, quota_name, cycle_start, cycle_end, resets_at, peak_utilization, total_delta
		FROM muse_reset_cycles WHERE quota_name = ? AND cycle_end IS NULL`,
		quotaName,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query active muse cycle: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, nil
	}
	cycle, err := scanMuseCycle(rows)
	if err != nil {
		return nil, err
	}
	return cycle, rows.Err()
}

// QueryMuseCycleHistory returns closed cycles, newest first.
func (s *Store) QueryMuseCycleHistory(quotaName string, limit ...int) ([]*MuseResetCycle, error) {
	query := `SELECT id, quota_name, cycle_start, cycle_end, resets_at, peak_utilization, total_delta
		FROM muse_reset_cycles WHERE quota_name = ? AND cycle_end IS NOT NULL ORDER BY cycle_start DESC`
	args := []interface{}{quotaName}
	if len(limit) > 0 && limit[0] > 0 {
		query += ` LIMIT ?`
		args = append(args, limit[0])
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query muse cycles: %w", err)
	}
	defer rows.Close()

	var cycles []*MuseResetCycle
	for rows.Next() {
		cycle, err := scanMuseCycle(rows)
		if err != nil {
			return nil, err
		}
		cycles = append(cycles, cycle)
	}

	return cycles, rows.Err()
}

// QueryMuseCyclesSince returns closed cycles started at or after since.
func (s *Store) QueryMuseCyclesSince(quotaName string, since time.Time) ([]*MuseResetCycle, error) {
	rows, err := s.db.Query(
		`SELECT id, quota_name, cycle_start, cycle_end, resets_at, peak_utilization, total_delta
		FROM muse_reset_cycles WHERE quota_name = ? AND cycle_end IS NOT NULL AND cycle_start >= ?
		ORDER BY cycle_start DESC`,
		quotaName, since.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query muse cycles since: %w", err)
	}
	defer rows.Close()

	var cycles []*MuseResetCycle
	for rows.Next() {
		cycle, err := scanMuseCycle(rows)
		if err != nil {
			return nil, err
		}
		cycles = append(cycles, cycle)
	}

	return cycles, rows.Err()
}

// QueryMuseUtilizationSeries returns per-quota utilization points since a time.
func (s *Store) QueryMuseUtilizationSeries(quotaName string, since time.Time) ([]UtilizationPoint, error) {
	rows, err := s.db.Query(
		`SELECT s.captured_at, qv.utilization
		FROM muse_quota_values qv
		JOIN muse_snapshots s ON s.id = qv.snapshot_id
		WHERE qv.quota_name = ? AND s.captured_at >= ?
		ORDER BY s.captured_at ASC`,
		quotaName, since.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query utilization series: %w", err)
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

// QueryMuseLatestPerQuota returns the newest stored value per quota window.
func (s *Store) QueryMuseLatestPerQuota() ([]MuseLatestQuota, error) {
	rows, err := s.db.Query(`
		SELECT qv.quota_name, qv.used, qv.limit_value, qv.utilization, qv.format, qv.resets_at,
		       s.captured_at, s.tier, s.model
		FROM muse_quota_values qv
		JOIN muse_snapshots s ON s.id = qv.snapshot_id
		WHERE s.id = (SELECT MAX(id) FROM muse_snapshots)
		ORDER BY qv.quota_name ASC`)
	if err != nil {
		return nil, fmt.Errorf("failed to query latest per-quota: %w", err)
	}
	defer rows.Close()

	var results []MuseLatestQuota
	for rows.Next() {
		var name, format, tier, model string
		var used, limitValue, utilization float64
		var resetsAt sql.NullString
		var capturedAt string

		if err := rows.Scan(&name, &used, &limitValue, &utilization, &format, &resetsAt, &capturedAt, &tier, &model); err != nil {
			return nil, fmt.Errorf("failed to scan latest quota: %w", err)
		}

		q := MuseLatestQuota{
			Name:        name,
			Used:        used,
			Limit:       limitValue,
			Utilization: utilization,
			Format:      format,
			Tier:        tier,
			Model:       model,
		}
		q.CapturedAt, _ = time.Parse(time.RFC3339Nano, capturedAt)
		if resetsAt.Valid && resetsAt.String != "" {
			t, _ := time.Parse(time.RFC3339Nano, resetsAt.String)
			q.ResetsAt = &t
		}
		results = append(results, q)
	}
	return results, rows.Err()
}

// QueryAllMuseQuotaNames returns quota windows seen in reset cycles.
func (s *Store) QueryAllMuseQuotaNames() ([]string, error) {
	rows, err := s.db.Query(
		`SELECT DISTINCT quota_name FROM muse_reset_cycles ORDER BY quota_name`,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query muse quota names: %w", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("failed to scan quota name: %w", err)
		}
		names = append(names, name)
	}

	return names, rows.Err()
}

// QueryMuseCycleOverview returns active + recent cycles with peak snapshots.
func (s *Store) QueryMuseCycleOverview(groupBy string, limit int) ([]CycleOverviewRow, error) {
	if limit <= 0 {
		limit = 50
	}

	var cycles []*MuseResetCycle
	activeCycle, err := s.QueryActiveMuseCycle(groupBy)
	if err != nil {
		return nil, fmt.Errorf("store.QueryMuseCycleOverview: active: %w", err)
	}
	if activeCycle != nil {
		cycles = append(cycles, activeCycle)
		limit--
	}

	completedCycles, err := s.QueryMuseCycleHistory(groupBy, limit)
	if err != nil {
		return nil, fmt.Errorf("store.QueryMuseCycleOverview: %w", err)
	}
	cycles = append(cycles, completedCycles...)

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
			`SELECT s.id, s.captured_at FROM muse_snapshots s
			JOIN muse_quota_values qv ON qv.snapshot_id = s.id
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
			return nil, fmt.Errorf("store.QueryMuseCycleOverview: peak snapshot: %w", err)
		}

		row.PeakTime, _ = time.Parse(time.RFC3339Nano, capturedAt)

		qRows, err := s.db.Query(
			`SELECT quota_name, utilization, used, limit_value FROM muse_quota_values WHERE snapshot_id = ? ORDER BY quota_name`,
			snapshotID,
		)
		if err != nil {
			return nil, fmt.Errorf("store.QueryMuseCycleOverview: quota values: %w", err)
		}
		for qRows.Next() {
			var entry CrossQuotaEntry
			if err := qRows.Scan(&entry.Name, &entry.Percent, &entry.Value, new(float64)); err != nil {
				qRows.Close()
				return nil, fmt.Errorf("store.QueryMuseCycleOverview: scan quota: %w", err)
			}
			row.CrossQuotas = append(row.CrossQuotas, entry)
		}
		qRows.Close()

		overviewRows = append(overviewRows, row)
	}

	return overviewRows, nil
}
