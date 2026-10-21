package store

import (
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// windowCycleRepairKey guards the one-off repair for Command Code and OpenCode
// reset cycles written before the reset-boundary fix.
const windowCycleRepairKey = "window_cycle_repair_v1"

// windowCycleRepairLimit matches the project's cycles<=200 query budget.
const windowCycleRepairLimit = 200

// shortWindowCycleCutoff is the churn length left by the reset-boundary bug.
const shortWindowCycleCutoff = 5 * time.Minute

// invertedMergeCutoff bounds how far apart an inverted cycle and its successor
// may start before the inverted one is clamped instead of merged. Churn cycles
// are adjacent, so any boundary this close belongs to the same window.
const invertedMergeCutoff = 15 * time.Minute

type windowCycleRepairCounts struct {
	InvertedMerged     int
	InvertedClamped    int
	ShortFolded        int
	Recalculated       int
	ActiveResetCleared int
}

func (c windowCycleRepairCounts) total() int {
	return c.InvertedMerged + c.InvertedClamped + c.ShortFolded + c.Recalculated + c.ActiveResetCleared
}

// windowCycleTables describes the two provider tables sharing this shape.
type windowCycleTables struct {
	cycles      string
	quotaValues string
	snapshots   string
}

func windowCycleTableSet(provider string) windowCycleTables {
	if provider == "opencode" {
		return windowCycleTables{
			cycles:      "opencode_reset_cycles",
			quotaValues: "opencode_quota_values",
			snapshots:   "opencode_snapshots",
		}
	}
	return windowCycleTables{
		cycles:      "commandcode_reset_cycles",
		quotaValues: "commandcode_quota_values",
		snapshots:   "commandcode_snapshots",
	}
}

// RepairWindowResetCyclesIfNeeded repairs derived reset cycles corrupted by
// the reset-boundary churn. It runs once, guarded by a settings marker. Raw
// snapshots are never modified.
func (s *Store) RepairWindowResetCyclesIfNeeded(logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}

	done, err := s.GetSetting(windowCycleRepairKey)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			// Partial databases used by schema-migration tests have no settings
			// table; there is nothing to repair there either.
			return nil
		}
		return fmt.Errorf("window cycle repair: check marker: %w", err)
	}
	if done == "done" {
		return nil
	}

	for _, provider := range []string{"commandcode", "opencode"} {
		counts, err := s.repairWindowResetCycles(windowCycleTableSet(provider))
		if err != nil {
			return fmt.Errorf("window cycle repair: %s: %w", provider, err)
		}
		if counts.total() == 0 {
			logger.Debug("No window reset cycles needed repair", "provider", provider)
			continue
		}
		logger.Info("Repaired window reset cycles",
			"provider", provider,
			"invertedMerged", counts.InvertedMerged,
			"invertedClamped", counts.InvertedClamped,
			"shortFolded", counts.ShortFolded,
			"recalculated", counts.Recalculated,
			"activeResetCleared", counts.ActiveResetCleared,
		)
	}

	if err := s.SetSetting(windowCycleRepairKey, "done"); err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil
		}
		return fmt.Errorf("window cycle repair: set marker: %w", err)
	}
	return nil
}

type windowCycleCandidate struct {
	id       int64
	quota    string
	start    time.Time
	end      *time.Time
	resetsAt *time.Time
	peak     float64
	delta    float64
}

