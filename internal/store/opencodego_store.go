package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

// InsertOpenCodeGoSnapshot inserts a snapshot and its window values in a transaction.
func (s *Store) InsertOpenCodeGoSnapshot(snapshot *api.OpenCodeGoSnapshot) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("opencodego: begin tx: %w", err)
	}
	defer tx.Rollback()

	quotaCount := len(snapshot.Windows)
	result, err := tx.Exec(
		`INSERT INTO opencodego_snapshots (captured_at, raw_json, quota_count) VALUES (?, ?, ?)`,
		snapshot.CapturedAt.Format(time.RFC3339Nano),
		snapshot.RawJSON,
		quotaCount,
	)
	if err != nil {
		return 0, fmt.Errorf("opencodego: insert snapshot: %w", err)
	}

	snapshotID, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("opencodego: get snapshot id: %w", err)
	}
	snapshot.ID = snapshotID

	for _, w := range snapshot.Windows {
		_, err := tx.Exec(
			`INSERT INTO opencodego_usage_values (snapshot_id, window_name, usage_percent, reset_in_sec, status)
			 VALUES (?, ?, ?, ?, ?)`,
			snapshotID,
			w.WindowName,
			w.UsagePercent,
			w.ResetInSec,
			w.Status,
		)
		if err != nil {
			return 0, fmt.Errorf("opencodego: insert usage value: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("opencodego: commit tx: %w", err)
	}

	return snapshotID, nil
}

// QueryLatestOpenCodeGo returns the most recent snapshot with all window values.
func (s *Store) QueryLatestOpenCodeGo() (*api.OpenCodeGoSnapshot, error) {
	var snapshot api.OpenCodeGoSnapshot
	var capturedAt string

	err := s.db.QueryRow(
		`SELECT id, captured_at, COALESCE(raw_json, ''), quota_count
		 FROM opencodego_snapshots ORDER BY captured_at DESC LIMIT 1`,
	).Scan(&snapshot.ID, &capturedAt, &snapshot.RawJSON, new(int))

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("opencodego: query latest snapshot: %w", err)
	}

	snapshot.CapturedAt, _ = time.Parse(time.RFC3339Nano, capturedAt)

	values, err := s.queryOpenCodeGoWindowValues(snapshot.ID)
	if err != nil {
		return nil, err
	}
	snapshot.Windows = values

	return &snapshot, nil
}

// QueryOpenCodeGoRange returns snapshots within a time range with optional limit.
func (s *Store) QueryOpenCodeGoRange(start, end time.Time, limit ...int) ([]*api.OpenCodeGoSnapshot, error) {
	maxResults := 200
	if len(limit) > 0 && limit[0] > 0 && limit[0] < maxResults {
		maxResults = limit[0]
	}

	rows, err := s.db.Query(
		`SELECT id, captured_at, COALESCE(raw_json, ''), quota_count
		 FROM (
			 SELECT id, captured_at, raw_json, quota_count
			 FROM opencodego_snapshots
			 WHERE captured_at BETWEEN ? AND ?
			 ORDER BY captured_at DESC
			 LIMIT ?
		 ) recent
		 ORDER BY captured_at ASC`,
		start.Format(time.RFC3339Nano),
		end.Format(time.RFC3339Nano),
		maxResults,
	)
	if err != nil {
		return nil, fmt.Errorf("opencodego: query range: %w", err)
	}
	defer rows.Close()

	var snapshots []*api.OpenCodeGoSnapshot
	for rows.Next() {
		var snapshot api.OpenCodeGoSnapshot
		var capturedAt string
		if err := rows.Scan(&snapshot.ID, &capturedAt, &snapshot.RawJSON, new(int)); err != nil {
			return nil, fmt.Errorf("opencodego: scan snapshot: %w", err)
		}
		snapshot.CapturedAt, _ = time.Parse(time.RFC3339Nano, capturedAt)

		values, err := s.queryOpenCodeGoWindowValues(snapshot.ID)
		if err != nil {
			return nil, err
		}
		snapshot.Windows = values

		snapshots = append(snapshots, &snapshot)
	}

	return snapshots, rows.Err()
}

