package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

// GeminiResetCycle represents a Gemini quota reset cycle.
type GeminiResetCycle struct {
	ID         int64
	ModelID    string
	CycleStart time.Time
	CycleEnd   *time.Time
	ResetTime  *time.Time
	PeakUsage  float64
	TotalDelta float64
}

// GeminiUsagePoint is a lightweight time+usage pair for series computation.
type GeminiUsagePoint struct {
	CapturedAt        time.Time
	RemainingFraction float64
}

func parseGeminiTime(value string, field string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to parse %s %q: %w", field, value, err)
	}
	return parsed, nil
}

// InsertGeminiSnapshot inserts a Gemini snapshot with its quota values.
func (s *Store) InsertGeminiSnapshot(snapshot *api.GeminiSnapshot) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.Exec(
		`INSERT INTO gemini_snapshots (captured_at, tier, project_id, raw_json, quota_count) VALUES (?, ?, ?, ?, ?)`,
		snapshot.CapturedAt.Format(time.RFC3339Nano),
		snapshot.Tier,
		snapshot.ProjectID,
		snapshot.RawJSON,
		len(snapshot.Quotas),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to insert gemini snapshot: %w", err)
	}

	snapshotID, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to get snapshot ID: %w", err)
	}

	for _, q := range snapshot.Quotas {
		var resetTimeVal interface{}
		if q.ResetTime != nil {
			resetTimeVal = q.ResetTime.Format(time.RFC3339Nano)
		}

		_, err := tx.Exec(
			`INSERT INTO gemini_quota_values (snapshot_id, model_id, remaining_fraction, usage_percent, reset_time)
			VALUES (?, ?, ?, ?, ?)`,
			snapshotID, q.ModelID, q.RemainingFraction, q.UsagePercent, resetTimeVal,
		)
		if err != nil {
			return 0, fmt.Errorf("failed to insert gemini quota value %s: %w", q.ModelID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit: %w", err)
	}

	return snapshotID, nil
}

// QueryLatestGemini returns the most recent Gemini snapshot with quotas.
func (s *Store) QueryLatestGemini() (*api.GeminiSnapshot, error) {
	var snapshot api.GeminiSnapshot
	var capturedAt string
	var tier, projectID sql.NullString

	err := s.db.QueryRow(
		`SELECT id, captured_at, tier, project_id, quota_count
		FROM gemini_snapshots ORDER BY captured_at DESC LIMIT 1`,
	).Scan(&snapshot.ID, &capturedAt, &tier, &projectID, new(int))

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query latest gemini: %w", err)
	}

	parsedCapturedAt, err := parseGeminiTime(capturedAt, "gemini snapshot captured_at")
	if err != nil {
		return nil, err
	}
	snapshot.CapturedAt = parsedCapturedAt
	if tier.Valid {
		snapshot.Tier = tier.String
	}
	if projectID.Valid {
		snapshot.ProjectID = projectID.String
	}

	rows, err := s.db.Query(
		`SELECT model_id, remaining_fraction, usage_percent, reset_time
		FROM gemini_quota_values WHERE snapshot_id = ? ORDER BY model_id`,
		snapshot.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query gemini quota values: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var q api.GeminiQuota
		var resetTime sql.NullString
		if err := rows.Scan(&q.ModelID, &q.RemainingFraction, &q.UsagePercent, &resetTime); err != nil {
			return nil, fmt.Errorf("failed to scan gemini quota value: %w", err)
		}
		if resetTime.Valid && resetTime.String != "" {
			parsedResetTime, err := parseGeminiTime(resetTime.String, "gemini quota reset_time")
			if err != nil {
				return nil, err
			}
			q.ResetTime = &parsedResetTime
		}
		snapshot.Quotas = append(snapshot.Quotas, q)
	}

	return &snapshot, rows.Err()
}

