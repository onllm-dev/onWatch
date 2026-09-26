package web

import (
	"testing"
	"time"
)

func TestMistralReconnectCard(t *testing.T) {
	for _, tc := range []struct {
		status string
		stale  bool
		want   string
	}{
		{"reconnect", true, "warning"},
		{"stale", false, "warning"},
		{"partial", true, "warning"},
		{"ok", false, "healthy"},
	} {
		t.Run(tc.status, func(t *testing.T) {
			p := map[string]interface{}{"status": tc.status, "capturedAt": time.Now().Format(time.RFC3339), "quotas": []interface{}{map[string]interface{}{"name": "vibe_included", "utilization": 9.0, "isStale": tc.stale}}}
			c := normalizeProviderCard("mistral", "Mistral", "", p, 80, 95)
			if c == nil {
				t.Fatal("no card")
			}
			if c.Status != tc.want || c.ConnectionStatus != tc.status || c.Quotas[0].IsStale != tc.stale {
				t.Fatalf("connection state lost: %+v", c)
			}
		})
	}
}