func (s *Store) queryOpenCodeGoWindowValues(snapshotID int64) ([]api.OpenCodeGoWindowValue, error) {
	rows, err := s.db.Query(
		`SELECT window_name, usage_percent, reset_in_sec, COALESCE(status, '')
		 FROM opencodego_usage_values WHERE snapshot_id = ?`,
		snapshotID,
	)
	if err != nil {
		return nil, fmt.Errorf("opencodego: query window values: %w", err)
	}
	defer rows.Close()

	var values []api.OpenCodeGoWindowValue
	for rows.Next() {
		var v api.OpenCodeGoWindowValue
		if err := rows.Scan(&v.WindowName, &v.UsagePercent, &v.ResetInSec, &v.Status); err != nil {
			return nil, fmt.Errorf("opencodego: scan window value: %w", err)
		}
		values = append(values, v)
	}

	return values, rows.Err()
}

// CreateOpenCodeGoCycle creates a new reset cycle for a window.
func (s *Store) CreateOpenCodeGoCycle(windowName string, cycleStart time.Time, resetsAt time.Time) (int64, error) {
	result, err := s.db.Exec(
		`INSERT INTO opencodego_reset_cycles (window_name, cycle_start, resets_at) VALUES (?, ?, ?)`,
		windowName,
		cycleStart.Format(time.RFC3339Nano),
		resetsAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return 0, fmt.Errorf("opencodego: create cycle: %w", err)
	}
	return result.LastInsertId()
}

// CloseOpenCodeGoCycle closes a reset cycle with final stats.
func (s *Store) CloseOpenCodeGoCycle(windowName string, cycleEnd time.Time, peak, delta float64) error {
	_, err := s.db.Exec(
		`UPDATE opencodego_reset_cycles SET cycle_end = ?, peak_usage = ?, total_delta = ?
		 WHERE window_name = ? AND cycle_end IS NULL`,
		cycleEnd.Format(time.RFC3339Nano), peak, delta, windowName,
	)
	return err
}

// UpdateOpenCodeGoCycle updates the peak and delta for an active cycle.
func (s *Store) UpdateOpenCodeGoCycle(windowName string, peak, delta float64) error {
	_, err := s.db.Exec(
		`UPDATE opencodego_reset_cycles SET peak_usage = ?, total_delta = ?
		 WHERE window_name = ? AND cycle_end IS NULL`,
		peak, delta, windowName,
	)
	return err
}

// QueryActiveOpenCodeGoCycle returns the active cycle for a window.
func (s *Store) QueryActiveOpenCodeGoCycle(windowName string) (*OpenCodeGoResetCycle, error) {
	var cycle OpenCodeGoResetCycle
	var cycleStart, resetsAt string

	err := s.db.QueryRow(
		`SELECT id, window_name, cycle_start, cycle_end, resets_at, peak_usage, total_delta
		 FROM opencodego_reset_cycles WHERE window_name = ? AND cycle_end IS NULL`,
		windowName,
	).Scan(&cycle.ID, &cycle.WindowName, &cycleStart, &cycle.CycleEnd, &resetsAt, &cycle.PeakUsage, &cycle.TotalDelta)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("opencodego: query active cycle: %w", err)
	}

	cycle.CycleStart, _ = time.Parse(time.RFC3339Nano, cycleStart)
	cycle.ResetsAt, _ = time.Parse(time.RFC3339Nano, resetsAt)

	return &cycle, nil
}

