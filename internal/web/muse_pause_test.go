package web

import (
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/store"
)

func newMusePauseHandler(t *testing.T) *Handler {
	t.Helper()
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return &Handler{store: st}
}

func TestMuseCLIPaused(t *testing.T) {
	h := newMusePauseHandler(t)

	if h.museCLIPaused() {
		t.Error("no marker recorded, want not paused")
	}

	if err := h.store.SetSetting(store.SettingMuseCLIActiveAt, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if !h.museCLIPaused() {
		t.Error("a marker written just now must read as paused")
	}

	// The marker expires so a single skip cannot pause the tab forever.
	stale := time.Now().UTC().Add(-store.MuseCLIActiveWindow - time.Minute)
	if err := h.store.SetSetting(store.SettingMuseCLIActiveAt, stale.Format(time.RFC3339)); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if h.museCLIPaused() {
		t.Error("a marker older than the window must not read as paused")
	}

	// A successful poll clears it.
	if err := h.store.SetSetting(store.SettingMuseCLIActiveAt, ""); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if h.museCLIPaused() {
		t.Error("a cleared marker must not read as paused")
	}

	if err := h.store.SetSetting(store.SettingMuseCLIActiveAt, "not-a-timestamp"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if h.museCLIPaused() {
		t.Error("an unparseable marker must not read as paused")
	}
}
