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

// readStaticFile reads an embedded dashboard asset for static wiring checks.
// Templates and static assets live in two separate embed.FS values.
func readStaticFile(t *testing.T, name string) string {
	t.Helper()
	fsys := staticFS
	if strings.HasPrefix(name, "templates/") {
		fsys = templatesFS
	}
	data, err := fsys.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

func createTestConfigWithCommandCode() *config.Config {
	return &config.Config{
		CommandCodeAPIKey:  "user_test_key",
		CommandCodeEnabled: true,
		PollInterval:       60 * time.Second,
		Port:               9213,
		AdminUser:          "admin",
		AdminPass:          "test",
		DBPath:             "./test.db",
	}
}

func insertTestCommandCodeSnapshot(t *testing.T, s *store.Store, capturedAt time.Time) {
	t.Helper()
	fiveHourReset := capturedAt.Add(4 * time.Hour)
	weeklyReset := capturedAt.Add(2 * 24 * time.Hour)
	periodEnd := capturedAt.Add(25 * 24 * time.Hour)
	snap := &api.CommandCodeSnapshot{
		CapturedAt:       capturedAt,
		AccountName:      "prakersh",
		AccountID:        "u_1",
		Plan:             "individual-goat",
		Status:           "active",
		MonthlyCredits:   50.99,
		RemainingCredits: 50.99,
		PeriodEnd:        &periodEnd,
		PeriodCostUSD:    18.99,
		PeriodReqs:       6190,
		PeriodTokens:     1_100_000_000,
		Quotas: []api.CommandCodeQuota{
			{Name: api.CommandCodeQuotaFiveHour, Used: 0.93, Limit: 14, Utilization: 6.64, Format: api.CommandCodeQuotaFormatCredits, ResetsAt: &fiveHourReset},
			{Name: api.CommandCodeQuotaWeekly, Used: 19.01, Limit: 35, Utilization: 54.31, Format: api.CommandCodeQuotaFormatCredits, ResetsAt: &weeklyReset},
			{Name: api.CommandCodeQuotaMonthly, Used: 18.99, Limit: 69.98, Utilization: 27.14, Format: api.CommandCodeQuotaFormatCurrency, Remaining: 50.99, ResetsAt: &periodEnd},
		},
	}
	if _, err := s.InsertCommandCodeSnapshot(snap); err != nil {
		t.Fatalf("insert test Command Code snapshot: %v", err)
	}
}

func commandCodeTestQuotas(t *testing.T, payload map[string]interface{}) map[string]map[string]interface{} {
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

func TestBuildCommandCodeCurrentUsesLatestSnapshot(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	insertTestCommandCodeSnapshot(t, s, now)

	h := NewHandler(s, nil, nil, nil, createTestConfigWithCommandCode())
	current := h.buildCommandCodeCurrent()

	if current["accountName"] != "prakersh" {
		t.Errorf("accountName = %v", current["accountName"])
	}
	if current["plan"] != "Individual Goat" {
		t.Errorf("plan = %v, want a display label", current["plan"])
	}
	if current["subscriptionStatus"] != "active" {
		t.Errorf("status = %v", current["subscriptionStatus"])
	}
	if current["remainingCredits"] != 50.99 {
		t.Errorf("remainingCredits = %v", current["remainingCredits"])
	}
	if current["periodRequests"] != int64(6190) {
		t.Errorf("periodRequests = %v", current["periodRequests"])
	}

	quotas := commandCodeTestQuotas(t, current)
	if len(quotas) != 3 {
		t.Fatalf("quotas = %d, want five_hour, weekly, monthly", len(quotas))
	}
	monthly := quotas[api.CommandCodeQuotaMonthly]
	if monthly == nil {
		t.Fatal("monthly quota missing")
	}
	if monthly["format"] != string(api.CommandCodeQuotaFormatCurrency) {
		t.Errorf("monthly format = %v", monthly["format"])
	}
	if monthly["remaining"] != 50.99 {
		t.Errorf("monthly remaining = %v", monthly["remaining"])
	}
	fiveHour := quotas[api.CommandCodeQuotaFiveHour]
	if fiveHour["format"] != string(api.CommandCodeQuotaFormatCredits) {
		t.Errorf("five hour format = %v", fiveHour["format"])
	}
}

func TestBuildCommandCodeCurrentEmptyStore(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	h := NewHandler(s, nil, nil, nil, createTestConfigWithCommandCode())
	current := h.buildCommandCodeCurrent()
	if _, ok := current["quotas"].([]interface{}); !ok {
		t.Fatalf("quotas = %T, want an empty slice so the UI renders its placeholder", current["quotas"])
	}
}

func TestBuildCommandCodeCurrentNilStore(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, createTestConfigWithCommandCode())
	current := h.buildCommandCodeCurrent()
	if _, ok := current["quotas"].([]interface{}); !ok {
		t.Fatalf("quotas = %T, want an empty slice", current["quotas"])
	}
}

func TestBuildCommandCodeCurrentMonthlyUnknownLimit(t *testing.T) {
	// A snapshot captured before the first usage summary has no derivable
	// grant; the card must say so instead of reporting a 0% bar.
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	reset := now.Add(4 * time.Hour)
	if _, err := s.InsertCommandCodeSnapshot(&api.CommandCodeSnapshot{
		CapturedAt:       now,
		RemainingCredits: 12,
		Quotas: []api.CommandCodeQuota{
			{Name: api.CommandCodeQuotaMonthly, Remaining: 12, Format: api.CommandCodeQuotaFormatCurrency, ResetsAt: &reset},
			{Name: api.CommandCodeQuotaFiveHour, Used: 1, Limit: 14, Utilization: 7, Format: api.CommandCodeQuotaFormatCredits, ResetsAt: &reset},
		},
	}); err != nil {
		t.Fatal(err)
	}

	h := NewHandler(s, nil, nil, nil, createTestConfigWithCommandCode())
	current := h.buildCommandCodeCurrent()
	quotas := commandCodeTestQuotas(t, current)
	monthly := quotas[api.CommandCodeQuotaMonthly]
	if monthly == nil {
		t.Fatal("monthly quota missing")
	}
	if monthly["limitUnknown"] != true {
		t.Errorf("limitUnknown = %v, want true with no derivable grant", monthly["limitUnknown"])
	}
	if monthly["limit"] != float64(0) {
		t.Errorf("limit = %v, want 0", monthly["limit"])
	}
	// The Ollama unknown-cap shape: utilization 0 and a healthy status, so the
	// menubar shows the dollar balance only and fires no threshold alert.
	if monthly["utilization"] != float64(0) {
		t.Errorf("utilization = %v, want 0 for an unknown cap", monthly["utilization"])
	}
	if monthly["status"] != "healthy" {
		t.Errorf("status = %v, want healthy for an unknown cap", monthly["status"])
	}
	if monthly["remaining"] != float64(12) {
		t.Errorf("remaining = %v, want the fallback balance", monthly["remaining"])
	}
}

func TestCurrentCommandCodeHandlerResponds(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()
	insertTestCommandCodeSnapshot(t, s, time.Now().UTC())

	h := NewHandler(s, nil, nil, nil, createTestConfigWithCommandCode())
	rec := httptest.NewRecorder()
	h.currentCommandCode(rec, httptest.NewRequest(http.MethodGet, "/api/current?provider=commandcode", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := body["quotas"]; !ok {
		t.Fatalf("body has no quotas: %v", body)
	}
}

func TestHistoryCommandCodeHandler(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		insertTestCommandCodeSnapshot(t, s, now.Add(-time.Duration(i)*time.Minute))
	}

	h := NewHandler(s, nil, nil, nil, createTestConfigWithCommandCode())
	rec := httptest.NewRecorder()
	h.historyCommandCode(rec, httptest.NewRequest(http.MethodGet, "/api/history?provider=commandcode&range=24h", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var rows []map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	if _, ok := rows[0]["quotas"].([]interface{}); !ok {
		t.Fatalf("row has no quotas: %v", rows[0])
	}
}

func TestHistoryCommandCodeNilStore(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, createTestConfigWithCommandCode())
	rec := httptest.NewRecorder()
	h.historyCommandCode(rec, httptest.NewRequest(http.MethodGet, "/api/history?provider=commandcode", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestCyclesCommandCodeHandler(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	// The completed cycle goes first: there is only ever one open cycle per
	// quota, and closing by `cycle_end IS NULL` would close both if the
	// active one were created earlier.
	if _, err := s.CreateCommandCodeCycle(api.CommandCodeQuotaFiveHour, now.Add(-time.Hour), nil); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseCommandCodeCycle(api.CommandCodeQuotaFiveHour, now.Add(-30*time.Minute), 40, 12); err != nil {
		t.Fatal(err)
	}
	reset := now.Add(4 * time.Hour)
	if _, err := s.CreateCommandCodeCycle(api.CommandCodeQuotaFiveHour, now, &reset); err != nil {
		t.Fatal(err)
	}

	h := NewHandler(s, nil, nil, nil, createTestConfigWithCommandCode())
	rec := httptest.NewRecorder()
	h.cyclesCommandCode(rec, httptest.NewRequest(http.MethodGet, "/api/cycles?provider=commandcode&type=five_hour", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var cycles []map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &cycles); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(cycles) != 2 {
		t.Fatalf("cycles = %d, want active + history", len(cycles))
	}
	if cycles[0]["isActive"] != true {
		t.Errorf("first cycle must be the active one: %v", cycles[0])
	}
	if cycles[0]["quotaName"] != api.CommandCodeQuotaFiveHour {
		t.Errorf("active quota = %v", cycles[0]["quotaName"])
	}
	if cycles[1]["peakUtilization"] != float64(40) {
		t.Errorf("history peak = %v, want 40", cycles[1]["peakUtilization"])
	}
	if cycles[1]["isActive"] != false {
		t.Errorf("second cycle must be closed: %v", cycles[1])
	}
}

func TestCycleOverviewCommandCodeHandler(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	if _, err := s.CreateCommandCodeCycle(api.CommandCodeQuotaFiveHour, now.Add(-2*time.Hour), nil); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseCommandCodeCycle(api.CommandCodeQuotaFiveHour, now.Add(-time.Hour), 30, 30); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertCommandCodeSnapshot(&api.CommandCodeSnapshot{
		CapturedAt: now.Add(-90 * time.Minute),
		Quotas: []api.CommandCodeQuota{
			{Name: api.CommandCodeQuotaFiveHour, Used: 4, Limit: 14, Utilization: 30, Format: api.CommandCodeQuotaFormatCredits},
		},
	}); err != nil {
		t.Fatal(err)
	}

	h := NewHandler(s, nil, nil, nil, createTestConfigWithCommandCode())
	rec := httptest.NewRecorder()
	h.cycleOverviewCommandCode(rec, httptest.NewRequest(http.MethodGet, "/api/cycle-overview?provider=commandcode&groupBy=five_hour", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body struct {
		Provider   string                   `json:"provider"`
		GroupBy    string                   `json:"groupBy"`
		QuotaNames []string                 `json:"quotaNames"`
		Cycles     []map[string]interface{} `json:"cycles"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Provider != "commandcode" || body.GroupBy != api.CommandCodeQuotaFiveHour {
		t.Errorf("provider/groupBy = %q/%q", body.Provider, body.GroupBy)
	}
	if len(body.QuotaNames) != 3 {
		t.Errorf("quotaNames = %v", body.QuotaNames)
	}
	if len(body.Cycles) != 1 {
		t.Fatalf("cycles = %d, want 1", len(body.Cycles))
	}
	if body.Cycles[0]["peakValue"] != float64(30) {
		t.Errorf("peak = %v, want 30", body.Cycles[0]["peakValue"])
	}
	if body.Cycles[0]["quotaType"] != api.CommandCodeQuotaFiveHour {
		t.Errorf("quotaType = %v", body.Cycles[0]["quotaType"])
	}
}

func TestSummaryCommandCodeHandler(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	reset := now.Add(4 * time.Hour)
	if _, err := s.CreateCommandCodeCycle(api.CommandCodeQuotaFiveHour, now, &reset); err != nil {
		t.Fatal(err)
	}
	insertTestCommandCodeSnapshot(t, s, now)

	h := NewHandler(s, nil, nil, nil, createTestConfigWithCommandCode())
	h.SetCommandCodeTracker(tracker.NewCommandCodeTracker(s, nil))
	rec := httptest.NewRecorder()
	h.summaryCommandCode(rec, httptest.NewRequest(http.MethodGet, "/api/summary?provider=commandcode", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body map[string]map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	entry, ok := body[api.CommandCodeQuotaFiveHour]
	if !ok {
		t.Fatalf("summary missing five_hour: %v", body)
	}
	if _, ok := entry["currentUtil"]; !ok {
		t.Errorf("entry has no currentUtil: %v", entry)
	}
}

func TestBuildCommandCodeInsights(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	insertTestCommandCodeSnapshot(t, s, now)
	h := NewHandler(s, nil, nil, nil, createTestConfigWithCommandCode())
	h.SetCommandCodeTracker(tracker.NewCommandCodeTracker(s, nil))

	resp := h.buildCommandCodeInsights(map[string]bool{}, time.Hour)
	labels := map[string]string{}
	for _, stat := range resp.Stats {
		labels[stat.Label] = stat.Value
	}
	if labels["Plan"] != "Individual Goat (active)" {
		t.Errorf("plan stat = %q", labels["Plan"])
	}
	if labels["Account"] != "prakersh" {
		t.Errorf("account stat = %q", labels["Account"])
	}
	if !strings.HasPrefix(labels["Credits"], "$50.99") {
		t.Errorf("credits stat = %q", labels["Credits"])
	}
	if labels["Requests"] != "6,190" {
		t.Errorf("requests stat = %q", labels["Requests"])
	}
	if labels["Renews"] == "" {
		t.Error("renews stat missing")
	}
}

func TestBuildCommandCodeInsightsHonoursHiddenKeys(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	insertTestCommandCodeSnapshot(t, s, time.Now().UTC())
	h := NewHandler(s, nil, nil, nil, createTestConfigWithCommandCode())

	hidden := map[string]bool{"forecast_five_hour": true}
	resp := h.buildCommandCodeInsights(hidden, time.Hour)
	for _, stat := range resp.Stats {
		if stat.Key == "forecast_five_hour" {
			t.Fatal("hidden insight key was still emitted")
		}
	}
}

func TestBuildCommandCodeInsightsEmptyStore(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	h := NewHandler(s, nil, nil, nil, createTestConfigWithCommandCode())
	resp := h.buildCommandCodeInsights(map[string]bool{}, time.Hour)
	if len(resp.Stats) != 0 || len(resp.Insights) != 0 {
		t.Fatalf("expected empty insights, got %+v", resp)
	}
}

func TestLoggingHistoryCommandCodeHandler(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	for i := 0; i < 2; i++ {
		insertTestCommandCodeSnapshot(t, s, now.Add(-time.Duration(i)*time.Minute))
	}

	h := NewHandler(s, nil, nil, nil, createTestConfigWithCommandCode())
	rec := httptest.NewRecorder()
	h.loggingHistoryCommandCode(rec, httptest.NewRequest(http.MethodGet, "/api/logging-history?provider=commandcode&range=1&limit=10", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body struct {
		Provider   string   `json:"provider"`
		QuotaNames []string `json:"quotaNames"`
		Logs       []any    `json:"logs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Provider != "commandcode" {
		t.Errorf("provider = %q", body.Provider)
	}
	if len(body.QuotaNames) != 3 || body.QuotaNames[0] != api.CommandCodeQuotaFiveHour {
		t.Errorf("quotaNames = %v, want five_hour first", body.QuotaNames)
	}
	if len(body.Logs) != 2 {
		t.Errorf("logs = %d, want 2", len(body.Logs))
	}
}

func TestProviderCatalogIncludesCommandCode(t *testing.T) {
	var found bool
	for _, item := range providerCatalog() {
		if item.Key == "commandcode" {
			found = true
			if item.Name != "Command Code" {
				t.Errorf("name = %q", item.Name)
			}
			if !item.AutoDetectable {
				t.Error("Command Code is auto-detected from local credentials")
			}
		}
	}
	if !found {
		t.Fatal("commandcode missing from the provider catalog")
	}
}

func TestDefaultProviderTabLabelCommandCode(t *testing.T) {
	if got := defaultProviderTabLabel("commandcode"); got != "Command Code" {
		t.Fatalf("label = %q", got)
	}
}

func TestIsProviderConfiguredCommandCode(t *testing.T) {
	cases := []struct {
		name string
		cfg  *config.Config
		want bool
	}{
		{"key present", &config.Config{CommandCodeAPIKey: "user_x"}, true},
		{"enabled without key", &config.Config{CommandCodeEnabled: true}, true},
		{"disabled wins over key", &config.Config{CommandCodeAPIKey: "user_x", CommandCodeDisabled: true}, false},
		{"nothing", &config.Config{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHandler(nil, nil, nil, nil, tc.cfg)
			// Point detection at an empty home so the developer's real auth
			// files cannot make the "nothing" case pass by accident.
			t.Setenv("HOME", t.TempDir())
			t.Setenv("COMMAND_CODE_API_KEY", "")
			t.Setenv("COMMANDCODE_API_KEY", "")
			t.Setenv("COMMANDCODE_AUTH_PATH", "/nonexistent/auth.json")
			if got := h.isProviderConfigured("commandcode"); got != tc.want {
				t.Fatalf("configured = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseProviderHistoryRange(t *testing.T) {
	now := time.Now().UTC()
	start, end := parseProviderHistoryRange("24h")
	if end.Before(now.Add(-time.Minute)) {
		t.Errorf("end = %v, want roughly now", end)
	}
	if diff := end.Sub(start) - 24*time.Hour; diff > time.Minute || diff < -time.Minute {
		t.Errorf("window = %v, want 24h", end.Sub(start))
	}
	start, end = parseProviderHistoryRange("")
	if diff := end.Sub(start) - 7*24*time.Hour; diff > time.Minute || diff < -time.Minute {
		t.Errorf("default window = %v, want 7d", end.Sub(start))
	}
	start, end = parseProviderHistoryRange("nonsense")
	if diff := end.Sub(start) - 7*24*time.Hour; diff > time.Minute || diff < -time.Minute {
		t.Errorf("unknown range window = %v, want the 7d default", end.Sub(start))
	}
}

func TestFormatCount(t *testing.T) {
	cases := map[int64]string{
		0:          "0",
		7:          "7",
		999:        "999",
		1000:       "1,000",
		6190:       "6,190",
		1234567:    "1,234,567",
		1000000000: "1,000,000,000",
	}
	for in, want := range cases {
		if got := formatCount(in); got != want {
			t.Errorf("formatCount(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatTokenCount(t *testing.T) {
	cases := map[int64]string{
		0:             "0",
		999:           "999",
		1500:          "1.5k",
		1_000_000:     "1.0M",
		1_100_000_000: "1.1B",
	}
	for in, want := range cases {
		if got := formatTokenCount(in); got != want {
			t.Errorf("formatTokenCount(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestCommandCodeStaticAssetsWired(t *testing.T) {
	// The dashboard reads the grid by element id, so a rename in the template
	// without a matching JS change would silently blank the tab.
	appJS := readStaticAppJS(t)
	dashboardHTML := readStaticFile(t, "templates/dashboard.html")
	menubarHTML := readStaticFile(t, "static/menubar.html")

	for _, needle := range []string{
		"quota-grid-commandcode",
		"function commandCodeQuotaSetsMatch(container, quotas)",
		"function commandCodeCardLabel(quota)",
		"function renderCommandCodeQuotaCards(quotas, containerId)",
		"function updateCommandCodeCard(quota)",
		"} else if (provider === 'commandcode') {",
		"toFixed(2)",
	} {
		if !strings.Contains(appJS, needle) {
			t.Errorf("app.js is missing %q", needle)
		}
	}
	updateIdx := strings.Index(appJS, "} else if (provider === 'commandcode') {")
	if updateIdx == -1 {
		t.Fatal("commandcode branch missing from the current-provider dispatch")
	}
	updateEnd := strings.Index(appJS[updateIdx:], "} else if (provider === 'zai')")
	if updateEnd == -1 {
		t.Fatal("commandcode branch is not followed by the zai branch; the dispatch order changed")
	}
	updateBody := appJS[updateIdx : updateIdx+updateEnd]
	if !strings.Contains(updateBody, "!commandCodeQuotaSetsMatch(container, data.quotas)") {
		t.Error("commandcode branch must gate rendering on the quota-set match check")
	}
	if !strings.Contains(dashboardHTML, "quota-grid-commandcode") {
		t.Error("dashboard.html is missing the commandcode grid")
	}
	if !strings.Contains(menubarHTML, "provider-icon-commandcode") {
		t.Error("menubar.html is missing the commandcode icon class")
	}
	// The menubar quota line must render credit windows with a unit suffix:
	// "5.37/14.00 cr" grounds the arc the same way "$21.26/$69.87" does for
	// the monthly card. Without the format branch, credits fall through to the
	// integer "5/14" shape and read as counts.
	for _, needle := range []string{
		`quota.format === "credits"`,
		"function formatCredits(value)",
		"/${formatCredits(limit)} cr",
	} {
		if !strings.Contains(menubarHTML, needle) {
			t.Errorf("menubar.html is missing %q for the credits usage line", needle)
		}
	}
}
