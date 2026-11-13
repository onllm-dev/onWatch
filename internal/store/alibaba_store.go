package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

// alibabaSnapshotColumns is the column list shared by every snapshot query.
const alibabaSnapshotColumns = `id, captured_at, raw_json, spec_code, status, remaining_days, auto_renew, start_time, end_time, instance_id`

// AlibabaResetCycle is one Alibaba Token Plan quota window cycle.
type AlibabaResetCycle struct {
	ID              int64
	QuotaName       string
	AccountID       int64
	CycleStart      time.Time
	CycleEnd        *time.Time
	ResetsAt        *time.Time
	PeakUtilization float64
	TotalDelta      float64
}

// AlibabaLatestQuota is the newest stored value per quota window, joined with
// the plan fields of the snapshot it came from.
type AlibabaLatestQuota struct {
	Name          string
	Utilization   float64
	ResetsAt      *time.Time
	CapturedAt    time.Time
	SpecCode      string
	Status        string
	RemainingDays int
	AutoRenew     bool
	EndTime       *time.Time
}

func nullableTimeString(t *time.Time) interface{} {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parseNullableTime(ns sql.NullString) *time.Time {
	if !ns.Valid || ns.String == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, ns.String)
	if err != nil {
		return nil
	}
	return &t
}

// InsertAlibabaSnapshot stores a snapshot and its per-quota values atomically
// for one account.
func (s *Store) InsertAlibabaSnapshot(snapshot *api.AlibabaSnapshot, accountID int64) (int64, error) {
	if snapshot == nil {
		return 0, fmt.Errorf("nil alibaba snapshot")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	autoRenew := 0
	if snapshot.AutoRenew {
		autoRenew = 1
	}

	result, err := tx.Exec(
		`INSERT INTO alibaba_snapshots (
			captured_at, raw_json, account_id, spec_code, status, remaining_days,
			auto_renew, start_time, end_time, instance_id, quota_count
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		snapshot.CapturedAt.UTC().Format(time.RFC3339Nano),
		snapshot.RawJSON,
		accountID,
		snapshot.SpecCode,
		snapshot.Status,
		snapshot.RemainingDays,
		autoRenew,
		nullableTimeString(snapshot.StartTime),
		nullableTimeString(snapshot.EndTime),
		snapshot.InstanceID,
		len(snapshot.Quotas),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to insert alibaba snapshot: %w", err)
	}

	snapshotID, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to get snapshot ID: %w", err)
	}

	for _, q := range snapshot.Quotas {
		if _, err := tx.Exec(
			`INSERT INTO alibaba_quota_values (snapshot_id, quota_name, utilization, resets_at) VALUES (?, ?, ?, ?)`,
			snapshotID, q.Name, q.Utilization, nullableTimeString(q.ResetsAt),
		); err != nil {
			return 0, fmt.Errorf("failed to insert alibaba quota value %s: %w", q.Name, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit: %w", err)
	}
	return snapshotID, nil
}

type alibabaRowScanner interface {
	Scan(dest ...interface{}) error
}

func scanAlibabaSnapshot(row alibabaRowScanner) (*api.AlibabaSnapshot, error) {
	var snap api.AlibabaSnapshot
	var capturedAt string
	var rawJSON sql.NullString
	var autoRenew int
	var startTime, endTime sql.NullString

	if err := row.Scan(&snap.ID, &capturedAt, &rawJSON, &snap.SpecCode, &snap.Status,
		&snap.RemainingDays, &autoRenew, &startTime, &endTime, &snap.InstanceID); err != nil {
		return nil, err
	}
	snap.CapturedAt, _ = time.Parse(time.RFC3339Nano, capturedAt)
	if rawJSON.Valid {
		snap.RawJSON = rawJSON.String
	}
	snap.AutoRenew = autoRenew != 0
	snap.StartTime = parseNullableTime(startTime)
	snap.EndTime = parseNullableTime(endTime)
	return &snap, nil
}

func (s *Store) queryAlibabaQuotaValues(snapshotID int64) ([]api.AlibabaQuota, error) {
	rows, err := s.db.Query(
		`SELECT quota_name, utilization, resets_at FROM alibaba_quota_values WHERE snapshot_id = ? ORDER BY quota_name`,
		snapshotID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query alibaba quota values: %w", err)
	}
	defer rows.Close()

	var quotas []api.AlibabaQuota
	for rows.Next() {
		var q api.AlibabaQuota
		var resetsAt sql.NullString
		if err := rows.Scan(&q.Name, &q.Utilization, &resetsAt); err != nil {
			return nil, fmt.Errorf("failed to scan alibaba quota value: %w", err)
		}
		q.ResetsAt = parseNullableTime(resetsAt)
		quotas = append(quotas, q)
	}
	return quotas, rows.Err()
}

// QueryLatestAlibaba returns the most recent snapshot with its quotas for an
// account, or nil when none exists.
func (s *Store) QueryLatestAlibaba(accountID int64) (*api.AlibabaSnapshot, error) {
	snap, err := scanAlibabaSnapshot(s.db.QueryRow(
		`SELECT `+alibabaSnapshotColumns+` FROM alibaba_snapshots
		WHERE account_id = ? ORDER BY captured_at DESC, id DESC LIMIT 1`,
		accountID,
	))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query latest alibaba: %w", err)
	}

	quotas, err := s.queryAlibabaQuotaValues(snap.ID)
	if err != nil {
		return nil, err
	}
	snap.Quotas = quotas
	return snap, nil
}

// QueryAlibabaRange returns an account's snapshots within a time range,
// ascending by capture time, with an optional row cap that keeps the newest
// rows.
func (s *Store) QueryAlibabaRange(start, end time.Time, accountID int64, limit ...int) ([]*api.AlibabaSnapshot, error) {
	query := `SELECT ` + alibabaSnapshotColumns + ` FROM alibaba_snapshots
		WHERE account_id = ? AND captured_at BETWEEN ? AND ? ORDER BY captured_at ASC, id ASC`
	args := []interface{}{accountID, start.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano)}
	if len(limit) > 0 && limit[0] > 0 {
		query = `SELECT ` + alibabaSnapshotColumns + ` FROM (
				SELECT ` + alibabaSnapshotColumns + ` FROM alibaba_snapshots
				WHERE account_id = ? AND captured_at BETWEEN ? AND ?
				ORDER BY captured_at DESC, id DESC
				LIMIT ?
			) recent
			ORDER BY captured_at ASC, id ASC`
		args = append(args, limit[0])
	}
	return s.queryAlibabaSnapshots(query, args)
}

// QueryAlibabaChartRange samples an account's range with at most about 200
// snapshots, always keeping the first and last.
func (s *Store) QueryAlibabaChartRange(start, end time.Time, accountID int64) ([]*api.AlibabaSnapshot, error) {
	query := windowChartQueryWhere("alibaba_snapshots", alibabaSnapshotColumns, "account_id = ?")
	return s.queryAlibabaSnapshots(query, []interface{}{
		start.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano), accountID,
	})
}

func (s *Store) queryAlibabaSnapshots(query string, args []interface{}) ([]*api.AlibabaSnapshot, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query alibaba range: %w", err)
	}
	snapshots, err := scanAlibabaSnapshotRows(rows)
	if err != nil {
		return nil, err
	}
	if err := s.attachAlibabaQuotas(snapshots); err != nil {
		return nil, err
	}
	return snapshots, nil
}

func scanAlibabaSnapshotRows(rows *sql.Rows) ([]*api.AlibabaSnapshot, error) {
	defer rows.Close()
	var snapshots []*api.AlibabaSnapshot
	for rows.Next() {
		snap, err := scanAlibabaSnapshot(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan alibaba snapshot: %w", err)
		}
		snapshots = append(snapshots, snap)
	}
	return snapshots, rows.Err()
}

// attachAlibabaQuotas loads quota values for the snapshots in batches of
// IN-list queries instead of one query per snapshot.
func (s *Store) attachAlibabaQuotas(snapshots []*api.AlibabaSnapshot) error {
	if len(snapshots) == 0 {
		return nil
	}
	ids := make([]int64, len(snapshots))
	for i, snap := range snapshots {
		ids[i] = snap.ID
	}
	bySnapshot, err := loadSnapshotValuesBatched(s, ids,
		"alibaba_quota_values", "quota_name, utilization, resets_at", "quota_name",
		func(rows *sql.Rows) (int64, api.AlibabaQuota, error) {
			var snapshotID int64
			var q api.AlibabaQuota
			var resetsAt sql.NullString
			if err := rows.Scan(&snapshotID, &q.Name, &q.Utilization, &resetsAt); err != nil {
				return 0, q, err
			}
			q.ResetsAt = parseNullableTime(resetsAt)
			return snapshotID, q, nil
		})
	if err != nil {
		return fmt.Errorf("failed to query alibaba quota values: %w", err)
	}
	for _, snap := range snapshots {
		snap.Quotas = bySnapshot[snap.ID]
	}
	return nil
}

// CreateAlibabaCycle opens a new reset cycle for an account's quota window.
func (s *Store) CreateAlibabaCycle(quotaName string, cycleStart time.Time, resetsAt *time.Time, accountID int64) (int64, error) {
	result, err := s.db.Exec(
		`INSERT INTO alibaba_reset_cycles (quota_name, account_id, cycle_start, resets_at) VALUES (?, ?, ?, ?)`,
		quotaName, accountID, cycleStart.UTC().Format(time.RFC3339Nano), nullableTimeString(resetsAt),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to create alibaba cycle: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to get cycle ID: %w", err)
	}
	return id, nil
}

// CloseAlibabaCycle closes the active cycle for an account's quota window.
func (s *Store) CloseAlibabaCycle(quotaName string, cycleEnd time.Time, peak, delta float64, accountID int64) error {
	_, err := s.db.Exec(
		`UPDATE alibaba_reset_cycles SET cycle_end = ?, peak_utilization = ?, total_delta = ?
		WHERE quota_name = ? AND account_id = ? AND cycle_end IS NULL`,
		cycleEnd.UTC().Format(time.RFC3339Nano), peak, delta, quotaName, accountID,
	)
	if err != nil {
		return fmt.Errorf("failed to close alibaba cycle: %w", err)
	}
	return nil
}

// UpdateAlibabaCycle updates peak and delta of the active cycle.
func (s *Store) UpdateAlibabaCycle(quotaName string, peak, delta float64, accountID int64) error {
	_, err := s.db.Exec(
		`UPDATE alibaba_reset_cycles SET peak_utilization = ?, total_delta = ?
		WHERE quota_name = ? AND account_id = ? AND cycle_end IS NULL`,
		peak, delta, quotaName, accountID,
	)
	if err != nil {
		return fmt.Errorf("failed to update alibaba cycle: %w", err)
	}
	return nil
}

// UpdateAlibabaCycleResetsAt attaches a reset time to the active cycle.
func (s *Store) UpdateAlibabaCycleResetsAt(quotaName string, resetsAt *time.Time, accountID int64) error {
	_, err := s.db.Exec(
		`UPDATE alibaba_reset_cycles SET resets_at = ?
		WHERE quota_name = ? AND account_id = ? AND cycle_end IS NULL`,
		nullableTimeString(resetsAt), quotaName, accountID,
	)
	if err != nil {
		return fmt.Errorf("failed to update alibaba cycle reset: %w", err)
	}
	return nil
}

// RestartAlibabaCycle moves an open cycle to a new start and reset, used when
// an idle window ended and the next session opens a separate window.
func (s *Store) RestartAlibabaCycle(quotaName string, cycleStart, resetsAt time.Time, accountID int64) error {
	_, err := s.db.Exec(
		`UPDATE alibaba_reset_cycles SET cycle_start = ?, peak_utilization = 0, total_delta = 0, resets_at = ?
		WHERE quota_name = ? AND account_id = ? AND cycle_end IS NULL`,
		cycleStart.UTC().Format(time.RFC3339Nano), resetsAt.UTC().Format(time.RFC3339Nano), quotaName, accountID,
	)
	if err != nil {
		return fmt.Errorf("failed to restart alibaba cycle: %w", err)
	}
	return nil
}

func scanAlibabaCycle(row alibabaRowScanner) (*AlibabaResetCycle, error) {
	var cycle AlibabaResetCycle
	var cycleStart string
	var cycleEnd, resetsAt sql.NullString

	if err := row.Scan(&cycle.ID, &cycle.QuotaName, &cycle.AccountID, &cycleStart, &cycleEnd, &resetsAt,
		&cycle.PeakUtilization, &cycle.TotalDelta); err != nil {
		return nil, err
	}
	cycle.CycleStart, _ = time.Parse(time.RFC3339Nano, cycleStart)
	cycle.CycleEnd = parseNullableTime(cycleEnd)
	cycle.ResetsAt = parseNullableTime(resetsAt)
	return &cycle, nil
}

const alibabaCycleColumns = `id, quota_name, account_id, cycle_start, cycle_end, resets_at, peak_utilization, total_delta`

// QueryActiveAlibabaCycle returns the open cycle for an account's quota
// window, or nil when none is open.
func (s *Store) QueryActiveAlibabaCycle(quotaName string, accountID int64) (*AlibabaResetCycle, error) {
	cycle, err := scanAlibabaCycle(s.db.QueryRow(
		`SELECT `+alibabaCycleColumns+` FROM alibaba_reset_cycles
		WHERE quota_name = ? AND account_id = ? AND cycle_end IS NULL
		ORDER BY cycle_start DESC, id DESC LIMIT 1`,
		quotaName, accountID,
	))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query active alibaba cycle: %w", err)
	}
	return cycle, nil
}

// QueryAlibabaCycleHistory returns closed cycles, newest first.
func (s *Store) QueryAlibabaCycleHistory(quotaName string, accountID int64, limit ...int) ([]*AlibabaResetCycle, error) {
	query := `SELECT ` + alibabaCycleColumns + ` FROM alibaba_reset_cycles
		WHERE quota_name = ? AND account_id = ? AND cycle_end IS NOT NULL ORDER BY cycle_start DESC, id DESC`
	args := []interface{}{quotaName, accountID}
	if len(limit) > 0 && limit[0] > 0 {
		query += ` LIMIT ?`
		args = append(args, limit[0])
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query alibaba cycles: %w", err)
	}
	defer rows.Close()

	var cycles []*AlibabaResetCycle
	for rows.Next() {
		cycle, err := scanAlibabaCycle(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan alibaba cycle: %w", err)
		}
		cycles = append(cycles, cycle)
	}
	return cycles, rows.Err()
}

// QueryAlibabaUtilizationSeries returns an account's utilization points for a
// quota window since a time.
func (s *Store) QueryAlibabaUtilizationSeries(quotaName string, since time.Time, accountID int64) ([]UtilizationPoint, error) {
	rows, err := s.db.Query(
		`SELECT s.captured_at, qv.utilization
		FROM alibaba_quota_values qv
		JOIN alibaba_snapshots s ON s.id = qv.snapshot_id
		WHERE qv.quota_name = ? AND s.account_id = ? AND s.captured_at >= ?
		ORDER BY s.captured_at ASC`,
		quotaName, accountID, since.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query alibaba utilization series: %w", err)
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

// QueryLatestAlibabaQuotas returns the quota windows of an account's newest
// snapshot together with its plan fields, ordered by quota name.
func (s *Store) QueryLatestAlibabaQuotas(accountID int64) ([]AlibabaLatestQuota, error) {
	rows, err := s.db.Query(`
		SELECT qv.quota_name, qv.utilization, qv.resets_at, s.captured_at,
		       s.spec_code, s.status, s.remaining_days, s.auto_renew, s.end_time
		FROM alibaba_quota_values qv
		JOIN alibaba_snapshots s ON s.id = qv.snapshot_id
		WHERE s.id = (
			SELECT id FROM alibaba_snapshots WHERE account_id = ?
			ORDER BY captured_at DESC, id DESC LIMIT 1
		)
		ORDER BY qv.quota_name ASC`, accountID)
	if err != nil {
		return nil, fmt.Errorf("failed to query latest alibaba quotas: %w", err)
	}
	defer rows.Close()

	var results []AlibabaLatestQuota
	for rows.Next() {
		var q AlibabaLatestQuota
		var resetsAt, endTime sql.NullString
		var capturedAt string
		var autoRenew int
		if err := rows.Scan(&q.Name, &q.Utilization, &resetsAt, &capturedAt,
			&q.SpecCode, &q.Status, &q.RemainingDays, &autoRenew, &endTime); err != nil {
			return nil, fmt.Errorf("failed to scan latest alibaba quota: %w", err)
		}
		q.CapturedAt, _ = time.Parse(time.RFC3339Nano, capturedAt)
		q.ResetsAt = parseNullableTime(resetsAt)
		q.EndTime = parseNullableTime(endTime)
		q.AutoRenew = autoRenew != 0
		results = append(results, q)
	}
	return results, rows.Err()
}

// QueryAllAlibabaQuotaNames returns every quota window seen in an account's
// cycles.
func (s *Store) QueryAllAlibabaQuotaNames(accountID int64) ([]string, error) {
	rows, err := s.db.Query(
		`SELECT DISTINCT quota_name FROM alibaba_reset_cycles WHERE account_id = ? ORDER BY quota_name`,
		accountID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query alibaba quota names: %w", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("failed to scan alibaba quota name: %w", err)
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// QueryAlibabaCycleOverview returns active plus recent cycles of one window
// with every window's value at the cycle's peak snapshot. A non-positive
// limit defaults to 50.
func (s *Store) QueryAlibabaCycleOverview(groupBy string, limit int, accountID int64) ([]CycleOverviewRow, error) {
	if limit <= 0 {
		limit = 50
	}

	var cycles []*AlibabaResetCycle
	activeCycle, err := s.QueryActiveAlibabaCycle(groupBy, accountID)
	if err != nil {
		return nil, fmt.Errorf("store.QueryAlibabaCycleOverview: active: %w", err)
	}
	if activeCycle != nil {
		cycles = append(cycles, activeCycle)
		limit--
	}

	// A limit fully consumed by the active cycle means no history at all.
	// Passing 0 through would drop the LIMIT clause and return every cycle.
	if limit > 0 {
		completed, err := s.QueryAlibabaCycleHistory(groupBy, accountID, limit)
		if err != nil {
			return nil, fmt.Errorf("store.QueryAlibabaCycleOverview: %w", err)
		}
		cycles = append(cycles, completed...)
	}

	var overviewRows []CycleOverviewRow
	type peakRef struct {
		rowIndex   int
		snapshotID int64
	}
	var peaks []peakRef
	for _, c := range cycles {
		row := CycleOverviewRow{
			ResetsAt:   c.ResetsAt,
			CycleID:    c.ID,
			QuotaType:  c.QuotaName,
			CycleStart: c.CycleStart,
			CycleEnd:   c.CycleEnd,
			PeakValue:  c.PeakUtilization,
			TotalDelta: c.TotalDelta,
		}

		endBoundary := time.Now().Add(time.Minute)
		if c.CycleEnd != nil {
			endBoundary = *c.CycleEnd
		}

		var snapshotID int64
		var capturedAt string
		err := s.db.QueryRow(
			`SELECT s.id, s.captured_at FROM alibaba_snapshots s
			JOIN alibaba_quota_values qv ON qv.snapshot_id = s.id
			WHERE qv.quota_name = ? AND s.account_id = ? AND s.captured_at >= ? AND s.captured_at < ?
			ORDER BY qv.utilization DESC, s.captured_at ASC LIMIT 1`,
			groupBy, accountID,
			c.CycleStart.UTC().Format(time.RFC3339Nano),
			endBoundary.UTC().Format(time.RFC3339Nano),
		).Scan(&snapshotID, &capturedAt)
		if err == sql.ErrNoRows {
			overviewRows = append(overviewRows, row)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("store.QueryAlibabaCycleOverview: peak snapshot: %w", err)
		}

		row.PeakTime, _ = time.Parse(time.RFC3339Nano, capturedAt)
		overviewRows = append(overviewRows, row)
		peaks = append(peaks, peakRef{rowIndex: len(overviewRows) - 1, snapshotID: snapshotID})
	}

	ids := make([]int64, len(peaks))
	for i, p := range peaks {
		ids[i] = p.snapshotID
	}
	bySnapshot, err := loadSnapshotValuesBatched(s, ids,
		"alibaba_quota_values", "quota_name, utilization", "quota_name",
		func(rows *sql.Rows) (int64, CrossQuotaEntry, error) {
			var snapshotID int64
			var entry CrossQuotaEntry
			if err := rows.Scan(&snapshotID, &entry.Name, &entry.Percent); err != nil {
				return 0, entry, err
			}
			entry.Value = entry.Percent
			entry.Limit = 100
			return snapshotID, entry, nil
		})
	if err != nil {
		return nil, fmt.Errorf("store.QueryAlibabaCycleOverview: quota values: %w", err)
	}
	for _, p := range peaks {
		overviewRows[p.rowIndex].CrossQuotas = bySnapshot[p.snapshotID]
	}

	return overviewRows, nil
}
