package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestZenmuxFetchSnapshotContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/management/subscription/detail" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer synthetic-test-value" {
			t.Error("missing management authorization")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":true,"data":{"plan":{"tier":"ultra","amount_usd":200,"interval":"month","expires_at":"2026-04-12T08:26:56Z"},"currency":"usd","base_usd_per_flow":0.03283,"effective_usd_per_flow":0.03283,"account_status":"healthy","quota_5_hour":{"usage_percentage":0.0715,"resets_at":"2026-03-24T08:35:09Z","remaining_resets":0,"max_flows":800,"used_flows":57.2,"remaining_flows":742.8,"used_value_usd":1.88,"max_value_usd":26.27},"quota_7_day":{"usage_percentage":0,"resets_at":null,"max_flows":6182},"quota_monthly":{"max_flows":34560,"max_value_usd":1134.33}}}`)
	}))
	defer server.Close()
	snap, err := NewZenmuxClient("synthetic-test-value", withZenmuxBaseURL(server.URL)).FetchSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snap.Plan == nil || snap.Plan.Tier == nil || *snap.Plan.Tier != "ultra" || snap.Plan.ExpiresAt == nil {
		t.Fatal("plan missing")
	}
	if snap.Quota5Hour == nil || snap.Quota5Hour.UsagePercentage == nil || *snap.Quota5Hour.UsagePercentage != 0.0715 {
		t.Fatal("wrong fraction")
	}
	if snap.Quota5Hour.RemainingResets == nil || *snap.Quota5Hour.RemainingResets != 0 {
		t.Fatal("known zero early resets lost")
	}
	if snap.Quota7Day == nil || snap.Quota7Day.UsagePercentage == nil || *snap.Quota7Day.UsagePercentage != 0 || snap.Quota7Day.UsedFlows != nil || snap.Quota7Day.ResetsAt != nil {
		t.Fatal("zero, missing, and null were conflated")
	}
	if snap.QuotaMonthly == nil || snap.QuotaMonthly.MaxFlows == nil || *snap.QuotaMonthly.MaxFlows != 34560 {
		t.Fatal("monthly limit missing")
	}
}

func TestZenmuxFetchRejectsFailureAndDoesNotEchoResponse(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
	}{
		{http.StatusUnauthorized, `{"error":"synthetic-test-value"}`},
		{http.StatusUnprocessableEntity, `{"error":"synthetic-test-value"}`},
		{http.StatusOK, `{"success":false,"error":"synthetic-test-value"}`},
		{http.StatusOK, `{"success":true,"data":null}`},
		{http.StatusOK, `{"success":true,"data":{}}`},
	} {
		t.Run(fmt.Sprint(tc.status, len(tc.body)), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); fmt.Fprint(w, tc.body) }))
			defer server.Close()
			_, err := NewZenmuxClient("synthetic-test-value", withZenmuxBaseURL(server.URL)).FetchSnapshot(context.Background())
			if err == nil {
				t.Fatal("expected error")
			}
			if strings.Contains(err.Error(), "synthetic-test-value") {
				t.Fatal("sensitive response leaked")
			}
		})
	}
}

func TestZenmuxNeverFollowsRedirect(t *testing.T) {
	received := false
	sink := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { received = true }))
	defer sink.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, sink.URL, http.StatusFound) }))
	defer source.Close()
	_, err := NewZenmuxClient("synthetic-test-value", withZenmuxBaseURL(source.URL)).FetchSnapshot(context.Background())
	if err == nil || received {
		t.Fatal("redirect was followed")
	}
}

func TestZenmuxRejectsCredentialEchoInSuccessfulResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"success":true,"data":{"account_status":"syntheti\u0063-test-value","quota_monthly":{}}}`)
	}))
	defer server.Close()
	_, err := NewZenmuxClient("synthetic-test-value", withZenmuxBaseURL(server.URL)).FetchSnapshot(context.Background())
	if err == nil || strings.Contains(err.Error(), "synthetic-test-value") {
		t.Fatal("credential echo must not be persisted or logged")
	}
}

func TestZenmuxPlanMissingTierStaysUnknown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"success":true,"data":{"plan":{"amount_usd":0}}}`)
	}))
	defer server.Close()
	snap, err := NewZenmuxClient("synthetic-test-value", withZenmuxBaseURL(server.URL)).FetchSnapshot(context.Background())
	if err != nil || snap.Plan == nil || snap.Plan.Tier != nil || snap.Plan.AmountUSD == nil || *snap.Plan.AmountUSD != 0 {
		t.Fatal("missing tier or known zero was lost")
	}
}
