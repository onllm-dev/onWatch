package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

const zenmuxTimeLayout = "2006-01-02T15:04:05.000000000Z"

// InsertZenmuxSnapshot stores the typed response, rather than an untrusted raw
// server body, so unknown fields and possible echoed secrets are not retained.
func (s *Store) InsertZenmuxSnapshot(snapshot *api.ZenmuxSnapshot) (int64, error) {
	if snapshot == nil {
		return 0, fmt.Errorf("nil zenmux snapshot")
	}
	body, err := json.Marshal(snapshot)
	if err != nil {
		return 0, err
	}
	result, err := s.db.Exec(`INSERT INTO zenmux_snapshots (captured_at, payload) VALUES (?, ?)`, snapshot.CapturedAt.UTC().Format(zenmuxTimeLayout), string(body))
	if err != nil {
		return 0, fmt.Errorf("insert zenmux snapshot: %w", err)
	}
	return result.LastInsertId()
}

func scanZenmuxSnapshot(scanner interface{ Scan(...interface{}) error }) (*api.ZenmuxSnapshot, error) {
	var id int64
	var payload string
	if err := scanner.Scan(&id, &payload); err != nil {
		return nil, err
	}
	var snapshot api.ZenmuxSnapshot
	if err := json.Unmarshal([]byte(payload), &snapshot); err != nil {
		return nil, fmt.Errorf("decode zenmux snapshot: %w", err)
	}
	snapshot.ID = id
	return &snapshot, nil
}

func (s *Store) QueryLatestZenmux() (*api.ZenmuxSnapshot, error) {
	snap, err := scanZenmuxSnapshot(s.db.QueryRow(`SELECT id, payload FROM zenmux_snapshots ORDER BY captured_at DESC, id DESC LIMIT 1`))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return snap, err
}

// QueryRecentZenmux returns a bounded, unsampled sequence for local rate estimates.
func (s *Store) QueryRecentZenmux(ctx context.Context, since time.Time) ([]*api.ZenmuxSnapshot, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, payload FROM zenmux_snapshots WHERE captured_at >= ? ORDER BY captured_at DESC, id DESC LIMIT 201`, since.UTC().Format(zenmuxTimeLayout))
	if err != nil {
		return nil, fmt.Errorf("query zenmux estimates: %w", err)
	}
	defer rows.Close()
	var snapshots []*api.ZenmuxSnapshot
	for rows.Next() {
		snap, err := scanZenmuxSnapshot(rows)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snap)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return snapshots, nil
}

// QueryZenmuxChartRange samples the whole range, including the newest value,
// at no more than 200 snapshots, matching other provider charts.
func (s *Store) QueryZenmuxChartRange(start, end time.Time) ([]*api.ZenmuxSnapshot, error) {
	rows, err := s.db.Query(windowChartQuery("zenmux_snapshots", "id, payload"), start.UTC().Format(zenmuxTimeLayout), end.UTC().Format(zenmuxTimeLayout))
	if err != nil {
		return nil, fmt.Errorf("query zenmux history: %w", err)
	}
	defer rows.Close()
	snapshots := make([]*api.ZenmuxSnapshot, 0)
	for rows.Next() {
		snap, err := scanZenmuxSnapshot(rows)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snap)
	}
	return snapshots, rows.Err()
}
