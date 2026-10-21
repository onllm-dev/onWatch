package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

func seedCodexResetCredits(t *testing.T, s *store.Store, now time.Time) {
	t.Helper()
	if _, err := s.InsertCodexSnapshot(&api.CodexSnapshot{
		CapturedAt: now,
		AccountID:  DefaultCodexAccountID,
		PlanType:   "plus",
		Quotas: []api.CodexQuota{
			{Name: "five_hour", Utilization: 20, Status: "healthy"},
		},
		ResetCreditsAvailableCount: api.CodexOptionalInt64{Value: 2, Valid: true},
	}); err != nil {
		t.Fatalf("InsertCodexSnapshot: %v", err)
	}

	sooner := now.Add(2 * time.Hour)
	later := now.Add(48 * time.Hour)
	credits := []store.CodexResetCreditRecord{
		{CreditID: "credit-later", Status: "available", ResetType: "codex_rate_limits", Title: "Full reset", ExpiresAt: &later},
		{CreditID: "credit-never", Status: "available", ResetType: "codex_rate_limits"},
		{CreditID: "credit-sooner", Status: "available", ResetType: "codex_rate_limits", Title: "Full reset", ExpiresAt: &sooner},
		{CreditID: "credit-redeemed", Status: "redeemed", ResetType: "codex_rate_limits"},
		{CreditID: "credit-unsupported", Status: "available", ResetType: "codex_rate_limits", UnsupportedByPlan: true, ExpiresAt: &sooner},
		{CreditID: "credit-unknown-expiry", Status: "available", ResetType: "codex_rate_limits", ExpiryUnknown: true},
	}
	if err := s.SyncCodexResetCredits(DefaultCodexAccountID, credits, now); err != nil {
		t.Fatalf("SyncCodexResetCredits: %v", err)
	}
	count := int64(4)
	applicable := int64(3)
	if err := s.SetCodexResetCreditCheckStatus(DefaultCodexAccountID, "ok", &count, &applicable, now); err != nil {
		t.Fatalf("SetCodexResetCreditCheckStatus: %v", err)
	}
}

func TestHandler_CodexUsage_ResetCreditsShape(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	seedCodexResetCredits(t, s, now)

	h := NewHandler(s, nil, nil, nil, createTestConfigWithCodex())
	req := httptest.NewRequest(http.MethodGet, "/api/codex/usage?account=1", nil)
	rr := httptest.NewRecorder()
	h.CodexUsage(rr, req)

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("parse JSON: %v", err)
	}

	// Existing fields must be unchanged.
	if _, ok := response["quotas"]; !ok {
		t.Fatal("existing quotas field disappeared")
	}
	if response["accountId"] != float64(DefaultCodexAccountID) {
		t.Fatalf("accountId = %v", response["accountId"])
	}

	rc, ok := response["resetCredits"].(map[string]interface{})
	if !ok {
		t.Fatalf("resetCredits = %T, want object", response["resetCredits"])
	}
	if rc["status"] != "ok" {
		t.Fatalf("status = %v, want ok", rc["status"])
	}
	if rc["available_count"] != float64(4) {
		t.Fatalf("available_count = %v, want 4", rc["available_count"])
	}
	if rc["applicable_count"] != float64(3) {
		t.Fatalf("applicable_count = %v, want 3", rc["applicable_count"])
	}
	if rc["last_checked_at"] != now.Format(time.RFC3339) {
		t.Fatalf("last_checked_at = %v", rc["last_checked_at"])
	}
	if rc["next_expires_at"] != now.Add(2*time.Hour).Format(time.RFC3339) {
		t.Fatalf("next_expires_at = %v", rc["next_expires_at"])
	}

	credits, ok := rc["credits"].([]interface{})
	if !ok {
		t.Fatalf("credits = %T, want array", rc["credits"])
	}
	// Available, plan-usable credits first (expiry ascending, no-expiry last),
	// then the unsupported ones the tooltip accounts for separately.
	if len(credits) != 5 {
		t.Fatalf("credits len = %d, want 5 (redeemed excluded)", len(credits))
	}
	wantOrder := []string{"credit-sooner", "credit-later", "credit-never", "credit-unknown-expiry", "credit-unsupported"}
	for i, want := range wantOrder {
		credit := credits[i].(map[string]interface{})
		if credit["id"] != want {
			t.Fatalf("credits[%d].id = %v, want %v", i, credit["id"], want)
		}
	}
	for _, entry := range credits {
		credit := entry.(map[string]interface{})
		id := credit["id"]
		if (id == "credit-never" || id == "credit-unknown-expiry") && credit["expires_at"] != nil {
			t.Fatalf("%v expires_at = %v, want null", id, credit["expires_at"])
		}
		if id == "credit-unsupported" && credit["unsupported_by_plan"] != true {
			t.Fatalf("credit-unsupported flag = %v, want true", credit["unsupported_by_plan"])
		}
	}
}

