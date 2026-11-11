package agent

import (
	"context"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/notify"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

type zenmuxStub struct {
	calls    int
	snapshot *api.ZenmuxSnapshot
}

func (s *zenmuxStub) FetchSnapshot(context.Context) (*api.ZenmuxSnapshot, error) {
	s.calls++
	return s.snapshot, nil
}

func TestZenmuxAgentPausedDoesNotPoll(t *testing.T) {
	db, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	client := &zenmuxStub{}
	ag := NewZenmuxAgent(client, db, time.Minute, nil, nil)
	ag.SetPollingCheck(func() bool { return false })
	ag.poll(context.Background())
	if client.calls != 0 {
		t.Fatal("poll ran while disabled")
	}
}

func TestZenmuxAgentPollStoresSnapshot(t *testing.T) {
	db, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	zero := 0.0
	client := &zenmuxStub{snapshot: &api.ZenmuxSnapshot{CapturedAt: time.Now().UTC(), Quota5Hour: &api.ZenmuxWindow{UsagePercentage: &zero}}}
	NewZenmuxAgent(client, db, time.Minute, nil, nil).poll(context.Background())
	got, err := db.QueryLatestZenmux()
	if err != nil || got == nil || got.Quota5Hour.UsagePercentage == nil || *got.Quota5Hour.UsagePercentage != 0 {
		t.Fatalf("poll not stored: %v", err)
	}
}

type zenmuxNotificationSink struct{ statuses []notify.QuotaStatus }

func (n *zenmuxNotificationSink) Check(s notify.QuotaStatus) { n.statuses = append(n.statuses, s) }

func TestZenmuxAgentResetsAlertOnProvenWindowRollover(t *testing.T) {
	db, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	oldReset := now.Add(time.Hour)
	newReset := now.Add(6 * time.Hour)
	high := 0.9
	low := 0.0
	_, err = db.InsertZenmuxSnapshot(&api.ZenmuxSnapshot{CapturedAt: now.Add(-time.Minute), Quota5Hour: &api.ZenmuxWindow{UsagePercentage: &high, ResetsAt: &oldReset}})
	if err != nil {
		t.Fatal(err)
	}
	sink := &zenmuxNotificationSink{}
	client := &zenmuxStub{snapshot: &api.ZenmuxSnapshot{CapturedAt: now, Quota5Hour: &api.ZenmuxWindow{UsagePercentage: &low, ResetsAt: &newReset}}}
	NewZenmuxAgent(client, db, time.Minute, nil, sink).poll(context.Background())
	if len(sink.statuses) != 2 || !sink.statuses[0].ResetOccurred || sink.statuses[1].ResetOccurred {
		t.Fatal("window change did not clear previous alerts")
	}
}
