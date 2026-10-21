package web

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

func TestWindowOverviewAndSampleContract(t *testing.T) {
	for _, provider := range []string{"opencode", "commandcode"} {
		t.Run(provider, func(t *testing.T) {
			s, err := store.New(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			now := time.Now().UTC().Add(-time.Minute)
			reset := now.Add(24 * time.Hour)
			if provider == "opencode" {
				_, err = s.InsertOpenCodeSnapshot(&api.OpenCodeSnapshot{CapturedAt: now, Quotas: []api.OpenCodeQuota{{Name: "weekly", Used: 2.5, Limit: 10, Utilization: 25, Format: api.OpenCodeQuotaFormat("currency"), ResetsAt: &reset}}})
				if err == nil {
					_, err = s.CreateOpenCodeCycle("weekly", now.Add(-time.Hour), &reset)
				}
			} else {
				insertTestCommandCodeSnapshot(t, s, now)
				_, err = s.CreateCommandCodeCycle("weekly", now.Add(-time.Hour), &reset)
			}
			if err != nil {
				t.Fatal(err)
			}
			h := NewHandler(s, nil, nil, nil, createTestConfigWithCommandCode())
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/api/cycle-overview?groupBy=weekly&limit=1", nil)
			if provider == "opencode" {
				h.cycleOverviewOpenCode(rec, req)
			} else {
				h.cycleOverviewCommandCode(rec, req)
			}
			var overview struct {
				GroupBy    string   `json:"groupBy"`
				QuotaNames []string `json:"quotaNames"`
				Cycles     []struct {
					QuotaType   string     `json:"quotaType"`
					ResetsAt    *time.Time `json:"resetsAt"`
					CrossQuotas []struct {
						Limit float64 `json:"limit"`
					} `json:"crossQuotas"`
				} `json:"cycles"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &overview); err != nil {
				t.Fatal(err)
			}
			if overview.GroupBy != "weekly" || len(overview.Cycles) != 1 || len(overview.QuotaNames) == 0 {
				t.Fatalf("invalid overview contract: %s", rec.Body.String())
			}
			if overview.Cycles[0].ResetsAt == nil {
				t.Error("stored reset omitted")
			}
			if len(overview.Cycles[0].CrossQuotas) == 0 || overview.Cycles[0].CrossQuotas[0].Limit <= 0 {
				t.Error("stored limit omitted")
			}
			rec = httptest.NewRecorder()
			req = httptest.NewRequest("GET", "/api/logging-history?range=7", nil)
			if provider == "opencode" {
				h.loggingHistoryOpenCode(rec, req)
			} else {
				h.loggingHistoryCommandCode(rec, req)
			}
			var samples struct {
				Logs []struct {
					CrossQuotas []struct {
						Format   string     `json:"format"`
						ResetsAt *time.Time `json:"resetsAt"`
					} `json:"crossQuotas"`
				} `json:"logs"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &samples); err != nil {
				t.Fatal(err)
			}
			if len(samples.Logs) != 1 || len(samples.Logs[0].CrossQuotas) == 0 {
				t.Fatal("sample missing")
			}
			if samples.Logs[0].CrossQuotas[0].Format == "" || samples.Logs[0].CrossQuotas[0].ResetsAt == nil {
				t.Error("sample format/reset omitted")
			}
		})
	}
}

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