func TestHandler_CodexUsage_ResetCreditsUnavailable(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	if _, err := s.InsertCodexSnapshot(&api.CodexSnapshot{
		CapturedAt: now,
		AccountID:  DefaultCodexAccountID,
		PlanType:   "plus",
		Quotas:     []api.CodexQuota{{Name: "five_hour", Utilization: 20, Status: "healthy"}},
	}); err != nil {
		t.Fatalf("InsertCodexSnapshot: %v", err)
	}
	if err := s.SetCodexResetCreditCheckStatus(DefaultCodexAccountID, "unavailable", nil, nil, now); err != nil {
		t.Fatalf("SetCodexResetCreditCheckStatus: %v", err)
	}

	h := NewHandler(s, nil, nil, nil, createTestConfigWithCodex())
	req := httptest.NewRequest(http.MethodGet, "/api/codex/usage?account=1", nil)
	rr := httptest.NewRecorder()
	h.CodexUsage(rr, req)

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("parse JSON: %v", err)
	}
	rc := response["resetCredits"].(map[string]interface{})
	if rc["status"] != "unavailable" {
		t.Fatalf("status = %v, want unavailable", rc["status"])
	}
	// The usage count is still known even when details are unavailable.
	if rc["available_count"] != nil {
		t.Fatalf("available_count = %v, want null (no usage count reported)", rc["available_count"])
	}
	if credits, _ := rc["credits"].([]interface{}); len(credits) != 0 {
		t.Fatalf("credits = %v, want empty", credits)
	}
}

func TestHandler_CodexUsage_ResetCreditsUnknownWithoutCheck(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	if _, err := s.InsertCodexSnapshot(&api.CodexSnapshot{
		CapturedAt:                 now,
		AccountID:                  DefaultCodexAccountID,
		PlanType:                   "plus",
		Quotas:                     []api.CodexQuota{{Name: "five_hour", Utilization: 20, Status: "healthy"}},
		ResetCreditsAvailableCount: api.CodexOptionalInt64{Value: 2, Valid: true},
	}); err != nil {
		t.Fatalf("InsertCodexSnapshot: %v", err)
	}

	h := NewHandler(s, nil, nil, nil, createTestConfigWithCodex())
	req := httptest.NewRequest(http.MethodGet, "/api/codex/usage?account=1", nil)
	rr := httptest.NewRecorder()
	h.CodexUsage(rr, req)

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("parse JSON: %v", err)
	}
	rc := response["resetCredits"].(map[string]interface{})
	if rc["status"] != "unknown" {
		t.Fatalf("status = %v, want unknown before any detail check", rc["status"])
	}
	if rc["available_count"] != float64(2) {
		t.Fatalf("available_count = %v, want the usage count 2", rc["available_count"])
	}
}

func TestHandler_CodexUsage_ResetCreditsAbsentCountIsNull(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	if _, err := s.InsertCodexSnapshot(&api.CodexSnapshot{
		CapturedAt: now,
		AccountID:  DefaultCodexAccountID,
		PlanType:   "plus",
		Quotas:     []api.CodexQuota{{Name: "five_hour", Utilization: 20, Status: "healthy"}},
	}); err != nil {
		t.Fatalf("InsertCodexSnapshot: %v", err)
	}

	h := NewHandler(s, nil, nil, nil, createTestConfigWithCodex())
	req := httptest.NewRequest(http.MethodGet, "/api/codex/usage?account=1", nil)
	rr := httptest.NewRecorder()
	h.CodexUsage(rr, req)

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("parse JSON: %v", err)
	}
	rc := response["resetCredits"].(map[string]interface{})
	if rc["available_count"] != nil {
		t.Fatalf("available_count = %v, want null for an unknown count", rc["available_count"])
	}
}

