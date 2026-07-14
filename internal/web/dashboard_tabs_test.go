package web

import (
	"path/filepath"
	"testing"

	"github.com/onllm-dev/onwatch/v2/internal/store"
)

func TestOrderDashboardProviders(t *testing.T) {
	t.Parallel()
	available := []string{"anthropic", "zai", "codex", "both"}
	got := orderDashboardProviders(available, []string{"zai", "missing", "anthropic", "zai"})
	want := []string{"zai", "anthropic", "codex", "both"}
	if len(got) != len(want) {
		t.Fatalf("len=%d want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestOrderDashboardProviders_NewProviderBeforeBoth(t *testing.T) {
	t.Parallel()
	// Saved order from an older build that never listed grok, and pinned both last.
	available := []string{"anthropic", "zai", "codex", "antigravity", "gemini", "grok", "both"}
	preferred := []string{"codex", "anthropic", "zai", "antigravity", "gemini", "both"}
	got := orderDashboardProviders(available, preferred)
	// grok must not land after both
	want := []string{"codex", "anthropic", "zai", "antigravity", "gemini", "grok", "both"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestNormalizeDashboardProviderLabel(t *testing.T) {
	t.Parallel()
	if _, ok := normalizeDashboardProviderLabel("   "); ok {
		t.Fatal("empty should clear")
	}
	got, ok := normalizeDashboardProviderLabel("  😆OpenCode+𝗚𝗟𝗠  ")
	if !ok || got != "😆OpenCode+𝗚𝗟𝗠" {
		t.Fatalf("emoji label: %q ok=%v", got, ok)
	}
	runes := make([]rune, 60)
	for i := range runes {
		runes[i] = 'a'
	}
	got, ok = normalizeDashboardProviderLabel(string(runes))
	if !ok || len([]rune(got)) != maxDashboardProviderLabelRunes {
		t.Fatalf("truncated: %d runes ok=%v", len([]rune(got)), ok)
	}
}

func TestResolveProviderTabLabel(t *testing.T) {
	t.Parallel()
	if got := resolveProviderTabLabel("zai", nil); got != "Z.ai" {
		t.Fatalf("default zai: %q", got)
	}
	if got := resolveProviderTabLabel("zai", map[string]string{"zai": "GLM"}); got != "GLM" {
		t.Fatalf("custom: %q", got)
	}
	if got := resolveProviderTabLabel("zai", map[string]string{"zai": "   "}); got != "Z.ai" {
		t.Fatalf("blank custom falls back: %q", got)
	}
}

func TestDashboardTabSettingsRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db, err := store.New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	h := &Handler{store: db}

	if err := h.saveDashboardProvidersOrder([]string{"zai", "Anthropic", "zai", ""}); err != nil {
		t.Fatal(err)
	}
	order := h.loadDashboardProvidersOrder()
	if len(order) != 2 || order[0] != "zai" || order[1] != "anthropic" {
		t.Fatalf("order: %v", order)
	}

	if err := h.saveDashboardProviderLabels(map[string]string{
		"zai":       "😆GLM",
		"anthropic": "Anthropic", // default — should be dropped
		"codex":     "   ",
	}); err != nil {
		t.Fatal(err)
	}
	labels := h.loadDashboardProviderLabels()
	if labels["zai"] != "😆GLM" {
		t.Fatalf("labels: %#v", labels)
	}
	if _, ok := labels["anthropic"]; ok {
		t.Fatalf("default anthropic should not be stored: %#v", labels)
	}
	if _, ok := labels["codex"]; ok {
		t.Fatalf("blank codex should not be stored: %#v", labels)
	}
}

func TestDefaultProviderTabLabel(t *testing.T) {
	t.Parallel()
	if defaultProviderTabLabel("both") != "All" {
		t.Fatal(defaultProviderTabLabel("both"))
	}
	if defaultProviderTabLabel("api-integrations") != "API Integrations" {
		t.Fatal(defaultProviderTabLabel("api-integrations"))
	}
}
