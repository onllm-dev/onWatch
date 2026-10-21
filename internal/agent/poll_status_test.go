package agent

import (
	"errors"
	"fmt"
	"testing"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

func TestPollStatusRegistrySetClearAndGet(t *testing.T) {
	r := NewPollStatusRegistry()
	if _, ok := r.Get("cursor", ""); ok {
		t.Fatal("empty registry must have no entry")
	}
	r.Report("cursor", "", PollOutcomeNetwork)
	outcome, ok := r.Get("cursor", "")
	if !ok || outcome != PollOutcomeNetwork {
		t.Fatalf("Get = %q, %v; want network, true", outcome, ok)
	}
	r.Clear("cursor", "")
	if _, ok := r.Get("cursor", ""); ok {
		t.Fatal("Clear must remove the entry")
	}
}

func TestPollStatusRegistryAccountScope(t *testing.T) {
	r := NewPollStatusRegistry()
	r.Report("codex", "1", PollOutcomeAuth)
	r.Report("codex", "2", PollOutcomeNetwork)
	if got, _ := r.Get("codex", "1"); got != PollOutcomeAuth {
		t.Fatalf("codex:1 = %q, want auth", got)
	}
	if got, _ := r.Get("codex", "2"); got != PollOutcomeNetwork {
		t.Fatalf("codex:2 = %q, want network", got)
	}
	if _, ok := r.Get("codex", ""); ok {
		t.Fatal("base provider must not inherit an account entry")
	}
	r.Clear("codex", "1")
	if _, ok := r.Get("codex", "1"); ok {
		t.Fatal("clearing one account must remove only that account")
	}
	if _, ok := r.Get("codex", "2"); !ok {
		t.Fatal("clearing one account must keep the other")
	}
}

func TestPollStatusRegistryIsBounded(t *testing.T) {
	r := NewPollStatusRegistry()
	for i := 0; i < maxPollStatusEntries*4; i++ {
		r.Report(fmt.Sprintf("provider-%d", i), "", PollOutcomeNetwork)
	}
	if got := r.Len(); got > maxPollStatusEntries {
		t.Fatalf("registry grew to %d entries, want <= %d", got, maxPollStatusEntries)
	}
	// Re-reporting an existing key must not grow the registry.
	first := r.entriesSnapshotKeys()
	if len(first) == 0 {
		t.Fatal("expected at least one entry")
	}
	for i := 0; i < 10; i++ {
		r.Report("provider-0", "", PollOutcomeNetwork)
	}
	if got := r.Len(); got != len(first) {
		t.Fatalf("re-reporting grew registry from %d to %d", len(first), got)
	}
}

func TestPollStatusReasonMapping(t *testing.T) {
	cases := []struct {
		outcome PollOutcome
		want    string
	}{
		{PollOutcomeNetwork, "network error"},
		{PollOutcomeAuth, "authentication error"},
		{PollOutcomeRateLimit, "rate limited"},
		{PollOutcomeUpstream, "upstream error"},
		{PollOutcomeParse, "invalid response"},
		{PollOutcomePaused, "polling paused"},
		{PollOutcomeSourceStale, "source data is old"},
	}
	for _, tc := range cases {
		if got := tc.outcome.Reason(); got != tc.want {
			t.Errorf("%s.Reason() = %q, want %q", tc.outcome, got, tc.want)
		}
	}
	if got := PollOutcome("weird").Reason(); got != "" {
		t.Errorf("unknown outcome reason = %q, want empty", got)
	}
}

func TestClassifyPollError(t *testing.T) {
	cases := []struct {
		err  error
		want PollOutcome
	}{
		{nil, ""},
		{api.ErrNetworkError, PollOutcomeNetwork},
		{api.ErrAnthropicNetworkError, PollOutcomeNetwork},
		{fmt.Errorf("wrapped: %w", api.ErrCursorNetworkError), PollOutcomeNetwork},
		{api.ErrUnauthorized, PollOutcomeAuth},
		{api.ErrAnthropicUnauthorized, PollOutcomeAuth},
		{api.ErrCursorSessionExpired, PollOutcomeAuth},
		{api.ErrMistralAuth, PollOutcomeAuth},
		{api.ErrAnthropicRateLimited, PollOutcomeRateLimit},
		{api.ErrCodexServerError, PollOutcomeUpstream},
		{api.ErrMiniMaxAccessBlocked, PollOutcomeUpstream},
		{api.ErrInvalidResponse, PollOutcomeParse},
		{api.ErrGrokParseFailed, PollOutcomeParse},
		{api.ErrMistralParse, PollOutcomeParse},
		{errors.New("connection refused"), PollOutcomeUpstream},
	}
	for _, tc := range cases {
		if got := ClassifyPollError(tc.err); got != tc.want {
			t.Errorf("ClassifyPollError(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

// The registry stores only the outcome class, never the error text, so a raw
// provider error (which can carry URLs or identifiers) cannot leak into the
// menubar response.
func TestPollStatusRegistryNeverStoresRawErrors(t *testing.T) {
	r := NewPollStatusRegistry()
	secret := errors.New("GET https://example.test/v1/x?token=super-secret failed")
	r.Report("anthropic", "", ClassifyPollError(secret))
	r.Report("cursor", "", PollOutcomeAuth)
	for _, entry := range r.entriesSnapshot() {
		if entry.outcome != PollOutcomeUpstream && entry.outcome != PollOutcomeAuth {
			t.Fatalf("stored outcome = %q, want only a class", entry.outcome)
		}
	}
}
