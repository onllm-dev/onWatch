package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGeminiClient_FetchQuotas(t *testing.T) {
	quotaResp := GeminiQuotaResponse{
		Quotas: []GeminiQuotaBucket{
			{RemainingFraction: 0.993, ResetTime: "2026-03-18T10:00:00Z", ModelID: "gemini-2.5-flash"},
			{RemainingFraction: 1.0, ResetTime: "2026-03-18T10:00:00Z", ModelID: "gemini-2.5-pro"},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != geminiQuotaPath {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(quotaResp)
	}))
	defer srv.Close()

	client := NewGeminiClient("test-token", nil, WithGeminiBaseURL(srv.URL))
	resp, err := client.FetchQuotas(context.Background())
	if err != nil {
		t.Fatalf("FetchQuotas() error = %v", err)
	}

	if len(resp.Quotas) != 2 {
		t.Fatalf("expected 2 quotas, got %d", len(resp.Quotas))
	}
}

func TestGeminiClient_FetchQuotas_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	client := NewGeminiClient("bad-token", nil, WithGeminiBaseURL(srv.URL))
	_, err := client.FetchQuotas(context.Background())
	if err != ErrGeminiUnauthorized {
		t.Errorf("expected ErrGeminiUnauthorized, got %v", err)
	}
}

func TestGeminiClient_FetchTier(t *testing.T) {
	tierResp := GeminiTierResponse{
		Tier:                    "standard",
		CloudAICompanionProject: "gen-lang-client-12345",
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != geminiTierPath {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tierResp)
	}))
	defer srv.Close()

	client := NewGeminiClient("test-token", nil, WithGeminiBaseURL(srv.URL))
	resp, err := client.FetchTier(context.Background())
	if err != nil {
		t.Fatalf("FetchTier() error = %v", err)
	}

	if resp.Tier != "standard" {
		t.Errorf("expected tier 'standard', got %q", resp.Tier)
	}
	if resp.CloudAICompanionProject != "gen-lang-client-12345" {
		t.Errorf("unexpected project: %q", resp.CloudAICompanionProject)
	}
}

func TestGeminiClient_FetchQuotas_WithProjectID(t *testing.T) {
	var receivedProject string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)
		if p, ok := body["project"]; ok {
			receivedProject = p.(string)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(GeminiQuotaResponse{})
	}))
	defer srv.Close()

	client := NewGeminiClient("token", nil, WithGeminiBaseURL(srv.URL))
	client.SetProjectID("my-project")

	_, err := client.FetchQuotas(context.Background())
	if err != nil {
		t.Fatalf("FetchQuotas() error = %v", err)
	}

	if receivedProject != "my-project" {
		t.Errorf("expected project 'my-project', got %q", receivedProject)
	}
}

func TestGeminiClient_SetToken(t *testing.T) {
	client := NewGeminiClient("initial", nil)
	if got := client.getToken(); got != "initial" {
		t.Errorf("expected 'initial', got %q", got)
	}
	client.SetToken("updated")
	if got := client.getToken(); got != "updated" {
		t.Errorf("expected 'updated', got %q", got)
	}
}
