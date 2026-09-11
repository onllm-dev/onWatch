package web

import (
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

func createTestConfigWithMuse() *config.Config {
	return &config.Config{
		MuseAPIKey:  "[REDACTED]",
		MuseModel:   "muse-spark-1.3",
		MuseEnabled: true,
		PollInterval: 60 * time.Second,
		Port:         9212,
		AdminUser:    "admin",
		AdminPass:    "test",
		DBPath:       "./test.db",
	}
}

func insertTestMuseSnapshot(t *testing.T, s *store.Store, capturedAt time.Time) {
	t.Helper()
	reset := capturedAt.Add(5 * time.Hour)
	weeklyReset := capturedAt.Add(7 * 24 * time.Hour)
	snap := &api.MuseSnapshot{
		CapturedAt:         capturedAt,
		Tier:               "pro",
		Model:              "muse-spark-1.3",
		WindowUsedPct:      34,
		WindowResetsAt:     &reset,
		WindowDurationMins: 300,
		WeeklyUsedPct:      12.5,
		WeeklyResetsAt:     &weeklyReset,
		Quotas: []api.MuseQuota{
			{Name: api.MuseQuotaWindow5H, Used: 34, Limit: 100, Utilization: 34, Format: api.MuseQuotaFormatPercent, ResetsAt: &reset},
			{Name: api.MuseQuotaWeekly, Used: 12.5, Limit: 100, Utilization: 12.5, Format: api.MuseQuotaFormatPercent, ResetsAt: &weeklyReset},
		},
	}
	if _, err := s.InsertMuseSnapshot(snap); err != nil {
		t.Fatalf("failed to insert test Muse snapshot: %v", err)
	}
}

func TestBuildMuseCurrent_UsesLatestSnapshot(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	insertTestMuseSnapshot(t, s, now)

	h := NewHandler(s, nil, nil, nil, createTestConfigWithMuse())
	current := h.buildMuseCurrent()

	if got := current["model"]; got != "muse-spark-1.3" {
		t.Fatalf("model = %v, want muse-spark-1.3", got)
	}
	if got := current["tier"]; got != "pro" {
		t.Fatalf("tier = %v, want pro", got)
	}
	quotas, ok := current["quotas"].([]interface{})
	if !ok || len(quotas) != 2 {
		t.Fatalf("quotas = %#v, want two quotas", current["quotas"])
	}
	first, ok := quotas[0].(map[string]interface{})
	if !ok {
		t.Fatalf("quota[0] = %#v", quotas[0])
	}
	if first["name"] != api.MuseQuotaWindow5H {
		t.Fatalf("quota[0].name = %v, want window_5h first", first["name"])
	}
}

func TestBuildMuseCurrent_NumericTierHidden(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	reset := now.Add(5 * time.Hour)
	snap := &api.MuseSnapshot{
		CapturedAt: now,
		Tier:       "27681527378179523",
		Quotas: []api.MuseQuota{
			{Name: api.MuseQuotaWeekly, Used: 1, Limit: 100, Utilization: 1, Format: api.MuseQuotaFormatPercent, ResetsAt: &reset},
		},
	}
	if _, err := s.InsertMuseSnapshot(snap); err != nil {
		t.Fatalf("insert: %v", err)
	}

	h := NewHandler(s, nil, nil, nil, createTestConfigWithMuse())
	current := h.buildMuseCurrent()
	if _, ok := current["tier"]; ok {
		t.Fatalf("numeric tier must not be exposed, got %v", current["tier"])
	}
}

func TestBuildMuseCurrent_EmptyStore(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	h := NewHandler(s, nil, nil, nil, createTestConfigWithMuse())
	current := h.buildMuseCurrent()
	quotas, ok := current["quotas"].([]interface{})
	if !ok || len(quotas) != 0 {
		t.Fatalf("quotas = %#v, want empty", current["quotas"])
	}
}

func TestBuildMuseInsights_BurnRate(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	insertTestMuseSnapshot(t, s, now)

	h := NewHandler(s, nil, nil, nil, createTestConfigWithMuse())
	h.SetMuseTracker(tracker.NewMuseTracker(s, nil))
	resp := h.buildMuseInsights(map[string]bool{}, time.Hour)
	if len(resp.Stats) == 0 {
		t.Fatal("expected insight stats")
	}
	found := false
	for _, st := range resp.Stats {
		if st.Label == "5h Prompts Burn Rate" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing 5h burn rate stat: %+v", resp.Stats)
	}
}

func TestBuildMuseSummaryMap_EmptyWithoutTracker(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	h := NewHandler(s, nil, nil, nil, createTestConfigWithMuse())
	if got := h.buildMuseSummaryMap(); len(got) != 0 {
		t.Fatalf("summary = %v, want empty", got)
	}
}
