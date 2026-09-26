package agent

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

type partialMistralFetcher struct{ calls int }

func (f *partialMistralFetcher) FetchSnapshot(_ context.Context, s api.MistralSession) (*api.MistralSnapshot, error) {
	f.calls++
	now := time.Now().UTC()
	status := "partial"
	if f.calls > 2 {
		status = "ok"
	}
	return &api.MistralSnapshot{Identity: s.Source.Identity(), CapturedAt: now, Status: status, AuthFailed: f.calls <= 2, Quotas: []api.MistralQuota{{Name: "api_included", Used: float64(f.calls), Limit: 100, Utilization: float64(f.calls), CapturedAt: now}}}, nil
}
func TestMistralBillingAuthDoesNotFreezeQuotas(t *testing.T) {
	db, e := store.New(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	a := NewMistralAgent(db, &config.Config{MistralEnabled: true, MistralAuthMode: "manual", MistralAuthCookie: "ory_session_test=synthetic"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer a.sm.Close()
	f := &partialMistralFetcher{}
	a.client = f
	a.poll(context.Background())
	if f.calls != 1 {
		t.Fatalf("partial success should not reimport credentials, got %d requests", f.calls)
	}
	a.next = time.Time{}
	a.poll(context.Background())
	if f.calls != 2 {
		t.Fatal("allowances remain readable, but unchanged cookie suppresses every subsequent network poll after billing authentication failure")
	}
	a.next = time.Time{}
	a.poll(context.Background())
	status, err := db.GetSetting("mistral_status")
	if err != nil || f.calls != 3 || status != "ok" || a.paused {
		t.Fatalf("did not recover: calls=%d status=%s paused=%v err=%v", f.calls, status, a.paused, err)
	}

}

func TestMistralSessionRejected(t *testing.T) {
	zero := 0.0
	for _, tc := range []struct {
		name     string
		snapshot *api.MistralSnapshot
		err      error
		want     bool
	}{
		{"full rejection", nil, api.ErrMistralAuth, true},
		{"empty auth failure", &api.MistralSnapshot{AuthFailed: true}, nil, true},
		{"billing still works", &api.MistralSnapshot{AuthFailed: true, Billing: &api.MistralBilling{Amount: &zero}}, nil, false},
		{"unavailable billing", &api.MistralSnapshot{AuthFailed: true, Billing: &api.MistralBilling{}}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := mistralSessionRejected(tc.snapshot, tc.err); got != tc.want {
				t.Fatalf("rejected=%v want %v", got, tc.want)
			}
		})
	}
}
