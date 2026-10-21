package web

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

func TestWindowHistoryCoversSelectedRange(t *testing.T) {
	for _, provider := range []string{"opencode", "commandcode"} {
		t.Run(provider, func(t *testing.T) {
			s, err := store.New(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			first := time.Now().UTC().Add(-6 * 24 * time.Hour).Truncate(time.Second)
			for i := 0; i < 401; i++ {
				at := first.Add(time.Duration(i) * 20 * time.Minute)
				if provider == "opencode" {
					_, err = s.InsertOpenCodeSnapshot(&api.OpenCodeSnapshot{CapturedAt: at, Quotas: []api.OpenCodeQuota{{Name: "weekly", Utilization: float64(i % 100)}}})
				} else {
					_, err = s.InsertCommandCodeSnapshot(&api.CommandCodeSnapshot{CapturedAt: at, Quotas: []api.CommandCodeQuota{{Name: "weekly", Utilization: float64(i % 100)}}})
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			h := NewHandler(s, nil, nil, nil, createTestConfigWithCommandCode())
			for _, span := range []string{"7d", "30d"} {
				rec := httptest.NewRecorder()
				req := httptest.NewRequest("GET", "/api/history?range="+span, nil)
				if provider == "opencode" {
					h.historyOpenCode(rec, req)
				} else {
					h.historyCommandCode(rec, req)
				}
				var rows []struct {
					CapturedAt time.Time `json:"capturedAt"`
					Quotas     []struct {
						Name string `json:"name"`
					} `json:"quotas"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
					t.Fatal(err)
				}
				if len(rows) == 0 || len(rows) > 200 {
					t.Fatalf("rows=%d, want 1..200", len(rows))
				}
				if !rows[0].CapturedAt.Equal(first) {
					t.Errorf("%s starts at %s, want earliest %s", span, rows[0].CapturedAt, first)
				}
				if !rows[len(rows)-1].CapturedAt.Equal(first.Add(400 * 20 * time.Minute)) {
					t.Error("latest sample missing")
				}
				if len(rows[0].Quotas) != 1 {
					t.Error("quota values missing")
				}
			}
		})
	}
}
