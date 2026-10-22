package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

// TestHandler_HistoryBoth_ReportsPerProviderReadFailure covers the All
// Providers chart: a failed read for one provider used to vanish from the
// response, so the dashboard treated the provider as empty and blanked its
// mini-chart. The handler must list the failure explicitly while the other
// providers still come back intact and the response stays HTTP 200.
func TestHandler_HistoryBoth_ReportsPerProviderReadFailure(t *testing.T) {
	newHandlerWithSyntheticRows := func(t *testing.T) (*Handler, string) {
		t.Helper()
		cfg := createTestConfigWithAllProviders()
		h, s, dsn := newSharedMemoryMenubarHandler(t, cfg)
		now := time.Now().UTC()
		for i := 0; i < 3; i++ {
			if _, err := s.InsertSnapshot(&api.Snapshot{
				CapturedAt: now.Add(-time.Duration(i) * 5 * time.Minute),
				Sub:        api.QuotaInfo{Limit: 1000, Requests: float64(100 + i*25)},
				Search:     api.QuotaInfo{Limit: 250, Requests: float64(i * 10)},
				ToolCall:   api.QuotaInfo{Limit: 16200, Requests: float64(i * 200)},
			}); err != nil {
				t.Fatalf("InsertSnapshot: %v", err)
			}
		}
		return h, dsn
	}

	t.Run("failed read is listed", func(t *testing.T) {
		h, dsn := newHandlerWithSyntheticRows(t)

		// Break one provider's storage while the rest stays readable.
		execSQLOnDSN(t, dsn, "ALTER TABLE zai_snapshots RENAME TO zai_snapshots_broken")

		req := httptest.NewRequest(http.MethodGet, "/api/history?provider=both&range=1h", nil)
		rr := httptest.NewRecorder()
		h.History(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (a partial read must not fail the whole view)", rr.Code)
		}

		var response map[string]interface{}
		if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode response: %v", err)
		}

		errorsMap, ok := response["errors"].(map[string]interface{})
		if !ok {
			t.Fatalf("response errors = %#v, want a map listing zai", response["errors"])
		}
		if _, ok := errorsMap["zai"]; !ok {
			t.Fatalf("errors = %#v, want a zai entry", errorsMap)
		}

		synData, ok := response["synthetic"].([]interface{})
		if !ok || len(synData) == 0 {
			t.Fatalf("synthetic rows = %#v, want intact data", response["synthetic"])
		}
		if _, present := response["zai"]; present {
			t.Error("failed provider must not be reported as an empty successful read")
		}
	})

	t.Run("healthy read reports no errors", func(t *testing.T) {
		h, _ := newHandlerWithSyntheticRows(t)

		req := httptest.NewRequest(http.MethodGet, "/api/history?provider=both&range=1h", nil)
		rr := httptest.NewRecorder()
		h.History(rr, req)

		var response map[string]interface{}
		if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if _, present := response["errors"]; present {
			t.Errorf("healthy response reports errors: %#v", response["errors"])
		}
		if _, ok := response["zai"]; !ok {
			t.Error("healthy response is missing zai")
		}
	})
}
