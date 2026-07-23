package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

// DefaultKimiAccountID is the default account ID for single-account Kimi setups.
const DefaultKimiAccountID int64 = 1

func parseKimiTime(value string, field string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to parse %s %q: %w", field, value, err)
	}
	return parsed, nil
}

// InsertKimiSnapshot inserts a Kimi snapshot with its quota values.
func (s *Store) InsertKimiSnapshot(snapshot *api.KimiSnapshot) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	accountID := snapshot.AccountID
	if accountID == 0 {
		accountID = DefaultKimiAccountID
	}

	result, err := tx.Exec(
		`INSERT INTO kimi_snapshots (captured_at, account_id, user_id, region, membership, raw_json, quota_count) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		snapshot.CapturedAt.Format(time.RFC3339Nano),
		accountID,
		snapshot.UserID,
		snapshot.Region,
		snapshot.Membership,
		snapshot.RawJSON,
		len(snapshot.Quotas),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to insert kimi snapshot: %w", err)
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
			`INSERT INTO kimi_quota_values (snapshot_id, quota_name, utilization, resets_at, status) VALUES (?, ?, ?, ?, ?)`,
			snapshotID,
			q.Name,
			q.Utilization,
			resetsAt,
			q.Status,
		)
		if err != nil {
			return 0, fmt.Errorf("failed to insert kimi quota value %s: %w", q.Name, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit: %w", err)
	}
	return snapshotID, nil
}

// QueryLatestKimi returns the most recent Kimi snapshot with quotas.
func (s *Store) QueryLatestKimi(accountID int64) (*api.KimiSnapshot, error) {
	if accountID == 0 {
		accountID = DefaultKimiAccountID
	}
	var snapshot api.KimiSnapshot
	var capturedAt string
	var userID, region, membership sql.NullString

	err := s.db.QueryRow(
		`SELECT id, captured_at, user_id, region, membership, raw_json, quota_count, account_id FROM kimi_snapshots WHERE account_id = ? ORDER BY captured_at DESC LIMIT 1`,
		accountID,
	).Scan(&snapshot.ID, &capturedAt, &userID, &region, &membership, &snapshot.RawJSON, new(int), &snapshot.AccountID)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query latest kimi: %w", err)
	}

	parsed, err := parseKimiTime(capturedAt, "kimi snapshot captured_at")
	if err != nil {
		return nil, err
	}
	snapshot.CapturedAt = parsed
	if userID.Valid {
		snapshot.UserID = userID.String
	}
	if region.Valid {
		snapshot.Region = region.String
	}
	if membership.Valid {
		snapshot.Membership = membership.String
	}

	rows, err := s.db.Query(
		`SELECT quota_name, utilization, resets_at, status FROM kimi_quota_values WHERE snapshot_id = ? ORDER BY quota_name`,
		snapshot.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query kimi quota values: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var q api.KimiQuota
		var resetsAt sql.NullString
		var status sql.NullString
		if err := rows.Scan(&q.Name, &q.Utilization, &resetsAt, &status); err != nil {
			return nil, fmt.Errorf("failed to scan kimi quota value: %w", err)
		}
		if resetsAt.Valid && resetsAt.String != "" {
			parsedR, err := parseKimiTime(resetsAt.String, "kimi quota resets_at")
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

// QueryKimiRange returns kimi snapshots in [start, end] for charts.
func (s *Store) QueryKimiRange(accountID int64, start, end time.Time, limit ...int) ([]*api.KimiSnapshot, error) {
	if accountID == 0 {
		accountID = DefaultKimiAccountID
	}
	query := `SELECT s.id, s.captured_at, s.user_id, s.region, s.membership, s.raw_json, s.account_id,
			v.quota_name, v.utilization, v.resets_at, v.status
		FROM kimi_snapshots s
		LEFT JOIN kimi_quota_values v ON v.snapshot_id = s.id
		WHERE s.account_id = ? AND s.captured_at BETWEEN ? AND ?
		ORDER BY s.captured_at ASC, v.quota_name`
	args := []interface{}{accountID, start.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano)}
	if len(limit) > 0 && limit[0] > 0 {
		query = `SELECT r.id, r.captured_at, r.user_id, r.region, r.membership, r.raw_json, r.account_id,
				v.quota_name, v.utilization, v.resets_at, v.status
			FROM (
				SELECT id, captured_at, user_id, region, membership, raw_json, account_id
				FROM kimi_snapshots
				WHERE account_id = ? AND captured_at BETWEEN ? AND ?
				ORDER BY captured_at DESC
				LIMIT ?
			) r
			LEFT JOIN kimi_quota_values v ON v.snapshot_id = r.id
			ORDER BY r.captured_at ASC, v.quota_name`
		args = append(args, limit[0])
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query kimi range: %w", err)
	}
	defer rows.Close()

	var out []*api.KimiSnapshot
	byID := make(map[int64]*api.KimiSnapshot)
	for rows.Next() {
		var (
			id                      int64
			capturedAt              string
			userID, region, member  sql.NullString
			rawJSON                 string
			accID                   int64
			qName, qResets, qStatus sql.NullString
			qUtil                   sql.NullFloat64
		)
		if err := rows.Scan(&id, &capturedAt, &userID, &region, &member, &rawJSON, &accID,
			&qName, &qUtil, &qResets, &qStatus); err != nil {
			return nil, fmt.Errorf("failed to scan kimi range row: %w", err)
		}
		snap, ok := byID[id]
		if !ok {
			parsed, perr := parseKimiTime(capturedAt, "kimi range captured_at")
			if perr != nil {
				return nil, perr
			}
			snap = &api.KimiSnapshot{ID: id, CapturedAt: parsed, RawJSON: rawJSON, AccountID: accID}
			if userID.Valid {
				snap.UserID = userID.String
			}
			if region.Valid {
				snap.Region = region.String
			}
			if member.Valid {
				snap.Membership = member.String
			}
			byID[id] = snap
			out = append(out, snap)
		}
		if qName.Valid {
			q := api.KimiQuota{Name: qName.String, Utilization: qUtil.Float64}
			if qResets.Valid && qResets.String != "" {
				if parsedR, perr := parseKimiTime(qResets.String, "kimi quota resets_at"); perr == nil {
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

// KimiResetCycle mirrors a reset cycle for the kimi provider.
type KimiResetCycle struct {
	ID              int64
	AccountID       int64
	QuotaName       string
	CycleStart      time.Time
	CycleEnd        *time.Time
	ResetsAt        *time.Time
	PeakUtilization float64
	TotalDelta      float64
}

// InsertKimiResetCycle creates a new cycle row.
func (s *Store) InsertKimiResetCycle(cycle *KimiResetCycle) (int64, error) {
	acc := cycle.AccountID
	if acc == 0 {
		acc = DefaultKimiAccountID
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
		`INSERT INTO kimi_reset_cycles (account_id, quota_name, cycle_start, cycle_end, resets_at, peak_utilization, total_delta)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		acc, cycle.QuotaName, cycle.CycleStart.Format(time.RFC3339Nano), end, resets, cycle.PeakUtilization, cycle.TotalDelta,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to insert kimi reset cycle: %w", err)
	}
	return res.LastInsertId()
}

