package web

import (
	"bytes"
	"net/http"
	"sync"
	"time"
)

// Dashboard aggregate endpoints (insights, history, cycle overview, logging
// history) are pure functions of stored data and settings, but the dashboard
// re-requests them on every load, tab switch and refresh tick while the data
// only changes when a provider polls. responseCache keeps each answer until the
// provider it covers polls again, so each result is computed once per new data
// instead of once per request.
//
// Validity:
//   - a poll of provider P invalidates responses for P and for the combined
//     views (provider "both", "all" or unset);
//   - any settings or provider-configuration write invalidates everything
//     (insight visibility, timezone and provider toggles shape responses);
//   - an entry never outlives responseCacheMaxAge, which bounds how far a
//     relative range ("last 6h") can drift.
//
// The cache is bounded by entry count and bytes; exceeding either drops it.
//
// A handler that answers 200 with partial data (some reads failed) sets
// partialResponseHeader; such answers are never cached, so the next request
// retries the failed reads.
const partialResponseHeader = "X-Onwatch-Partial"

const (
	responseCacheMaxAge     = 60 * time.Second
	responseCacheMaxEntries = 128
	responseCacheMaxBytes   = 8 << 20
)

type cachedResponse struct {
	status      int
	contentType string
	body        []byte
	provider    string
	providerGen uint64
	allGen      uint64
	globalGen   uint64
	storedAt    time.Time
}

type responseCache struct {
	mu          sync.Mutex
	entries     map[string]*cachedResponse
	bytes       int
	providerGen map[string]uint64
	allGen      uint64 // bumps on any provider poll
	globalGen   uint64 // bumps on settings writes
	now         func() time.Time
}

func newResponseCache() *responseCache {
	return &responseCache{
		entries:     make(map[string]*cachedResponse),
		providerGen: make(map[string]uint64),
	}
}

func (c *responseCache) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// combinedProvider reports whether a provider query value covers every
// provider, so any poll invalidates it.
func combinedProvider(provider string) bool {
	return provider == "" || provider == "both" || provider == "all"
}

// notePoll invalidates responses for one provider and the combined views.
func (c *responseCache) notePoll(provider string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.providerGen[provider]++
	c.allGen++
	c.mu.Unlock()
}

// invalidateAll drops every cached response (settings writes).
func (c *responseCache) invalidateAll() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.globalGen++
	c.entries = make(map[string]*cachedResponse)
	c.bytes = 0
	c.mu.Unlock()
}

func (c *responseCache) get(key, provider string) *cachedResponse {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entries[key]
	if entry == nil {
		return nil
	}
	valid := entry.globalGen == c.globalGen &&
		c.clock().Sub(entry.storedAt) < responseCacheMaxAge
	if valid {
		if combinedProvider(provider) {
			valid = entry.allGen == c.allGen
		} else {
			valid = entry.providerGen == c.providerGen[provider]
		}
	}
	if !valid {
		c.bytes -= len(entry.body)
		delete(c.entries, key)
		return nil
	}
	return entry
}

// put stores a response computed under the generations captured before the
// handler ran, so a poll that lands mid-computation leaves it already stale.
func (c *responseCache) put(key string, entry *cachedResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry.globalGen != c.globalGen {
		return
	}
	if old := c.entries[key]; old != nil {
		c.bytes -= len(old.body)
	}
	if len(c.entries) >= responseCacheMaxEntries || c.bytes+len(entry.body) > responseCacheMaxBytes {
		c.entries = make(map[string]*cachedResponse)
		c.bytes = 0
	}
	c.entries[key] = entry
	c.bytes += len(entry.body)
}

func (c *responseCache) generations(provider string) (providerGen, allGen, globalGen uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.providerGen[provider], c.allGen, c.globalGen
}

// bufferedResponse captures a handler's output so it can be cached.
type bufferedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (b *bufferedResponse) Header() http.Header { return b.header }
func (b *bufferedResponse) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	return b.body.Write(p)
}
func (b *bufferedResponse) WriteHeader(status int) {
	if b.status == 0 {
		b.status = status
	}
}

// cachedGET serves GET requests for a data-derived endpoint from the response
// cache, computing and storing the answer on a miss. Only 200 responses are
// cached; errors are always recomputed.
func (h *Handler) cachedGET(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cache := h.responses
		if cache == nil || r.Method != http.MethodGet {
			next(w, r)
			return
		}
		provider := r.URL.Query().Get("provider")
		key := r.URL.Path + "?" + r.URL.RawQuery
		if entry := cache.get(key, provider); entry != nil {
			writeCachedResponse(w, entry)
			return
		}
		providerGen, allGen, globalGen := cache.generations(provider)
		buf := &bufferedResponse{header: make(http.Header)}
		next(buf, r)
		status := buf.status
		if status == 0 {
			status = http.StatusOK
		}
		entry := &cachedResponse{
			status:      status,
			contentType: buf.header.Get("Content-Type"),
			body:        buf.body.Bytes(),
			provider:    provider,
			providerGen: providerGen,
			allGen:      allGen,
			globalGen:   globalGen,
			storedAt:    cache.clock(),
		}
		for k, values := range buf.header {
			for _, v := range values {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(status)
		_, _ = w.Write(entry.body)
		if status == http.StatusOK && buf.header.Get(partialResponseHeader) == "" {
			cache.put(key, entry)
		}
	}
}

func writeCachedResponse(w http.ResponseWriter, entry *cachedResponse) {
	if entry.contentType != "" {
		w.Header().Set("Content-Type", entry.contentType)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(entry.status)
	_, _ = w.Write(entry.body)
}
