package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"
)

// Explicit opt-in only. Default test/CI runs never access developer credentials.
func TestMistralLive(t *testing.T) {
	if os.Getenv("ONWATCH_MISTRAL_LIVE") != "1" {
		t.Skip("live check is opt-in")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	sources, e := MistralSources("chrome", "")
	if e != nil {
		t.Fatal(e)
	}
	client := NewMistralClient()
	for _, source := range sources {
		sessions, e := ImportMistralSessions(ctx, source, nil)
		if e != nil {
			continue
		}
		for _, session := range sessions {
			snap, e := client.FetchSnapshot(ctx, session)
			if e != nil {
				continue
			}
			for _, q := range snap.Quotas {
				fmt.Printf("MISTRAL_LIVE %s used=%.6f limit=%.2f currency=%s reset=%v\n", q.Name, q.Used, q.Limit, q.Currency, q.ResetsAt)
			}
			now := time.Now().UTC()
			data, e := client.get(ctx, session, fmt.Sprintf("https://admin.mistral.ai/api/billing/v2/usage?month=%d&year=%d", now.Month(), now.Year()))
			if e != nil {
				t.Fatal(e)
			}
			// Report schema and numeric billing fields only; never cookies/account metadata.
			var root map[string]any
			if json.Unmarshal(data, &root) != nil {
				t.Fatal("invalid billing JSON")
			}
			var sanitize func(any) any
			sanitize = func(v any) any {
				switch x := v.(type) {
				case map[string]any:
					out := map[string]any{}
					for k, v := range x {
						switch k {
						case "currency", "price", "value_paid", "value", "vibe_usage", "billing_metric", "billing_group", "start_date", "end_date", "date":
							out[k] = v
						default:
							out[k] = sanitize(v)
						}
					}
					return out
				case []any:
					if len(x) > 0 {
						return []any{sanitize(x[0])}
					}
					return []any{}
				case float64:
					return x
				default:
					return "<redacted>"
				}
			}
			safe, _ := json.Marshal(sanitize(root))
			fmt.Printf("MISTRAL_BILLING_SCHEMA %s\n", safe)
			return
		}
	}
	t.Fatal("automatic import could not retrieve a Mistral session")
}
