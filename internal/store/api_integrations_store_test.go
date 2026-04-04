package store

import (
	"errors"
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
}