// QueryGeminiRange returns Gemini snapshots within a time range.
func (s *Store) QueryGeminiRange(start, end time.Time, limit ...int) ([]*api.GeminiSnapshot, error) {
	query := `SELECT id, captured_at, tier, project_id, quota_count FROM gemini_snapshots
		WHERE captured_at BETWEEN ? AND ? ORDER BY captured_at ASC`
	args := []interface{}{start.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano)}
	if len(limit) > 0 && limit[0] > 0 {
		query = `SELECT id, captured_at, tier, project_id, quota_count
			FROM (
				SELECT id, captured_at, tier, project_id, quota_count
				FROM gemini_snapshots
				WHERE captured_at BETWEEN ? AND ?
				ORDER BY captured_at DESC
				LIMIT ?
			) recent
			ORDER BY captured_at ASC`
		args = append(args, limit[0])
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query gemini range: %w", err)
	}
	defer rows.Close()

	var snapshots []*api.GeminiSnapshot
	for rows.Next() {
		var snap api.GeminiSnapshot
		var capturedAt string
		var tier, projectID sql.NullString
		if err := rows.Scan(&snap.ID, &capturedAt, &tier, &projectID, new(int)); err != nil {
			return nil, fmt.Errorf("failed to scan gemini snapshot: %w", err)
		}
		parsedCapturedAt, err := parseGeminiTime(capturedAt, "gemini snapshot captured_at")
		if err != nil {
			return nil, err
		}
		snap.CapturedAt = parsedCapturedAt
		if tier.Valid {
			snap.Tier = tier.String
		}
		if projectID.Valid {
			snap.ProjectID = projectID.String
		}
		snapshots = append(snapshots, &snap)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Load quota values for each snapshot
	for _, snap := range snapshots {
		qRows, err := s.db.Query(
			`SELECT model_id, remaining_fraction, usage_percent, reset_time
			FROM gemini_quota_values WHERE snapshot_id = ? ORDER BY model_id`,
			snap.ID,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to query gemini quota values for snapshot %d: %w", snap.ID, err)
		}
		for qRows.Next() {
			var q api.GeminiQuota
			var resetTime sql.NullString
			if err := qRows.Scan(&q.ModelID, &q.RemainingFraction, &q.UsagePercent, &resetTime); err != nil {
				qRows.Close()
				return nil, fmt.Errorf("failed to scan gemini quota value: %w", err)
			}
			if resetTime.Valid && resetTime.String != "" {
				parsedResetTime, err := parseGeminiTime(resetTime.String, "gemini quota reset_time")
				if err != nil {
					qRows.Close()
					return nil, err
				}
				q.ResetTime = &parsedResetTime
			}
			snap.Quotas = append(snap.Quotas, q)
		}
		qRows.Close()
	}

	return snapshots, nil
}

// CreateGeminiCycle creates a new Gemini reset cycle.
func (s *Store) CreateGeminiCycle(modelID string, cycleStart time.Time, resetTime *time.Time) (int64, error) {
	var resetTimeVal interface{}
	if resetTime != nil {
		resetTimeVal = resetTime.Format(time.RFC3339Nano)
	}

	result, err := s.db.Exec(
		`INSERT INTO gemini_reset_cycles (model_id, cycle_start, reset_time) VALUES (?, ?, ?)`,
		modelID,
		cycleStart.Format(time.RFC3339Nano),
		resetTimeVal,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to create gemini cycle: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to get cycle ID: %w", err)
	}
	return id, nil
}

// CloseGeminiCycle closes a Gemini reset cycle with final stats.
func (s *Store) CloseGeminiCycle(modelID string, cycleEnd time.Time, peakUsage, totalDelta float64) error {
	_, err := s.db.Exec(
		`UPDATE gemini_reset_cycles SET cycle_end = ?, peak_usage = ?, total_delta = ?
		WHERE model_id = ? AND cycle_end IS NULL`,
		cycleEnd.Format(time.RFC3339Nano),
		peakUsage,
		totalDelta,
		modelID,
	)
	if err != nil {
		return fmt.Errorf("failed to close gemini cycle: %w", err)
	}
	return nil
}

// UpdateGeminiCycle updates the peak and delta for an active Gemini cycle.
func (s *Store) UpdateGeminiCycle(modelID string, peakUsage, totalDelta float64) error {
	_, err := s.db.Exec(
		`UPDATE gemini_reset_cycles SET peak_usage = ?, total_delta = ?
		WHERE model_id = ? AND cycle_end IS NULL`,
		peakUsage,
		totalDelta,
		modelID,
	)
	if err != nil {
		return fmt.Errorf("failed to update gemini cycle: %w", err)
	}
	return nil
}

