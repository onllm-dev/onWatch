package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

type OllamaResetCycle struct {
	ID              int64
	QuotaName       string
	CycleStart      time.Time
	CycleEnd        *time.Time
	ResetsAt        *time.Time
	PeakUtilization float64
	TotalDelta      float64
}

type OllamaLatestQuota struct {
	Name        string
	Used        float64
	Limit       float64
	Utilization float64
	Format      string
	ResetsAt    *time.Time
	CapturedAt  time.Time
	Plan        string
	AccountName string
}

func (s *Store) InsertOllamaSnapshot(snapshot *api.OllamaSnapshot) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.Exec(
		`INSERT INTO ollama_snapshots (captured_at, raw_json, plan, account_name, account_email, monthly_used, monthly_limit, extra_cost, quota_count) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		snapshot.CapturedAt.Format(time.RFC3339Nano),
		snapshot.RawJSON,
		snapshot.Plan,
		snapshot.AccountName,
		snapshot.AccountEmail,
		snapshot.MonthlyUsedUSD,
		snapshot.MonthlyLimitUSD,
		snapshot.ExtraCostUSD,
		len(snapshot.Quotas),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to insert ollama snapshot: %w", err)
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
			`INSERT INTO ollama_quota_values (snapshot_id, quota_name, used, limit_value, utilization, format, resets_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			snapshotID, q.Name, q.Used, q.Limit, q.Utilization, string(q.Format), resetsAt,
		)
		if err != nil {
			return 0, fmt.Errorf("failed to insert quota value %s: %w", q.Name, err)
		}
	}

	for _, m := range snapshot.Models {
		if _, err := tx.Exec(
			`INSERT INTO ollama_model_usage (snapshot_id, model, request_count, cost) VALUES (?, ?, ?, ?)`,
			snapshotID, m.Name, m.RequestCount, m.Cost,
		); err != nil {
			return 0, fmt.Errorf("failed to insert model usage %s: %w", m.Name, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit: %w", err)
	}

	return snapshotID, nil
}

func (s *Store) QueryLatestOllama() (*api.OllamaSnapshot, error) {
	var snapshot api.OllamaSnapshot
	var capturedAt string

	err := s.db.QueryRow(
		`SELECT id, captured_at, plan, account_name, account_email, monthly_used, monthly_limit, extra_cost FROM ollama_snapshots ORDER BY captured_at DESC LIMIT 1`,
	).Scan(&snapshot.ID, &capturedAt, &snapshot.Plan, &snapshot.AccountName, &snapshot.AccountEmail, &snapshot.MonthlyUsedUSD, &snapshot.MonthlyLimitUSD, &snapshot.ExtraCostUSD)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query latest ollama: %w", err)
	}

	snapshot.CapturedAt, _ = time.Parse(time.RFC3339Nano, capturedAt)
	if snapshot.Models, err = s.queryOllamaModelUsage(snapshot.ID); err != nil {
		return nil, err
	}

	rows, err := s.db.Query(
		`SELECT quota_name, used, limit_value, utilization, format, resets_at FROM ollama_quota_values WHERE snapshot_id = ? ORDER BY quota_name`,
		snapshot.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query quota values: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var q api.OllamaQuota
		var format string
		var resetsAt sql.NullString
		if err := rows.Scan(&q.Name, &q.Used, &q.Limit, &q.Utilization, &format, &resetsAt); err != nil {
			return nil, fmt.Errorf("failed to scan quota value: %w", err)
		}
		q.Format = api.OllamaQuotaFormat(format)
		if resetsAt.Valid && resetsAt.String != "" {
			t, _ := time.Parse(time.RFC3339Nano, resetsAt.String)
			q.ResetsAt = &t
		}
		snapshot.Quotas = append(snapshot.Quotas, q)
	}

	return &snapshot, rows.Err()
}

