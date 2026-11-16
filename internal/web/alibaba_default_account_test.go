package web

import (
	"strings"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/menubar"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// The migrations seed an Alibaba "default" provider_accounts row for every
// existing database (mirroring MiniMax). A user who never configured Alibaba
// must not see a tab, menubar card, or configured status because of it.
func TestAlibabaDefaultAccountRowIsInertForUnconfiguredUser(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	accounts, err := s.QueryActiveProviderAccounts("alibaba")
	if err != nil || len(accounts) != 1 || accounts[0].Name != "default" {
		t.Fatalf("expected the seeded default Alibaba row, got %v (%v)", accounts, err)
	}

	cfg := &config.Config{PollInterval: time.Minute}
	h := NewHandler(s, nil, nil, nil, cfg)
	h.SetVersion("test")

	if stringInSlice(cfg.AvailableProviders(), "alibaba") {
		t.Fatalf("AvailableProviders = %v, must not include unconfigured alibaba", cfg.AvailableProviders())
	}
	for _, tab := range h.buildDashboardProviderTabs() {
		if tab == "alibaba" {
			t.Fatal("the dashboard must not grow an Alibaba tab from the default row")
		}
	}
	cards, _, _ := h.buildMenubarProviders(menubar.DefaultSettings(), true)
	for _, card := range cards {
		if card.ID == "alibaba" || strings.HasPrefix(card.ID, "alibaba:") {
			t.Fatalf("the menubar must not grow an Alibaba card from the default row: %s", card.ID)
		}
	}
	if h.isProviderConfigured("alibaba") {
		t.Fatal("the empty default row must not mark Alibaba configured")
	}
}

func stringInSlice(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
