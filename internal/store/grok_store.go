package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

// DefaultGrokAccountID is the default account ID for single-account Grok setups.
const DefaultGrokAccountID int64 = 1

func parseGrokTime(value string, field string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to parse %s %q: %w", field, value, err)
	}
	return parsed, nil
}

// InsertGrokSnapshot inserts a Grok snapshot (credits quota) with its quota values.
func (s *Store) InsertGrokSnapshot(snapshot *api.GrokSnapshot) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	accountID := snapshot.AccountID
	if accountID == 0 {
		accountID = DefaultGrokAccountID
	}

	result, err := tx.Exec(
		`INSERT INTO grok_snapshots (captured_at, account_id, email, team_id, login_method, raw_json, quota_count) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		snapshot.CapturedAt.Format(time.RFC3339Nano),
		accountID,
		snapshot.Email,
		snapshot.TeamID,
		snapshot.LoginMethod,
		snapshot.RawJSON,
		len(snapshot.Quotas),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to insert grok snapshot: %w", err)
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
			`INSERT INTO grok_quota_values (snapshot_id, quota_name, utilization, resets_at, status) VALUES (?, ?, ?, ?, ?)`,
			snapshotID,
			q.Name,
			q.Utilization,
			resetsAt,
			q.Status,
		)
		if err != nil {
			return 0, fmt.Errorf("failed to insert grok quota value %s: %w", q.Name, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit: %w", err)
	}

	return snapshotID, nil
}

// QueryLatestGrok returns the most recent Grok snapshot with quotas for the given account.
func (s *Store) QueryLatestGrok(accountID int64) (*api.GrokSnapshot, error) {
	if accountID == 0 {
		accountID = DefaultGrokAccountID
	}
	var snapshot api.GrokSnapshot
	var capturedAt string
	var email, teamID, loginMethod sql.NullString

	err := s.db.QueryRow(
		`SELECT id, captured_at, email, team_id, login_method, raw_json, quota_count, account_id FROM grok_snapshots WHERE account_id = ? ORDER BY captured_at DESC LIMIT 1`,
		accountID,
	).Scan(&snapshot.ID, &capturedAt, &email, &teamID, &loginMethod, &snapshot.RawJSON, new(int), &snapshot.AccountID)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query latest grok: %w", err)
	}

	parsed, err := parseGrokTime(capturedAt, "grok snapshot captured_at")
	if err != nil {
		return nil, err
	}
	snapshot.CapturedAt = parsed
	if email.Valid {
		snapshot.Email = email.String
	}
	if teamID.Valid {
		snapshot.TeamID = teamID.String
	}
	if loginMethod.Valid {
		snapshot.LoginMethod = loginMethod.String
	}

	rows, err := s.db.Query(
		`SELECT quota_name, utilization, resets_at, status FROM grok_quota_values WHERE snapshot_id = ? ORDER BY quota_name`,
		snapshot.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query grok quota values: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var q api.GrokQuota
		var resetsAt sql.NullString
		var status sql.NullString
		if err := rows.Scan(&q.Name, &q.Utilization, &resetsAt, &status); err != nil {
			return nil, fmt.Errorf("failed to scan grok quota value: %w", err)
		}
		if resetsAt.Valid && resetsAt.String != "" {
			parsedR, err := parseGrokTime(resetsAt.String, "grok quota resets_at")
			if err != nil {
				return nil, err
			}
			q.ResetsAt = &parsedR
		}
		if status.Valid {
			q.Status = status.String
		}
		snapshot.Quotas = append(snapshot.Quotas, q)
	}

	return &snapshot, rows.Err()
}

// QueryGrokRange returns grok snapshots in [start, end] for charts (account aware).
func (s *Store) QueryGrokRange(accountID int64, start, end time.Time, limit ...int) ([]*api.GrokSnapshot, error) {
	if accountID == 0 {
		accountID = DefaultGrokAccountID
	}
	// Single LEFT JOIN so snapshots and their quota values come back in one cursor.
	// A previous two-query version raced with the grok agent's concurrent writes on
	// the single SQLite connection and returned partial snapshot sets.
	query := `SELECT s.id, s.captured_at, s.email, s.team_id, s.login_method, s.raw_json, s.account_id,
			v.quota_name, v.utilization, v.resets_at, v.status
		FROM grok_snapshots s
		LEFT JOIN grok_quota_values v ON v.snapshot_id = s.id
		WHERE s.account_id = ? AND s.captured_at BETWEEN ? AND ?
		ORDER BY s.captured_at ASC, v.quota_name`
	args := []interface{}{accountID, start.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano)}
	if len(limit) > 0 && limit[0] > 0 {
		// Limit applies to snapshots, not joined rows, so bound them in a subquery first.
		query = `SELECT r.id, r.captured_at, r.email, r.team_id, r.login_method, r.raw_json, r.account_id,
				v.quota_name, v.utilization, v.resets_at, v.status
			FROM (
				SELECT id, captured_at, email, team_id, login_method, raw_json, account_id
				FROM grok_snapshots
				WHERE account_id = ? AND captured_at BETWEEN ? AND ?
				ORDER BY captured_at DESC
				LIMIT ?
			) r
			LEFT JOIN grok_quota_values v ON v.snapshot_id = r.id
			ORDER BY r.captured_at ASC, v.quota_name`
		args = append(args, limit[0])
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query grok range: %w", err)
	}
	defer rows.Close()

	var out []*api.GrokSnapshot
	byID := make(map[int64]*api.GrokSnapshot)
	for rows.Next() {
		var (
			id                            int64
			capturedAt                    string
			email, teamID, loginMethod    sql.NullString
			rawJSON                       string
			accID                         int64
			qName, qResets, qStatus       sql.NullString
			qUtil                         sql.NullFloat64
		)
		if err := rows.Scan(&id, &capturedAt, &email, &teamID, &loginMethod, &rawJSON, &accID,
			&qName, &qUtil, &qResets, &qStatus); err != nil {
			return nil, fmt.Errorf("failed to scan grok range row: %w", err)
		}
		snap, ok := byID[id]
		if !ok {
			parsed, perr := parseGrokTime(capturedAt, "grok range captured_at")
			if perr != nil {
				return nil, perr
			}
			snap = &api.GrokSnapshot{ID: id, CapturedAt: parsed, RawJSON: rawJSON, AccountID: accID}
			if email.Valid {
				snap.Email = email.String
			}
			if teamID.Valid {
				snap.TeamID = teamID.String
			}
			if loginMethod.Valid {
				snap.LoginMethod = loginMethod.String
			}
			byID[id] = snap
			out = append(out, snap)
		}
		if qName.Valid {
			q := api.GrokQuota{Name: qName.String, Utilization: qUtil.Float64}
			if qResets.Valid && qResets.String != "" {
				if parsedR, perr := parseGrokTime(qResets.String, "grok quota resets_at"); perr == nil {
					q.ResetsAt = &parsedR
				}
			}
			if qStatus.Valid {
				q.Status = qStatus.String
			}
			snap.Quotas = append(snap.Quotas, q)
		}
	}
	return out, rows.Err()
}

// GrokResetCycle mirrors the reset cycle for the grok provider.
type GrokResetCycle struct {
	ID               int64
	AccountID        int64
	QuotaName        string
	CycleStart       time.Time
	CycleEnd         *time.Time
	ResetsAt         *time.Time
	PeakUtilization  float64
	TotalDelta       float64
}

// InsertGrokResetCycle creates a new cycle row.
func (s *Store) InsertGrokResetCycle(cycle *GrokResetCycle) (int64, error) {
	acc := cycle.AccountID
	if acc == 0 {
		acc = DefaultGrokAccountID
	}
	var end interface{}
	if cycle.CycleEnd != nil {
		end = cycle.CycleEnd.Format(time.RFC3339Nano)
	}
	var resets interface{}
	if cycle.ResetsAt != nil {
		resets = cycle.ResetsAt.Format(time.RFC3339Nano)
	}
	res, err := s.db.Exec(
		`INSERT INTO grok_reset_cycles (account_id, quota_name, cycle_start, cycle_end, resets_at, peak_utilization, total_delta)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		acc, cycle.QuotaName, cycle.CycleStart.Format(time.RFC3339Nano), end, resets, cycle.PeakUtilization, cycle.TotalDelta,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to insert grok reset cycle: %w", err)
	}
	return res.LastInsertId()
}

