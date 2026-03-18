package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

// CodexResetCycle represents a Codex quota reset cycle.
type CodexResetCycle struct {
	ID              int64
	AccountID       int64
	QuotaName       string
	CycleStart      time.Time
	CycleEnd        *time.Time
	ResetsAt        *time.Time
	PeakUtilization float64
	TotalDelta      float64
}

// DefaultCodexAccountID is the default account ID for single-account setups.
const DefaultCodexAccountID int64 = 1

func parseCodexTime(value string, field string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to parse %s %q: %w", field, value, err)
	}
	return parsed, nil
}

// InsertCodexSnapshot inserts a Codex snapshot with its quota values.
func (s *Store) InsertCodexSnapshot(snapshot *api.CodexSnapshot) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	var creditsBalance interface{}
	if snapshot.CreditsBalance != nil {
		creditsBalance = *snapshot.CreditsBalance
	}

	accountID := snapshot.AccountID
	if accountID == 0 {
		accountID = DefaultCodexAccountID
	}

	result, err := tx.Exec(
		`INSERT INTO codex_snapshots (captured_at, account_id, plan_type, credits_balance, raw_json, quota_count) VALUES (?, ?, ?, ?, ?, ?)`,
		snapshot.CapturedAt.Format(time.RFC3339Nano),
		accountID,
		snapshot.PlanType,
		creditsBalance,
		snapshot.RawJSON,
		len(snapshot.Quotas),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to insert codex snapshot: %w", err)
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
			`INSERT INTO codex_quota_values (snapshot_id, quota_name, utilization, resets_at, status) VALUES (?, ?, ?, ?, ?)`,
			snapshotID,
			q.Name,
			q.Utilization,
			resetsAt,
			q.Status,
		)
		if err != nil {
			return 0, fmt.Errorf("failed to insert codex quota value %s: %w", q.Name, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit: %w", err)
	}

	return snapshotID, nil
}

// QueryLatestCodex returns the most recent Codex snapshot with quotas for the given account.
func (s *Store) QueryLatestCodex(accountID int64) (*api.CodexSnapshot, error) {
	if accountID == 0 {
		accountID = DefaultCodexAccountID
	}
	var snapshot api.CodexSnapshot
	var capturedAt string
	var planType sql.NullString
	var creditsBalance sql.NullFloat64

	err := s.db.QueryRow(
		`SELECT id, captured_at, plan_type, credits_balance, quota_count, account_id FROM codex_snapshots WHERE account_id = ? ORDER BY captured_at DESC LIMIT 1`,
		accountID,
	).Scan(&snapshot.ID, &capturedAt, &planType, &creditsBalance, new(int), &snapshot.AccountID)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query latest codex: %w", err)
	}

	parsedCapturedAt, err := parseCodexTime(capturedAt, "codex snapshot captured_at")
	if err != nil {
		return nil, err
	}
	snapshot.CapturedAt = parsedCapturedAt
	if planType.Valid {
		snapshot.PlanType = planType.String
	}
	if creditsBalance.Valid {
		snapshot.CreditsBalance = &creditsBalance.Float64
	}

	rows, err := s.db.Query(
		`SELECT quota_name, utilization, resets_at, status FROM codex_quota_values WHERE snapshot_id = ? ORDER BY quota_name`,
		snapshot.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query codex quota values: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var q api.CodexQuota
		var resetsAt sql.NullString
		var status sql.NullString
		if err := rows.Scan(&q.Name, &q.Utilization, &resetsAt, &status); err != nil {
			return nil, fmt.Errorf("failed to scan codex quota value: %w", err)
		}
		if resetsAt.Valid && resetsAt.String != "" {
			parsedResetsAt, err := parseCodexTime(resetsAt.String, "codex quota resets_at")
			if err != nil {
				return nil, err
			}
			q.ResetsAt = &parsedResetsAt
		}
		if status.Valid {
			q.Status = status.String
		}
		snapshot.Quotas = append(snapshot.Quotas, q)
	}

	return &snapshot, rows.Err()
}

