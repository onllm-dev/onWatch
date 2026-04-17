package agent

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apiintegrations "github.com/onllm-dev/onwatch/v2/internal/api_integrations"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

func newBufferedJSONLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewJSONHandler(&buf, nil)), &buf
}

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

	ag := NewAPIIntegrationsIngestAgent(st, dir, 0, slog.Default())
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

func TestAPIIntegrationsIngestAgent_ScanFile_PartialLineCap(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer st.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "oversized.jsonl")
	content := strings.Repeat("a", apiIntegrationIngestMaxReadBytes*3)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	logger, logBuf := newBufferedJSONLogger()
	ag := NewAPIIntegrationsIngestAgent(st, dir, 0, logger)

	for i := 0; i < 3; i++ {
		if err := ag.scanFile(path); err != nil {
			t.Fatalf("scanFile(%d): %v", i+1, err)
		}
		state, err := st.GetAPIIntegrationIngestState(path)
		if err != nil {
			t.Fatalf("GetAPIIntegrationIngestState(%d): %v", i+1, err)
		}
		if state == nil {
			t.Fatalf("expected ingest state after scan %d", i+1)
		}
		if len(state.PartialLine) > apiintegrations.MaxIngestPartialLineBytes {
			t.Fatalf("partial line length=%d exceeds cap %d after scan %d", len(state.PartialLine), apiintegrations.MaxIngestPartialLineBytes, i+1)
		}
	}

	state, err := st.GetAPIIntegrationIngestState(path)
	if err != nil {
		t.Fatalf("GetAPIIntegrationIngestState(final): %v", err)
	}
	if state == nil {
		t.Fatal("expected final ingest state")
	}
	if state.Offset != int64(len(content)) {
		t.Fatalf("offset=%d want %d", state.Offset, len(content))
	}
	if state.PartialLine != "" {
		t.Fatalf("expected oversized partial line to be dropped, got len=%d", len(state.PartialLine))
	}

	logs := logBuf.String()
	if !strings.Contains(logs, "API integrations ingester discarded oversized partial line") {
		t.Fatalf("expected oversized partial line warning, logs=%s", logs)
	}
}