// UpdateGeminiCycleResetTime updates the reset timestamp for an active Gemini cycle.
func (s *Store) UpdateGeminiCycleResetTime(modelID string, resetTime *time.Time) error {
	var resetTimeValue interface{}
	if resetTime != nil {
		resetTimeValue = resetTime.Format(time.RFC3339Nano)
	}

	_, err := s.db.Exec(
		`UPDATE gemini_reset_cycles SET reset_time = ?
		WHERE model_id = ? AND cycle_end IS NULL`,
		resetTimeValue,
		modelID,
	)
	if err != nil {
		return fmt.Errorf("failed to update gemini cycle reset_time: %w", err)
	}
	return nil
}

// QueryActiveGeminiCycle returns the active cycle for a Gemini model.
func (s *Store) QueryActiveGeminiCycle(modelID string) (*GeminiResetCycle, error) {
	var cycle GeminiResetCycle
	var cycleStart string
	var cycleEnd, resetTime sql.NullString

	err := s.db.QueryRow(
		`SELECT id, model_id, cycle_start, cycle_end, reset_time, peak_usage, total_delta
		FROM gemini_reset_cycles WHERE model_id = ? AND cycle_end IS NULL`,
		modelID,
	).Scan(
		&cycle.ID,
		&cycle.ModelID,
		&cycleStart,
		&cycleEnd,
		&resetTime,
		&cycle.PeakUsage,
		&cycle.TotalDelta,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query active gemini cycle: %w", err)
	}

	parsedCycleStart, err := parseGeminiTime(cycleStart, "gemini cycle_start")
	if err != nil {
		return nil, err
	}
	cycle.CycleStart = parsedCycleStart
	if cycleEnd.Valid {
		parsedCycleEnd, err := parseGeminiTime(cycleEnd.String, "gemini cycle_end")
		if err != nil {
			return nil, err
		}
		cycle.CycleEnd = &parsedCycleEnd
	}
	if resetTime.Valid {
		parsedResetTime, err := parseGeminiTime(resetTime.String, "gemini cycle reset_time")
		if err != nil {
			return nil, err
		}
		cycle.ResetTime = &parsedResetTime
	}

	return &cycle, nil
}

// QueryGeminiCycleHistory returns completed cycles for a Gemini model with optional limit.
func (s *Store) QueryGeminiCycleHistory(modelID string, limit ...int) ([]*GeminiResetCycle, error) {
	query := `SELECT id, model_id, cycle_start, cycle_end, reset_time, peak_usage, total_delta
		FROM gemini_reset_cycles WHERE model_id = ? AND cycle_end IS NOT NULL ORDER BY cycle_start DESC`
	args := []interface{}{modelID}
	if len(limit) > 0 && limit[0] > 0 {
		query += ` LIMIT ?`
		args = append(args, limit[0])
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query gemini cycles: %w", err)
	}
	defer rows.Close()

	var cycles []*GeminiResetCycle
	for rows.Next() {
		var cycle GeminiResetCycle
		var cycleStart, cycleEnd string
		var resetTime sql.NullString

		if err := rows.Scan(
			&cycle.ID,
			&cycle.ModelID,
			&cycleStart,
			&cycleEnd,
			&resetTime,
			&cycle.PeakUsage,
			&cycle.TotalDelta,
		); err != nil {
			return nil, fmt.Errorf("failed to scan gemini cycle: %w", err)
		}

		parsedCycleStart, err := parseGeminiTime(cycleStart, "gemini cycle_start")
		if err != nil {
			return nil, err
		}
		cycle.CycleStart = parsedCycleStart

		parsedCycleEnd, err := parseGeminiTime(cycleEnd, "gemini cycle_end")
		if err != nil {
			return nil, err
		}
		cycle.CycleEnd = &parsedCycleEnd
		if resetTime.Valid {
			parsedResetTime, err := parseGeminiTime(resetTime.String, "gemini cycle reset_time")
			if err != nil {
				return nil, err
			}
			cycle.ResetTime = &parsedResetTime
		}

		cycles = append(cycles, &cycle)
	}

	return cycles, rows.Err()
}