func (s *Store) QueryOllamaRange(start, end time.Time, limit ...int) ([]*api.OllamaSnapshot, error) {
	query := `SELECT id, captured_at, plan, account_name, monthly_used, monthly_limit, extra_cost FROM ollama_snapshots
		WHERE captured_at BETWEEN ? AND ? ORDER BY captured_at ASC`
	args := []interface{}{start.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano)}
	if len(limit) > 0 && limit[0] > 0 {
		query = `SELECT id, captured_at, plan, account_name, monthly_used, monthly_limit, extra_cost
			FROM (
				SELECT id, captured_at, plan, account_name, monthly_used, monthly_limit, extra_cost
				FROM ollama_snapshots
				WHERE captured_at BETWEEN ? AND ?
				ORDER BY captured_at DESC
				LIMIT ?
			) recent
			ORDER BY captured_at ASC`
		args = append(args, limit[0])
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query ollama range: %w", err)
	}
	defer rows.Close()

	var snapshots []*api.OllamaSnapshot
	for rows.Next() {
		var snap api.OllamaSnapshot
		var capturedAt string
		if err := rows.Scan(&snap.ID, &capturedAt, &snap.Plan, &snap.AccountName, &snap.MonthlyUsedUSD, &snap.MonthlyLimitUSD, &snap.ExtraCostUSD); err != nil {
			return nil, fmt.Errorf("failed to scan ollama snapshot: %w", err)
		}
		snap.CapturedAt, _ = time.Parse(time.RFC3339Nano, capturedAt)
		snapshots = append(snapshots, &snap)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, snap := range snapshots {
		qRows, err := s.db.Query(
			`SELECT quota_name, used, limit_value, utilization, format, resets_at FROM ollama_quota_values WHERE snapshot_id = ? ORDER BY quota_name`,
			snap.ID,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to query quota values for snapshot %d: %w", snap.ID, err)
		}
		for qRows.Next() {
			var q api.OllamaQuota
			var format string
			var resetsAt sql.NullString
			if err := qRows.Scan(&q.Name, &q.Used, &q.Limit, &q.Utilization, &format, &resetsAt); err != nil {
				qRows.Close()
				return nil, fmt.Errorf("failed to scan quota value: %w", err)
			}
			q.Format = api.OllamaQuotaFormat(format)
			if resetsAt.Valid && resetsAt.String != "" {
				t, _ := time.Parse(time.RFC3339Nano, resetsAt.String)
				q.ResetsAt = &t
			}
			snap.Quotas = append(snap.Quotas, q)
		}
		qRows.Close()
	}

	return snapshots, nil
}

func (s *Store) CreateOllamaCycle(quotaName string, cycleStart time.Time, resetsAt *time.Time) (int64, error) {
	var resetsAtVal interface{}
	if resetsAt != nil {
		resetsAtVal = resetsAt.Format(time.RFC3339Nano)
	}

	result, err := s.db.Exec(
		`INSERT INTO ollama_reset_cycles (quota_name, cycle_start, resets_at) VALUES (?, ?, ?)`,
		quotaName, cycleStart.Format(time.RFC3339Nano), resetsAtVal,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to create ollama cycle: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to get cycle ID: %w", err)
	}
	return id, nil
}

func (s *Store) CloseOllamaCycle(quotaName string, cycleEnd time.Time, peak, delta float64) error {
	_, err := s.db.Exec(
		`UPDATE ollama_reset_cycles SET cycle_end = ?, peak_utilization = ?, total_delta = ?
		WHERE quota_name = ? AND cycle_end IS NULL`,
		cycleEnd.Format(time.RFC3339Nano), peak, delta, quotaName,
	)
	if err != nil {
		return fmt.Errorf("failed to close ollama cycle: %w", err)
	}
	return nil
}

func (s *Store) UpdateOllamaCycle(quotaName string, peak, delta float64) error {
	_, err := s.db.Exec(
		`UPDATE ollama_reset_cycles SET peak_utilization = ?, total_delta = ?
		WHERE quota_name = ? AND cycle_end IS NULL`,
		peak, delta, quotaName,
	)
	if err != nil {
		return fmt.Errorf("failed to update ollama cycle: %w", err)
	}
	return nil
}

func (s *Store) QueryActiveOllamaCycle(quotaName string) (*OllamaResetCycle, error) {
	var cycle OllamaResetCycle
	var cycleStart string
	var cycleEnd, resetsAt sql.NullString

	err := s.db.QueryRow(
		`SELECT id, quota_name, cycle_start, cycle_end, resets_at, peak_utilization, total_delta
		FROM ollama_reset_cycles WHERE quota_name = ? AND cycle_end IS NULL`,
		quotaName,
	).Scan(
		&cycle.ID, &cycle.QuotaName, &cycleStart, &cycleEnd, &resetsAt,
		&cycle.PeakUtilization, &cycle.TotalDelta,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query active ollama cycle: %w", err)
	}

	cycle.CycleStart, _ = time.Parse(time.RFC3339Nano, cycleStart)
	if cycleEnd.Valid {
		t, _ := time.Parse(time.RFC3339Nano, cycleEnd.String)
		cycle.CycleEnd = &t
	}
	if resetsAt.Valid {
		t, _ := time.Parse(time.RFC3339Nano, resetsAt.String)
		cycle.ResetsAt = &t
	}

	return &cycle, nil
}

