package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

// MoonshotResetCycle represents a Moonshot usage reset cycle.
type MoonshotResetCycle struct {
	ID         int64
	QuotaType  string
	CycleStart time.Time
	CycleEnd   *time.Time
	PeakUsage  float64
	TotalDelta float64
}

// InsertMoonshotSnapshot inserts a Moonshot usage snapshot.
func (s *Store) InsertMoonshotSnapshot(snapshot *api.MoonshotSnapshot) (int64, error) {
	result, err := s.db.Exec(
		`INSERT INTO moonshot_snapshots
		(captured_at, available_balance, voucher_balance, cash_balance)
		VALUES (?, ?, ?, ?)`,
		snapshot.CapturedAt.Format(time.RFC3339Nano),
		snapshot.AvailableBalance,
		snapshot.VoucherBalance,
		snapshot.CashBalance,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to insert moonshot snapshot: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to get last insert ID: %w", err)
	}

	return id, nil
}

// QueryLatestMoonshot returns the most recent Moonshot snapshot.
func (s *Store) QueryLatestMoonshot() (*api.MoonshotSnapshot, error) {
	var snapshot api.MoonshotSnapshot
	var capturedAt string

	err := s.db.QueryRow(
		`SELECT id, captured_at, available_balance, voucher_balance, cash_balance
		FROM moonshot_snapshots ORDER BY captured_at DESC LIMIT 1`,
	).Scan(
		&snapshot.ID, &capturedAt, &snapshot.AvailableBalance, &snapshot.VoucherBalance, &snapshot.CashBalance,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query latest moonshot: %w", err)
	}

	snapshot.CapturedAt, _ = time.Parse(time.RFC3339Nano, capturedAt)

	return &snapshot, nil
}

// QueryMoonshotRange returns Moonshot snapshots within a time range with optional limit.
func (s *Store) QueryMoonshotRange(start, end time.Time, limit ...int) ([]*api.MoonshotSnapshot, error) {
	query := `SELECT id, captured_at, available_balance, voucher_balance, cash_balance
		FROM moonshot_snapshots
		WHERE captured_at BETWEEN ? AND ?
		ORDER BY captured_at ASC`
	args := []interface{}{start.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano)}
	if len(limit) > 0 && limit[0] > 0 {
		query = `SELECT id, captured_at, available_balance, voucher_balance, cash_balance
			FROM (
				SELECT id, captured_at, available_balance, voucher_balance, cash_balance
				FROM moonshot_snapshots
				WHERE captured_at BETWEEN ? AND ?
				ORDER BY captured_at DESC
				LIMIT ?
			) recent
			ORDER BY captured_at ASC`
		args = append(args, limit[0])
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query moonshot range: %w", err)
	}
	defer rows.Close()

	var snapshots []*api.MoonshotSnapshot
	for rows.Next() {
		var snapshot api.MoonshotSnapshot
		var capturedAt string

		err := rows.Scan(
			&snapshot.ID, &capturedAt, &snapshot.AvailableBalance, &snapshot.VoucherBalance, &snapshot.CashBalance,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan moonshot snapshot: %w", err)
		}

		snapshot.CapturedAt, _ = time.Parse(time.RFC3339Nano, capturedAt)
		snapshots = append(snapshots, &snapshot)
	}

	return snapshots, rows.Err()
}

// CreateMoonshotCycle creates a new Moonshot reset cycle.
func (s *Store) CreateMoonshotCycle(quotaType string, cycleStart time.Time) (int64, error) {
	result, err := s.db.Exec(
		`INSERT INTO moonshot_reset_cycles (quota_type, cycle_start) VALUES (?, ?)`,
		quotaType, cycleStart.Format(time.RFC3339Nano),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to create moonshot cycle: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to get cycle ID: %w", err)
	}

	return id, nil
}

// CloseMoonshotCycle closes a Moonshot reset cycle with final stats.
func (s *Store) CloseMoonshotCycle(quotaType string, cycleEnd time.Time, peakUsage, totalDelta float64) error {
	_, err := s.db.Exec(
		`UPDATE moonshot_reset_cycles SET cycle_end = ?, peak_usage = ?, total_delta = ?
		WHERE quota_type = ? AND cycle_end IS NULL`,
		cycleEnd.Format(time.RFC3339Nano), peakUsage, totalDelta, quotaType,
	)
	if err != nil {
		return fmt.Errorf("failed to close moonshot cycle: %w", err)
	}
	return nil
}

// UpdateMoonshotCycle updates the peak and delta for an active Moonshot cycle.
func (s *Store) UpdateMoonshotCycle(quotaType string, peakUsage, totalDelta float64) error {
	_, err := s.db.Exec(
		`UPDATE moonshot_reset_cycles SET peak_usage = ?, total_delta = ?
		WHERE quota_type = ? AND cycle_end IS NULL`,
		peakUsage, totalDelta, quotaType,
	)
	if err != nil {
		return fmt.Errorf("failed to update moonshot cycle: %w", err)
	}
	return nil
}

// QueryActiveMoonshotCycle returns the active cycle for a Moonshot quota type.
func (s *Store) QueryActiveMoonshotCycle(quotaType string) (*MoonshotResetCycle, error) {
	var cycle MoonshotResetCycle
	var cycleStart string
	var cycleEnd sql.NullString

	err := s.db.QueryRow(
		`SELECT id, quota_type, cycle_start, cycle_end, peak_usage, total_delta
		FROM moonshot_reset_cycles WHERE quota_type = ? AND cycle_end IS NULL`,
		quotaType,
	).Scan(
		&cycle.ID, &cycle.QuotaType, &cycleStart, &cycleEnd, &cycle.PeakUsage, &cycle.TotalDelta,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query active moonshot cycle: %w", err)
	}

	cycle.CycleStart, _ = time.Parse(time.RFC3339Nano, cycleStart)
	if cycleEnd.Valid {
		endTime, _ := time.Parse(time.RFC3339Nano, cycleEnd.String)
		cycle.CycleEnd = &endTime
	}

	return &cycle, nil
}

// QueryMoonshotCycleHistory returns completed cycles for a Moonshot quota type with optional limit.
func (s *Store) QueryMoonshotCycleHistory(quotaType string, limit ...int) ([]*MoonshotResetCycle, error) {
	query := `SELECT id, quota_type, cycle_start, cycle_end, peak_usage, total_delta
		FROM moonshot_reset_cycles WHERE quota_type = ? AND cycle_end IS NOT NULL ORDER BY cycle_start DESC`
	args := []interface{}{quotaType}
	if len(limit) > 0 && limit[0] > 0 {
		query += ` LIMIT ?`
		args = append(args, limit[0])
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query moonshot cycles: %w", err)
	}
	defer rows.Close()

	var cycles []*MoonshotResetCycle
	for rows.Next() {
		var cycle MoonshotResetCycle
		var cycleStart, cycleEnd string

		err := rows.Scan(
			&cycle.ID, &cycle.QuotaType, &cycleStart, &cycleEnd, &cycle.PeakUsage, &cycle.TotalDelta,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan moonshot cycle: %w", err)
		}

		cycle.CycleStart, _ = time.Parse(time.RFC3339Nano, cycleStart)
		endTime, _ := time.Parse(time.RFC3339Nano, cycleEnd)
		cycle.CycleEnd = &endTime

		cycles = append(cycles, &cycle)
	}

	return cycles, rows.Err()
}
