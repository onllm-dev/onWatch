package tracker

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

func TestGrokTracker_ProcessAndSummary(t *testing.T) {
	dir := t.TempDir()
	dbp := filepath.Join(dir, "t.db")
	s, _ := store.New(dbp)
	defer s.Close()
	defer os.Remove(dbp)

	tr := NewGrokTracker(s, nil)

	now := time.Now().UTC()
	snap := &api.GrokSnapshot{
		CapturedAt: now,
		Quotas: []api.GrokQuota{
			{Name: "credits", Utilization: 10.0, Status: "healthy"},
		},
	}
	if err := tr.Process(snap); err != nil {
		t.Fatalf("process1: %v", err)
	}

	// second higher
	snap2 := &api.GrokSnapshot{
		CapturedAt: now.Add(time.Minute),
		Quotas: []api.GrokQuota{
			{Name: "credits", Utilization: 15.0},
		},
	}
	_ = tr.Process(snap2)

	sum := tr.GetGrokSummary(1, "credits", snap2)
	if sum == nil || sum.CurrentUtil != 15.0 {
		t.Errorf("summary util: %v", sum)
	}
}
