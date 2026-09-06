package agent

import (
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

func pollTestAgent(t *testing.T, interval time.Duration) (*AnthropicAgent, *store.Store) {
	t.Helper()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	a := NewAnthropicAgent(nil, s, nil, interval, nil, nil)
	// The hybrid schedule only exists when the statusline bridge is on; this is
	// what the daemon does in auto mode, and it sets the default 10 cycles.
	a.EnableStatuslineBridge()
	return a, s
}

func seedAPISnapshot(t *testing.T, s *store.Store, at time.Time) {
	t.Helper()
	_, err := s.InsertAnthropicSnapshot(&api.AnthropicSnapshot{
		CapturedAt: at,
		Quotas:     []api.AnthropicQuota{{Name: "five_hour", Utilization: 10}},
		RawJSON:    `{"five_hour":{"utilization":10}}`,
	})
	if err != nil {
		t.Fatalf("insert snapshot: %v", err)
	}
}

// The bug this replaces: pollCycleCount starts at zero in memory, so every
// restart delayed supplementary quotas by a full interval - 50 minutes at the
// defaults. An upgrade therefore looked like it had changed nothing.
func TestBeginSupplementalAPIPoll_PollsWhenNoAPISnapshotExists(t *testing.T) {
	a, _ := pollTestAgent(t, 5*time.Minute)

	if !a.beginSupplementalAPIPoll() {
		t.Fatal("expected an API poll when nothing has ever been recorded")
	}
}

func TestBeginSupplementalAPIPoll_PollsWhenStoredReadingIsStale(t *testing.T) {
	a, s := pollTestAgent(t, 5*time.Minute)
	// Interval 10 x 5m = 50m, so 90 minutes old is stale.
	seedAPISnapshot(t, s, time.Now().UTC().Add(-90*time.Minute))

	if !a.beginSupplementalAPIPoll() {
		t.Fatal("expected an API poll for a 90 minute old reading")
	}
}

// Restarting must not fire a fresh API call each time. Anthropic's usage API
// rate limits hard, so a restart loop used to be a way to get 429ed.
func TestBeginSupplementalAPIPoll_SkipsWhenStoredReadingIsFresh(t *testing.T) {
	a, s := pollTestAgent(t, 5*time.Minute)
	seedAPISnapshot(t, s, time.Now().UTC().Add(-2*time.Minute))

	if a.beginSupplementalAPIPoll() {
		t.Fatal("a 2 minute old reading should not trigger another API poll")
	}
}

// Once a poll is claimed the next call must wait out the interval, whether or
// not that poll succeeded. Retrying a failing API on every cycle would turn one
// 429 into a storm.
func TestBeginSupplementalAPIPoll_ClaimsTheInterval(t *testing.T) {
	a, _ := pollTestAgent(t, 5*time.Minute)

	if !a.beginSupplementalAPIPoll() {
		t.Fatal("first call should poll")
	}
	if a.beginSupplementalAPIPoll() {
		t.Fatal("second call should wait for the interval to elapse")
	}
}

func TestBeginSupplementalAPIPoll_DisabledByZeroInterval(t *testing.T) {
	a, _ := pollTestAgent(t, 5*time.Minute)
	a.SetAPIPollCycleInterval(0)

	if a.beginSupplementalAPIPoll() {
		t.Fatal("interval 0 means statusline only - the API must never be called")
	}
}

// A successful API poll anywhere in the loop resets the clock.
func TestNoteAPIPoll_ResetsTheInterval(t *testing.T) {
	a, _ := pollTestAgent(t, 5*time.Minute)
	a.noteAPIPoll()

	if a.beginSupplementalAPIPoll() {
		t.Fatal("an API poll was just recorded; another should not be due")
	}
}