// QueryCodexRange returns Codex snapshots within a time range for the given account.
func (s *Store) QueryCodexRange(accountID int64, start, end time.Time, limit ...int) ([]*api.CodexSnapshot, error) {
	if accountID == 0 {
		accountID = DefaultCodexAccountID
	}
	query := `SELECT id, captured_at, plan_type, credits_balance, quota_count, account_id FROM codex_snapshots
		WHERE account_id = ? AND captured_at BETWEEN ? AND ? ORDER BY captured_at ASC`
	args := []interface{}{accountID, start.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano)}
	if len(limit) > 0 && limit[0] > 0 {
		query = `SELECT id, captured_at, plan_type, credits_balance, quota_count, account_id
			FROM (
				SELECT id, captured_at, plan_type, credits_balance, quota_count, account_id
				FROM codex_snapshots
				WHERE account_id = ? AND captured_at BETWEEN ? AND ?
				ORDER BY captured_at DESC
				LIMIT ?
			) recent
			ORDER BY captured_at ASC`
		args = append(args, limit[0])
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query codex range: %w", err)
	}
	defer rows.Close()

	var snapshots []*api.CodexSnapshot
	for rows.Next() {
		var snap api.CodexSnapshot
		var capturedAt string
		var planType sql.NullString
		var creditsBalance sql.NullFloat64
		if err := rows.Scan(&snap.ID, &capturedAt, &planType, &creditsBalance, new(int), &snap.AccountID); err != nil {
			return nil, fmt.Errorf("failed to scan codex snapshot: %w", err)
		}
		parsedCapturedAt, err := parseCodexTime(capturedAt, "codex snapshot captured_at")
		if err != nil {
			return nil, err
		}
		snap.CapturedAt = parsedCapturedAt
		if planType.Valid {
			snap.PlanType = planType.String
		}
		if creditsBalance.Valid {
			snap.CreditsBalance = &creditsBalance.Float64
		}
		snapshots = append(snapshots, &snap)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, snap := range snapshots {
		qRows, err := s.db.Query(
			`SELECT quota_name, utilization, resets_at, status FROM codex_quota_values WHERE snapshot_id = ? ORDER BY quota_name`,
			snap.ID,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to query codex quota values for snapshot %d: %w", snap.ID, err)
		}
		for qRows.Next() {
			var q api.CodexQuota
			var resetsAt sql.NullString
			var status sql.NullString
			if err := qRows.Scan(&q.Name, &q.Utilization, &resetsAt, &status); err != nil {
				qRows.Close()
				return nil, fmt.Errorf("failed to scan codex quota value: %w", err)
			}
			if resetsAt.Valid && resetsAt.String != "" {
				parsedResetsAt, err := parseCodexTime(resetsAt.String, "codex quota resets_at")
				if err != nil {
					qRows.Close()
					return nil, err
				}
				q.ResetsAt = &parsedResetsAt
			}
			if status.Valid {
				q.Status = status.String
			}
			snap.Quotas = append(snap.Quotas, q)
		}
		qRows.Close()
	}

	return snapshots, nil
}

// CreateCodexCycle creates a new Codex reset cycle.
func (s *Store) CreateCodexCycle(accountID int64, quotaName string, cycleStart time.Time, resetsAt *time.Time) (int64, error) {
	if accountID == 0 {
		accountID = DefaultCodexAccountID
	}

	var resetsAtVal interface{}
	if resetsAt != nil {
		resetsAtVal = resetsAt.Format(time.RFC3339Nano)
	}

	result, err := s.db.Exec(
		`INSERT INTO codex_reset_cycles (account_id, quota_name, cycle_start, resets_at) VALUES (?, ?, ?, ?)`,
		accountID,
		quotaName,
		cycleStart.Format(time.RFC3339Nano),
		resetsAtVal,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to create codex cycle: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to get cycle ID: %w", err)
	}
	return id, nil
}

// CloseCodexCycle closes a Codex reset cycle with final stats.
func (s *Store) CloseCodexCycle(accountID int64, quotaName string, cycleEnd time.Time, peak, delta float64) error {
	if accountID == 0 {
		accountID = DefaultCodexAccountID
	}
	_, err := s.db.Exec(
		`UPDATE codex_reset_cycles SET cycle_end = ?, peak_utilization = ?, total_delta = ?
		WHERE account_id = ? AND quota_name = ? AND cycle_end IS NULL`,
		cycleEnd.Format(time.RFC3339Nano),
		peak,
		delta,
		accountID,
		quotaName,
	)
	if err != nil {
		return fmt.Errorf("failed to close codex cycle: %w", err)
	}
	return nil
}

// UpdateCodexCycle updates the peak and delta for an active Codex cycle.
func (s *Store) UpdateCodexCycle(accountID int64, quotaName string, peak, delta float64) error {
	if accountID == 0 {
		accountID = DefaultCodexAccountID
	}
	_, err := s.db.Exec(
		`UPDATE codex_reset_cycles SET peak_utilization = ?, total_delta = ?
		WHERE account_id = ? AND quota_name = ? AND cycle_end IS NULL`,
		peak,
		delta,
		accountID,
		quotaName,
	)
	if err != nil {
		return fmt.Errorf("failed to update codex cycle: %w", err)
	}
	return nil
}

