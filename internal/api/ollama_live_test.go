package api

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

// TestOllamaLive hits the real ollama.com API. It is skipped unless
// ONWATCH_OLLAMA_LIVE=1 and OLLAMA_API_KEY are set, so CI never touches the
// network here.
func TestOllamaLive(t *testing.T) {
	if os.Getenv("ONWATCH_OLLAMA_LIVE") != "1" {
		t.Skip("set ONWATCH_OLLAMA_LIVE=1 and OLLAMA_API_KEY to run")
	}
	key := os.Getenv("OLLAMA_API_KEY")
	if key == "" {
		t.Skip("OLLAMA_API_KEY not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	snap, err := NewOllamaClient(key, nil).FetchSnapshot(ctx)
	if err != nil {
		t.Fatalf("FetchSnapshot: %v", err)
	}
	snap.RawJSON = "" // keep output short
	snap.AccountEmail = "<redacted>"
	out, _ := json.MarshalIndent(snap, "", "  ")
	t.Logf("live snapshot:\n%s", out)
	if len(snap.Quotas) != 1 || snap.Quotas[0].Name != OllamaQuotaMonthly {
		t.Fatalf("unexpected quotas: %+v", snap.Quotas)
	}
	if snap.Plan == "" {
		t.Errorf("plan missing - /api/me failed?")
	}
}
