package store

import (
	"errors"
	"fmt"
	"testing"
	"time"

	apiintegrations "github.com/onllm-dev/onwatch/v2/internal/api_integrations"
)

func TestStore_InsertAPIIntegrationUsageEvent_Dedup(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	event, err := apiintegrations.ParseUsageEventLine([]byte(`{"ts":"2026-04-03T12:00:00Z","integration":"notes","provider":"anthropic","model":"claude-3-7-sonnet","prompt_tokens":10,"completion_tokens":5}`), "/tmp/api-integrations/notes.jsonl")
	if err != nil {
		t.Fatalf("ParseUsageEventLine: %v", err)
	}

	if _, err := s.InsertAPIIntegrationUsageEvent(event); err != nil {
		t.Fatalf("InsertAPIIntegrationUsageEvent: %v", err)
	}
	if _, err := s.InsertAPIIntegrationUsageEvent(event); !errors.Is(err, ErrDuplicateAPIIntegrationUsageEvent) {
		t.Fatalf("expected ErrDuplicateAPIIntegrationUsageEvent, got %v", err)
	}
}

func TestStore_QueryAPIIntegrationUsageSummary(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	lines := []string{
		`{"ts":"2026-04-03T12:00:00Z","integration":"notes","provider":"anthropic","model":"claude-3-7-sonnet","prompt_tokens":10,"completion_tokens":5,"cost_usd":0.1}`,
		`{"ts":"2026-04-03T12:01:00Z","integration":"notes","provider":"anthropic","model":"claude-3-7-sonnet","prompt_tokens":2,"completion_tokens":3,"cost_usd":0.2}`,
		`{"ts":"2026-04-03T12:02:00Z","integration":"notes","provider":"mistral","model":"mistral-small-latest","prompt_tokens":4,"completion_tokens":1}`,
	}
	for i, line := range lines {
		event, err := apiintegrations.ParseUsageEventLine([]byte(line), "/tmp/api-integrations/test.jsonl")
		if err != nil {
			t.Fatalf("ParseUsageEventLine(%d): %v", i, err)
		}
		if _, err := s.InsertAPIIntegrationUsageEvent(event); err != nil {
			t.Fatalf("InsertAPIIntegrationUsageEvent(%d): %v", i, err)
		}
	}

	summary, err := s.QueryAPIIntegrationUsageSummary()
	if err != nil {
		t.Fatalf("QueryAPIIntegrationUsageSummary: %v", err)
	}
	if len(summary) != 2 {
		t.Fatalf("len(summary)=%d want 2", len(summary))
	}
	if summary[0].Provider != "anthropic" || summary[0].RequestCount != 2 || summary[0].TotalTokens != 20 {
		t.Fatalf("anthropic summary=%+v", summary[0])
	}
	if summary[0].TotalCostUSD != 0.30000000000000004 && summary[0].TotalCostUSD != 0.3 {
		t.Fatalf("anthropic cost=%v", summary[0].TotalCostUSD)
	}
}

func TestStore_QueryAPIIntegrationUsageSummary_BoundedAndOrdered(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)
	totalGroups := apiIntegrationUsageSummaryLimit + 10
	for i := 0; i < totalGroups; i++ {
		line := fmt.Sprintf(`{"ts":"%s","integration":"integration-%03d","provider":"openai","model":"gpt-4.1-mini","prompt_tokens":1,"completion_tokens":1}`,
			base.Add(time.Duration(i)*time.Minute).Format(time.RFC3339),
			i,
		)
		event, err := apiintegrations.ParseUsageEventLine([]byte(line), "/tmp/api-integrations/bounded.jsonl")
		if err != nil {
			t.Fatalf("ParseUsageEventLine(%d): %v", i, err)
		}
		if _, err := s.InsertAPIIntegrationUsageEvent(event); err != nil {
			t.Fatalf("InsertAPIIntegrationUsageEvent(%d): %v", i, err)
		}
	}

	summary, err := s.QueryAPIIntegrationUsageSummary()
	if err != nil {
		t.Fatalf("QueryAPIIntegrationUsageSummary: %v", err)
	}
	if len(summary) != apiIntegrationUsageSummaryLimit {
		t.Fatalf("len(summary)=%d want %d", len(summary), apiIntegrationUsageSummaryLimit)
	}
	if summary[0].IntegrationName != "integration-000" {
		t.Fatalf("first summary row=%+v", summary[0])
	}
	last := summary[len(summary)-1]
	if last.IntegrationName != fmt.Sprintf("integration-%03d", apiIntegrationUsageSummaryLimit-1) {
		t.Fatalf("last summary row=%+v", last)
	}
}