func TestHandler_CodexUsage_ResetCreditsFallsBackToSnapshotCountWhenCheckCountNil(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	if _, err := s.InsertCodexSnapshot(&api.CodexSnapshot{
		CapturedAt:                 now,
		AccountID:                  DefaultCodexAccountID,
		PlanType:                   "plus",
		Quotas:                     []api.CodexQuota{{Name: "five_hour", Utilization: 20, Status: "healthy"}},
		ResetCreditsAvailableCount: api.CodexOptionalInt64{Value: 2, Valid: true},
	}); err != nil {
		t.Fatalf("InsertCodexSnapshot: %v", err)
	}
	// A successful detail fetch that omitted available_count: the check row is
	// "ok" with a NULL count. That must not override the snapshot's 2 with 0.
	if err := s.SetCodexResetCreditCheckStatus(DefaultCodexAccountID, "ok", nil, nil, now); err != nil {
		t.Fatalf("SetCodexResetCreditCheckStatus: %v", err)
	}

	h := NewHandler(s, nil, nil, nil, createTestConfigWithCodex())
	req := httptest.NewRequest(http.MethodGet, "/api/codex/usage?account=1", nil)
	rr := httptest.NewRecorder()
	h.CodexUsage(rr, req)

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("parse JSON: %v", err)
	}
	rc := response["resetCredits"].(map[string]interface{})
	if rc["available_count"] != float64(2) {
		t.Fatalf("available_count = %v, want the snapshot fallback 2 (not 0/unknown)", rc["available_count"])
	}
	if rc["status"] != "ok" {
		t.Fatalf("status = %v, want ok", rc["status"])
	}
}

func TestHandler_CodexUsage_ResetCreditsApplicableCountBackfillsFromSnapshot(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	if _, err := s.InsertCodexSnapshot(&api.CodexSnapshot{
		CapturedAt:                  now,
		AccountID:                   DefaultCodexAccountID,
		PlanType:                    "plus",
		Quotas:                      []api.CodexQuota{{Name: "five_hour", Utilization: 20, Status: "healthy"}},
		ResetCreditsAvailableCount:  api.CodexOptionalInt64{Value: 3, Valid: true},
		ResetCreditsApplicableCount: api.CodexOptionalInt64{Value: 1, Valid: true},
	}); err != nil {
		t.Fatalf("InsertCodexSnapshot: %v", err)
	}

	h := NewHandler(s, nil, nil, nil, createTestConfigWithCodex())
	req := httptest.NewRequest(http.MethodGet, "/api/codex/usage?account=1", nil)
	rr := httptest.NewRecorder()
	h.CodexUsage(rr, req)

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("parse JSON: %v", err)
	}
	rc := response["resetCredits"].(map[string]interface{})
	if rc["applicable_count"] != float64(1) {
		t.Fatalf("applicable_count = %v, want the snapshot's 1", rc["applicable_count"])
	}
}

func TestHandler_CodexUsage_NextExpiryIgnoresUnsupportedCredits(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	unsupportedExpiry := now.Add(1 * time.Hour)
	usableExpiry := now.Add(5 * time.Hour)
	if _, err := s.InsertCodexSnapshot(&api.CodexSnapshot{
		CapturedAt: now,
		AccountID:  DefaultCodexAccountID,
		PlanType:   "plus",
		Quotas:     []api.CodexQuota{{Name: "five_hour", Utilization: 20, Status: "healthy"}},
	}); err != nil {
		t.Fatalf("InsertCodexSnapshot: %v", err)
	}
	if err := s.SyncCodexResetCredits(DefaultCodexAccountID, []store.CodexResetCreditRecord{
		{CreditID: "unsupported", Status: "available", ExpiresAt: &unsupportedExpiry, UnsupportedByPlan: true},
		{CreditID: "usable", Status: "available", ExpiresAt: &usableExpiry},
	}, now); err != nil {
		t.Fatalf("SyncCodexResetCredits: %v", err)
	}

	h := NewHandler(s, nil, nil, nil, createTestConfigWithCodex())
	req := httptest.NewRequest(http.MethodGet, "/api/codex/usage?account=1", nil)
	rr := httptest.NewRecorder()
	h.CodexUsage(rr, req)

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("parse JSON: %v", err)
	}
	rc := response["resetCredits"].(map[string]interface{})
	if rc["next_expires_at"] != usableExpiry.UTC().Format(time.RFC3339) {
		t.Fatalf("next_expires_at = %v, want the usable credit's expiry %v", rc["next_expires_at"], usableExpiry)
	}
}