// UpdateCodexCycleResetsAt updates the reset timestamp for an active Codex cycle.
func (s *Store) UpdateCodexCycleResetsAt(accountID int64, quotaName string, resetsAt *time.Time) error {
	if accountID == 0 {
		accountID = DefaultCodexAccountID
	}
	var resetsAtValue interface{}
	if resetsAt != nil {
		resetsAtValue = resetsAt.Format(time.RFC3339Nano)
	}

	_, err := s.db.Exec(
		`UPDATE codex_reset_cycles SET resets_at = ?
		WHERE account_id = ? AND quota_name = ? AND cycle_end IS NULL`,
		resetsAtValue,
		accountID,
		quotaName,
	)
	if err != nil {
		return fmt.Errorf("failed to update codex cycle resets_at: %w", err)
	}
	return nil
}

// QueryActiveCodexCycle returns the active cycle for a Codex quota.
func (s *Store) QueryActiveCodexCycle(accountID int64, quotaName string) (*CodexResetCycle, error) {
	if accountID == 0 {
		accountID = DefaultCodexAccountID
	}
	var cycle CodexResetCycle
	var cycleStart string
	var cycleEnd, resetsAt sql.NullString

	err := s.db.QueryRow(
		`SELECT id, account_id, quota_name, cycle_start, cycle_end, resets_at, peak_utilization, total_delta
		FROM codex_reset_cycles WHERE account_id = ? AND quota_name = ? AND cycle_end IS NULL`,
		accountID,
		quotaName,
	).Scan(
		&cycle.ID,
		&cycle.AccountID,
		&cycle.QuotaName,
		&cycleStart,
		&cycleEnd,
		&resetsAt,
		&cycle.PeakUtilization,
		&cycle.TotalDelta,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query active codex cycle: %w", err)
	}

	parsedCycleStart, err := parseCodexTime(cycleStart, "codex cycle_start")
	if err != nil {
		return nil, err
	}
	cycle.CycleStart = parsedCycleStart
	if cycleEnd.Valid {
		parsedCycleEnd, err := parseCodexTime(cycleEnd.String, "codex cycle_end")
		if err != nil {
			return nil, err
		}
		cycle.CycleEnd = &parsedCycleEnd
	}
	if resetsAt.Valid {
		parsedResetsAt, err := parseCodexTime(resetsAt.String, "codex cycle resets_at")
		if err != nil {
			return nil, err
		}
		cycle.ResetsAt = &parsedResetsAt
	}

	return &cycle, nil
}

// QueryCodexCycleHistory returns completed cycles for a Codex quota with optional limit.
func (s *Store) QueryCodexCycleHistory(accountID int64, quotaName string, limit ...int) ([]*CodexResetCycle, error) {
	if accountID == 0 {
		accountID = DefaultCodexAccountID
	}
	query := `SELECT id, account_id, quota_name, cycle_start, cycle_end, resets_at, peak_utilization, total_delta
		FROM codex_reset_cycles WHERE account_id = ? AND quota_name = ? AND cycle_end IS NOT NULL ORDER BY cycle_start DESC`
	args := []interface{}{accountID, quotaName}
	if len(limit) > 0 && limit[0] > 0 {
		query += ` LIMIT ?`
		args = append(args, limit[0])
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query codex cycles: %w", err)
	}
	defer rows.Close()

	var cycles []*CodexResetCycle
	for rows.Next() {
		var cycle CodexResetCycle
		var cycleStart, cycleEnd string
		var resetsAt sql.NullString

		if err := rows.Scan(
			&cycle.ID,
			&cycle.AccountID,
			&cycle.QuotaName,
			&cycleStart,
			&cycleEnd,
			&resetsAt,
			&cycle.PeakUtilization,
			&cycle.TotalDelta,
		); err != nil {
			return nil, fmt.Errorf("failed to scan codex cycle: %w", err)
		}

		parsedCycleStart, err := parseCodexTime(cycleStart, "codex cycle_start")
		if err != nil {
			return nil, err
		}
		cycle.CycleStart = parsedCycleStart

		parsedCycleEnd, err := parseCodexTime(cycleEnd, "codex cycle_end")
		if err != nil {
			return nil, err
		}
		cycle.CycleEnd = &parsedCycleEnd
		if resetsAt.Valid {
			parsedResetsAt, err := parseCodexTime(resetsAt.String, "codex cycle resets_at")
			if err != nil {
				return nil, err
			}
			cycle.ResetsAt = &parsedResetsAt
		}

		cycles = append(cycles, &cycle)
	}

	return cycles, rows.Err()
}

