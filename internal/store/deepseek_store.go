package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

// DeepSeekResetCycle represents a DeepSeek usage reset cycle.
type DeepSeekResetCycle struct {
	ID         int64
	QuotaType  string
	Currency   string
	CycleStart time.Time
	CycleEnd   *time.Time
	PeakUsage  float64
	TotalDelta float64
}

// InsertDeepSeekSnapshot inserts a DeepSeek usage snapshot.
func (s *Store) InsertDeepSeekSnapshot(snapshot *api.DeepSeekSnapshot) (int64, error) {
	isAvailable := 0
	if snapshot.IsAvailable {
		isAvailable = 1
	}

	result, err := s.db.Exec(
		`INSERT INTO deepseek_snapshots
		(captured_at, is_available, currency, total_balance, granted_balance, topped_up_balance)
		VALUES (?, ?, ?, ?, ?, ?)`,
		snapshot.CapturedAt.Format(time.RFC3339Nano),
		isAvailable,
		snapshot.Currency,
		snapshot.TotalBalance,
		snapshot.GrantedBalance,
		snapshot.ToppedUpBalance,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to insert deepseek snapshot: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to get last insert ID: %w", err)
	}

	return id, nil
}

// QueryLatestDeepSeek returns the most recent DeepSeek snapshot.
func (s *Store) QueryLatestDeepSeek() (*api.DeepSeekSnapshot, error) {
	var snapshot api.DeepSeekSnapshot
	var capturedAt string
	var isAvailable int

	err := s.db.QueryRow(
		`SELECT id, captured_at, is_available, currency, total_balance, granted_balance, topped_up_balance
		FROM deepseek_snapshots ORDER BY captured_at DESC LIMIT 1`,
	).Scan(
		&snapshot.ID, &capturedAt, &isAvailable, &snapshot.Currency,
		&snapshot.TotalBalance, &snapshot.GrantedBalance, &snapshot.ToppedUpBalance,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query latest deepseek: %w", err)
	}

	snapshot.CapturedAt, _ = time.Parse(time.RFC3339Nano, capturedAt)
	snapshot.IsAvailable = isAvailable != 0

	return &snapshot, nil
}

// QueryDeepSeekRange returns DeepSeek snapshots within a time range with optional limit.
func (s *Store) QueryDeepSeekRange(start, end time.Time, limit ...int) ([]*api.DeepSeekSnapshot, error) {
	query := `SELECT id, captured_at, is_available, currency, total_balance, granted_balance, topped_up_balance
		FROM deepseek_snapshots
		WHERE captured_at BETWEEN ? AND ?
		ORDER BY captured_at ASC`
	args := []interface{}{start.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano)}
	if len(limit) > 0 && limit[0] > 0 {
		query = `SELECT id, captured_at, is_available, currency, total_balance, granted_balance, topped_up_balance
			FROM (
				SELECT id, captured_at, is_available, currency, total_balance, granted_balance, topped_up_balance
				FROM deepseek_snapshots
				WHERE captured_at BETWEEN ? AND ?
				ORDER BY captured_at DESC
				LIMIT ?
			) recent
			ORDER BY captured_at ASC`
		args = append(args, limit[0])
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query deepseek range: %w", err)
	}
	defer rows.Close()

	var snapshots []*api.DeepSeekSnapshot
	for rows.Next() {
		var snapshot api.DeepSeekSnapshot
		var capturedAt string
		var isAvailable int

		err := rows.Scan(
			&snapshot.ID, &capturedAt, &isAvailable, &snapshot.Currency,
			&snapshot.TotalBalance, &snapshot.GrantedBalance, &snapshot.ToppedUpBalance,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan deepseek snapshot: %w", err)
		}

		snapshot.CapturedAt, _ = time.Parse(time.RFC3339Nano, capturedAt)
		snapshot.IsAvailable = isAvailable != 0
		snapshots = append(snapshots, &snapshot)
	}

	return snapshots, rows.Err()
}

