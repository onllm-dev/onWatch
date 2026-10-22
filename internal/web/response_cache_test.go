package web

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func countingHandler(calls *int32, partial bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(calls, 1)
		if partial {
			w.Header().Set(partialResponseHeader, "1")
		}
		respondJSON(w, http.StatusOK, map[string]interface{}{"n": n})
	}
}

func cachedCall(h *Handler, next http.HandlerFunc, url string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.cachedGET(next)(rec, httptest.NewRequest(http.MethodGet, url, nil))
	return rec
}

func TestResponseCacheServesUntilProviderPolls(t *testing.T) {
	h := &Handler{responses: newResponseCache()}
	var calls int32
	next := countingHandler(&calls, false)

	first := cachedCall(h, next, "/api/insights?provider=codex&range=7d")
	second := cachedCall(h, next, "/api/insights?provider=codex&range=7d")
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (second request served from cache)", calls)
	}
	if first.Body.String() != second.Body.String() {
		t.Fatalf("cached body differs: %q vs %q", first.Body.String(), second.Body.String())
	}
	if second.Header().Get("Content-Type") == "" {
		t.Fatal("cached response lost its Content-Type")
	}

	// Another provider polling leaves the codex answer valid.
	h.responses.notePoll("anthropic")
	cachedCall(h, next, "/api/insights?provider=codex&range=7d")
	if calls != 1 {
		t.Fatalf("calls = %d after an unrelated poll, want 1", calls)
	}

	// A codex poll invalidates it.
	h.responses.notePoll("codex")
	cachedCall(h, next, "/api/insights?provider=codex&range=7d")
	if calls != 2 {
		t.Fatalf("calls = %d after a codex poll, want 2", calls)
	}
}

func TestResponseCacheCombinedViewFollowsAnyPoll(t *testing.T) {
	h := &Handler{responses: newResponseCache()}
	var calls int32
	next := countingHandler(&calls, false)

	cachedCall(h, next, "/api/history?provider=both&range=6h")
	cachedCall(h, next, "/api/history?provider=both&range=6h")
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
	h.responses.notePoll("grok")
	cachedCall(h, next, "/api/history?provider=both&range=6h")
	if calls != 2 {
		t.Fatalf("calls = %d after any poll, want 2 for the combined view", calls)
	}
}

func TestResponseCacheSettingsWriteInvalidatesAll(t *testing.T) {
	h := &Handler{responses: newResponseCache()}
	var calls int32
	next := countingHandler(&calls, false)

	cachedCall(h, next, "/api/insights?provider=codex")
	h.invalidateMenubarSnapshot()
	cachedCall(h, next, "/api/insights?provider=codex")
	if calls != 2 {
		t.Fatalf("calls = %d after a settings write, want 2", calls)
	}
}

func TestResponseCacheSkipsPartialAndNonGET(t *testing.T) {
	h := &Handler{responses: newResponseCache()}
	var calls int32
	partial := countingHandler(&calls, true)
	cachedCall(h, partial, "/api/history?provider=both")
	cachedCall(h, partial, "/api/history?provider=both")
	if calls != 2 {
		t.Fatalf("calls = %d, want 2: partial responses must not be cached", calls)
	}

	var postCalls int32
	next := countingHandler(&postCalls, false)
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		h.cachedGET(next)(rec, httptest.NewRequest(http.MethodPost, "/api/insights?provider=codex", nil))
	}
	if postCalls != 2 {
		t.Fatalf("POST calls = %d, want 2 (never cached)", postCalls)
	}
}

func TestResponseCacheExpiresAfterMaxAge(t *testing.T) {
	h := &Handler{responses: newResponseCache()}
	now := time.Now()
	h.responses.now = func() time.Time { return now }
	var calls int32
	next := countingHandler(&calls, false)

	cachedCall(h, next, "/api/history?provider=codex&range=6h")
	now = now.Add(responseCacheMaxAge + time.Second)
	cachedCall(h, next, "/api/history?provider=codex&range=6h")
	if calls != 2 {
		t.Fatalf("calls = %d past max age, want 2", calls)
	}
}

func TestResponseCacheNilIsPassThrough(t *testing.T) {
	h := &Handler{}
	var calls int32
	next := countingHandler(&calls, false)
	cachedCall(h, next, "/api/insights?provider=codex")
	cachedCall(h, next, "/api/insights?provider=codex")
	if calls != 2 {
		t.Fatalf("calls = %d, want 2 without a cache", calls)
	}
	h.invalidateMenubarSnapshot()
}