// repairWindowResetCycles repairs one provider table inside a single
// transaction. Every query is aggregated per cycle, so memory stays bounded by
// the candidate slice rather than the snapshot history.
func (s *Store) repairWindowResetCycles(t windowCycleTables) (windowCycleRepairCounts, error) {
	var counts windowCycleRepairCounts

	candidates, err := s.windowCycleCandidates(t)
	if err != nil {
		return counts, err
	}
	if len(candidates) == 0 {
		return counts, nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return counts, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	deleted := make(map[int64]bool)

	if err := repairInvertedWindowCycles(tx, t, candidates, deleted, &counts); err != nil {
		return counts, err
	}
	if err := foldShortWindowCycles(tx, t, candidates, deleted, &counts); err != nil {
		return counts, err
	}
	if err := recalculateWindowCycleStats(tx, t, candidates, deleted, &counts); err != nil {
		return counts, err
	}
	if err := clearStaleActiveWindowResets(tx, t, &counts); err != nil {
		return counts, err
	}

	if err := tx.Commit(); err != nil {
		return counts, fmt.Errorf("commit: %w", err)
	}
	return counts, nil
}

func (s *Store) windowCycleCandidates(t windowCycleTables) ([]*windowCycleCandidate, error) {
	query := fmt.Sprintf(`
		SELECT id, quota_name, cycle_start, cycle_end, resets_at, peak_utilization, total_delta
		FROM (
			SELECT id, quota_name, cycle_start, cycle_end, resets_at, peak_utilization, total_delta,
			       ROW_NUMBER() OVER (PARTITION BY quota_name ORDER BY cycle_start DESC, id DESC) AS rn
			FROM %s
		) WHERE rn <= ?
		ORDER BY quota_name ASC, cycle_start ASC, id ASC`, t.cycles)

	rows, err := s.db.Query(query, windowCycleRepairLimit)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil, nil
		}
		return nil, fmt.Errorf("query candidates: %w", err)
	}
	defer rows.Close()

	var candidates []*windowCycleCandidate
	for rows.Next() {
		var c windowCycleCandidate
		var start string
		var end, resetsAt sql.NullString
		if err := rows.Scan(&c.id, &c.quota, &start, &end, &resetsAt, &c.peak, &c.delta); err != nil {
			return nil, fmt.Errorf("scan candidate: %w", err)
		}
		c.start, _ = time.Parse(time.RFC3339Nano, start)
		if end.Valid && end.String != "" {
			if parsed, err := time.Parse(time.RFC3339Nano, end.String); err == nil {
				c.end = &parsed
			}
		}
		if resetsAt.Valid && resetsAt.String != "" {
			if parsed, err := time.Parse(time.RFC3339Nano, resetsAt.String); err == nil {
				c.resetsAt = &parsed
			}
		}
		candidates = append(candidates, &c)
	}
	return candidates, rows.Err()
}

// successor returns the next candidate for the same quota, skipping rows that
// have already been merged away.
func successor(candidates []*windowCycleCandidate, idx int, deleted map[int64]bool) *windowCycleCandidate {
	quota := candidates[idx].quota
	start := candidates[idx].start
	for _, c := range candidates[idx+1:] {
		if c.quota != quota {
			return nil
		}
		if deleted[c.id] {
			continue
		}
		if c.start.After(start) {
			return c
		}
	}
	return nil
}

func repairInvertedWindowCycles(tx *sql.Tx, t windowCycleTables, candidates []*windowCycleCandidate, deleted map[int64]bool, counts *windowCycleRepairCounts) error {
	for i, c := range candidates {
		if deleted[c.id] || c.end == nil || !c.end.Before(c.start) {
			continue
		}

		next := successor(candidates, i, deleted)
		if next != nil && next.start.Sub(c.start) <= invertedMergeCutoff {
			if err := mergeIntoNextWindowCycle(tx, t, c, next, deleted, counts); err != nil {
				return err
			}
			continue
		}

		newEnd, ok, err := lastWindowSample(tx, t, c, next)
		if err != nil {
			return err
		}
		if !ok || newEnd.Before(c.start) {
			// No retained sample can repair the boundary; keeping the row
			// inverted is worse than collapsing it onto its start.
			newEnd = c.start
		}
		if c.end.After(newEnd) {
			newEnd = *c.end
		}
		if newEnd.Equal(*c.end) {
			continue
		}
		if _, err := tx.Exec(
			fmt.Sprintf(`UPDATE %s SET cycle_end = ? WHERE id = ?`, t.cycles),
			newEnd.Format(time.RFC3339Nano), c.id,
		); err != nil {
			return fmt.Errorf("clamp inverted cycle: %w", err)
		}
		c.end = &newEnd
		counts.InvertedClamped++
	}
	return nil
}