func TestStore_QueryAPIIntegrationUsageRange_AndIngestState(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	event, err := apiintegrations.ParseUsageEventLine([]byte(`{"ts":"2026-04-03T12:00:00Z","integration":"notes","provider":"openai","model":"gpt-4.1-mini","prompt_tokens":7,"completion_tokens":2}`), "/tmp/api-integrations/notes.jsonl")
	if err != nil {
		t.Fatalf("ParseUsageEventLine: %v", err)
	}
	if _, err := s.InsertAPIIntegrationUsageEvent(event); err != nil {
		t.Fatalf("InsertAPIIntegrationUsageEvent: %v", err)
	}

	start := time.Date(2026, 4, 3, 11, 0, 0, 0, time.UTC)
	end := time.Date(2026, 4, 3, 13, 0, 0, 0, time.UTC)
	events, err := s.QueryAPIIntegrationUsageRange(start, end)
	if err != nil {
		t.Fatalf("QueryAPIIntegrationUsageRange: %v", err)
	}
	if len(events) != 1 || events[0].TotalTokens != 9 {
		t.Fatalf("events=%+v", events)
	}

	state := &apiintegrations.IngestState{
		SourcePath:  "/tmp/api-integrations/notes.jsonl",
		Offset:      42,
		FileSize:    100,
		FileModTime: time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC),
		PartialLine: `{"ts":"2026`,
	}
	if err := s.UpsertAPIIntegrationIngestState(state); err != nil {
		t.Fatalf("UpsertAPIIntegrationIngestState: %v", err)
	}
	got, err := s.GetAPIIntegrationIngestState(state.SourcePath)
	if err != nil {
		t.Fatalf("GetAPIIntegrationIngestState: %v", err)
	}
	if got == nil || got.Offset != 42 || got.PartialLine != state.PartialLine {
		t.Fatalf("state=%+v", got)
	}
}

func TestStore_DeleteAPIIntegrationUsageEventsOlderThan(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	lines := []string{
		`{"ts":"2026-01-01T12:00:00Z","integration":"notes","provider":"openai","model":"gpt-4.1-mini","prompt_tokens":1,"completion_tokens":1}`,
		`{"ts":"2026-03-15T12:00:00Z","integration":"notes","provider":"openai","model":"gpt-4.1-mini","prompt_tokens":2,"completion_tokens":2}`,
	}
	for i, line := range lines {
		event, err := apiintegrations.ParseUsageEventLine([]byte(line), "/tmp/api-integrations/retention.jsonl")
		if err != nil {
			t.Fatalf("ParseUsageEventLine(%d): %v", i, err)
		}
		if _, err := s.InsertAPIIntegrationUsageEvent(event); err != nil {
			t.Fatalf("InsertAPIIntegrationUsageEvent(%d): %v", i, err)
		}
	}

	deleted, err := s.DeleteAPIIntegrationUsageEventsOlderThan(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("DeleteAPIIntegrationUsageEventsOlderThan: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted=%d want 1", deleted)
	}

	events, err := s.QueryAPIIntegrationUsageRange(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("QueryAPIIntegrationUsageRange: %v", err)
	}
	if len(events) != 1 || events[0].Timestamp.Format(time.RFC3339) != "2026-03-15T12:00:00Z" {
		t.Fatalf("events=%+v", events)
	}
}

