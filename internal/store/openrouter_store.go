package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

// OpenRouterResetCycle represents an OpenRouter usage reset cycle.
type OpenRouterResetCycle struct {
	ID         int64
	QuotaType  string
	CycleStart time.Time
	CycleEnd   *time.Time
	PeakUsage  float64
	TotalDelta float64
}

// InsertOpenRouterSnapshot inserts an OpenRouter usage snapshot.
func (s *Store) InsertOpenRouterSnapshot(snapshot *api.OpenRouterSnapshot) (int64, error) {
	var limitVal interface{}
	if snapshot.Limit != nil {
		limitVal = *snapshot.Limit
	}
	var limitRemainingVal interface{}
	if snapshot.LimitRemaining != nil {
		limitRemainingVal = *snapshot.LimitRemaining
	}

	isFreeTier := 0
	if snapshot.IsFreeTier {
		isFreeTier = 1
	}

	result, err := s.db.Exec(
		`INSERT INTO openrouter_snapshots
		(captured_at, label, usage, usage_daily, usage_weekly, usage_monthly,
		 credit_limit, limit_remaining, is_free_tier, rate_limit_requests, rate_limit_interval)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		snapshot.CapturedAt.Format(time.RFC3339Nano),
		snapshot.Label,
		snapshot.Usage,
		snapshot.UsageDaily,
		snapshot.UsageWeekly,
		snapshot.UsageMonthly,
		limitVal,
		limitRemainingVal,
		isFreeTier,
		snapshot.RateLimitRequests,
		snapshot.RateLimitInterval,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to insert openrouter snapshot: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to get last insert ID: %w", err)
	}

	return id, nil
}

// QueryLatestOpenRouter returns the most recent OpenRouter snapshot.
func (s *Store) QueryLatestOpenRouter() (*api.OpenRouterSnapshot, error) {
	var snapshot api.OpenRouterSnapshot
	var capturedAt string
	var creditLimit, limitRemaining sql.NullFloat64
	var isFreeTier int

	err := s.db.QueryRow(
		`SELECT id, captured_at, label, usage, usage_daily, usage_weekly, usage_monthly,
		 credit_limit, limit_remaining, is_free_tier, rate_limit_requests, rate_limit_interval
		FROM openrouter_snapshots ORDER BY captured_at DESC LIMIT 1`,
	).Scan(
		&snapshot.ID, &capturedAt, &snapshot.Label,
		&snapshot.Usage, &snapshot.UsageDaily, &snapshot.UsageWeekly, &snapshot.UsageMonthly,
		&creditLimit, &limitRemaining, &isFreeTier,
		&snapshot.RateLimitRequests, &snapshot.RateLimitInterval,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query latest openrouter: %w", err)
	}

	snapshot.CapturedAt, _ = time.Parse(time.RFC3339Nano, capturedAt)
	if creditLimit.Valid {
		snapshot.Limit = &creditLimit.Float64
	}
	if limitRemaining.Valid {
		snapshot.LimitRemaining = &limitRemaining.Float64
	}
	snapshot.IsFreeTier = isFreeTier != 0

	return &snapshot, nil
}

// QueryOpenRouterRange returns OpenRouter snapshots within a time range with optional limit.
func (s *Store) QueryOpenRouterRange(start, end time.Time, limit ...int) ([]*api.OpenRouterSnapshot, error) {
	query := `SELECT id, captured_at, label, usage, usage_daily, usage_weekly, usage_monthly,
		 credit_limit, limit_remaining, is_free_tier, rate_limit_requests, rate_limit_interval
		FROM openrouter_snapshots
		WHERE captured_at BETWEEN ? AND ?
		ORDER BY captured_at ASC`
	args := []interface{}{start.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano)}
	if len(limit) > 0 && limit[0] > 0 {
		query = `SELECT id, captured_at, label, usage, usage_daily, usage_weekly, usage_monthly,
			 credit_limit, limit_remaining, is_free_tier, rate_limit_requests, rate_limit_interval
			FROM (
				SELECT id, captured_at, label, usage, usage_daily, usage_weekly, usage_monthly,
					 credit_limit, limit_remaining, is_free_tier, rate_limit_requests, rate_limit_interval
				FROM openrouter_snapshots
				WHERE captured_at BETWEEN ? AND ?
				ORDER BY captured_at DESC
				LIMIT ?
			) recent
			ORDER BY captured_at ASC`
		args = append(args, limit[0])
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query openrouter range: %w", err)
	}
	defer rows.Close()

	var snapshots []*api.OpenRouterSnapshot
	for rows.Next() {
		var snapshot api.OpenRouterSnapshot
		var capturedAt string
		var creditLimit, limitRemaining sql.NullFloat64
		var isFreeTier int

		err := rows.Scan(
			&snapshot.ID, &capturedAt, &snapshot.Label,
			&snapshot.Usage, &snapshot.UsageDaily, &snapshot.UsageWeekly, &snapshot.UsageMonthly,
			&creditLimit, &limitRemaining, &isFreeTier,
			&snapshot.RateLimitRequests, &snapshot.RateLimitInterval,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan openrouter snapshot: %w", err)
		}

		snapshot.CapturedAt, _ = time.Parse(time.RFC3339Nano, capturedAt)
		if creditLimit.Valid {
			snapshot.Limit = &creditLimit.Float64
		}
		if limitRemaining.Valid {
			snapshot.LimitRemaining = &limitRemaining.Float64
		}
		snapshot.IsFreeTier = isFreeTier != 0

		snapshots = append(snapshots, &snapshot)
	}

	return snapshots, rows.Err()
}

// CreateOpenRouterCycle creates a new OpenRouter reset cycle.
func (s *Store) CreateOpenRouterCycle(quotaType string, cycleStart time.Time) (int64, error) {
	result, err := s.db.Exec(
		`INSERT INTO openrouter_reset_cycles (quota_type, cycle_start) VALUES (?, ?)`,
		quotaType, cycleStart.Format(time.RFC3339Nano),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to create openrouter cycle: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to get cycle ID: %w", err)
	}

	return id, nil
}

// CloseOpenRouterCycle closes an OpenRouter reset cycle with final stats.
func (s *Store) CloseOpenRouterCycle(quotaType string, cycleEnd time.Time, peakUsage, totalDelta float64) error {
	_, err := s.db.Exec(
		`UPDATE openrouter_reset_cycles SET cycle_end = ?, peak_usage = ?, total_delta = ?
		WHERE quota_type = ? AND cycle_end IS NULL`,
		cycleEnd.Format(time.RFC3339Nano), peakUsage, totalDelta, quotaType,
	)
	if err != nil {
		return fmt.Errorf("failed to close openrouter cycle: %w", err)
	}
	return nil
}

// UpdateOpenRouterCycle updates the peak and delta for an active OpenRouter cycle.
func (s *Store) UpdateOpenRouterCycle(quotaType string, peakUsage, totalDelta float64) error {
	_, err := s.db.Exec(
		`UPDATE openrouter_reset_cycles SET peak_usage = ?, total_delta = ?
		WHERE quota_type = ? AND cycle_end IS NULL`,
		peakUsage, totalDelta, quotaType,
	)
	if err != nil {
		return fmt.Errorf("failed to update openrouter cycle: %w", err)
	}
	return nil
}

// QueryActiveOpenRouterCycle returns the active cycle for an OpenRouter quota type.
func (s *Store) QueryActiveOpenRouterCycle(quotaType string) (*OpenRouterResetCycle, error) {
	var cycle OpenRouterResetCycle
	var cycleStart string
	var cycleEnd sql.NullString

	err := s.db.QueryRow(
		`SELECT id, quota_type, cycle_start, cycle_end, peak_usage, total_delta
		FROM openrouter_reset_cycles WHERE quota_type = ? AND cycle_end IS NULL`,
		quotaType,
	).Scan(
		&cycle.ID, &cycle.QuotaType, &cycleStart, &cycleEnd, &cycle.PeakUsage, &cycle.TotalDelta,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query active openrouter cycle: %w", err)
	}

	cycle.CycleStart, _ = time.Parse(time.RFC3339Nano, cycleStart)
	if cycleEnd.Valid {
		endTime, _ := time.Parse(time.RFC3339Nano, cycleEnd.String)
		cycle.CycleEnd = &endTime
	}

	return &cycle, nil
}

// QueryOpenRouterCycleHistory returns completed cycles for an OpenRouter quota type with optional limit.
func (s *Store) QueryOpenRouterCycleHistory(quotaType string, limit ...int) ([]*OpenRouterResetCycle, error) {
	query := `SELECT id, quota_type, cycle_start, cycle_end, peak_usage, total_delta
		FROM openrouter_reset_cycles WHERE quota_type = ? AND cycle_end IS NOT NULL ORDER BY cycle_start DESC`
	args := []interface{}{quotaType}
	if len(limit) > 0 && limit[0] > 0 {
		query += ` LIMIT ?`
		args = append(args, limit[0])
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query openrouter cycles: %w", err)
	}
	defer rows.Close()

	var cycles []*OpenRouterResetCycle
	for rows.Next() {
		var cycle OpenRouterResetCycle
		var cycleStart, cycleEnd string

		err := rows.Scan(
			&cycle.ID, &cycle.QuotaType, &cycleStart, &cycleEnd, &cycle.PeakUsage, &cycle.TotalDelta,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan openrouter cycle: %w", err)
		}

		cycle.CycleStart, _ = time.Parse(time.RFC3339Nano, cycleStart)
		endTime, _ := time.Parse(time.RFC3339Nano, cycleEnd)
		cycle.CycleEnd = &endTime

		cycles = append(cycles, &cycle)
	}

	return cycles, rows.Err()
}