// QueryActiveGrokResetCycle returns the open cycle (no end) for a quota if present.
func (s *Store) QueryActiveGrokResetCycle(accountID int64, quotaName string) (*GrokResetCycle, error) {
	if accountID == 0 {
		accountID = DefaultGrokAccountID
	}
	var c GrokResetCycle
	var startStr, endStr, resetsStr sql.NullString
	err := s.db.QueryRow(
		`SELECT id, account_id, quota_name, cycle_start, cycle_end, resets_at, peak_utilization, total_delta
		 FROM grok_reset_cycles WHERE account_id = ? AND quota_name = ? AND cycle_end IS NULL
		 ORDER BY cycle_start DESC LIMIT 1`,
		accountID, quotaName,
	).Scan(&c.ID, &c.AccountID, &c.QuotaName, &startStr, &endStr, &resetsStr, &c.PeakUtilization, &c.TotalDelta)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query active grok cycle: %w", err)
	}
	c.CycleStart, _ = parseGrokTime(startStr.String, "cycle_start")
	if endStr.Valid && endStr.String != "" {
		t, _ := parseGrokTime(endStr.String, "cycle_end")
		c.CycleEnd = &t
	}
	if resetsStr.Valid && resetsStr.String != "" {
		t, _ := parseGrokTime(resetsStr.String, "resets_at")
		c.ResetsAt = &t
	}
	return &c, nil
}