func TestStore_QueryAPIIntegrationUsageBuckets(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	lines := []string{
		`{"ts":"2026-04-03T12:01:00Z","integration":"notes","provider":"anthropic","model":"claude-3-7-sonnet","prompt_tokens":10,"completion_tokens":5,"cost_usd":0.1}`,
		`{"ts":"2026-04-03T12:04:00Z","integration":"notes","provider":"anthropic","model":"claude-3-7-sonnet","prompt_tokens":2,"completion_tokens":3,"cost_usd":0.2}`,
		`{"ts":"2026-04-03T12:16:00Z","integration":"notes","provider":"anthropic","model":"claude-3-7-sonnet","prompt_tokens":4,"completion_tokens":1}`,
		`{"ts":"2026-04-03T12:08:00Z","integration":"daily-report","provider":"openai","model":"gpt-4.1-mini","prompt_tokens":6,"completion_tokens":2}`,
	}
	for i, line := range lines {
		event, err := apiintegrations.ParseUsageEventLine([]byte(line), "/tmp/api-integrations/test.jsonl")
		if err != nil {
			t.Fatalf("ParseUsageEventLine(%d): %v", i, err)
		}
		if _, err := s.InsertAPIIntegrationUsageEvent(event); err != nil {
			t.Fatalf("InsertAPIIntegrationUsageEvent(%d): %v", i, err)
		}
	}

	start := time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)
	end := time.Date(2026, 4, 3, 13, 0, 0, 0, time.UTC)
	rows, err := s.QueryAPIIntegrationUsageBuckets(start, end, 15*time.Minute)
	if err != nil {
		t.Fatalf("QueryAPIIntegrationUsageBuckets: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("len(rows)=%d want 3", len(rows))
	}

	if rows[0].IntegrationName != "daily-report" || rows[0].BucketStart.Format(time.RFC3339) != "2026-04-03T12:00:00Z" || rows[0].TotalTokens != 8 {
		t.Fatalf("unexpected first bucket: %+v", rows[0])
	}
	if rows[1].IntegrationName != "notes" || rows[1].BucketStart.Format(time.RFC3339) != "2026-04-03T12:00:00Z" || rows[1].RequestCount != 2 || rows[1].TotalTokens != 20 {
		t.Fatalf("unexpected second bucket: %+v", rows[1])
	}
	if rows[1].TotalCostUSD < 0.299 || rows[1].TotalCostUSD > 0.301 {
		t.Fatalf("unexpected second bucket cost: %+v", rows[1])
	}
	if rows[2].IntegrationName != "notes" || rows[2].BucketStart.Format(time.RFC3339) != "2026-04-03T12:15:00Z" || rows[2].TotalTokens != 5 {
		t.Fatalf("unexpected third bucket: %+v", rows[2])
	}
}

func TestStore_QueryAPIIntegrationUsageBuckets_HourlyRange(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	lines := []string{
		`{"ts":"2026-04-03T12:10:00Z","integration":"notes","provider":"anthropic","model":"claude-3-7-sonnet","prompt_tokens":3,"completion_tokens":2}`,
		`{"ts":"2026-04-03T12:50:00Z","integration":"notes","provider":"anthropic","model":"claude-3-7-sonnet","prompt_tokens":4,"completion_tokens":1}`,
		`{"ts":"2026-04-03T13:05:00Z","integration":"notes","provider":"anthropic","model":"claude-3-7-sonnet","prompt_tokens":5,"completion_tokens":5,"cost_usd":0.5}`,
		`{"ts":"2026-04-03T13:25:00Z","integration":"report","provider":"openai","model":"gpt-4.1-mini","prompt_tokens":7,"completion_tokens":3}`,
	}
	for i, line := range lines {
		event, err := apiintegrations.ParseUsageEventLine([]byte(line), "/tmp/api-integrations/hourly.jsonl")
		if err != nil {
			t.Fatalf("ParseUsageEventLine(%d): %v", i, err)
		}
		if _, err := s.InsertAPIIntegrationUsageEvent(event); err != nil {
			t.Fatalf("InsertAPIIntegrationUsageEvent(%d): %v", i, err)
		}
	}

	start := time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)
	end := time.Date(2026, 4, 3, 14, 0, 0, 0, time.UTC)
	rows, err := s.QueryAPIIntegrationUsageBuckets(start, end, time.Hour)
	if err != nil {
		t.Fatalf("QueryAPIIntegrationUsageBuckets: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("len(rows)=%d want 3", len(rows))
	}
	if rows[0].IntegrationName != "notes" || rows[0].BucketStart.Format(time.RFC3339) != "2026-04-03T12:00:00Z" || rows[0].TotalTokens != 10 {
		t.Fatalf("unexpected first hourly bucket: %+v", rows[0])
	}
	if rows[1].IntegrationName != "notes" || rows[1].BucketStart.Format(time.RFC3339) != "2026-04-03T13:00:00Z" || rows[1].TotalCostUSD != 0.5 {
		t.Fatalf("unexpected second hourly bucket: %+v", rows[1])
	}
	if rows[2].IntegrationName != "report" || rows[2].BucketStart.Format(time.RFC3339) != "2026-04-03T13:00:00Z" || rows[2].TotalTokens != 10 {
		t.Fatalf("unexpected third hourly bucket: %+v", rows[2])
	}
}

