package web

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/menubar"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

func TestMenubarPeakDecoratorsAttachDeepSeekSchedule(t *testing.T) {
	t.Parallel()

	commandCode := &menubar.ProviderCard{ID: "commandcode"}
	commandCodePeakDecorator(commandCode)
	if commandCode.Peak == nil {
		t.Fatal("Command Code card must carry peak metadata")
	}
	if commandCode.Peak.Provider != "commandcode" {
		t.Fatalf("Command Code peak provider = %q", commandCode.Peak.Provider)
	}
	if commandCode.Peak.Pricing != "peak $0.30 / $1.20" {
		t.Fatalf("Command Code pricing = %q, want the user-supplied figure", commandCode.Peak.Pricing)
	}
	if commandCode.Peak.ScheduleText != menubar.DeepSeekPeakScheduleText {
		t.Fatalf("Command Code schedule text = %q", commandCode.Peak.ScheduleText)
	}
	if commandCode.Peak.Timezone != "UTC" {
		t.Fatalf("peak timezone = %q, want UTC", commandCode.Peak.Timezone)
	}
	if len(commandCode.Peak.Windows) != 2 {
		t.Fatalf("peak windows = %+v, want the 01-04 and 06-10 windows", commandCode.Peak.Windows)
	}

	openCode := &menubar.ProviderCard{ID: "opencode"}
	openCodePeakDecorator(openCode)
	if openCode.Peak == nil {
		t.Fatal("OpenCode card must carry peak metadata")
	}
	if openCode.Peak.ModelNote != "DeepSeek V4.1 Flash / V4 Pro" {
		t.Fatalf("OpenCode model note = %q", openCode.Peak.ModelNote)
	}
	if openCode.Peak.Pricing != "" {
		t.Fatalf("OpenCode pricing = %q, want empty (not published there)", openCode.Peak.Pricing)
	}
}

// Command Code and OpenCode Go cards carry the peak metadata through the real
// provider build. DeepSeek itself has no menubar card, and Anthropic never
// carries peak metadata.
func TestMenubarProvidersAttachPeakOnlyToDeepSeekServingCards(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	insertTestCommandCodeSnapshot(t, s, now)
	insertTestOpenCodeSnapshot(t, s, now, []api.OpenCodeQuota{
		{Name: "five_hour", Utilization: 10, Used: 10, Limit: 100, Format: api.OpenCodeQuotaFormatPercent},
	})
	anthropicSnap := &api.Snapshot{
		CapturedAt: now,
		Sub:        api.QuotaInfo{Limit: 100, Requests: 10, RenewsAt: now.Add(2 * time.Hour)},
	}
	if _, err := s.InsertSnapshot(anthropicSnap); err != nil {
		t.Fatalf("insert anthropic snapshot: %v", err)
	}

	cfg := &config.Config{
		CommandCodeAPIKey:     "user_test",
		CommandCodeEnabled:    true,
		OpenCodeGoWorkspaceID: "ws-test",
		OpenCodeGoAuthCookie:  "cookie-test",
		DeepSeekAPIKey:        "sk-test",
		AnthropicToken:        "token",
		PollInterval:          time.Minute,
	}
	h := NewHandler(s, nil, nil, nil, cfg)
	h.SetVersion("test-version")

	providers, _ := h.buildMenubarProviders(menubar.DefaultSettings(), true)
	byID := make(map[string]menubar.ProviderCard, len(providers))
	for _, card := range providers {
		byID[card.ID] = card
	}

	for _, id := range []string{"commandcode", "opencode"} {
		card, ok := byID[id]
		if !ok {
			t.Fatalf("%s card missing from the menubar build", id)
		}
		if card.Peak == nil {
			t.Fatalf("%s card must carry peak metadata", id)
		}
		encoded, err := json.Marshal(card)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(encoded), `"peak"`) {
			t.Fatalf("%s card JSON is missing peak: %s", id, encoded)
		}
		if strings.Contains(string(encoded), `"promo"`) {
			t.Fatalf("%s card JSON still exposes promo: %s", id, encoded)
		}
	}
	if _, ok := byID["deepseek"]; ok {
		t.Fatal("DeepSeek must not gain a menubar card")
	}
	if card, ok := byID["anthropic"]; ok && card.Peak != nil {
		t.Fatalf("Anthropic card must not carry peak metadata: %+v", card.Peak)
	}
}

// Anthropic no longer publishes a peak-hours promo, so its current payload
// must not carry one for the dashboard or menubar to render.
func TestBuildAnthropicCurrent_NoPromo(t *testing.T) {
	t.Parallel()
	h := NewHandler(nil, nil, nil, nil, createTestConfigWithSynthetic())
	resp := h.buildAnthropicCurrent()
	if _, ok := resp["promo"]; ok {
		t.Fatal("Anthropic response must not carry a promo key")
	}
}