// mergeIntoNextWindowCycle folds an adjacent inverted cycle into its successor
// and moves the successor's start back to the earliest boundary, so the churn
// samples end up inside a single window.
func mergeIntoNextWindowCycle(tx *sql.Tx, t windowCycleTables, c, next *windowCycleCandidate, deleted map[int64]bool, counts *windowCycleRepairCounts) error {
	if _, err := tx.Exec(
		fmt.Sprintf(`UPDATE %s SET cycle_start = ? WHERE id = ? AND julianday(cycle_start) > julianday(?)`, t.cycles),
		c.start.Format(time.RFC3339Nano), next.id, c.start.Format(time.RFC3339Nano),
	); err != nil {
		return fmt.Errorf("merge inverted cycle: %w", err)
	}
	if _, err := tx.Exec(fmt.Sprintf(`DELETE FROM %s WHERE id = ?`, t.cycles), c.id); err != nil {
		return fmt.Errorf("delete inverted cycle: %w", err)
	}
	next.start = c.start
	deleted[c.id] = true
	counts.InvertedMerged++
	return nil
}

// lastWindowSample returns the last sample captured at or after the cycle's
// start, bounded by the next cycle when one is known. The stored end is not a
// usable bound here: it is the value being repaired. Without a successor the
// cycle's own reset bounds the window, so samples from the next one are not
// borrowed into it.
func lastWindowSample(tx *sql.Tx, t windowCycleTables, c *windowCycleCandidate, next *windowCycleCandidate) (time.Time, bool, error) {
	query := fmt.Sprintf(`
		SELECT MAX(s.captured_at)
		FROM %s qv
		JOIN %s s ON s.id = qv.snapshot_id
		WHERE qv.quota_name = ? AND julianday(s.captured_at) >= julianday(?)`, t.quotaValues, t.snapshots)
	args := []interface{}{c.quota, c.start.Format(time.RFC3339Nano)}
	var upper *time.Time
	if next != nil {
		upper = &next.start
	} else if c.resetsAt != nil && c.resetsAt.After(c.start) {
		upper = c.resetsAt
	}
	if upper != nil {
		query += ` AND julianday(s.captured_at) < julianday(?)`
		args = append(args, upper.Format(time.RFC3339Nano))
	}

	var captured sql.NullString
	if err := tx.QueryRow(query, args...).Scan(&captured); err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return time.Time{}, false, nil
		}
		return time.Time{}, false, fmt.Errorf("last window sample: %w", err)
	}
	if !captured.Valid || captured.String == "" {
		return time.Time{}, false, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, captured.String)
	if err != nil {
		return time.Time{}, false, nil
	}
	return parsed, true, nil
}

// foldShortWindowCycles deletes churn-length cycles that carry no reset and
// hands their start to the successor, so the samples stay inside a window.
func foldShortWindowCycles(tx *sql.Tx, t windowCycleTables, candidates []*windowCycleCandidate, deleted map[int64]bool, counts *windowCycleRepairCounts) error {
	for i, c := range candidates {
		if deleted[c.id] || c.end == nil || c.resetsAt != nil {
			continue
		}
		if c.end.Sub(c.start) >= shortWindowCycleCutoff {
			continue
		}
		next := successor(candidates, i, deleted)
		if next == nil {
			continue
		}
		if _, err := tx.Exec(
			fmt.Sprintf(`UPDATE %s SET cycle_start = ? WHERE id = ?`, t.cycles),
			c.start.Format(time.RFC3339Nano), next.id,
		); err != nil {
			return fmt.Errorf("fold short cycle into successor: %w", err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`DELETE FROM %s WHERE id = ?`, t.cycles), c.id); err != nil {
			return fmt.Errorf("delete short cycle: %w", err)
		}
		next.start = c.start
		deleted[c.id] = true
		counts.ShortFolded++
	}
	return nil
}