func (s *Store) QueryOllamaCycleHistory(quotaName string, limit ...int) ([]*OllamaResetCycle, error) {
	query := `SELECT id, quota_name, cycle_start, cycle_end, resets_at, peak_utilization, total_delta
		FROM ollama_reset_cycles WHERE quota_name = ? AND cycle_end IS NOT NULL ORDER BY cycle_start DESC`
	args := []interface{}{quotaName}
	if len(limit) > 0 && limit[0] > 0 {
		query += ` LIMIT ?`
		args = append(args, limit[0])
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query ollama cycles: %w", err)
	}
	defer rows.Close()

	var cycles []*OllamaResetCycle
	for rows.Next() {
		var cycle OllamaResetCycle
		var cycleStart, cycleEnd string
		var resetsAt sql.NullString

		if err := rows.Scan(&cycle.ID, &cycle.QuotaName, &cycleStart, &cycleEnd, &resetsAt,
			&cycle.PeakUtilization, &cycle.TotalDelta); err != nil {
			return nil, fmt.Errorf("failed to scan ollama cycle: %w", err)
		}

		cycle.CycleStart, _ = time.Parse(time.RFC3339Nano, cycleStart)
		t, _ := time.Parse(time.RFC3339Nano, cycleEnd)
		cycle.CycleEnd = &t
		if resetsAt.Valid {
			rt, _ := time.Parse(time.RFC3339Nano, resetsAt.String)
			cycle.ResetsAt = &rt
		}

		cycles = append(cycles, &cycle)
	}

	return cycles, rows.Err()
}

func (s *Store) QueryOllamaCyclesSince(quotaName string, since time.Time) ([]*OllamaResetCycle, error) {
	rows, err := s.db.Query(
		`SELECT id, quota_name, cycle_start, cycle_end, resets_at, peak_utilization, total_delta
		FROM ollama_reset_cycles WHERE quota_name = ? AND cycle_end IS NOT NULL AND cycle_start >= ?
		ORDER BY cycle_start DESC`,
		quotaName, since.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query ollama cycles since: %w", err)
	}
	defer rows.Close()

	var cycles []*OllamaResetCycle
	for rows.Next() {
		var cycle OllamaResetCycle
		var cycleStart, cycleEnd string
		var resetsAt sql.NullString

		if err := rows.Scan(&cycle.ID, &cycle.QuotaName, &cycleStart, &cycleEnd, &resetsAt,
			&cycle.PeakUtilization, &cycle.TotalDelta); err != nil {
			return nil, fmt.Errorf("failed to scan ollama cycle: %w", err)
		}

		cycle.CycleStart, _ = time.Parse(time.RFC3339Nano, cycleStart)
		t, _ := time.Parse(time.RFC3339Nano, cycleEnd)
		cycle.CycleEnd = &t
		if resetsAt.Valid {
			rt, _ := time.Parse(time.RFC3339Nano, resetsAt.String)
			cycle.ResetsAt = &rt
		}

		cycles = append(cycles, &cycle)
	}

	return cycles, rows.Err()
}

