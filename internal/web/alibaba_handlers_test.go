package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

func createTestConfigWithAlibaba() *config.Config {
	return &config.Config{
		AlibabaCookies: "a=1; b=2",
		AlibabaEnabled: true,
		AlibabaRegion:  "ap-southeast-1",
		PollInterval:   60 * time.Second,
		Port:           9213,
		AdminUser:      "admin",
		AdminPass:      "test",
		DBPath:         "./test.db",
	}
}

func alibabaTestDefaultAccountID(t *testing.T, s *store.Store) int64 {
	t.Helper()
	accounts, err := s.QueryActiveProviderAccounts("alibaba")
	if err != nil || len(accounts) == 0 {
		t.Fatalf("expected seeded default alibaba account, got %v (err %v)", accounts, err)
	}
	return accounts[0].ID
}

func insertTestAlibabaSnapshot(t *testing.T, s *store.Store, accountID int64, capturedAt time.Time) {
	t.Helper()
	fiveHourReset := capturedAt.Add(4 * time.Hour)
	weeklyReset := capturedAt.Add(3 * 24 * time.Hour)
	monthlyReset := capturedAt.Add(20 * 24 * time.Hour)
	endTime := capturedAt.Add(21 * 24 * time.Hour)
	snap := &api.AlibabaSnapshot{
		CapturedAt:    capturedAt,
		SpecCode:      "lite",
		Status:        "VALID",
		RemainingDays: 21,
		AutoRenew:     false,
		EndTime:       &endTime,
		InstanceID:    "sfm_test",
		Quotas: []api.AlibabaQuota{
			{Name: api.AlibabaQuotaFiveHour, Utilization: 12.5, ResetsAt: &fiveHourReset},
			{Name: api.AlibabaQuotaWeekly, Utilization: 40, ResetsAt: &weeklyReset},
			{Name: api.AlibabaQuotaMonthly, Utilization: 7, ResetsAt: &monthlyReset},
		},
	}
	if _, err := s.InsertAlibabaSnapshot(snap, accountID); err != nil {
		t.Fatalf("insert test Alibaba snapshot: %v", err)
	}
}

func alibabaTestQuotas(t *testing.T, payload map[string]interface{}) map[string]map[string]interface{} {
	t.Helper()
	raw, ok := payload["quotas"].([]interface{})
	if !ok {
		t.Fatalf("quotas is %T, want a slice", payload["quotas"])
	}
	out := make(map[string]map[string]interface{}, len(raw))
	for _, item := range raw {
		q, ok := item.(map[string]interface{})
		if !ok {
			t.Fatalf("quota is %T, want a map", item)
		}
		name, _ := q["name"].(string)
		out[name] = q
	}
	return out
}

func TestBuildAlibabaCurrentUsesLatestSnapshot(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	accountID := alibabaTestDefaultAccountID(t, s)
	insertTestAlibabaSnapshot(t, s, accountID, time.Now().UTC())

	h := NewHandler(s, nil, nil, nil, createTestConfigWithAlibaba())
	h.SetAlibabaTracker(tracker.NewAlibabaTracker(s, nil))
	current := h.buildAlibabaCurrent(accountID)

	if current["plan"] != "lite" {
		t.Errorf("plan = %v, want lite", current["plan"])
	}
	if current["subscriptionStatus"] != "VALID" {
		t.Errorf("subscriptionStatus = %v", current["subscriptionStatus"])
	}
	if current["remainingDays"] != 21 {
		t.Errorf("remainingDays = %v, want 21", current["remainingDays"])
	}

	quotas := alibabaTestQuotas(t, current)
	if len(quotas) != 3 {
		t.Fatalf("quotas = %d, want five_hour, weekly, monthly", len(quotas))
	}
	fiveHour := quotas[api.AlibabaQuotaFiveHour]
	if fiveHour == nil {
		t.Fatal("five_hour quota missing")
	}
	if fiveHour["utilization"] != 12.5 {
		t.Errorf("five_hour utilization = %v, want 12.5", fiveHour["utilization"])
	}
	if fiveHour["displayName"] != "5-Hour" {
		t.Errorf("five_hour displayName = %v", fiveHour["displayName"])
	}
	if fiveHour["status"] != utilStatus(12.5) {
		t.Errorf("five_hour status = %v", fiveHour["status"])
	}
	if _, ok := fiveHour["resetsAt"]; !ok {
		t.Error("five_hour resetsAt missing")
	}
	if quotas[api.AlibabaQuotaWeekly]["utilization"] != 40.0 {
		t.Errorf("weekly utilization = %v", quotas[api.AlibabaQuotaWeekly]["utilization"])
	}

	order := current["quotas"].([]interface{})
	if first := order[0].(map[string]interface{})["name"]; first != api.AlibabaQuotaFiveHour {
		t.Errorf("first quota = %v, want five_hour", first)
	}
}

