package store

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// snapshotValueBatchSize bounds the IN list per query, well under SQLite's
// variable limit. Ranges longer than this are loaded in several queries
// instead of one query per snapshot.
const snapshotValueBatchSize = 400

// loadSnapshotValuesBatched loads rows from a per-snapshot value table for
// every id, in batches of at most snapshotValueBatchSize, and groups them by
// snapshot id. Rows come back ordered by snapshot_id then orderBy, so each
// snapshot's slice keeps the same order a single-snapshot query would return.
//
// table, columns and orderBy must be compile-time constants: SQL identifiers
// cannot be bound as parameters. scan receives the row (with snapshot_id as
// the first selected column) and returns the parsed value.
func loadSnapshotValuesBatched[T any](
	s *Store,
	ids []int64,
	table, columns, orderBy string,
	scan func(rows *sql.Rows) (snapshotID int64, value T, err error),
) (map[int64][]T, error) {
	values := make(map[int64][]T, len(ids))
	for offset := 0; offset < len(ids); offset += snapshotValueBatchSize {
		batch := ids[offset:min(offset+snapshotValueBatchSize, len(ids))]
		args := make([]interface{}, len(batch))
		for i, id := range batch {
			args[i] = id
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")

		rows, err := s.db.Query(
			`SELECT snapshot_id, `+columns+` FROM `+table+
				` WHERE snapshot_id IN (`+placeholders+`) ORDER BY snapshot_id, `+orderBy,
			args...,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to query %s: %w", table, err)
		}
		for rows.Next() {
			snapshotID, value, err := scan(rows)
			if err != nil {
				rows.Close()
				return nil, fmt.Errorf("failed to scan %s row: %w", table, err)
			}
			values[snapshotID] = append(values[snapshotID], value)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, fmt.Errorf("failed reading %s: %w", table, err)
		}
	}
	return values, nil
}

// loadQuotaUtilization loads quota_name/utilization pairs for the snapshots in
// batches, keyed by snapshot id. Used where a function needs every quota's
// value at one snapshot per cycle.
func loadQuotaUtilization(s *Store, ids []int64, table string) (map[int64]map[string]float64, error) {
	type pair struct {
		name        string
		utilization float64
	}
	bySnapshot, err := loadSnapshotValuesBatched(s, ids, table, "quota_name, utilization", "quota_name",
		func(rows *sql.Rows) (int64, pair, error) {
			var snapshotID int64
			var p pair
			if err := rows.Scan(&snapshotID, &p.name, &p.utilization); err != nil {
				return 0, p, err
			}
			return snapshotID, p, nil
		})
	if err != nil {
		return nil, err
	}
	out := make(map[int64]map[string]float64, len(bySnapshot))
	for snapshotID, pairs := range bySnapshot {
		values := make(map[string]float64, len(pairs))
		for _, p := range pairs {
			values[p.name] = p.utilization
		}
		out[snapshotID] = values
	}
	return out, nil
}

// sortedUtilizationNames returns the map's keys in ascending order, matching
// the ORDER BY quota_name the per-snapshot queries used.
func sortedUtilizationNames(values map[string]float64) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