// UpdateGrokResetCycleEnd closes a cycle.
func (s *Store) UpdateGrokResetCycleEnd(id int64, end time.Time, peak, delta float64) error {
	_, err := s.db.Exec(
		`UPDATE grok_reset_cycles SET cycle_end = ?, peak_utilization = ?, total_delta = ? WHERE id = ?`,
		end.Format(time.RFC3339Nano), peak, delta, id,
	)
	if err != nil {
		return fmt.Errorf("failed to close grok reset cycle: %w", err)
	}
	return nil
}

// QueryGrokCyclesForQuota returns recent cycles (closed + active) for summary stats. Limit applied.
func (s *Store) QueryGrokCyclesForQuota(accountID int64, quotaName string, limit int) ([]*GrokResetCycle, error) {
	if accountID == 0 {
		accountID = DefaultGrokAccountID
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(
		`SELECT id, account_id, quota_name, cycle_start, cycle_end, resets_at, peak_utilization, total_delta
		 FROM grok_reset_cycles
		 WHERE account_id = ? AND quota_name = ?
		 ORDER BY cycle_start DESC
		 LIMIT ?`,
		accountID, quotaName, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query grok cycles: %w", err)
	}
	defer rows.Close()

	var res []*GrokResetCycle
	for rows.Next() {
		var c GrokResetCycle
		var startStr, endStr, resetsStr sql.NullString
		if err := rows.Scan(&c.ID, &c.AccountID, &c.QuotaName, &startStr, &endStr, &resetsStr, &c.PeakUtilization, &c.TotalDelta); err != nil {
			return nil, err
		}
		if startStr.Valid {
			c.CycleStart, _ = parseGrokTime(startStr.String, "cycle_start")
		}
		if endStr.Valid && endStr.String != "" {
			t, _ := parseGrokTime(endStr.String, "cycle_end")
			c.CycleEnd = &t
		}
		if resetsStr.Valid && resetsStr.String != "" {
			t, _ := parseGrokTime(resetsStr.String, "resets_at")
			c.ResetsAt = &t
		}
		res = append(res, &c)
	}
	return res, rows.Err()
}
