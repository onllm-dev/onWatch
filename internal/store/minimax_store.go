package store

import (
	"database/sql"
	"fmt"
	"sort"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

// MiniMaxResetCycle represents a reset cycle for one MiniMax model.
type MiniMaxResetCycle struct {
	ID         int64
	ModelName  string
	CycleStart time.Time
	CycleEnd   *time.Time
	ResetAt    *time.Time
	PeakUsed   int
	TotalDelta int
}

// MiniMaxUsagePoint is a lightweight usage point for chart/cycle computations.
type MiniMaxUsagePoint struct {
	CapturedAt time.Time
	Total      int
	Remain     int
	Used       int
}

// InsertMiniMaxSnapshot inserts a MiniMax snapshot and all model rows.
func (s *Store) InsertMiniMaxSnapshot(snapshot *api.MiniMaxSnapshot, accountID int64) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.Exec(
		`INSERT INTO minimax_snapshots (captured_at, raw_json, model_count, account_id) VALUES (?, ?, ?, ?)`,
		snapshot.CapturedAt.Format(time.RFC3339Nano),
		snapshot.RawJSON,
		len(snapshot.Models),
		accountID,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to insert minimax snapshot: %w", err)
	}

	snapshotID, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to get snapshot ID: %w", err)
	}

	for _, m := range snapshot.Models {
		var resetAtVal interface{}
		var windowStartVal interface{}
		var windowEndVal interface{}
		if m.ResetAt != nil {
			resetAtVal = m.ResetAt.Format(time.RFC3339Nano)
		}
		if m.WindowStart != nil {
			windowStartVal = m.WindowStart.Format(time.RFC3339Nano)
		}
		if m.WindowEnd != nil {
			windowEndVal = m.WindowEnd.Format(time.RFC3339Nano)
		}

		var weeklyResetAtVal, weeklyWindowStartVal, weeklyWindowEndVal interface{}
		if m.WeeklyResetAt != nil {
			weeklyResetAtVal = m.WeeklyResetAt.Format(time.RFC3339Nano)
		}
		if m.WeeklyWindowStart != nil {
			weeklyWindowStartVal = m.WeeklyWindowStart.Format(time.RFC3339Nano)
		}
		if m.WeeklyWindowEnd != nil {
			weeklyWindowEndVal = m.WeeklyWindowEnd.Format(time.RFC3339Nano)
		}

		_, err := tx.Exec(
			`INSERT INTO minimax_model_values
			(snapshot_id, model_name, total, remain, used, used_percent, reset_at, window_start, window_end,
			 weekly_total, weekly_remain, weekly_used, weekly_used_percent, weekly_reset_at, weekly_window_start, weekly_window_end)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			snapshotID,
			m.ModelName,
			m.Total,
			m.Remain,
			m.Used,
			m.UsedPercent,
			resetAtVal,
			windowStartVal,
			windowEndVal,
			m.WeeklyTotal,
			m.WeeklyRemain,
			m.WeeklyUsed,
			m.WeeklyUsedPercent,
			weeklyResetAtVal,
			weeklyWindowStartVal,
			weeklyWindowEndVal,
		)
		if err != nil {
			return 0, fmt.Errorf("failed to insert minimax model value %s: %w", m.ModelName, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit: %w", err)
	}

	return snapshotID, nil
}

// QueryLatestMiniMax returns the latest MiniMax snapshot for an account.
func (s *Store) QueryLatestMiniMax(accountID int64) (*api.MiniMaxSnapshot, error) {
	var snapshot api.MiniMaxSnapshot
	var capturedAt string
	var rawJSON sql.NullString

	err := s.db.QueryRow(
		`SELECT id, captured_at, raw_json, model_count FROM minimax_snapshots WHERE account_id = ? ORDER BY captured_at DESC LIMIT 1`,
		accountID,
	).Scan(&snapshot.ID, &capturedAt, &rawJSON, new(int))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query latest minimax: %w", err)
	}

	snapshot.CapturedAt, _ = time.Parse(time.RFC3339Nano, capturedAt)
	if rawJSON.Valid {
		snapshot.RawJSON = rawJSON.String
	}

	models, err := s.queryMiniMaxModelValues(snapshot.ID)
	if err != nil {
		return nil, err
	}
	snapshot.Models = models

	return &snapshot, nil
}

// QueryMiniMaxRange returns snapshots in a time range ordered ascending by capture time.
func (s *Store) QueryMiniMaxRange(start, end time.Time, accountID int64, limit ...int) ([]*api.MiniMaxSnapshot, error) {
	query := `SELECT id, captured_at, raw_json, model_count
		FROM minimax_snapshots
		WHERE account_id = ? AND captured_at BETWEEN ? AND ?
		ORDER BY captured_at ASC`
	args := []interface{}{accountID, start.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano)}

	if len(limit) > 0 && limit[0] > 0 {
		query = `SELECT id, captured_at, raw_json, model_count
			FROM (
				SELECT id, captured_at, raw_json, model_count
				FROM minimax_snapshots
				WHERE account_id = ? AND captured_at BETWEEN ? AND ?
				ORDER BY captured_at DESC
				LIMIT ?
			) recent
			ORDER BY captured_at ASC`
		args = []interface{}{accountID, start.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano), limit[0]}
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query minimax range: %w", err)
	}
	defer rows.Close()

	var snapshots []*api.MiniMaxSnapshot
	for rows.Next() {
		var snap api.MiniMaxSnapshot
		var capturedAt string
		var rawJSON sql.NullString
		if err := rows.Scan(&snap.ID, &capturedAt, &rawJSON, new(int)); err != nil {
			return nil, fmt.Errorf("failed to scan minimax snapshot: %w", err)
		}
		snap.CapturedAt, _ = time.Parse(time.RFC3339Nano, capturedAt)
		if rawJSON.Valid {
			snap.RawJSON = rawJSON.String
		}
		snapshots = append(snapshots, &snap)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, snap := range snapshots {
		models, err := s.queryMiniMaxModelValues(snap.ID)
		if err != nil {
			return nil, err
		}
		snap.Models = models
	}

	return snapshots, nil
}

func (s *Store) queryMiniMaxModelValues(snapshotID int64) ([]api.MiniMaxModelQuota, error) {
	rows, err := s.db.Query(
		`SELECT model_name, total, remain, used, used_percent, reset_at, window_start, window_end,
		 weekly_total, weekly_remain, weekly_used, weekly_used_percent, weekly_reset_at, weekly_window_start, weekly_window_end
		FROM minimax_model_values WHERE snapshot_id = ? ORDER BY model_name`,
		snapshotID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query minimax model values: %w", err)
	}
	defer rows.Close()

	models := make([]api.MiniMaxModelQuota, 0)
	for rows.Next() {
		var m api.MiniMaxModelQuota
		var resetAt, windowStart, windowEnd sql.NullString
		var weeklyResetAt, weeklyWindowStart, weeklyWindowEnd sql.NullString
		if err := rows.Scan(&m.ModelName, &m.Total, &m.Remain, &m.Used, &m.UsedPercent, &resetAt, &windowStart, &windowEnd,
			&m.WeeklyTotal, &m.WeeklyRemain, &m.WeeklyUsed, &m.WeeklyUsedPercent, &weeklyResetAt, &weeklyWindowStart, &weeklyWindowEnd); err != nil {
			return nil, fmt.Errorf("failed to scan minimax model value: %w", err)
		}
		if resetAt.Valid {
			t, _ := time.Parse(time.RFC3339Nano, resetAt.String)
			m.ResetAt = &t
			m.TimeUntilReset = time.Until(t)
			if m.TimeUntilReset < 0 {
				m.TimeUntilReset = 0
			}
		}
		if windowStart.Valid {
			t, _ := time.Parse(time.RFC3339Nano, windowStart.String)
			m.WindowStart = &t
		}
		if windowEnd.Valid {
			t, _ := time.Parse(time.RFC3339Nano, windowEnd.String)
			m.WindowEnd = &t
		}
		// Weekly quota fields.
		if m.WeeklyTotal > 0 || m.WeeklyUsed > 0 {
			m.HasWeeklyQuota = true
		}
		if weeklyResetAt.Valid {
			t, _ := time.Parse(time.RFC3339Nano, weeklyResetAt.String)
			m.WeeklyResetAt = &t
			m.WeeklyTimeUntilReset = time.Until(t)
			if m.WeeklyTimeUntilReset < 0 {
				m.WeeklyTimeUntilReset = 0
			}
		}
		if weeklyWindowStart.Valid {
			t, _ := time.Parse(time.RFC3339Nano, weeklyWindowStart.String)
			m.WeeklyWindowStart = &t
		}
		if weeklyWindowEnd.Valid {
			t, _ := time.Parse(time.RFC3339Nano, weeklyWindowEnd.String)
			m.WeeklyWindowEnd = &t
		}
		models = append(models, m)
	}
	return models, rows.Err()
}

// CreateMiniMaxCycle creates a new active cycle for a model.
func (s *Store) CreateMiniMaxCycle(modelName string, cycleStart time.Time, resetAt *time.Time, accountID int64) (int64, error) {
	var resetAtVal interface{}
	if resetAt != nil {
		resetAtVal = resetAt.Format(time.RFC3339Nano)
	}

	result, err := s.db.Exec(
		`INSERT INTO minimax_reset_cycles (model_name, cycle_start, reset_at, account_id) VALUES (?, ?, ?, ?)`,
		modelName,
		cycleStart.Format(time.RFC3339Nano),
		resetAtVal,
		accountID,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to create minimax cycle: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to get minimax cycle ID: %w", err)
	}
	return id, nil
}

// CloseMiniMaxCycle closes an active model cycle.
func (s *Store) CloseMiniMaxCycle(modelName string, cycleEnd time.Time, peakUsed, totalDelta int, accountID int64) error {
	_, err := s.db.Exec(
		`UPDATE minimax_reset_cycles SET cycle_end = ?, peak_used = ?, total_delta = ?
		WHERE model_name = ? AND account_id = ? AND cycle_end IS NULL`,
		cycleEnd.Format(time.RFC3339Nano), peakUsed, totalDelta, modelName, accountID,
	)
	if err != nil {
		return fmt.Errorf("failed to close minimax cycle: %w", err)
	}
	return nil
}

// UpdateMiniMaxCycle updates an active cycle's peak/delta.
func (s *Store) UpdateMiniMaxCycle(modelName string, peakUsed, totalDelta int, accountID int64) error {
	_, err := s.db.Exec(
		`UPDATE minimax_reset_cycles SET peak_used = ?, total_delta = ?
		WHERE model_name = ? AND account_id = ? AND cycle_end IS NULL`,
		peakUsed, totalDelta, modelName, accountID,
	)
	if err != nil {
		return fmt.Errorf("failed to update minimax cycle: %w", err)
	}
	return nil
}

// QueryActiveMiniMaxCycle returns the currently active cycle for a model.
func (s *Store) QueryActiveMiniMaxCycle(modelName string, accountID int64) (*MiniMaxResetCycle, error) {
	var cycle MiniMaxResetCycle
	var cycleStart string
	var cycleEnd, resetAt sql.NullString

	err := s.db.QueryRow(
		`SELECT id, model_name, cycle_start, cycle_end, reset_at, peak_used, total_delta
		FROM minimax_reset_cycles
		WHERE model_name = ? AND account_id = ? AND cycle_end IS NULL
		ORDER BY cycle_start DESC, id DESC
		LIMIT 1`,
		modelName, accountID,
	).Scan(&cycle.ID, &cycle.ModelName, &cycleStart, &cycleEnd, &resetAt, &cycle.PeakUsed, &cycle.TotalDelta)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query active minimax cycle: %w", err)
	}

	cycle.CycleStart, _ = time.Parse(time.RFC3339Nano, cycleStart)
	if cycleEnd.Valid {
		t, _ := time.Parse(time.RFC3339Nano, cycleEnd.String)
		cycle.CycleEnd = &t
	}
	if resetAt.Valid {
		t, _ := time.Parse(time.RFC3339Nano, resetAt.String)
		cycle.ResetAt = &t
	}

	return &cycle, nil
}

// QueryMiniMaxCycleHistory returns completed cycles for a model.
func (s *Store) QueryMiniMaxCycleHistory(modelName string, accountID int64, limit ...int) ([]*MiniMaxResetCycle, error) {
	query := `SELECT id, model_name, cycle_start, cycle_end, reset_at, peak_used, total_delta
		FROM minimax_reset_cycles
		WHERE model_name = ? AND account_id = ? AND cycle_end IS NOT NULL
		ORDER BY cycle_start DESC`
	args := []interface{}{modelName, accountID}
	if len(limit) > 0 && limit[0] > 0 {
		query += ` LIMIT ?`
		args = append(args, limit[0])
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query minimax cycle history: %w", err)
	}
	defer rows.Close()

	cycles := make([]*MiniMaxResetCycle, 0)
	for rows.Next() {
		var c MiniMaxResetCycle
		var cycleStart string
		var cycleEnd sql.NullString
		var resetAt sql.NullString
		if err := rows.Scan(&c.ID, &c.ModelName, &cycleStart, &cycleEnd, &resetAt, &c.PeakUsed, &c.TotalDelta); err != nil {
			return nil, fmt.Errorf("failed to scan minimax cycle: %w", err)
		}
		c.CycleStart, _ = time.Parse(time.RFC3339Nano, cycleStart)
		if cycleEnd.Valid {
			t, _ := time.Parse(time.RFC3339Nano, cycleEnd.String)
			c.CycleEnd = &t
		}
		if resetAt.Valid {
			t, _ := time.Parse(time.RFC3339Nano, resetAt.String)
			c.ResetAt = &t
		}
		cycles = append(cycles, &c)
	}

	return cycles, rows.Err()
}

// QueryMiniMaxUsageSeries returns usage points for one model since time `since`.
func (s *Store) QueryMiniMaxUsageSeries(modelName string, since time.Time, accountID int64) ([]MiniMaxUsagePoint, error) {
	rows, err := s.db.Query(
		`SELECT s.captured_at, mv.total, mv.remain, mv.used
		FROM minimax_model_values mv
		JOIN minimax_snapshots s ON s.id = mv.snapshot_id
		WHERE mv.model_name = ? AND s.account_id = ? AND s.captured_at >= ?
		ORDER BY s.captured_at ASC`,
		modelName,
		accountID,
		since.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query minimax usage series: %w", err)
	}
	defer rows.Close()

	points := make([]MiniMaxUsagePoint, 0)
	for rows.Next() {
		var p MiniMaxUsagePoint
		var capturedAt string
		if err := rows.Scan(&capturedAt, &p.Total, &p.Remain, &p.Used); err != nil {
			return nil, fmt.Errorf("failed to scan minimax usage point: %w", err)
		}
		p.CapturedAt, _ = time.Parse(time.RFC3339Nano, capturedAt)
		points = append(points, p)
	}

	return points, rows.Err()
}

// QueryAllMiniMaxModelNames returns distinct model names seen in MiniMax snapshots for an account.
func (s *Store) QueryAllMiniMaxModelNames(accountID int64) ([]string, error) {
	rows, err := s.db.Query(
		`SELECT DISTINCT mv.model_name
		FROM minimax_model_values mv
		JOIN minimax_snapshots s ON s.id = mv.snapshot_id
		WHERE s.account_id = ?
		ORDER BY mv.model_name`,
		accountID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query minimax model names: %w", err)
	}
	defer rows.Close()

	models := make([]string, 0)
	for rows.Next() {
		var model string
		if err := rows.Scan(&model); err != nil {
			return nil, fmt.Errorf("failed to scan minimax model name: %w", err)
		}
		models = append(models, model)
	}
	return models, rows.Err()
}

func (s *Store) queryMiniMaxSnapshotAtOrBefore(t time.Time, accountID int64) (*api.MiniMaxSnapshot, error) {
	var snap api.MiniMaxSnapshot
	var capturedAt string
	var rawJSON sql.NullString

	err := s.db.QueryRow(
		`SELECT id, captured_at, raw_json, model_count
		FROM minimax_snapshots
		WHERE account_id = ? AND captured_at <= ?
		ORDER BY captured_at DESC
		LIMIT 1`,
		accountID, t.UTC().Format(time.RFC3339Nano),
	).Scan(&snap.ID, &capturedAt, &rawJSON, new(int))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query minimax snapshot at time: %w", err)
	}

	snap.CapturedAt, _ = time.Parse(time.RFC3339Nano, capturedAt)
	if rawJSON.Valid {
		snap.RawJSON = rawJSON.String
	}
	models, err := s.queryMiniMaxModelValues(snap.ID)
	if err != nil {
		return nil, err
	}
	snap.Models = models
	return &snap, nil
}

// QueryMiniMaxCycleOverview returns overview rows with cross-model values at cycle peak/end times.
func (s *Store) QueryMiniMaxCycleOverview(groupBy string, limit int, accountID int64) ([]CycleOverviewRow, error) {
	if groupBy == "" {
		models, err := s.QueryAllMiniMaxModelNames(accountID)
		if err != nil {
			return nil, err
		}
		if len(models) == 0 {
			return nil, nil
		}
		groupBy = models[0]
	}

	rows := make([]CycleOverviewRow, 0)

	if active, err := s.QueryActiveMiniMaxCycle(groupBy, accountID); err == nil && active != nil {
		refTime := active.CycleStart
		if active.ResetAt != nil {
			refTime = *active.ResetAt
		}
		cross, crossErr := s.minimaxCrossQuotasAt(refTime, accountID)
		if crossErr != nil {
			return nil, crossErr
		}
		rows = append(rows, CycleOverviewRow{
			CycleID:     active.ID,
			QuotaType:   active.ModelName,
			CycleStart:  active.CycleStart,
			CycleEnd:    nil,
			PeakValue:   float64(active.PeakUsed),
			TotalDelta:  float64(active.TotalDelta),
			PeakTime:    refTime,
			CrossQuotas: cross,
		})
	}

	history, err := s.QueryMiniMaxCycleHistory(groupBy, accountID, limit)
	if err != nil {
		return nil, err
	}
	for _, cycle := range history {
		refTime := cycle.CycleStart
		if cycle.CycleEnd != nil {
			refTime = *cycle.CycleEnd
		}
		cross, crossErr := s.minimaxCrossQuotasAt(refTime, accountID)
		if crossErr != nil {
			return nil, crossErr
		}
		rows = append(rows, CycleOverviewRow{
			CycleID:     cycle.ID,
			QuotaType:   cycle.ModelName,
			CycleStart:  cycle.CycleStart,
			CycleEnd:    cycle.CycleEnd,
			PeakValue:   float64(cycle.PeakUsed),
			TotalDelta:  float64(cycle.TotalDelta),
			PeakTime:    refTime,
			CrossQuotas: cross,
		})
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].CycleEnd == nil && rows[j].CycleEnd != nil {
			return true
		}
		if rows[i].CycleEnd != nil && rows[j].CycleEnd == nil {
			return false
		}
		return rows[i].CycleStart.After(rows[j].CycleStart)
	})
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}

	return rows, nil
}

func (s *Store) minimaxCrossQuotasAt(referenceTime time.Time, accountID int64) ([]CrossQuotaEntry, error) {
	snap, err := s.queryMiniMaxSnapshotAtOrBefore(referenceTime, accountID)
	if err != nil {
		return nil, err
	}
	if snap == nil {
		snap, err = s.QueryLatestMiniMax(accountID)
		if err != nil {
			return nil, err
		}
	}
	if snap == nil || len(snap.Models) == 0 {
		return nil, nil
	}

	entries := make([]CrossQuotaEntry, 0, len(snap.Models)*2)
	for _, model := range snap.Models {
		entries = append(entries, CrossQuotaEntry{
			Name:         model.ModelName,
			Value:        float64(model.Used),
			Limit:        float64(model.Total),
			Percent:      model.UsedPercent,
			StartPercent: 0,
			Delta:        model.UsedPercent,
		})
		if model.HasWeeklyQuota && (model.WeeklyTotal > 0 || model.WeeklyUsed > 0) {
			entries = append(entries, CrossQuotaEntry{
				Name:         "weekly_" + model.ModelName,
				Value:        float64(model.WeeklyUsed),
				Limit:        float64(model.WeeklyTotal),
				Percent:      model.WeeklyUsedPercent,
				StartPercent: 0,
				Delta:        model.WeeklyUsedPercent,
			})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries, nil
}