// QueryGeminiUsageSeries returns per-model usage points since a given time.
func (s *Store) QueryGeminiUsageSeries(modelID string, since time.Time) ([]GeminiUsagePoint, error) {
	rows, err := s.db.Query(
		`SELECT s.captured_at, qv.remaining_fraction
		FROM gemini_quota_values qv
		JOIN gemini_snapshots s ON s.id = qv.snapshot_id
		WHERE qv.model_id = ? AND s.captured_at >= ?
		ORDER BY s.captured_at ASC`,
		modelID,
		since.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query gemini usage series: %w", err)
	}
	defer rows.Close()

	var points []GeminiUsagePoint
	for rows.Next() {
		var capturedAt string
		var remaining float64
		if err := rows.Scan(&capturedAt, &remaining); err != nil {
			return nil, fmt.Errorf("failed to scan gemini usage point: %w", err)
		}
		parsedCapturedAt, err := parseGeminiTime(capturedAt, "gemini usage captured_at")
		if err != nil {
			return nil, err
		}
		points = append(points, GeminiUsagePoint{CapturedAt: parsedCapturedAt, RemainingFraction: remaining})
	}

	return points, rows.Err()
}

// QueryGeminiCycleOverview returns Gemini cycles for a given model
// with cross-model snapshot data at the peak moment of each cycle.
func (s *Store) QueryGeminiCycleOverview(modelID string, limit int) ([]CycleOverviewRow, error) {
	if limit <= 0 {
		limit = 50
	}

	var cycles []*GeminiResetCycle
	activeCycle, err := s.QueryActiveGeminiCycle(modelID)
	if err != nil {
		return nil, fmt.Errorf("store.QueryGeminiCycleOverview: active: %w", err)
	}
	if activeCycle != nil {
		cycles = append(cycles, activeCycle)
		limit--
	}

	completedCycles, err := s.QueryGeminiCycleHistory(modelID, limit)
	if err != nil {
		return nil, fmt.Errorf("store.QueryGeminiCycleOverview: %w", err)
	}
	cycles = append(cycles, completedCycles...)

	var overviewRows []CycleOverviewRow
	for _, c := range cycles {
		row := CycleOverviewRow{
			CycleID:    c.ID,
			QuotaType:  c.ModelID,
			CycleStart: c.CycleStart,
			CycleEnd:   c.CycleEnd,
			PeakValue:  c.PeakUsage,
			TotalDelta: c.TotalDelta,
		}

		var endBoundary time.Time
		if c.CycleEnd != nil {
			endBoundary = *c.CycleEnd
		} else {
			endBoundary = time.Now().Add(time.Minute)
		}

		// Find peak snapshot
		var snapshotID int64
		var capturedAt string
		err := s.db.QueryRow(
			`SELECT s.id, s.captured_at FROM gemini_snapshots s
			JOIN gemini_quota_values qv ON qv.snapshot_id = s.id
			WHERE qv.model_id = ? AND s.captured_at >= ? AND s.captured_at < ?
			ORDER BY qv.usage_percent DESC LIMIT 1`,
			modelID,
			c.CycleStart.Format(time.RFC3339Nano),
			endBoundary.Format(time.RFC3339Nano),
		).Scan(&snapshotID, &capturedAt)

		if err == sql.ErrNoRows {
			overviewRows = append(overviewRows, row)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("store.QueryGeminiCycleOverview: peak snapshot: %w", err)
		}

		parsedPeakTime, err := parseGeminiTime(capturedAt, "gemini peak captured_at")
		if err != nil {
			return nil, fmt.Errorf("store.QueryGeminiCycleOverview: peak time: %w", err)
		}
		row.PeakTime = parsedPeakTime

		// Cross-model values at peak
		qRows, err := s.db.Query(
			`SELECT model_id, usage_percent FROM gemini_quota_values WHERE snapshot_id = ? ORDER BY model_id`,
			snapshotID,
		)
		if err != nil {
			return nil, fmt.Errorf("store.QueryGeminiCycleOverview: quota values: %w", err)
		}
		for qRows.Next() {
			var entry CrossQuotaEntry
			if err := qRows.Scan(&entry.Name, &entry.Percent); err != nil {
				qRows.Close()
				return nil, fmt.Errorf("store.QueryGeminiCycleOverview: scan quota: %w", err)
			}
			entry.Value = entry.Percent
			row.CrossQuotas = append(row.CrossQuotas, entry)
		}
		qRows.Close()

		overviewRows = append(overviewRows, row)
	}

	return overviewRows, nil
}

// QueryAllGeminiModelIDs returns all distinct model IDs from Gemini quota values.
func (s *Store) QueryAllGeminiModelIDs() ([]string, error) {
	rows, err := s.db.Query(
		`SELECT DISTINCT model_id FROM gemini_quota_values ORDER BY model_id`,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query gemini model IDs: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("failed to scan gemini model ID: %w", err)
		}
		ids = append(ids, id)
	}

	return ids, rows.Err()
}