// QueryCodexCyclesSince returns completed cycles for a quota since a given time.
func (s *Store) QueryCodexCyclesSince(accountID int64, quotaName string, since time.Time) ([]*CodexResetCycle, error) {
	if accountID == 0 {
		accountID = DefaultCodexAccountID
	}
	rows, err := s.db.Query(
		`SELECT id, account_id, quota_name, cycle_start, cycle_end, resets_at, peak_utilization, total_delta
		FROM codex_reset_cycles WHERE account_id = ? AND quota_name = ? AND cycle_end IS NOT NULL AND cycle_start >= ?
		ORDER BY cycle_start DESC`,
		accountID,
		quotaName,
		since.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query codex cycles since: %w", err)
	}
	defer rows.Close()

	var cycles []*CodexResetCycle
	for rows.Next() {
		var cycle CodexResetCycle
		var cycleStart, cycleEnd string
		var resetsAt sql.NullString

		if err := rows.Scan(
			&cycle.ID,
			&cycle.AccountID,
			&cycle.QuotaName,
			&cycleStart,
			&cycleEnd,
			&resetsAt,
			&cycle.PeakUtilization,
			&cycle.TotalDelta,
		); err != nil {
			return nil, fmt.Errorf("failed to scan codex cycle: %w", err)
		}

		parsedCycleStart, err := parseCodexTime(cycleStart, "codex cycle_start")
		if err != nil {
			return nil, err
		}
		cycle.CycleStart = parsedCycleStart

		parsedCycleEnd, err := parseCodexTime(cycleEnd, "codex cycle_end")
		if err != nil {
			return nil, err
		}
		cycle.CycleEnd = &parsedCycleEnd
		if resetsAt.Valid {
			parsedResetsAt, err := parseCodexTime(resetsAt.String, "codex cycle resets_at")
			if err != nil {
				return nil, err
			}
			cycle.ResetsAt = &parsedResetsAt
		}

		cycles = append(cycles, &cycle)
	}

	return cycles, rows.Err()
}

// QueryCodexUtilizationSeries returns per-quota utilization points since a given time.
func (s *Store) QueryCodexUtilizationSeries(accountID int64, quotaName string, since time.Time) ([]UtilizationPoint, error) {
	if accountID == 0 {
		accountID = DefaultCodexAccountID
	}
	rows, err := s.db.Query(
		`SELECT s.captured_at, qv.utilization
		FROM codex_quota_values qv
		JOIN codex_snapshots s ON s.id = qv.snapshot_id
		WHERE s.account_id = ? AND qv.quota_name = ? AND s.captured_at >= ?
		ORDER BY s.captured_at ASC`,
		accountID,
		quotaName,
		since.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query codex utilization series: %w", err)
	}
	defer rows.Close()

	var points []UtilizationPoint
	for rows.Next() {
		var capturedAt string
		var util float64
		if err := rows.Scan(&capturedAt, &util); err != nil {
			return nil, fmt.Errorf("failed to scan codex utilization point: %w", err)
		}
		parsedCapturedAt, err := parseCodexTime(capturedAt, "codex utilization captured_at")
		if err != nil {
			return nil, err
		}
		points = append(points, UtilizationPoint{CapturedAt: parsedCapturedAt, Utilization: util})
	}

	return points, rows.Err()
}

