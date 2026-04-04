package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	apiintegrations "github.com/onllm-dev/onwatch/v2/internal/api_integrations"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

const (
	apiIntegrationIngestIntervalDefault = 5 * time.Second
	apiIntegrationIngestMaxReadBytes    = 256 * 1024
)

// APIIntegrationsIngestAgent tails normalized JSONL API integration usage files and stores the events.
type APIIntegrationsIngestAgent struct {
	store    *store.Store
	dir      string
	interval time.Duration
	logger   *slog.Logger
}

// NewAPIIntegrationsIngestAgent creates a new API integrations file ingester.
func NewAPIIntegrationsIngestAgent(store *store.Store, dir string, logger *slog.Logger) *APIIntegrationsIngestAgent {
	if logger == nil {
		logger = slog.Default()
	}
	return &APIIntegrationsIngestAgent{
		store:    store,
		dir:      dir,
		interval: apiIntegrationIngestIntervalDefault,
		logger:   logger,
	}
}

// SetInterval overrides the scan interval. Used in tests.
func (a *APIIntegrationsIngestAgent) SetInterval(interval time.Duration) {
	if interval > 0 {
		a.interval = interval
	}
}

// Run starts the periodic ingestion loop until context cancellation.
func (a *APIIntegrationsIngestAgent) Run(ctx context.Context) error {
	a.logger.Info("API integrations ingester started", "dir", a.dir, "interval", a.interval)
	defer a.logger.Info("API integrations ingester stopped")

	if err := os.MkdirAll(a.dir, 0o700); err != nil {
		return fmt.Errorf("create API integrations dir: %w", err)
	}

	a.scan()

	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			a.scan()
		case <-ctx.Done():
			return nil
		}
	}
}

func (a *APIIntegrationsIngestAgent) scan() {
	pattern := filepath.Join(a.dir, "*.jsonl")
	paths, err := filepath.Glob(pattern)
	if err != nil {
		a.logger.Error("API integrations ingester glob failed", "dir", a.dir, "error", err)
		return
	}
	sort.Strings(paths)

	for _, path := range paths {
		if err := a.scanFile(path); err != nil {
			a.logger.Error("API integrations ingester scan failed", "path", path, "error", err)
		}
	}
}

func (a *APIIntegrationsIngestAgent) scanFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat file: %w", err)
	}
	if info.IsDir() {
		return nil
	}

	state, err := a.store.GetAPIIntegrationIngestState(path)
	if err != nil {
		return err
	}
	if state == nil {
		state = &apiintegrations.IngestState{SourcePath: path}
	}

	if info.Size() < state.Offset {
		state.Offset = 0
		state.PartialLine = ""
	}

	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	if _, err := file.Seek(state.Offset, io.SeekStart); err != nil {
		return fmt.Errorf("seek file: %w", err)
	}

	data, err := io.ReadAll(io.LimitReader(file, apiIntegrationIngestMaxReadBytes))
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	if len(data) == 0 {
		state.FileSize = info.Size()
		state.FileModTime = info.ModTime().UTC()
		return a.store.UpsertAPIIntegrationIngestState(state)
	}

	state.Offset += int64(len(data))
	state.FileSize = info.Size()
	state.FileModTime = info.ModTime().UTC()

	combined := state.PartialLine + string(data)
	lines, remainder := splitCompleteLines(combined)
	state.PartialLine = remainder

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		event, err := apiintegrations.ParseUsageEventLine([]byte(trimmed), path)
		if err != nil {
			a.recordInvalidLine(path, trimmed, err)
			continue
		}
		if _, err := a.store.InsertAPIIntegrationUsageEvent(event); err != nil {
			if errors.Is(err, store.ErrDuplicateAPIIntegrationUsageEvent) {
				continue
			}
			return err
		}
	}

	return a.store.UpsertAPIIntegrationIngestState(state)
}

func splitCompleteLines(data string) ([]string, string) {
	if data == "" {
		return nil, ""
	}
	if strings.HasSuffix(data, "\n") {
		lines := strings.Split(strings.TrimSuffix(data, "\n"), "\n")
		return lines, ""
	}
	lines := strings.Split(data, "\n")
	if len(lines) == 1 {
		return nil, data
	}
	return lines[:len(lines)-1], lines[len(lines)-1]
}

func (a *APIIntegrationsIngestAgent) recordInvalidLine(path, line string, err error) {
	msg := fmt.Sprintf("%s: %v", filepath.Base(path), err)
	if len(line) > 180 {
		line = line[:180] + "..."
	}
	metadata := fmt.Sprintf(`{"source_path":%q,"line":%q}`, path, line)
	if _, createErr := a.store.CreateSystemAlert("api_integrations", "ingest_error", "API integrations ingest skipped invalid event", msg, "warning", metadata); createErr != nil {
		a.logger.Warn("Failed to create API integrations ingest alert", "path", path, "error", createErr)
	}
}