// CreateDeepSeekCycle creates a new DeepSeek reset cycle.
func (s *Store) CreateDeepSeekCycle(quotaType string, currency string, cycleStart time.Time) (int64, error) {
	result, err := s.db.Exec(
		`INSERT INTO deepseek_reset_cycles (quota_type, currency, cycle_start) VALUES (?, ?, ?)`,
		quotaType, currency, cycleStart.Format(time.RFC3339Nano),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to create deepseek cycle: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to get cycle ID: %w", err)
	}

	return id, nil
}

// CloseDeepSeekCycle closes a DeepSeek reset cycle with final stats.
func (s *Store) CloseDeepSeekCycle(quotaType string, currency string, cycleEnd time.Time, peakUsage, totalDelta float64) error {
	_, err := s.db.Exec(
		`UPDATE deepseek_reset_cycles SET cycle_end = ?, peak_usage = ?, total_delta = ?
		WHERE quota_type = ? AND currency = ? AND cycle_end IS NULL`,
		cycleEnd.Format(time.RFC3339Nano), peakUsage, totalDelta, quotaType, currency,
	)
	if err != nil {
		return fmt.Errorf("failed to close deepseek cycle: %w", err)
	}
	return nil
}

// UpdateDeepSeekCycle updates the peak and delta for an active DeepSeek cycle.
func (s *Store) UpdateDeepSeekCycle(quotaType string, currency string, peakUsage, totalDelta float64) error {
	_, err := s.db.Exec(
		`UPDATE deepseek_reset_cycles SET peak_usage = ?, total_delta = ?
		WHERE quota_type = ? AND currency = ? AND cycle_end IS NULL`,
		peakUsage, totalDelta, quotaType, currency,
	)
	if err != nil {
		return fmt.Errorf("failed to update deepseek cycle: %w", err)
	}
	return nil
}

// QueryActiveDeepSeekCycle returns the active cycle for a DeepSeek quota type and currency.
func (s *Store) QueryActiveDeepSeekCycle(quotaType string, currency string) (*DeepSeekResetCycle, error) {
	var cycle DeepSeekResetCycle
	var cycleStart string
	var cycleEnd sql.NullString

	err := s.db.QueryRow(
		`SELECT id, quota_type, currency, cycle_start, cycle_end, peak_usage, total_delta
		FROM deepseek_reset_cycles WHERE quota_type = ? AND currency = ? AND cycle_end IS NULL`,
		quotaType, currency,
	).Scan(
		&cycle.ID, &cycle.QuotaType, &cycle.Currency, &cycleStart, &cycleEnd, &cycle.PeakUsage, &cycle.TotalDelta,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query active deepseek cycle: %w", err)
	}

	cycle.CycleStart, _ = time.Parse(time.RFC3339Nano, cycleStart)
	if cycleEnd.Valid {
		endTime, _ := time.Parse(time.RFC3339Nano, cycleEnd.String)
		cycle.CycleEnd = &endTime
	}

	return &cycle, nil
}

// QueryDeepSeekCycleHistory returns completed cycles for a DeepSeek quota type with optional limit.
func (s *Store) QueryDeepSeekCycleHistory(quotaType string, currency string, limit ...int) ([]*DeepSeekResetCycle, error) {
	query := `SELECT id, quota_type, currency, cycle_start, cycle_end, peak_usage, total_delta
		FROM deepseek_reset_cycles WHERE quota_type = ? AND currency = ? AND cycle_end IS NOT NULL ORDER BY cycle_start DESC`
	args := []interface{}{quotaType, currency}
	if len(limit) > 0 && limit[0] > 0 {
		query += ` LIMIT ?`
		args = append(args, limit[0])
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query deepseek cycles: %w", err)
	}
	defer rows.Close()

	var cycles []*DeepSeekResetCycle
	for rows.Next() {
		var cycle DeepSeekResetCycle
		var cycleStart, cycleEnd string

		err := rows.Scan(
			&cycle.ID, &cycle.QuotaType, &cycle.Currency, &cycleStart, &cycleEnd, &cycle.PeakUsage, &cycle.TotalDelta,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan deepseek cycle: %w", err)
		}

		cycle.CycleStart, _ = time.Parse(time.RFC3339Nano, cycleStart)
		endTime, _ := time.Parse(time.RFC3339Nano, cycleEnd)
		cycle.CycleEnd = &endTime

		cycles = append(cycles, &cycle)
	}

	return cycles, rows.Err()
}