// QueryCodexCycleOverview returns Codex cycles for a given quota
// with cross-quota snapshot data at the peak moment of each cycle.
func (s *Store) QueryCodexCycleOverview(accountID int64, groupBy string, limit int) ([]CycleOverviewRow, error) {
	if accountID == 0 {
		accountID = DefaultCodexAccountID
	}
	if limit <= 0 {
		limit = 50
	}

	var cycles []*CodexResetCycle
	activeCycle, err := s.QueryActiveCodexCycle(accountID, groupBy)
	if err != nil {
		return nil, fmt.Errorf("store.QueryCodexCycleOverview: active: %w", err)
	}
	if activeCycle != nil {
		cycles = append(cycles, activeCycle)
		limit--
	}

	completedCycles, err := s.QueryCodexCycleHistory(accountID, groupBy, limit)
	if err != nil {
		return nil, fmt.Errorf("store.QueryCodexCycleOverview: %w", err)
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
			`SELECT s.id, s.captured_at FROM codex_snapshots s
			JOIN codex_quota_values qv ON qv.snapshot_id = s.id
			WHERE s.account_id = ? AND qv.quota_name = ? AND s.captured_at >= ? AND s.captured_at < ?
			ORDER BY qv.utilization DESC LIMIT 1`,
			accountID,
			groupBy,
			c.CycleStart.Format(time.RFC3339Nano),
			endBoundary.Format(time.RFC3339Nano),
		).Scan(&snapshotID, &capturedAt)

		if err == sql.ErrNoRows {
			overviewRows = append(overviewRows, row)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("store.QueryCodexCycleOverview: peak snapshot: %w", err)
		}

		parsedPeakTime, err := parseCodexTime(capturedAt, "codex peak captured_at")
		if err != nil {
			return nil, fmt.Errorf("store.QueryCodexCycleOverview: peak time: %w", err)
		}
		row.PeakTime = parsedPeakTime

		startValues := make(map[string]float64)
		var firstSnapshotID int64
		err = s.db.QueryRow(
			`SELECT id FROM codex_snapshots
			WHERE account_id = ? AND captured_at >= ? AND captured_at < ?
			ORDER BY captured_at ASC LIMIT 1`,
			accountID,
			c.CycleStart.Format(time.RFC3339Nano),
			endBoundary.Format(time.RFC3339Nano),
		).Scan(&firstSnapshotID)
		if err == nil {
			startRows, err := s.db.Query(
				`SELECT quota_name, utilization FROM codex_quota_values WHERE snapshot_id = ?`,
				firstSnapshotID,
			)
			if err == nil {
				for startRows.Next() {
					var name string
					var util float64
					if startRows.Scan(&name, &util) == nil {
						startValues[name] = util
					}
				}
				startRows.Close()
			}
		}

		qRows, err := s.db.Query(
			`SELECT quota_name, utilization FROM codex_quota_values WHERE snapshot_id = ? ORDER BY quota_name`,
			snapshotID,
		)
		if err != nil {
			return nil, fmt.Errorf("store.QueryCodexCycleOverview: quota values: %w", err)
		}
		for qRows.Next() {
			var entry CrossQuotaEntry
			if err := qRows.Scan(&entry.Name, &entry.Percent); err != nil {
				qRows.Close()
				return nil, fmt.Errorf("store.QueryCodexCycleOverview: scan quota: %w", err)
			}
			entry.Value = entry.Percent
			entry.StartPercent = startValues[entry.Name]
			entry.Delta = entry.Percent - entry.StartPercent
			row.CrossQuotas = append(row.CrossQuotas, entry)
		}
		qRows.Close()

		overviewRows = append(overviewRows, row)
	}

	return overviewRows, nil
}

// QueryAllCodexQuotaNames returns all distinct quota names from Codex quota values.
func (s *Store) QueryAllCodexQuotaNames() ([]string, error) {
	rows, err := s.db.Query(
		`SELECT DISTINCT quota_name FROM codex_quota_values ORDER BY quota_name`,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query codex quota names: %w", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("failed to scan codex quota name: %w", err)
		}
		names = append(names, name)
	}

	return names, rows.Err()
}

// QueryCodexAccounts returns all distinct account IDs from Codex snapshots.
func (s *Store) QueryCodexAccounts() ([]int64, error) {
	rows, err := s.db.Query(
		`SELECT DISTINCT account_id FROM codex_snapshots ORDER BY account_id`,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query codex accounts: %w", err)
	}
	defer rows.Close()

	var accounts []int64
	for rows.Next() {
		var account int64
		if err := rows.Scan(&account); err != nil {
			return nil, fmt.Errorf("failed to scan codex account: %w", err)
		}
		accounts = append(accounts, account)
	}

	return accounts, rows.Err()
}

// ProviderAccount represents an account for a provider.
type ProviderAccount struct {
	ID         int64
	Provider   string
	Name       string
	CreatedAt  time.Time
	Metadata   string
	DeletedAt  *time.Time
	ExternalID string // Provider-specific account identifier (e.g., Codex account_id from API)
}

