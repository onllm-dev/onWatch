package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

func seedAnthropicAccounts(t *testing.T) (*store.Store, int64) {
	t.Helper()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	acc, err := s.CreateOrRestoreProviderAccount("anthropic", "work")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	snap := func(util float64) *api.AnthropicSnapshot {
		return &api.AnthropicSnapshot{CapturedAt: now, RawJSON: `{}`,
			Quotas: []api.AnthropicQuota{{Name: "five_hour", Utilization: util}}}
	}
	if _, err := s.InsertAnthropicSnapshot(snap(11)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertAnthropicSnapshotForAccount(acc.ID, snap(77)); err != nil {
		t.Fatal(err)
	}
	return s, acc.ID
}

type anthCurrent struct {
	Quotas []struct {
		Name        string  `json:"name"`
		Utilization float64 `json:"utilization"`
	} `json:"quotas"`
	Accounts []struct {
		AccountID   int64  `json:"accountId"`
		AccountName string `json:"accountName"`
		Quotas      []struct {
			Utilization float64 `json:"utilization"`
		} `json:"quotas"`
	} `json:"accounts"`
}

func TestCurrentAnthropic_MultiAccount(t *testing.T) {
	s, accID := seedAnthropicAccounts(t)
	h := NewHandler(s, nil, nil, nil, createTestConfigWithAnthropic())

	rr := httptest.NewRecorder()
	h.Current(rr, httptest.NewRequest(http.MethodGet, "/api/current?provider=anthropic", nil))
	var resp anthCurrent
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Quotas) != 1 || resp.Quotas[0].Utilization != 11 {
		t.Fatalf("primary quotas = %+v", resp.Quotas)
	}
	if len(resp.Accounts) != 1 || resp.Accounts[0].AccountName != "work" || resp.Accounts[0].Quotas[0].Utilization != 77 {
		t.Fatalf("extra accounts = %+v", resp.Accounts)
	}

	rr = httptest.NewRecorder()
	h.Current(rr, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/current?provider=anthropic&account=%d", accID), nil))
	resp = anthCurrent{}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.Quotas) != 1 || resp.Quotas[0].Utilization != 77 || len(resp.Accounts) != 0 {
		t.Fatalf("account-scoped response = %+v", resp)
	}
}

func TestCurrentBoth_AnthropicAccountsAndVisibility(t *testing.T) {
	s, accID := seedAnthropicAccounts(t)
	h := NewHandler(s, nil, nil, nil, createTestConfigWithAnthropic())

	get := func() map[string]json.RawMessage {
		rr := httptest.NewRecorder()
		h.currentBoth(rr, httptest.NewRequest(http.MethodGet, "/api/current?provider=both", nil))
		var m map[string]json.RawMessage
		if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	m := get()
	if _, ok := m["anthropic"]; !ok {
		t.Fatal("primary anthropic missing from both view")
	}
	if _, ok := m["anthropicAccounts"]; !ok {
		t.Fatal("anthropicAccounts missing from both view")
	}

	// Hiding the extra account from the dashboard removes it from the payload.
	vis := fmt.Sprintf(`{"anthropic:%d":{"polling":true,"dashboard":false}}`, accID)
	if err := s.SetSetting("provider_visibility", vis); err != nil {
		t.Fatal(err)
	}
	m = get()
	if _, ok := m["anthropicAccounts"]; ok {
		t.Fatal("hidden extra account should not be returned")
	}
	if _, ok := m["anthropic"]; !ok {
		t.Fatal("primary must stay visible")
	}
}

func TestAnthropicAccountsEndpoint(t *testing.T) {
	s, accID := seedAnthropicAccounts(t)
	h := NewHandler(s, nil, nil, nil, createTestConfigWithAnthropic())
	rr := httptest.NewRecorder()
	h.AnthropicAccounts(rr, httptest.NewRequest(http.MethodGet, "/api/anthropic/accounts", nil))
	var resp struct {
		Accounts []struct {
			ID        int64  `json:"id"`
			Name      string `json:"name"`
			IsDefault bool   `json:"isDefault"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Accounts) != 2 || !resp.Accounts[0].IsDefault || resp.Accounts[0].ID != 0 ||
		resp.Accounts[1].ID != accID || resp.Accounts[1].Name != "work" {
		t.Fatalf("accounts = %+v", resp.Accounts)
	}

	rr = httptest.NewRecorder()
	h.AnthropicAccounts(rr, httptest.NewRequest(http.MethodPost, "/api/anthropic/accounts", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d", rr.Code)
	}
}

func TestHistoryAnthropic_AccountScoped(t *testing.T) {
	s, accID := seedAnthropicAccounts(t)
	h := NewHandler(s, nil, nil, nil, createTestConfigWithAnthropic())
	rr := httptest.NewRecorder()
	h.historyAnthropic(rr, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/history?provider=anthropic&range=1h&account=%d", accID), nil))
	var rows []map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0]["five_hour"].(float64) != 77 {
		t.Fatalf("history rows = %v", rows)
	}
}

func TestProviderToggle_AnthropicAccountKeyAccepted(t *testing.T) {
	s, accID := seedAnthropicAccounts(t)
	h := NewHandler(s, nil, nil, nil, createTestConfigWithAnthropic())
	body := fmt.Sprintf(`{"provider":"anthropic:%d","polling":false}`, accID)
	rr := httptest.NewRecorder()
	h.ToggleProvider(rr, httptest.NewRequest(http.MethodPost, "/api/providers/toggle", bytes.NewBufferString(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	raw, _ := s.GetSetting("provider_visibility")
	var vis map[string]map[string]bool
	if err := json.Unmarshal([]byte(raw), &vis); err != nil {
		t.Fatal(err)
	}
	if vis[fmt.Sprintf("anthropic:%d", accID)]["polling"] {
		t.Fatalf("per-account polling not saved: %s", raw)
	}
}