// QueryActiveKimiResetCycle returns the open cycle for a quota if present.
func (s *Store) QueryActiveKimiResetCycle(accountID int64, quotaName string) (*KimiResetCycle, error) {
	if accountID == 0 {
		accountID = DefaultKimiAccountID
	}
	var c KimiResetCycle
	var startStr, endStr, resetsStr sql.NullString
	err := s.db.QueryRow(
		`SELECT id, account_id, quota_name, cycle_start, cycle_end, resets_at, peak_utilization, total_delta
		 FROM kimi_reset_cycles WHERE account_id = ? AND quota_name = ? AND cycle_end IS NULL
		 ORDER BY cycle_start DESC LIMIT 1`,
		accountID, quotaName,
	).Scan(&c.ID, &c.AccountID, &c.QuotaName, &startStr, &endStr, &resetsStr, &c.PeakUtilization, &c.TotalDelta)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query active kimi cycle: %w", err)
	}
	c.CycleStart, _ = parseKimiTime(startStr.String, "cycle_start")
	if endStr.Valid && endStr.String != "" {
		t, _ := parseKimiTime(endStr.String, "cycle_end")
		c.CycleEnd = &t
	}
	if resetsStr.Valid && resetsStr.String != "" {
		t, _ := parseKimiTime(resetsStr.String, "resets_at")
		c.ResetsAt = &t
	}
	return &c, nil
}

// UpdateKimiResetCycleEnd closes a cycle.
func (s *Store) UpdateKimiResetCycleEnd(id int64, end time.Time, peak, delta float64) error {
	_, err := s.db.Exec(
		`UPDATE kimi_reset_cycles SET cycle_end = ?, peak_utilization = ?, total_delta = ? WHERE id = ?`,
		end.Format(time.RFC3339Nano), peak, delta, id,
	)
	if err != nil {
		return fmt.Errorf("failed to close kimi reset cycle: %w", err)
	}
	return nil
}

// QueryKimiCyclesForQuota returns recent cycles for summary stats.
func (s *Store) QueryKimiCyclesForQuota(accountID int64, quotaName string, limit int) ([]*KimiResetCycle, error) {
	if accountID == 0 {
		accountID = DefaultKimiAccountID
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(
		`SELECT id, account_id, quota_name, cycle_start, cycle_end, resets_at, peak_utilization, total_delta
		 FROM kimi_reset_cycles
		 WHERE account_id = ? AND quota_name = ?
		 ORDER BY cycle_start DESC
		 LIMIT ?`,
		accountID, quotaName, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query kimi cycles: %w", err)
	}
	defer rows.Close()

	var res []*KimiResetCycle
	for rows.Next() {
		var c KimiResetCycle
		var startStr, endStr, resetsStr sql.NullString
		if err := rows.Scan(&c.ID, &c.AccountID, &c.QuotaName, &startStr, &endStr, &resetsStr, &c.PeakUtilization, &c.TotalDelta); err != nil {
			return nil, err
		}
		if startStr.Valid {
			c.CycleStart, _ = parseKimiTime(startStr.String, "cycle_start")
		}
		if endStr.Valid && endStr.String != "" {
			t, _ := parseKimiTime(endStr.String, "cycle_end")
			c.CycleEnd = &t
		}
		if resetsStr.Valid && resetsStr.String != "" {
			t, _ := parseKimiTime(resetsStr.String, "resets_at")
			c.ResetsAt = &t
		}
		res = append(res, &c)
	}
	return res, rows.Err()
}
