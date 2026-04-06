package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	apiintegrations "github.com/onllm-dev/onwatch/v2/internal/api_integrations"
	sqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

const apiIntegrationUsageSummaryLimit = 500

// APIIntegrationUsageSummaryRow contains grouped usage totals for backend reporting.
type APIIntegrationUsageSummaryRow struct {
	IntegrationName  string
	Provider         string
	AccountName      string
	Model            string
	RequestCount     int
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	TotalCostUSD     float64
	LastCapturedAt   time.Time
}

// APIIntegrationUsageBucketRow contains aggregated usage for one integration and time bucket.
type APIIntegrationUsageBucketRow struct {
	IntegrationName  string
	BucketStart      time.Time
	RequestCount     int
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	TotalCostUSD     float64
}

// APIIntegrationIngestHealthRow contains persisted ingest state with last seen event time.
type APIIntegrationIngestHealthRow struct {
	SourcePath     string
	OffsetBytes    int64
	FileSize       int64
	FileModTime    *time.Time
	PartialLine    string
	UpdatedAt      time.Time
	LastCapturedAt *time.Time
}

// InsertAPIIntegrationUsageEvent stores a normalized API integrations telemetry event.
func (s *Store) InsertAPIIntegrationUsageEvent(event *apiintegrations.UsageEvent) (int64, error) {
	if event == nil {
		return 0, fmt.Errorf("API integration usage event is nil")
	}
	res, err := s.db.Exec(`
		INSERT INTO api_integration_usage_events (
			captured_at, integration_name, provider, account_name, model, request_id,
			prompt_tokens, completion_tokens, total_tokens, cost_usd, latency_ms,
			metadata_json, source_path, fingerprint, raw_line, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		event.Timestamp.Format(time.RFC3339Nano),
		event.Integration,
		event.Provider,
		event.Account,
		event.Model,
		event.RequestID,
		event.PromptTokens,
		event.CompletionTokens,
		event.TotalTokens,
		event.CostUSD,
		event.LatencyMS,
		event.MetadataJSON,
		event.SourcePath,
		event.Fingerprint,
		event.RawLine,
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		if isSQLiteUniqueConstraintError(err) {
			return 0, ErrDuplicateAPIIntegrationUsageEvent
		}
		return 0, fmt.Errorf("failed to insert API integration usage event: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to get API integration usage event id: %w", err)
	}
	return id, nil
}

// QueryAPIIntegrationUsageRange returns API integration usage events ordered by capture time ascending.
func (s *Store) QueryAPIIntegrationUsageRange(start, end time.Time, limit ...int) ([]apiintegrations.UsageEvent, error) {
	query := `
		SELECT captured_at, integration_name, provider, account_name, model, request_id,
		       prompt_tokens, completion_tokens, total_tokens, cost_usd, latency_ms,
		       metadata_json, source_path, fingerprint, raw_line
		FROM api_integration_usage_events
		WHERE captured_at BETWEEN ? AND ?
		ORDER BY captured_at ASC
	`
	args := []interface{}{start.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano)}
	if len(limit) > 0 && limit[0] > 0 {
		query += ` LIMIT ?`
		args = append(args, limit[0])
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query API integration usage range: %w", err)
	}
	defer rows.Close()

	var events []apiintegrations.UsageEvent
	for rows.Next() {
		var event apiintegrations.UsageEvent
		var capturedAt string
		var costUSD sql.NullFloat64
		var latencyMS sql.NullInt64
		if err := rows.Scan(
			&capturedAt,
			&event.Integration,
			&event.Provider,
			&event.Account,
			&event.Model,
			&event.RequestID,
			&event.PromptTokens,
			&event.CompletionTokens,
			&event.TotalTokens,
			&costUSD,
			&latencyMS,
			&event.MetadataJSON,
			&event.SourcePath,
			&event.Fingerprint,
			&event.RawLine,
		); err != nil {
			return nil, fmt.Errorf("failed to scan API integration usage event: %w", err)
		}
		event.Timestamp, _ = time.Parse(time.RFC3339Nano, capturedAt)
		if costUSD.Valid {
			v := costUSD.Float64
			event.CostUSD = &v
		}
		if latencyMS.Valid {
			v := int(latencyMS.Int64)
			event.LatencyMS = &v
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

// DeleteAPIIntegrationUsageEventsOlderThan removes stored usage events older than the cutoff.
func (s *Store) DeleteAPIIntegrationUsageEventsOlderThan(cutoff time.Time) (int64, error) {
	res, err := s.db.Exec(`
		DELETE FROM api_integration_usage_events
		WHERE captured_at < ?
	`, cutoff.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, fmt.Errorf("failed to delete expired API integration usage events: %w", err)
	}
	deleted, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to count deleted API integration usage events: %w", err)
	}
	return deleted, nil
}

// QueryAPIIntegrationUsageSummary groups usage by integration/provider/account/model.
func (s *Store) QueryAPIIntegrationUsageSummary() ([]APIIntegrationUsageSummaryRow, error) {
	rows, err := s.db.Query(`
		SELECT integration_name, provider, account_name, model,
		       COUNT(*),
		       COALESCE(SUM(prompt_tokens), 0),
		       COALESCE(SUM(completion_tokens), 0),
		       COALESCE(SUM(total_tokens), 0),
		       COALESCE(SUM(cost_usd), 0),
		       MAX(captured_at)
		FROM api_integration_usage_events
		GROUP BY integration_name, provider, account_name, model
		ORDER BY integration_name, provider, account_name, model
		LIMIT ?
	`, apiIntegrationUsageSummaryLimit)
	if err != nil {
		return nil, fmt.Errorf("failed to query API integration usage summary: %w", err)
	}
	defer rows.Close()

	var summary []APIIntegrationUsageSummaryRow
	for rows.Next() {
		var row APIIntegrationUsageSummaryRow
		var lastCapturedAt string
		if err := rows.Scan(
			&row.IntegrationName,
			&row.Provider,
			&row.AccountName,
			&row.Model,
			&row.RequestCount,
			&row.PromptTokens,
			&row.CompletionTokens,
			&row.TotalTokens,
			&row.TotalCostUSD,
			&lastCapturedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan API integration usage summary: %w", err)
		}
		row.LastCapturedAt, _ = time.Parse(time.RFC3339Nano, lastCapturedAt)
		summary = append(summary, row)
	}
	return summary, rows.Err()
}

// QueryAPIIntegrationUsageBuckets groups usage into time buckets over a range.
func (s *Store) QueryAPIIntegrationUsageBuckets(start, end time.Time, bucketSize time.Duration) ([]APIIntegrationUsageBucketRow, error) {
	if bucketSize <= 0 {
		return nil, fmt.Errorf("bucket size must be positive")
	}

	bucketSeconds := int64(bucketSize / time.Second)
	rows, err := s.db.Query(`
		SELECT integration_name,
		       strftime('%Y-%m-%dT%H:%M:%SZ', (CAST(strftime('%s', captured_at) AS INTEGER) / ?) * ?, 'unixepoch'),
		       COUNT(*),
		       COALESCE(SUM(prompt_tokens), 0),
		       COALESCE(SUM(completion_tokens), 0),
		       COALESCE(SUM(total_tokens), 0),
		       COALESCE(SUM(cost_usd), 0)
		FROM api_integration_usage_events
		WHERE captured_at BETWEEN ? AND ?
		GROUP BY integration_name, 2
		ORDER BY integration_name, 2
	`, bucketSeconds, bucketSeconds, start.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano))
	if err != nil {
		return nil, fmt.Errorf("failed to query API integration usage buckets: %w", err)
	}
	defer rows.Close()

	var buckets []APIIntegrationUsageBucketRow
	for rows.Next() {
		var row APIIntegrationUsageBucketRow
		var bucketStart string
		if err := rows.Scan(
			&row.IntegrationName,
			&bucketStart,
			&row.RequestCount,
			&row.PromptTokens,
			&row.CompletionTokens,
			&row.TotalTokens,
			&row.TotalCostUSD,
		); err != nil {
			return nil, fmt.Errorf("failed to scan API integration usage bucket: %w", err)
		}
		row.BucketStart, _ = time.Parse(time.RFC3339Nano, bucketStart)
		buckets = append(buckets, row)
	}
	return buckets, rows.Err()
}

// GetAPIIntegrationIngestState returns the persisted tail cursor for a source file.
func (s *Store) GetAPIIntegrationIngestState(sourcePath string) (*apiintegrations.IngestState, error) {
	var state apiintegrations.IngestState
	var modTime sql.NullString
	var updatedAt string
	err := s.db.QueryRow(`
		SELECT source_path, offset_bytes, file_size, file_mod_time, partial_line, updated_at
		FROM api_integration_ingest_state
		WHERE source_path = ?
	`, sourcePath).Scan(&state.SourcePath, &state.Offset, &state.FileSize, &modTime, &state.PartialLine, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get API integration ingest state: %w", err)
	}
	if modTime.Valid {
		state.FileModTime, _ = time.Parse(time.RFC3339Nano, modTime.String)
	}
	state.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return &state, nil
}

// UpsertAPIIntegrationIngestState persists the current tail cursor for a source file.
func (s *Store) UpsertAPIIntegrationIngestState(state *apiintegrations.IngestState) error {
	if state == nil {
		return fmt.Errorf("API integration ingest state is nil")
	}
	var modTime interface{}
	if !state.FileModTime.IsZero() {
		modTime = state.FileModTime.Format(time.RFC3339Nano)
	}
	_, err := s.db.Exec(`
		INSERT INTO api_integration_ingest_state (source_path, offset_bytes, file_size, file_mod_time, partial_line, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_path) DO UPDATE SET
			offset_bytes = excluded.offset_bytes,
			file_size = excluded.file_size,
			file_mod_time = excluded.file_mod_time,
			partial_line = excluded.partial_line,
			updated_at = excluded.updated_at
	`, state.SourcePath, state.Offset, state.FileSize, modTime, state.PartialLine, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("failed to upsert API integration ingest state: %w", err)
	}
	return nil
}

// QueryAPIIntegrationIngestHealth returns ingest cursor state plus last event timestamp per file.
func (s *Store) QueryAPIIntegrationIngestHealth() ([]APIIntegrationIngestHealthRow, error) {
	rows, err := s.db.Query(`
		SELECT s.source_path, s.offset_bytes, s.file_size, s.file_mod_time, s.partial_line, s.updated_at,
		       MAX(e.captured_at) as last_captured_at
		FROM api_integration_ingest_state s
		LEFT JOIN api_integration_usage_events e ON e.source_path = s.source_path
		GROUP BY s.source_path, s.offset_bytes, s.file_size, s.file_mod_time, s.partial_line, s.updated_at
		ORDER BY s.source_path
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query API integration ingest health: %w", err)
	}
	defer rows.Close()

	var result []APIIntegrationIngestHealthRow
	for rows.Next() {
		var row APIIntegrationIngestHealthRow
		var fileModTime sql.NullString
		var updatedAt string
		var lastCapturedAt sql.NullString
		if err := rows.Scan(
			&row.SourcePath,
			&row.OffsetBytes,
			&row.FileSize,
			&fileModTime,
			&row.PartialLine,
			&updatedAt,
			&lastCapturedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan API integration ingest health row: %w", err)
		}
		if fileModTime.Valid {
			t, _ := time.Parse(time.RFC3339Nano, fileModTime.String)
			row.FileModTime = &t
		}
		row.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
		if lastCapturedAt.Valid {
			t, _ := time.Parse(time.RFC3339Nano, lastCapturedAt.String)
			row.LastCapturedAt = &t
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// GetActiveSystemAlertsByProvider returns active alerts for a provider, most recent first.
func (s *Store) GetActiveSystemAlertsByProvider(provider string, limit int) ([]SystemAlert, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`
		SELECT id, provider, alert_type, title, message, severity, created_at, metadata
		FROM system_alerts
		WHERE dismissed_at IS NULL AND provider = ?
		ORDER BY created_at DESC
		LIMIT ?
	`, provider, limit)
	if err != nil {
		return nil, fmt.Errorf("store.GetActiveSystemAlertsByProvider: %w", err)
	}
	defer rows.Close()

	var alerts []SystemAlert
	for rows.Next() {
		var a SystemAlert
		var createdAt, metadata string
		if err := rows.Scan(&a.ID, &a.Provider, &a.AlertType, &a.Title, &a.Message, &a.Severity, &createdAt, &metadata); err != nil {
			return nil, fmt.Errorf("store.GetActiveSystemAlertsByProvider: scan: %w", err)
		}
		if t, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
			a.CreatedAt = t
		}
		a.Metadata = metadata
		alerts = append(alerts, a)
	}
	return alerts, rows.Err()
}

func isSQLiteUniqueConstraintError(err error) bool {
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	return sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE
}
