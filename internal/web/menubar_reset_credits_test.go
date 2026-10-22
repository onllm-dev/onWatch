package web

import (
	"testing"

	"github.com/onllm-dev/onwatch/v2/internal/menubar"
)

func TestCodexResetCreditsDecorator(t *testing.T) {
	t.Run("uses available count even when applicable is zero", func(t *testing.T) {
		usage := map[string]interface{}{
			"resetCredits": map[string]interface{}{
				"status":           "ok",
				"available_count":  int64(1),
				"applicable_count": int64(0),
				"next_expires_at":  "2026-11-06T21:34:35Z",
			},
		}
		decorate := codexResetCreditsDecorator(usage)
		if decorate == nil {
			t.Fatal("decorator is nil, want a chip for 1 banked reset")
		}
		card := &menubar.ProviderCard{}
		decorate(card)
		if card.ResetCredits == nil || card.ResetCredits.Count != 1 {
			t.Fatalf("ResetCredits = %+v, want count 1", card.ResetCredits)
		}
		if card.ResetCredits.NextExpiresAt != "2026-11-06T21:34:35Z" {
			t.Fatalf("NextExpiresAt = %q", card.ResetCredits.NextExpiresAt)
		}
	})

	for name, usage := range map[string]map[string]interface{}{
		"zero":          {"resetCredits": map[string]interface{}{"available_count": int64(0)}},
		"unknown count": {"resetCredits": map[string]interface{}{"available_count": nil}},
		"no payload":    {},
	} {
		t.Run(name, func(t *testing.T) {
			if decorate := codexResetCreditsDecorator(usage); decorate != nil {
				card := &menubar.ProviderCard{}
				decorate(card)
				t.Fatalf("want no chip, got %+v", card.ResetCredits)
			}
		})
	}
}
