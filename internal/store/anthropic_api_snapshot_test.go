package store

import (
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

// insertSourcedSnapshot stores one snapshot tagged as coming from the
// statusline bridge or the OAuth usage API.
func insertSourcedSnapshot(t *testing.T, s *Store, at time.Time, statusline bool) {
	t.Helper()
	snap := &api.AnthropicSnapshot{
		CapturedAt: at,
		Quotas:     []api.AnthropicQuota{{Name: "five_hour", Utilization: 10}},
		RawJSON:    `{"five_hour":{"utilization":10}}`,
	}
	if statusline {
		snap.RawJSON = `{"_source":"statusline","rate_limits":{"five_hour":{"used_percentage":10}}}`
	}
	if _, err := s.InsertAnthropicSnapshot(snap); err != nil {
		t.Fatalf("insert snapshot: %v", err)
	}
}

// The hybrid poll decides from how old the last API reading is, so a run of
// statusline snapshots must not be mistaken for a recent API poll.
func TestLastAnthropicAPISnapshot_IgnoresStatuslineRows(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer s.Close()

	want := time.Now().UTC().Add(-90 * time.Minute).Truncate(time.Second)
	insertSourcedSnapshot(t, s, want, false)
	for i := 0; i < 5; i++ {
		insertSourcedSnapshot(t, s, time.Now().UTC().Add(-time.Duration(i)*time.Minute), true)
	}

	got, ok, err := s.LastAnthropicAPISnapshot()
	if err != nil {
		t.Fatalf("LastAnthropicAPISnapshot: %v", err)
	}
	if !ok {
		t.Fatal("expected an API snapshot to be found")
	}
	if diff := got.Sub(want); diff > time.Second || diff < -time.Second {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestLastAnthropicAPISnapshot_NoneRecorded(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer s.Close()

	insertSourcedSnapshot(t, s, time.Now().UTC(), true)

	if _, ok, err := s.LastAnthropicAPISnapshot(); err != nil {
		t.Fatalf("LastAnthropicAPISnapshot: %v", err)
	} else if ok {
		t.Error("expected no API snapshot when only statusline rows exist")
	}
}

func TestLastAnthropicAPISnapshot_EmptyTable(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer s.Close()

	if _, ok, err := s.LastAnthropicAPISnapshot(); err != nil {
		t.Fatalf("LastAnthropicAPISnapshot: %v", err)
	} else if ok {
		t.Error("expected no API snapshot in an empty table")
	}
}