// QueryProviderAccounts returns all accounts for a given provider (including deleted).
func (s *Store) QueryProviderAccounts(provider string) ([]ProviderAccount, error) {
	rows, err := s.db.Query(
		`SELECT id, provider, name, created_at, COALESCE(metadata, ''), deleted_at, COALESCE(external_id, '') FROM provider_accounts WHERE provider = ? ORDER BY id`,
		provider,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query provider accounts: %w", err)
	}
	defer rows.Close()

	var accounts []ProviderAccount
	for rows.Next() {
		var acc ProviderAccount
		var createdAt string
		var deletedAt sql.NullString
		if err := rows.Scan(&acc.ID, &acc.Provider, &acc.Name, &createdAt, &acc.Metadata, &deletedAt, &acc.ExternalID); err != nil {
			return nil, fmt.Errorf("failed to scan provider account: %w", err)
		}
		acc.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		if deletedAt.Valid && deletedAt.String != "" {
			t, _ := time.Parse(time.RFC3339Nano, deletedAt.String)
			acc.DeletedAt = &t
		}
		accounts = append(accounts, acc)
	}

	return accounts, rows.Err()
}

// QueryActiveProviderAccounts returns only non-deleted accounts for a given provider.
func (s *Store) QueryActiveProviderAccounts(provider string) ([]ProviderAccount, error) {
	rows, err := s.db.Query(
		`SELECT id, provider, name, created_at, COALESCE(metadata, '') FROM provider_accounts WHERE provider = ? AND deleted_at IS NULL ORDER BY id`,
		provider,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query active provider accounts: %w", err)
	}
	defer rows.Close()

	var accounts []ProviderAccount
	for rows.Next() {
		var acc ProviderAccount
		var createdAt string
		if err := rows.Scan(&acc.ID, &acc.Provider, &acc.Name, &createdAt, &acc.Metadata); err != nil {
			return nil, fmt.Errorf("failed to scan provider account: %w", err)
		}
		acc.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		accounts = append(accounts, acc)
	}

	return accounts, rows.Err()
}

// MarkProviderAccountDeleted soft-deletes a provider account by name.
func (s *Store) MarkProviderAccountDeleted(provider, name string) error {
	_, err := s.db.Exec(
		`UPDATE provider_accounts SET deleted_at = ? WHERE provider = ? AND name = ? AND deleted_at IS NULL`,
		time.Now().UTC().Format(time.RFC3339Nano),
		provider,
		name,
	)
	if err != nil {
		return fmt.Errorf("failed to mark provider account deleted: %w", err)
	}
	return nil
}

// UndeleteProviderAccount clears the deleted_at flag for a provider account.
func (s *Store) UndeleteProviderAccount(provider, name string) error {
	_, err := s.db.Exec(
		`UPDATE provider_accounts SET deleted_at = NULL WHERE provider = ? AND name = ?`,
		provider,
		name,
	)
	if err != nil {
		return fmt.Errorf("failed to undelete provider account: %w", err)
	}
	return nil
}

// GetOrCreateProviderAccount gets an existing account by name or creates a new one.
// If the account doesn't exist and "default" is the only account for this provider,
// it renames "default" to the new name (preserving historical data).
func (s *Store) GetOrCreateProviderAccount(provider, name string) (*ProviderAccount, error) {
	// Try to get existing account
	var acc ProviderAccount
	var createdAt string
	var deletedAt sql.NullString
	err := s.db.QueryRow(
		`SELECT id, provider, name, created_at, COALESCE(metadata, ''), deleted_at FROM provider_accounts WHERE provider = ? AND name = ?`,
		provider, name,
	).Scan(&acc.ID, &acc.Provider, &acc.Name, &createdAt, &acc.Metadata, &deletedAt)

	if err == nil {
		acc.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		// If account was previously deleted, undelete it (profile re-added)
		if deletedAt.Valid && deletedAt.String != "" {
			if undelErr := s.UndeleteProviderAccount(provider, name); undelErr == nil {
				acc.DeletedAt = nil
			}
		}
		return &acc, nil
	}

	if err != sql.ErrNoRows {
		return nil, fmt.Errorf("failed to query provider account: %w", err)
	}

	// Account doesn't exist. Check if we should rename "default" instead of creating new.
	// This preserves historical data when user saves their first custom profile.
	if name != "default" {
		var defaultAcc ProviderAccount
		var defaultCreatedAt string
		var accountCount int

		// Check if "default" exists and is the only account
		err = s.db.QueryRow(
			`SELECT id, provider, name, created_at, COALESCE(metadata, '') FROM provider_accounts WHERE provider = ? AND name = 'default'`,
			provider,
		).Scan(&defaultAcc.ID, &defaultAcc.Provider, &defaultAcc.Name, &defaultCreatedAt, &defaultAcc.Metadata)

		if err == nil {
			// "default" exists, check if it's the only account
			s.db.QueryRow(`SELECT COUNT(*) FROM provider_accounts WHERE provider = ?`, provider).Scan(&accountCount)

			if accountCount == 1 {
				// Only "default" exists - rename it to preserve historical data
				_, err = s.db.Exec(
					`UPDATE provider_accounts SET name = ? WHERE id = ?`,
					name, defaultAcc.ID,
				)
				if err != nil {
					return nil, fmt.Errorf("failed to rename default account: %w", err)
				}

				defaultAcc.Name = name
				defaultAcc.CreatedAt, _ = time.Parse(time.RFC3339Nano, defaultCreatedAt)
				return &defaultAcc, nil
			}
		}
	}

	// Create new account
	result, err := s.db.Exec(
		`INSERT INTO provider_accounts (provider, name, created_at) VALUES (?, ?, ?)`,
		provider, name, time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create provider account: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("failed to get account ID: %w", err)
	}

	return &ProviderAccount{
		ID:        id,
		Provider:  provider,
		Name:      name,
		CreatedAt: time.Now().UTC(),
	}, nil
}