// recalculateWindowCycleStats recomputes peak and delta from the samples that
// fall inside each closed cycle. Cycles with no retained samples are left
// alone; derived values are only written when they actually differ. The active
// cycle is skipped: the tracker owns its running totals.
func recalculateWindowCycleStats(tx *sql.Tx, t windowCycleTables, candidates []*windowCycleCandidate, deleted map[int64]bool, counts *windowCycleRepairCounts) error {
	for _, c := range candidates {
		if deleted[c.id] || c.end == nil {
			continue
		}

		var samples int
		var peak float64
		err := tx.QueryRow(fmt.Sprintf(`
			SELECT COUNT(*), COALESCE(MAX(qv.utilization), 0)
			FROM %s qv
			JOIN %s s ON s.id = qv.snapshot_id
			WHERE qv.quota_name = ? AND julianday(s.captured_at) >= julianday(?)%s`,
			t.quotaValues, t.snapshots, windowCycleEndClause(c)),
			append([]interface{}{c.quota, c.start.Format(time.RFC3339Nano)}, windowCycleEndArgs(c)...)...,
		).Scan(&samples, &peak)
		if err != nil {
			return fmt.Errorf("aggregate cycle stats: %w", err)
		}
		if samples == 0 {
			// Retention may have removed every sample behind this cycle.
			continue
		}

		delta, err := windowCycleDelta(tx, t, c)
		if err != nil {
			return err
		}
		if peak == c.peak && delta == c.delta {
			continue
		}
		if _, err := tx.Exec(
			fmt.Sprintf(`UPDATE %s SET peak_utilization = ?, total_delta = ? WHERE id = ?`, t.cycles),
			peak, delta, c.id,
		); err != nil {
			return fmt.Errorf("update cycle stats: %w", err)
		}
		c.peak, c.delta = peak, delta
		counts.Recalculated++
	}
	return nil
}

// windowCycleEndClause and windowCycleEndArgs keep the open-ended active cycle
// in the same query shape as closed ones.
func windowCycleEndClause(c *windowCycleCandidate) string {
	if c.end == nil {
		return ""
	}
	return " AND julianday(s.captured_at) < julianday(?)"
}

func windowCycleEndArgs(c *windowCycleCandidate) []interface{} {
	if c.end == nil {
		return nil
	}
	return []interface{}{c.end.Format(time.RFC3339Nano)}
}

// windowCycleDelta sums positive consecutive increases over the cycle's
// samples. LAG keeps the walk inside SQLite instead of materialising rows.
func windowCycleDelta(tx *sql.Tx, t windowCycleTables, c *windowCycleCandidate) (float64, error) {
	query := fmt.Sprintf(`
		SELECT COALESCE(SUM(CASE WHEN u > prev THEN u - prev ELSE 0 END), 0)
		FROM (
			SELECT qv.utilization AS u,
			       LAG(qv.utilization) OVER (ORDER BY s.captured_at ASC, s.id ASC) AS prev
			FROM %s qv
			JOIN %s s ON s.id = qv.snapshot_id
			WHERE qv.quota_name = ? AND julianday(s.captured_at) >= julianday(?)%s
		)`, t.quotaValues, t.snapshots, windowCycleEndClause(c))

	args := append([]interface{}{c.quota, c.start.Format(time.RFC3339Nano)}, windowCycleEndArgs(c)...)
	var delta float64
	if err := tx.QueryRow(query, args...).Scan(&delta); err != nil {
		return 0, fmt.Errorf("compute cycle delta: %w", err)
	}
	return delta, nil
}

func clearStaleActiveWindowResets(tx *sql.Tx, t windowCycleTables, counts *windowCycleRepairCounts) error {
	res, err := tx.Exec(fmt.Sprintf(`
		UPDATE %s SET resets_at = NULL
		WHERE cycle_end IS NULL AND resets_at IS NOT NULL
		  AND julianday(resets_at) <= julianday(cycle_start)`, t.cycles))
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil
		}
		return fmt.Errorf("clear stale active resets: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil
	}
	counts.ActiveResetCleared += int(n)
	return nil
}
