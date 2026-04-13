package apiintegrations

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	maxIntegrationFieldLen = 256
	maxMetadataJSONLen     = 4096
)

var allowedProviders = map[string]struct{}{
	"anthropic":  {},
	"openai":     {},
	"mistral":    {},
	"openrouter": {},
	"gemini":     {},
}

// UsageEvent is the normalized API integration telemetry event stored by onWatch.
type UsageEvent struct {
	Timestamp        time.Time
	Integration      string
	Provider         string
	Account          string
	Model            string
	RequestID        string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	CostUSD          *float64
	LatencyMS        *int
	MetadataJSON     string
	SourcePath       string
	Fingerprint      string
}

// IngestState stores the persistent cursor for a tailed JSONL file.
type IngestState struct {
	SourcePath  string
	Offset      int64
	FileSize    int64
	FileModTime time.Time
	PartialLine string
	UpdatedAt   time.Time
}

type usageEventWire struct {
	TS               string          `json:"ts"`
	Integration      string          `json:"integration"`
	Provider         string          `json:"provider"`
	Account          string          `json:"account"`
	Model            string          `json:"model"`
	RequestID        string          `json:"request_id"`
	PromptTokens     int             `json:"prompt_tokens"`
	CompletionTokens int             `json:"completion_tokens"`
	TotalTokens      *int            `json:"total_tokens"`
	CostUSD          *float64        `json:"cost_usd"`
	LatencyMS        *int            `json:"latency_ms"`
	Metadata         json.RawMessage `json:"metadata"`
}

// ParseUsageEventLine validates and normalizes a single JSONL event line.
func ParseUsageEventLine(line []byte, sourcePath string) (*UsageEvent, error) {
	trimmed := strings.TrimSpace(string(line))
	if trimmed == "" {
		return nil, fmt.Errorf("empty event line")
	}

	var wire usageEventWire
	if err := json.Unmarshal([]byte(trimmed), &wire); err != nil {
		return nil, fmt.Errorf("parse API integration usage event: %w", err)
	}

	ts, err := time.Parse(time.RFC3339, strings.TrimSpace(wire.TS))
	if err != nil {
		return nil, fmt.Errorf("invalid ts: %w", err)
	}

	integrationName := strings.TrimSpace(wire.Integration)
	if integrationName == "" {
		return nil, fmt.Errorf("integration is required")
	}
	if len(integrationName) > maxIntegrationFieldLen {
		return nil, fmt.Errorf("integration exceeds %d characters", maxIntegrationFieldLen)
	}

	provider := strings.ToLower(strings.TrimSpace(wire.Provider))
	if _, ok := allowedProviders[provider]; !ok {
		return nil, fmt.Errorf("unsupported provider %q", provider)
	}

	model := strings.TrimSpace(wire.Model)
	if model == "" {
		return nil, fmt.Errorf("model is required")
	}
	if len(model) > maxIntegrationFieldLen {
		return nil, fmt.Errorf("model exceeds %d characters", maxIntegrationFieldLen)
	}

	if wire.PromptTokens < 0 {
		return nil, fmt.Errorf("prompt_tokens must be >= 0")
	}
	if wire.CompletionTokens < 0 {
		return nil, fmt.Errorf("completion_tokens must be >= 0")
	}

	totalTokens := wire.PromptTokens + wire.CompletionTokens
	if wire.TotalTokens != nil {
		if *wire.TotalTokens < 0 {
			return nil, fmt.Errorf("total_tokens must be >= 0")
		}
		totalTokens = *wire.TotalTokens
	}

	if wire.CostUSD != nil && *wire.CostUSD < 0 {
		return nil, fmt.Errorf("cost_usd must be >= 0")
	}
	if wire.LatencyMS != nil && *wire.LatencyMS < 0 {
		return nil, fmt.Errorf("latency_ms must be >= 0")
	}

	account := strings.TrimSpace(wire.Account)
	if account == "" {
		account = "default"
	}
	if len(account) > maxIntegrationFieldLen {
		return nil, fmt.Errorf("account exceeds %d characters", maxIntegrationFieldLen)
	}

	metadataJSON := ""
	if len(wire.Metadata) > 0 && string(wire.Metadata) != "null" {
		var metadata map[string]interface{}
		if err := json.Unmarshal(wire.Metadata, &metadata); err != nil {
			return nil, fmt.Errorf("metadata must be a JSON object: %w", err)
		}
		compact, err := json.Marshal(metadata)
		if err != nil {
			return nil, fmt.Errorf("compact metadata: %w", err)
		}
		metadataJSON = string(compact)
	}
	if len(metadataJSON) > maxMetadataJSONLen {
		return nil, fmt.Errorf("metadata_json exceeds %d bytes after compaction", maxMetadataJSONLen)
	}

	event := &UsageEvent{
		Timestamp:        ts.UTC(),
		Integration:      integrationName,
		Provider:         provider,
		Account:          account,
		Model:            model,
		RequestID:        strings.TrimSpace(wire.RequestID),
		PromptTokens:     wire.PromptTokens,
		CompletionTokens: wire.CompletionTokens,
		TotalTokens:      totalTokens,
		CostUSD:          wire.CostUSD,
		LatencyMS:        wire.LatencyMS,
		MetadataJSON:     metadataJSON,
		SourcePath:       sourcePath,
	}
	event.Fingerprint = eventFingerprint(event)
	return event, nil
}

func eventFingerprint(event *UsageEvent) string {
	h := sha256.New()
	writeHashPart(h, event.SourcePath)
	writeHashPart(h, event.Timestamp.Format(time.RFC3339Nano))
	writeHashPart(h, event.Integration)
	writeHashPart(h, event.Provider)
	writeHashPart(h, event.Account)
	writeHashPart(h, event.Model)
	writeHashPart(h, fmt.Sprintf("%d", event.PromptTokens))
	writeHashPart(h, fmt.Sprintf("%d", event.CompletionTokens))
	writeHashPart(h, fmt.Sprintf("%d", event.TotalTokens))
	writeHashPart(h, event.RequestID)
	return hex.EncodeToString(h.Sum(nil))
}

func writeHashPart(h interface{ Write([]byte) (int, error) }, part string) {
	_, _ = h.Write([]byte(part))
	_, _ = h.Write([]byte{0})
}