// GetOrCreateProviderAccountByExternalID finds or creates a provider account,
// using the external_id (e.g., Codex account_id from API) as the dedup key.
// If an existing account has the same external_id, it reuses that row (updating
// the name if different) instead of creating a duplicate. This ensures one DB
// row per real provider account, regardless of how many profile names point to it.
func (s *Store) GetOrCreateProviderAccountByExternalID(provider, name, externalID string) (*ProviderAccount, error) {
	if externalID == "" {
		// No external_id - fall back to name-based lookup
		return s.GetOrCreateProviderAccount(provider, name)
	}

	// First, check if an account with this external_id already exists
	var acc ProviderAccount
	var createdAt string
	var deletedAt sql.NullString
	err := s.db.QueryRow(
		`SELECT id, provider, name, created_at, COALESCE(metadata, ''), deleted_at, COALESCE(external_id, '') FROM provider_accounts WHERE provider = ? AND external_id = ?`,
		provider, externalID,
	).Scan(&acc.ID, &acc.Provider, &acc.Name, &createdAt, &acc.Metadata, &deletedAt, &acc.ExternalID)

	if err == nil {
		acc.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		// Found existing account with same external_id
		// Best-effort: update name if it changed (profile was renamed)
		if acc.Name != name {
			if _, execErr := s.db.Exec(`UPDATE provider_accounts SET name = ? WHERE id = ?`, name, acc.ID); execErr == nil {
				acc.Name = name
			}
		}
		// If account was previously deleted, undelete it (profile re-added)
		if deletedAt.Valid && deletedAt.String != "" {
			if undelErr := s.UndeleteProviderAccount(provider, acc.Name); undelErr == nil {
				acc.DeletedAt = nil
			}
		}
		return &acc, nil
	}

	if err != sql.ErrNoRows {
		return nil, fmt.Errorf("failed to query provider account by external_id: %w", err)
	}

	// No account with this external_id - try name-based lookup and set external_id
	acc2, err := s.GetOrCreateProviderAccount(provider, name)
	if err != nil {
		return nil, err
	}

	// Best-effort: set the external_id on the account
	if _, execErr := s.db.Exec(`UPDATE provider_accounts SET external_id = ? WHERE id = ?`, externalID, acc2.ID); execErr == nil {
		acc2.ExternalID = externalID
	}

	return acc2, nil
}

// MergeCodexAccountData merges codex snapshot and cycle data from a source account
// into a target account, then marks the source as deleted. Used to consolidate
// duplicate profiles that belong to the same Codex account.
func (s *Store) MergeCodexAccountData(targetID, sourceID int64) error {
	if targetID == sourceID {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin merge transaction: %w", err)
	}
	defer tx.Rollback()

	// Move snapshots from source to target
	if _, err := tx.Exec(
		`UPDATE codex_snapshots SET account_id = ? WHERE account_id = ?`,
		targetID, sourceID,
	); err != nil {
		return fmt.Errorf("failed to merge snapshots: %w", err)
	}

	// Move reset cycles from source to target
	if _, err := tx.Exec(
		`UPDATE codex_reset_cycles SET account_id = ? WHERE account_id = ?`,
		targetID, sourceID,
	); err != nil {
		return fmt.Errorf("failed to merge reset cycles: %w", err)
	}

	// Mark source account as deleted
	if _, err := tx.Exec(
		`UPDATE provider_accounts SET deleted_at = ? WHERE id = ?`,
		time.Now().UTC().Format(time.RFC3339Nano), sourceID,
	); err != nil {
		return fmt.Errorf("failed to mark source account deleted: %w", err)
	}

	return tx.Commit()
}