func TestBuildAlibabaCurrentScopedByAccount(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	accountID := alibabaTestDefaultAccountID(t, s)
	other, err := s.CreateOrRestoreProviderAccount("alibaba", "other")
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	insertTestAlibabaSnapshot(t, s, accountID, time.Now().UTC())

	h := NewHandler(s, nil, nil, nil, createTestConfigWithAlibaba())
	if quotas := h.buildAlibabaCurrent(other.ID)["quotas"].([]interface{}); len(quotas) != 0 {
		t.Fatalf("other account got %d quotas, want 0", len(quotas))
	}
	if quotas := h.buildAlibabaCurrent(accountID)["quotas"].([]interface{}); len(quotas) != 3 {
		t.Fatalf("default account got %d quotas, want 3", len(quotas))
	}
}

func TestBuildAlibabaCurrentEmpty(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	h := NewHandler(s, nil, nil, nil, createTestConfigWithAlibaba())
	response, readOK, hasData := h.buildAlibabaCurrentOK(alibabaTestDefaultAccountID(t, s))
	if !readOK || hasData {
		t.Fatalf("readOK=%v hasData=%v, want true/false", readOK, hasData)
	}
	if quotas := response["quotas"].([]interface{}); len(quotas) != 0 {
		t.Fatalf("quotas = %d, want 0", len(quotas))
	}
}

func TestAlibabaCurrentEndpointDefaultsToFirstAccount(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	insertTestAlibabaSnapshot(t, s, alibabaTestDefaultAccountID(t, s), time.Now().UTC())
	h := NewHandler(s, nil, nil, nil, createTestConfigWithAlibaba())

	for _, target := range []string{"/api/alibaba/current", "/api/current?provider=alibaba"} {
		rr := httptest.NewRecorder()
		if strings.HasPrefix(target, "/api/alibaba") {
			h.currentAlibaba(rr, httptest.NewRequest("GET", target, nil))
		} else {
			h.Current(rr, httptest.NewRequest("GET", target, nil))
		}
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: status = %d body=%s", target, rr.Code, rr.Body.String())
		}
		var body map[string]interface{}
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: decode: %v", target, err)
		}
		if quotas, _ := body["quotas"].([]interface{}); len(quotas) != 3 {
			t.Fatalf("%s: quotas = %v, want 3", target, body["quotas"])
		}
	}
}

