package store

import (
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

func TestOllamaStore_InsertAndQueryLatest(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	reset := now.Add(5 * time.Hour)
	snap := &api.OllamaSnapshot{
		CapturedAt:  now,
		Plan:        "pro",
		AccountName: "Ollama Go",
		Quotas: []api.OllamaQuota{
			{Name: "five_hour", Used: 10, Limit: 100, Utilization: 10, Format: api.OllamaQuotaFormatPercent, ResetsAt: &reset},
			{Name: "weekly", Used: 20, Limit: 100, Utilization: 20, Format: api.OllamaQuotaFormatPercent, ResetsAt: &reset},
		},
	}
	id, err := s.InsertOllamaSnapshot(snap)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if id == 0 {
		t.Error("expected id > 0")
	}

	latest, err := s.QueryLatestOllama()
	if err != nil {
		t.Fatalf("query latest: %v", err)
	}
	if latest == nil || latest.AccountName != "Ollama Go" || len(latest.Quotas) != 2 {
		t.Errorf("latest mismatch: %+v", latest)
	}
}

func TestOllamaStore_QueryRangeLoadsQuotas(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()

	base := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < 3; i++ {
		snap := &api.OllamaSnapshot{
			CapturedAt: base.Add(time.Duration(i) * time.Minute),
			Quotas: []api.OllamaQuota{
				{Name: "five_hour", Utilization: float64(i+1) * 10, Format: api.OllamaQuotaFormatPercent},
			},
		}
		if _, err := s.InsertOllamaSnapshot(snap); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	snaps, err := s.QueryOllamaRange(base.Add(-time.Minute), time.Now().UTC())
	if err != nil {
		t.Fatalf("query range: %v", err)
	}
	if len(snaps) != 3 {
		t.Fatalf("snapshots = %d, want 3", len(snaps))
	}
	if len(snaps[0].Quotas) != 1 {
		t.Fatalf("first snapshot quotas = %d, want 1", len(snaps[0].Quotas))
	}
}

func TestOllamaStore_CycleLifecycle(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	reset := now.Add(5 * time.Hour)
	id, err := s.CreateOllamaCycle("five_hour", now, &reset)
	if err != nil {
		t.Fatalf("CreateOllamaCycle: %v", err)
	}
	if id == 0 {
		t.Fatal("expected cycle id")
	}

	active, err := s.QueryActiveOllamaCycle("five_hour")
	if err != nil {
		t.Fatalf("QueryActiveOllamaCycle: %v", err)
	}
	if active == nil || active.QuotaName != "five_hour" {
		t.Fatalf("active cycle mismatch: %+v", active)
	}

	if err := s.UpdateOllamaCycle("five_hour", 15, 5); err != nil {
		t.Fatalf("UpdateOllamaCycle: %v", err)
	}
	if err := s.CloseOllamaCycle("five_hour", now.Add(time.Hour), 15, 5); err != nil {
		t.Fatalf("CloseOllamaCycle: %v", err)
	}

	history, err := s.QueryOllamaCycleHistory("five_hour")
	if err != nil {
		t.Fatalf("QueryOllamaCycleHistory: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("history = %d, want 1", len(history))
	}
}