// DeduplicateProviderAccounts consolidates duplicate provider accounts that share
// the same external_id. For each group of duplicates:
//   - If one is active and others are deleted: active one is the target
//   - If both/all are active: lowest ID is the target
//   - All telemetry data (snapshots, cycles) is merged into the target
//   - Duplicate accounts are soft-deleted (telemetry data preserved, not removed)
//
// Returns the number of accounts merged.
func (s *Store) DeduplicateProviderAccounts(provider string) (int, error) {
	// Find external_ids with multiple accounts
	rows, err := s.db.Query(
		`SELECT external_id, COUNT(*) as cnt FROM provider_accounts
		WHERE provider = ? AND external_id IS NOT NULL AND external_id != ''
		GROUP BY external_id HAVING cnt > 1`,
		provider,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to query duplicate accounts: %w", err)
	}
	defer rows.Close()

	var duplicateExternalIDs []string
	for rows.Next() {
		var extID string
		var cnt int
		if err := rows.Scan(&extID, &cnt); err != nil {
			return 0, fmt.Errorf("failed to scan duplicate: %w", err)
		}
		duplicateExternalIDs = append(duplicateExternalIDs, extID)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	merged := 0
	for _, extID := range duplicateExternalIDs {
		accounts, err := s.queryProviderAccountsByExternalID(provider, extID)
		if err != nil {
			return merged, err
		}
		if len(accounts) < 2 {
			continue
		}

		// Determine the target: prefer active (non-deleted), then lowest ID
		target := accounts[0]
		for _, acc := range accounts[1:] {
			if target.DeletedAt != nil && acc.DeletedAt == nil {
				// Current target is deleted but this one is active - prefer active
				target = acc
			} else if target.DeletedAt == nil && acc.DeletedAt == nil && acc.ID < target.ID {
				// Both active - prefer lower ID
				target = acc
			}
		}

		// Merge all non-target accounts into the target
		for _, acc := range accounts {
			if acc.ID == target.ID {
				continue
			}
			if err := s.MergeCodexAccountData(target.ID, acc.ID); err != nil {
				return merged, fmt.Errorf("failed to merge account %d into %d: %w", acc.ID, target.ID, err)
			}
			merged++
		}
	}

	return merged, nil
}

// queryProviderAccountsByExternalID returns all accounts with a given external_id.
func (s *Store) queryProviderAccountsByExternalID(provider, externalID string) ([]ProviderAccount, error) {
	rows, err := s.db.Query(
		`SELECT id, provider, name, created_at, COALESCE(metadata, ''), deleted_at, COALESCE(external_id, '')
		FROM provider_accounts WHERE provider = ? AND external_id = ? ORDER BY id`,
		provider, externalID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query accounts by external_id: %w", err)
	}
	defer rows.Close()

	var accounts []ProviderAccount
	for rows.Next() {
		var acc ProviderAccount
		var createdAt string
		var deletedAt sql.NullString
		if err := rows.Scan(&acc.ID, &acc.Provider, &acc.Name, &createdAt, &acc.Metadata, &deletedAt, &acc.ExternalID); err != nil {
			return nil, fmt.Errorf("failed to scan account: %w", err)
		}
		acc.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		if deletedAt.Valid && deletedAt.String != "" {
			t, _ := time.Parse(time.RFC3339Nano, deletedAt.String)
			acc.DeletedAt = &t
		}
		accounts = append(accounts, acc)
	}
	return accounts, rows.Err()
}

// GetProviderAccountByID returns an account by its ID.
func (s *Store) GetProviderAccountByID(id int64) (*ProviderAccount, error) {
	var acc ProviderAccount
	var createdAt string
	var deletedAt sql.NullString
	err := s.db.QueryRow(
		`SELECT id, provider, name, created_at, COALESCE(metadata, ''), deleted_at, COALESCE(external_id, '') FROM provider_accounts WHERE id = ?`,
		id,
	).Scan(&acc.ID, &acc.Provider, &acc.Name, &createdAt, &acc.Metadata, &deletedAt, &acc.ExternalID)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query provider account: %w", err)
	}

	acc.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	if deletedAt.Valid && deletedAt.String != "" {
		t, _ := time.Parse(time.RFC3339Nano, deletedAt.String)
		acc.DeletedAt = &t
	}
	return &acc, nil
}