func TestAlibabaHistoryAndCycles(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	accountID := alibabaTestDefaultAccountID(t, s)
	tr := tracker.NewAlibabaTracker(s, nil)
	now := time.Now().UTC()
	for _, offset := range []time.Duration{-30 * time.Minute, -10 * time.Minute} {
		insertTestAlibabaSnapshot(t, s, accountID, now.Add(offset))
	}
	snap, err := s.QueryLatestAlibaba(accountID)
	if err != nil || snap == nil {
		t.Fatalf("QueryLatestAlibaba: %v %v", snap, err)
	}
	if err := tr.Process(snap, accountID); err != nil {
		t.Fatalf("Process: %v", err)
	}

	h := NewHandler(s, nil, nil, nil, createTestConfigWithAlibaba())
	h.SetAlibabaTracker(tr)

	rr := httptest.NewRecorder()
	h.historyAlibaba(rr, httptest.NewRequest("GET", "/api/alibaba/history?range=1d", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("history status = %d", rr.Code)
	}
	var rows []struct {
		CapturedAt string                   `json:"capturedAt"`
		Quotas     []map[string]interface{} `json:"quotas"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &rows); err != nil {
		t.Fatalf("history decode: %v", err)
	}
	if len(rows) == 0 || len(rows[0].Quotas) != 3 {
		t.Fatalf("history rows = %+v, want rows with 3 quotas", rows)
	}

	rr = httptest.NewRecorder()
	h.cyclesAlibaba(rr, httptest.NewRequest("GET", "/api/alibaba/cycles?type=five_hour", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("cycles status = %d", rr.Code)
	}
	var cycles []map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &cycles); err != nil {
		t.Fatalf("cycles decode: %v", err)
	}
	if len(cycles) == 0 || cycles[0]["isActive"] != true {
		t.Fatalf("cycles = %v, want an active cycle first", cycles)
	}

	rr = httptest.NewRecorder()
	h.insightsAlibaba(rr, httptest.NewRequest("GET", "/api/alibaba/insights", nil), 24*time.Hour)
	if rr.Code != http.StatusOK {
		t.Fatalf("insights status = %d", rr.Code)
	}
	var insights struct {
		Stats []map[string]interface{} `json:"stats"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &insights); err != nil {
		t.Fatalf("insights decode: %v", err)
	}
	if len(insights.Stats) == 0 || insights.Stats[0]["label"] != "Plan" {
		t.Fatalf("insights stats = %v, want the Plan stat first", insights.Stats)
	}
}

func TestAlibabaAccountsCRUD(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	cfg := &config.Config{}
	h := NewHandler(s, nil, nil, nil, cfg)

	do := func(method, target, body string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		h.AlibabaAccounts(rr, httptest.NewRequest(method, target, strings.NewReader(body)))
		return rr
	}

	rr := do("POST", "/api/alibaba/accounts", `{"name":"work","cookies":"Cookie: login=abc; sid=xyz","region":"ap-southeast-1"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", rr.Code, rr.Body.String())
	}
	var created map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &created)
	id := int64(created["id"].(float64))

	if !cfg.HasProvider("alibaba") {
		t.Error("creating an account with cookies should enable the provider")
	}

	acc, err := s.GetProviderAccountByID(id)
	if err != nil || acc == nil {
		t.Fatalf("GetProviderAccountByID: %v %v", acc, err)
	}
	meta := parseAlibabaAccountMetadata(acc.Metadata)
	if meta.Cookies != "login=abc; sid=xyz" || meta.Region != "ap-southeast-1" {
		t.Fatalf("metadata = %+v, want the cookie header and region", meta)
	}

	rr = do("GET", "/api/alibaba/accounts", "")
	if strings.Contains(rr.Body.String(), "login=abc") {
		t.Fatalf("list response leaked cookies: %s", rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"hasCookies":true`) {
		t.Fatalf("list response missing hasCookies: %s", rr.Body.String())
	}

	for name, body := range map[string]string{
		"bad name":    `{"name":"bad name!","cookies":"a=1"}`,
		"bad region":  `{"name":"x","cookies":"a=1","region":"../etc"}`,
		"empty ck":    `{"name":"x","cookies":"# only a comment"}`,
		"no name":     `{"name":"","cookies":"a=1"}`,
		"bad payload": `not json`,
	} {
		if rr := do("POST", "/api/alibaba/accounts", body); rr.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, rr.Code)
		}
	}

	rr = do("PUT", "/api/alibaba/accounts?id="+formatID(id), `{"region":"cn-hangzhou"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("update status = %d body=%s", rr.Code, rr.Body.String())
	}
	acc, _ = s.GetProviderAccountByID(id)
	meta = parseAlibabaAccountMetadata(acc.Metadata)
	if meta.Region != "cn-hangzhou" || meta.Cookies != "login=abc; sid=xyz" {
		t.Fatalf("metadata after update = %+v, want region changed and cookies kept", meta)
	}

	if rr := do("DELETE", "/api/alibaba/accounts?id="+formatID(id), ""); rr.Code != http.StatusOK {
		t.Fatalf("delete status = %d", rr.Code)
	}
	if rr := do("DELETE", "/api/alibaba/accounts?id=999999", ""); rr.Code != http.StatusNotFound {
		t.Fatalf("delete unknown status = %d, want 404", rr.Code)
	}
}

func TestAlibabaAccountsUsageHidesEmptyDefault(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	h := NewHandler(s, nil, nil, nil, createTestConfigWithAlibaba())
	rr := httptest.NewRecorder()
	body := `{"name":"work","cookies":"a=1"}`
	h.AlibabaAccounts(rr, httptest.NewRequest("POST", "/api/alibaba/accounts", strings.NewReader(body)))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	h.AlibabaAccountsUsage(rr, httptest.NewRequest("GET", "/api/alibaba/accounts/usage", nil))
	var resp struct {
		Accounts []map[string]interface{} `json:"accounts"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Accounts) != 1 || resp.Accounts[0]["accountName"] != "work" {
		t.Fatalf("accounts = %v, want only the work account", resp.Accounts)
	}
}