func TestStore_QueryAPIIntegrationUsageBuckets_Bounded(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	// Insert apiIntegrationUsageBucketsLimit + 10 events, each in its own 1-minute bucket
	// across different integrations so GROUP BY produces many rows.
	base := time.Date(2026, 4, 3, 0, 0, 0, 0, time.UTC)
	total := apiIntegrationUsageBucketsLimit + 10
	for i := 0; i < total; i++ {
		line := fmt.Sprintf(`{"ts":"%s","integration":"integ-%04d","provider":"openai","model":"gpt-4.1-mini","prompt_tokens":1,"completion_tokens":1}`,
			base.Add(time.Duration(i)*time.Minute).Format(time.RFC3339), i)
		event, err := apiintegrations.ParseUsageEventLine([]byte(line), "/tmp/bounded.jsonl")
		if err != nil {
			t.Fatalf("ParseUsageEventLine(%d): %v", i, err)
		}
		if _, err := s.InsertAPIIntegrationUsageEvent(event); err != nil {
			t.Fatalf("InsertAPIIntegrationUsageEvent(%d): %v", i, err)
		}
	}

	start := base
	end := base.Add(time.Duration(total+1) * time.Minute)
	rows, err := s.QueryAPIIntegrationUsageBuckets(start, end, time.Minute)
	if err != nil {
		t.Fatalf("QueryAPIIntegrationUsageBuckets: %v", err)
	}
	if len(rows) != apiIntegrationUsageBucketsLimit {
		t.Fatalf("len(rows)=%d want %d", len(rows), apiIntegrationUsageBucketsLimit)
	}
}

func TestStore_QueryAPIIntegrationIngestHealth_AndAlertsByProvider(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	stateA := &apiintegrations.IngestState{
		SourcePath:  "/tmp/api-integrations/notes.jsonl",
		Offset:      128,
		FileSize:    256,
		FileModTime: time.Date(2026, 4, 3, 12, 5, 0, 0, time.UTC),
		PartialLine: `{"ts":"2026-04`,
	}
	stateB := &apiintegrations.IngestState{
		SourcePath:  "/tmp/api-integrations/report.jsonl",
		Offset:      64,
		FileSize:    64,
		FileModTime: time.Date(2026, 4, 3, 12, 6, 0, 0, time.UTC),
	}
	if err := s.UpsertAPIIntegrationIngestState(stateA); err != nil {
		t.Fatalf("UpsertAPIIntegrationIngestState(stateA): %v", err)
	}
	if err := s.UpsertAPIIntegrationIngestState(stateB); err != nil {
		t.Fatalf("UpsertAPIIntegrationIngestState(stateB): %v", err)
	}

	event, err := apiintegrations.ParseUsageEventLine([]byte(`{"ts":"2026-04-03T12:07:00Z","integration":"notes","provider":"anthropic","model":"claude-3-7-sonnet","prompt_tokens":10,"completion_tokens":5}`), stateA.SourcePath)
	if err != nil {
		t.Fatalf("ParseUsageEventLine: %v", err)
	}
	if _, err := s.InsertAPIIntegrationUsageEvent(event); err != nil {
		t.Fatalf("InsertAPIIntegrationUsageEvent: %v", err)
	}

	if _, err := s.CreateSystemAlert("api_integrations", "ingest_warning", "Bad line", "Skipped malformed JSON", "warning", `{"sourcePath":"/tmp/api-integrations/notes.jsonl"}`); err != nil {
		t.Fatalf("CreateSystemAlert(api_integrations): %v", err)
	}
	if _, err := s.CreateSystemAlert("anthropic", "auth_error", "Nope", "ignore me", "error", ""); err != nil {
		t.Fatalf("CreateSystemAlert(anthropic): %v", err)
	}

	healthRows, err := s.QueryAPIIntegrationIngestHealth()
	if err != nil {
		t.Fatalf("QueryAPIIntegrationIngestHealth: %v", err)
	}
	if len(healthRows) != 2 {
		t.Fatalf("len(healthRows)=%d want 2", len(healthRows))
	}
	if healthRows[0].SourcePath != stateA.SourcePath {
		t.Fatalf("unexpected first health row: %+v", healthRows[0])
	}
	if healthRows[0].LastCapturedAt == nil || healthRows[0].LastCapturedAt.Format(time.RFC3339) != "2026-04-03T12:07:00Z" {
		t.Fatalf("unexpected first health lastCapturedAt: %+v", healthRows[0])
	}
	if healthRows[1].SourcePath != stateB.SourcePath || healthRows[1].LastCapturedAt != nil {
		t.Fatalf("unexpected second health row: %+v", healthRows[1])
	}

	alerts, err := s.GetActiveSystemAlertsByProvider("api_integrations", 10)
	if err != nil {
		t.Fatalf("GetActiveSystemAlertsByProvider: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("len(alerts)=%d want 1", len(alerts))
	}
	if alerts[0].Provider != "api_integrations" || alerts[0].AlertType != "ingest_warning" {
		t.Fatalf("unexpected alert: %+v", alerts[0])
	}
	if alerts[0].CreatedAt.Format(time.RFC3339) == "0001-01-01T00:00:00Z" {
		t.Fatalf("expected parsed alert createdAt, got %+v", alerts[0])
	}
}
