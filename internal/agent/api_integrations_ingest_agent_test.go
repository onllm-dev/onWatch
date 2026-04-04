package agent

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/store"
)

func TestAPIIntegrationsIngestAgent_ScanFile_PartialLineAndCompletion(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer st.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "notes.jsonl")
	if err := os.WriteFile(path, []byte(`{"ts":"2026-04-03T12:00:00Z","integration":"notes","provider":"anthropic","model":"claude-3-7-sonnet","prompt_tokens":10,"completion_tokens":2}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ag := NewAPIIntegrationsIngestAgent(st, dir, slog.Default())
	if err := ag.scanFile(path); err != nil {
		t.Fatalf("scanFile(1): %v", err)
	}

	events, err := st.QueryAPIIntegrationUsageRange(time.Date(2026, 4, 3, 11, 0, 0, 0, time.UTC), time.Date(2026, 4, 3, 13, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("QueryAPIIntegrationUsageRange: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 events before newline, got %d", len(events))
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	if _, err := f.WriteString("\n"); err != nil {
		t.Fatalf("WriteString: %v", err)
	}
	_ = f.Close()

	if err := ag.scanFile(path); err != nil {
		t.Fatalf("scanFile(2): %v", err)
	}
	events, err = st.QueryAPIIntegrationUsageRange(time.Date(2026, 4, 3, 11, 0, 0, 0, time.UTC), time.Date(2026, 4, 3, 13, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("QueryAPIIntegrationUsageRange(2): %v", err)
	}
	if len(events) != 1 || events[0].TotalTokens != 12 {
		t.Fatalf("events=%+v", events)
	}
}

func TestAPIIntegrationsIngestAgent_ScanFile_InvalidLineCreatesAlert(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer st.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "bad.jsonl")
	content := "{not-json}\n" +
		`{"ts":"2026-04-03T12:00:00Z","integration":"notes","provider":"mistral","model":"mistral-small-latest","prompt_tokens":1,"completion_tokens":1}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ag := NewAPIIntegrationsIngestAgent(st, dir, slog.Default())
	if err := ag.scanFile(path); err != nil {
		t.Fatalf("scanFile: %v", err)
	}

	alerts, err := st.GetActiveSystemAlerts()
	if err != nil {
		t.Fatalf("GetActiveSystemAlerts: %v", err)
	}
	if len(alerts) == 0 || alerts[0].Provider != "api_integrations" {
		t.Fatalf("alerts=%+v", alerts)
	}
	events, err := st.QueryAPIIntegrationUsageRange(time.Date(2026, 4, 3, 11, 0, 0, 0, time.UTC), time.Date(2026, 4, 3, 13, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("QueryAPIIntegrationUsageRange: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events=%+v", events)
	}
}

func TestAPIIntegrationsIngestAgent_ScanFile_DedupAndTruncation(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer st.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "tool.jsonl")
	line := `{"ts":"2026-04-03T12:00:00Z","integration":"notes","provider":"openai","model":"gpt-4.1-mini","prompt_tokens":3,"completion_tokens":2}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ag := NewAPIIntegrationsIngestAgent(st, dir, slog.Default())
	if err := ag.scanFile(path); err != nil {
		t.Fatalf("scanFile(1): %v", err)
	}
	if err := ag.scanFile(path); err != nil {
		t.Fatalf("scanFile(2): %v", err)
	}

	events, err := st.QueryAPIIntegrationUsageRange(time.Date(2026, 4, 3, 11, 0, 0, 0, time.UTC), time.Date(2026, 4, 3, 13, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("QueryAPIIntegrationUsageRange: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event after dedup, got %d", len(events))
	}

	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatalf("WriteFile(truncate): %v", err)
	}
	if err := ag.scanFile(path); err != nil {
		t.Fatalf("scanFile(3): %v", err)
	}
	events, err = st.QueryAPIIntegrationUsageRange(time.Date(2026, 4, 3, 11, 0, 0, 0, time.UTC), time.Date(2026, 4, 3, 13, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("QueryAPIIntegrationUsageRange(2): %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event after truncation reread, got %d", len(events))
	}
}

func TestAPIIntegrationsIngestAgent_Run_ProcessesMultipleFiles(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer st.Close()

	dir := t.TempDir()
	files := map[string]string{
		"anthropic.jsonl": `{"ts":"2026-04-03T12:00:00Z","integration":"notes","provider":"anthropic","model":"claude-3-7-sonnet","prompt_tokens":5,"completion_tokens":2}` + "\n",
		"mistral.jsonl":   `{"ts":"2026-04-03T12:01:00Z","integration":"summariser","provider":"mistral","model":"mistral-small-latest","prompt_tokens":4,"completion_tokens":1}` + "\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile(%s): %v", name, err)
		}
	}

	ag := NewAPIIntegrationsIngestAgent(st, dir, slog.Default())
	ag.SetInterval(10 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = ag.Run(ctx)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	summary, err := st.QueryAPIIntegrationUsageSummary()
	if err != nil {
		t.Fatalf("QueryAPIIntegrationUsageSummary: %v", err)
	}
	if len(summary) != 2 {
		t.Fatalf("summary=%+v", summary)
	}
}