func TestAPIIntegrationsIngestAgent_ScanFile_DropsOversizedPersistedPartialLine(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer st.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "persisted.jsonl")
	line := `{"ts":"2026-04-03T12:00:00Z","integration":"notes","provider":"openai","model":"gpt-4.1-mini","prompt_tokens":3,"completion_tokens":2}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	oversizedPartial := strings.Repeat("x", apiintegrations.MaxIngestPartialLineBytes+1)
	if err := st.UpsertAPIIntegrationIngestState(&apiintegrations.IngestState{
		SourcePath:  path,
		Offset:      0,
		FileSize:    int64(len(line)),
		FileModTime: time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC),
		PartialLine: oversizedPartial,
	}); err != nil {
		t.Fatalf("UpsertAPIIntegrationIngestState: %v", err)
	}

	logger, logBuf := newBufferedJSONLogger()
	ag := NewAPIIntegrationsIngestAgent(st, dir, 0, logger)
	if err := ag.scanFile(path); err != nil {
		t.Fatalf("scanFile: %v", err)
	}

	state, err := st.GetAPIIntegrationIngestState(path)
	if err != nil {
		t.Fatalf("GetAPIIntegrationIngestState: %v", err)
	}
	if state == nil {
		t.Fatal("expected ingest state")
	}
	if state.PartialLine != "" {
		t.Fatalf("expected persisted oversized partial line to be cleared, got len=%d", len(state.PartialLine))
	}

	events, err := st.QueryAPIIntegrationUsageRange(time.Date(2026, 4, 3, 11, 0, 0, 0, time.UTC), time.Date(2026, 4, 3, 13, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("QueryAPIIntegrationUsageRange: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events=%+v", events)
	}

	logs := logBuf.String()
	if !strings.Contains(logs, "API integrations ingester discarded oversized persisted partial line") {
		t.Fatalf("expected persisted partial line warning, logs=%s", logs)
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

	ag := NewAPIIntegrationsIngestAgent(st, dir, 0, slog.Default())
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

func TestAPIIntegrationsIngestAgent_ScanFile_InvalidLineAlertCap(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer st.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "bad.jsonl")
	var content strings.Builder
	for i := 0; i < apiIntegrationIngestMaxInvalidAlertsPerFilePerScan+2; i++ {
		content.WriteString("{not-json}\n")
	}
	content.WriteString(`{"ts":"2026-04-03T12:00:00Z","integration":"notes","provider":"mistral","model":"mistral-small-latest","prompt_tokens":1,"completion_tokens":1}` + "\n")
	if err := os.WriteFile(path, []byte(content.String()), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	logger, logBuf := newBufferedJSONLogger()
	ag := NewAPIIntegrationsIngestAgent(st, dir, 0, logger)
	if err := ag.scanFile(path); err != nil {
		t.Fatalf("scanFile: %v", err)
	}

	alerts, err := st.GetActiveSystemAlertsByProvider("api_integrations", 20)
	if err != nil {
		t.Fatalf("GetActiveSystemAlertsByProvider: %v", err)
	}
	if len(alerts) != apiIntegrationIngestMaxInvalidAlertsPerFilePerScan {
		t.Fatalf("len(alerts)=%d want %d", len(alerts), apiIntegrationIngestMaxInvalidAlertsPerFilePerScan)
	}

	events, err := st.QueryAPIIntegrationUsageRange(time.Date(2026, 4, 3, 11, 0, 0, 0, time.UTC), time.Date(2026, 4, 3, 13, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("QueryAPIIntegrationUsageRange: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events=%+v", events)
	}

	logs := logBuf.String()
	if !strings.Contains(logs, "API integrations ingester suppressed invalid line alerts") {
		t.Fatalf("expected invalid alert suppression warning, logs=%s", logs)
	}
	if !strings.Contains(logs, `"alerts_suppressed":2`) {
		t.Fatalf("expected suppressed invalid alert count in logs, logs=%s", logs)
	}
}

func TestAPIIntegrationsIngestAgent_Scan_InvalidLineAlertCap_IsPerFile(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer st.Close()

	dir := t.TempDir()
	validLine := `{"ts":"2026-04-03T12:00:00Z","integration":"notes","provider":"openai","model":"gpt-4.1-mini","prompt_tokens":3,"completion_tokens":2}` + "\n"
	for i := 0; i < 2; i++ {
		path := filepath.Join(dir, fmt.Sprintf("bad-%d.jsonl", i))
		var content strings.Builder
		for j := 0; j < apiIntegrationIngestMaxInvalidAlertsPerFilePerScan+2; j++ {
			content.WriteString("{not-json}\n")
		}
		content.WriteString(validLine)
		if err := os.WriteFile(path, []byte(content.String()), 0o600); err != nil {
			t.Fatalf("WriteFile(%d): %v", i, err)
		}
	}

	logger, logBuf := newBufferedJSONLogger()
	ag := NewAPIIntegrationsIngestAgent(st, dir, 0, logger)
	ag.scan()

	alerts, err := st.GetActiveSystemAlertsByProvider("api_integrations", 50)
	if err != nil {
		t.Fatalf("GetActiveSystemAlertsByProvider: %v", err)
	}
	wantAlerts := apiIntegrationIngestMaxInvalidAlertsPerFilePerScan * 2
	if len(alerts) != wantAlerts {
		t.Fatalf("len(alerts)=%d want %d", len(alerts), wantAlerts)
	}

	events, err := st.QueryAPIIntegrationUsageRange(time.Date(2026, 4, 3, 11, 0, 0, 0, time.UTC), time.Date(2026, 4, 3, 13, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("QueryAPIIntegrationUsageRange: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events=%+v", events)
	}

	logs := logBuf.String()
	if strings.Count(logs, "API integrations ingester suppressed invalid line alerts") != 2 {
		t.Fatalf("expected suppression warning per file, logs=%s", logs)
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

	ag := NewAPIIntegrationsIngestAgent(st, dir, 0, slog.Default())
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

	ag := NewAPIIntegrationsIngestAgent(st, dir, 0, slog.Default())
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

func TestAPIIntegrationsIngestAgent_Scan_FileCap_RotatesAcrossCycles(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer st.Close()

	dir := t.TempDir()
	base := time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)
	totalFiles := apiIntegrationIngestMaxFilesPerScan + 5
	for i := 0; i < totalFiles; i++ {
		name := fmt.Sprintf("%03d.jsonl", i)
		line := fmt.Sprintf(
			`{"ts":"%s","integration":"integration-%03d","provider":"openai","model":"gpt-4.1-mini","prompt_tokens":3,"completion_tokens":2}`+"\n",
			base.Add(time.Duration(i)*time.Minute).Format(time.RFC3339),
			i,
		)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(line), 0o600); err != nil {
			t.Fatalf("WriteFile(%s): %v", name, err)
		}
	}

	logger, logBuf := newBufferedJSONLogger()
	ag := NewAPIIntegrationsIngestAgent(st, dir, 0, logger)
	ag.scan()

	events, err := st.QueryAPIIntegrationUsageRange(base.Add(-time.Hour), base.Add(24*time.Hour), totalFiles)
	if err != nil {
		t.Fatalf("QueryAPIIntegrationUsageRange: %v", err)
	}
	if len(events) != apiIntegrationIngestMaxFilesPerScan {
		t.Fatalf("len(events)=%d want %d", len(events), apiIntegrationIngestMaxFilesPerScan)
	}

	seenPaths := make(map[string]bool, len(events))
	for _, event := range events {
		seenPaths[event.SourcePath] = true
	}
	for i := 0; i < apiIntegrationIngestMaxFilesPerScan; i++ {
		path := filepath.Join(dir, fmt.Sprintf("%03d.jsonl", i))
		if !seenPaths[path] {
			t.Fatalf("expected scanned file %s to be ingested", path)
		}
	}
	for i := apiIntegrationIngestMaxFilesPerScan; i < totalFiles; i++ {
		path := filepath.Join(dir, fmt.Sprintf("%03d.jsonl", i))
		if seenPaths[path] {
			t.Fatalf("did not expect skipped file %s to be ingested on first scan", path)
		}
	}

	ag.scan()

	events, err = st.QueryAPIIntegrationUsageRange(base.Add(-time.Hour), base.Add(24*time.Hour), totalFiles)
	if err != nil {
		t.Fatalf("QueryAPIIntegrationUsageRange(second): %v", err)
	}
	if len(events) != totalFiles {
		t.Fatalf("len(events)=%d want %d after rotation", len(events), totalFiles)
	}
	seenPaths = make(map[string]bool, len(events))
	for _, event := range events {
		seenPaths[event.SourcePath] = true
	}
	for i := 0; i < totalFiles; i++ {
		path := filepath.Join(dir, fmt.Sprintf("%03d.jsonl", i))
		if !seenPaths[path] {
			t.Fatalf("expected rotated scan to ingest file %s", path)
		}
	}

	logs := logBuf.String()
	if !strings.Contains(logs, "API integrations ingester skipped files beyond scan cap") {
		t.Fatalf("expected file cap warning, logs=%s", logs)
	}
	if !strings.Contains(logs, fmt.Sprintf(`"skipped_files":%d`, totalFiles-apiIntegrationIngestMaxFilesPerScan)) {
		t.Fatalf("expected skipped file count in logs, logs=%s", logs)
	}
}

func TestAPIIntegrationsIngestAgent_Scan_PrunesExpiredDatabaseRows(t *testing.T) {
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer st.Close()

	oldEvent := `{"ts":"2025-12-01T12:00:00Z","integration":"notes","provider":"openai","model":"gpt-4.1-mini","prompt_tokens":2,"completion_tokens":1}`
	parsedOld, err := apiintegrations.ParseUsageEventLine([]byte(oldEvent), "/tmp/api-integrations/notes.jsonl")
	if err != nil {
		t.Fatalf("ParseUsageEventLine(old): %v", err)
	}
	if _, err := st.InsertAPIIntegrationUsageEvent(parsedOld); err != nil {
		t.Fatalf("InsertAPIIntegrationUsageEvent(old): %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "notes.jsonl")
	newLine := `{"ts":"2026-04-03T12:00:00Z","integration":"notes","provider":"openai","model":"gpt-4.1-mini","prompt_tokens":3,"completion_tokens":2}` + "\n"
	if err := os.WriteFile(path, []byte(newLine), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ag := NewAPIIntegrationsIngestAgent(st, dir, 24*time.Hour, slog.Default())
	ag.pruneInterval = time.Millisecond
	if err := ag.pruneExpiredUsageEvents(); err != nil {
		t.Fatalf("pruneExpiredUsageEvents: %v", err)
	}
	if err := ag.scanFile(path); err != nil {
		t.Fatalf("scanFile: %v", err)
	}

	events, err := st.QueryAPIIntegrationUsageRange(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("QueryAPIIntegrationUsageRange: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events=%+v", events)
	}
	if events[0].Timestamp.Format(time.RFC3339) != "2026-04-03T12:00:00Z" {
		t.Fatalf("expected retained new event, got %+v", events[0])
	}
}