// QueryOpenCodeGoCycleHistory returns completed cycles for a window.
func (s *Store) QueryOpenCodeGoCycleHistory(windowName string, limit int) ([]*OpenCodeGoResetCycle, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := s.db.Query(
		`SELECT id, window_name, cycle_start, cycle_end, resets_at, peak_usage, total_delta
		 FROM opencodego_reset_cycles WHERE window_name = ? AND cycle_end IS NOT NULL
		 ORDER BY cycle_start DESC LIMIT ?`,
		windowName, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("opencodego: query cycle history: %w", err)
	}
	defer rows.Close()

	var cycles []*OpenCodeGoResetCycle
	for rows.Next() {
		var cycle OpenCodeGoResetCycle
		var cycleStart, cycleEnd, resetsAt string
		if err := rows.Scan(&cycle.ID, &cycle.WindowName, &cycleStart, &cycleEnd, &resetsAt, &cycle.PeakUsage, &cycle.TotalDelta); err != nil {
			return nil, fmt.Errorf("opencodego: scan cycle: %w", err)
		}
		cycle.CycleStart, _ = time.Parse(time.RFC3339Nano, cycleStart)
		cycle.ResetsAt, _ = time.Parse(time.RFC3339Nano, resetsAt)
		endTime, _ := time.Parse(time.RFC3339Nano, cycleEnd)
		cycle.CycleEnd = &endTime
		cycles = append(cycles, &cycle)
	}

	return cycles, rows.Err()
}

// OpenCodeGoResetCycle is a local type for reset cycle records.
type OpenCodeGoResetCycle struct {
	ID          int64
	WindowName  string
	CycleStart  time.Time
	CycleEnd    *time.Time
	ResetsAt    time.Time
	PeakUsage   float64
	TotalDelta  float64
}

// QueryOpenCodeGoUsageSeries returns usage time series for a window.
func (s *Store) QueryOpenCodeGoUsageSeries(windowName string, start, end time.Time) ([]api.OpenCodeGoUsagePoint, error) {
	rows, err := s.db.Query(
		`SELECT s.captured_at, v.usage_percent
		 FROM opencodego_snapshots s
		 JOIN opencodego_usage_values v ON v.snapshot_id = s.id
		 WHERE v.window_name = ? AND s.captured_at BETWEEN ? AND ?
		 ORDER BY s.captured_at ASC`,
		windowName,
		start.Format(time.RFC3339Nano),
		end.Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, fmt.Errorf("opencodego: query usage series: %w", err)
	}
	defer rows.Close()

	var points []api.OpenCodeGoUsagePoint
	for rows.Next() {
		var p api.OpenCodeGoUsagePoint
		var capturedAt string
		if err := rows.Scan(&capturedAt, &p.UsagePercent); err != nil {
			return nil, fmt.Errorf("opencodego: scan usage point: %w", err)
		}
		p.CapturedAt, _ = time.Parse(time.RFC3339Nano, capturedAt)
		points = append(points, p)
	}

	return points, rows.Err()
}

// QueryOpenCodeGoCycleOverview returns cycle overviews with cross-quota data.
func (s *Store) QueryOpenCodeGoCycleOverview(windowName string, limit int) ([]api.OpenCodeGoCycleOverviewRow, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := s.db.Query(
		`SELECT id, window_name, cycle_start, cycle_end, resets_at, peak_usage, total_delta
		 FROM opencodego_reset_cycles WHERE window_name = ? AND cycle_end IS NOT NULL
		 ORDER BY cycle_start DESC LIMIT ?`,
		windowName, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("opencodego: query cycle overview: %w", err)
	}
	defer rows.Close()

	var overviews []api.OpenCodeGoCycleOverviewRow
	for rows.Next() {
		var row api.OpenCodeGoCycleOverviewRow
		var cycleStart, cycleEnd, resetsAt string
		if err := rows.Scan(&row.CycleID, &row.WindowName, &cycleStart, &cycleEnd, &resetsAt, &row.PeakValue, &row.TotalDelta); err != nil {
			return nil, fmt.Errorf("opencodego: scan overview row: %w", err)
		}
		row.CycleStart, _ = time.Parse(time.RFC3339Nano, cycleStart)
		endTime, _ := time.Parse(time.RFC3339Nano, cycleEnd)
		row.CycleEnd = &endTime
		overviews = append(overviews, row)
	}

	return overviews, rows.Err()
}
