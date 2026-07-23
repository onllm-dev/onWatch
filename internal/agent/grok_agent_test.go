package agent

import (
	"context"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

func TestNewGrokAgent_Basic(t *testing.T) {
	// Just construction + interface satisfaction smoke (Run is tested via manager in higher tests)
	a := NewGrokAgent(nil, nil, nil, 60*time.Second, nil, nil)
	if a == nil {
		t.Fatal("nil agent")
	}
	a.SetPollingCheck(func() bool { return true })
	a.SetNotifier(nil)
	// Run would block; we don't call it here.
	_ = a
}

func TestGrokAgent_Poll_NoClientSafe(t *testing.T) {
	// With nil client, poll should log error and return without panic.
	dir := t.TempDir()
	dbp := dir + "/a.db"
	st, _ := store.New(dbp)
	defer st.Close()
	tr := tracker.NewGrokTracker(st, nil)
	ag := NewGrokAgent(nil, st, tr, time.Second, nil, NewSessionManager(st, "grok", 60*time.Second, nil))
	ag.poll(context.Background())
	// no crash = pass
}