func (s *Store) QueryOllamaUtilizationSeries(quotaName string, since time.Time) ([]UtilizationPoint, error) {
	rows, err := s.db.Query(
		`SELECT s.captured_at, qv.utilization
		FROM ollama_quota_values qv
		JOIN ollama_snapshots s ON s.id = qv.snapshot_id
		WHERE qv.quota_name = ? AND s.captured_at >= ?
		ORDER BY s.captured_at ASC`,
		quotaName, since.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query utilization series: %w", err)
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

func (s *Store) QueryOllamaLatestPerQuota() ([]OllamaLatestQuota, error) {
	rows, err := s.db.Query(`
		SELECT qv.quota_name, qv.used, qv.limit_value, qv.utilization, qv.format, qv.resets_at,
		       s.captured_at, s.plan, s.account_name
		FROM ollama_quota_values qv
		JOIN ollama_snapshots s ON s.id = qv.snapshot_id
		WHERE s.id = (SELECT MAX(id) FROM ollama_snapshots)
		ORDER BY qv.quota_name ASC`)
	if err != nil {
		return nil, fmt.Errorf("failed to query latest per-quota: %w", err)
	}
	defer rows.Close()

	var results []OllamaLatestQuota
	for rows.Next() {
		var name, format, plan, accountName string
		var used, limitValue, utilization float64
		var resetsAt sql.NullString
		var capturedAt string

		if err := rows.Scan(&name, &used, &limitValue, &utilization, &format, &resetsAt, &capturedAt, &plan, &accountName); err != nil {
			return nil, fmt.Errorf("failed to scan latest quota: %w", err)
		}

		q := OllamaLatestQuota{
			Name:        name,
			Used:        used,
			Limit:       limitValue,
			Utilization: utilization,
			Format:      format,
			Plan:        plan,
			AccountName: accountName,
		}
		q.CapturedAt, _ = time.Parse(time.RFC3339Nano, capturedAt)
		if resetsAt.Valid && resetsAt.String != "" {
			t, _ := time.Parse(time.RFC3339Nano, resetsAt.String)
			q.ResetsAt = &t
		}
		results = append(results, q)
	}
	return results, rows.Err()
}

func (s *Store) QueryAllOllamaQuotaNames() ([]string, error) {
	rows, err := s.db.Query(
		`SELECT DISTINCT quota_name FROM ollama_reset_cycles ORDER BY quota_name`,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query ollama quota names: %w", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("failed to scan quota name: %w", err)
		}
		names = append(names, name)
	}

	return names, rows.Err()
}

func (s *Store) QueryOllamaCycleOverview(groupBy string, limit int) ([]CycleOverviewRow, error) {
	if limit <= 0 {
		limit = 50
	}

	var cycles []*OllamaResetCycle
	activeCycle, err := s.QueryActiveOllamaCycle(groupBy)
	if err != nil {
		return nil, fmt.Errorf("store.QueryOllamaCycleOverview: active: %w", err)
	}
	if activeCycle != nil {
		cycles = append(cycles, activeCycle)
		limit--
	}

	completedCycles, err := s.QueryOllamaCycleHistory(groupBy, limit)
	if err != nil {
		return nil, fmt.Errorf("store.QueryOllamaCycleOverview: %w", err)
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
			`SELECT s.id, s.captured_at FROM ollama_snapshots s
			JOIN ollama_quota_values qv ON qv.snapshot_id = s.id
			WHERE qv.quota_name = ? AND s.captured_at >= ? AND s.captured_at < ?
			ORDER BY qv.utilization DESC LIMIT 1`,
			groupBy,
			c.CycleStart.Format(time.RFC3339Nano),
			endBoundary.Format(time.RFC3339Nano),
		).Scan(&snapshotID, &capturedAt)

		if err == sql.ErrNoRows {
			overviewRows = append(overviewRows, row)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("store.QueryOllamaCycleOverview: peak snapshot: %w", err)
		}

		row.PeakTime, _ = time.Parse(time.RFC3339Nano, capturedAt)

		qRows, err := s.db.Query(
			`SELECT quota_name, utilization, used, limit_value FROM ollama_quota_values WHERE snapshot_id = ? ORDER BY quota_name`,
			snapshotID,
		)
		if err != nil {
			return nil, fmt.Errorf("store.QueryOllamaCycleOverview: quota values: %w", err)
		}
		for qRows.Next() {
			var entry CrossQuotaEntry
			if err := qRows.Scan(&entry.Name, &entry.Percent, &entry.Value, new(float64)); err != nil {
				qRows.Close()
				return nil, fmt.Errorf("store.QueryOllamaCycleOverview: scan quota: %w", err)
			}
			row.CrossQuotas = append(row.CrossQuotas, entry)
		}
		qRows.Close()

		overviewRows = append(overviewRows, row)
	}

	return overviewRows, nil
}

// queryOllamaModelUsage loads the per-model breakdown stored with a snapshot.
func (s *Store) queryOllamaModelUsage(snapshotID int64) ([]api.OllamaModelUsage, error) {
	rows, err := s.db.Query(
		`SELECT model, request_count, cost FROM ollama_model_usage WHERE snapshot_id = ? ORDER BY request_count DESC, model ASC`,
		snapshotID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query ollama model usage: %w", err)
	}
	defer rows.Close()

	var models []api.OllamaModelUsage
	for rows.Next() {
		var m api.OllamaModelUsage
		if err := rows.Scan(&m.Name, &m.RequestCount, &m.Cost); err != nil {
			return nil, fmt.Errorf("failed to scan ollama model usage: %w", err)
		}
		models = append(models, m)
	}
	return models, rows.Err()
}
