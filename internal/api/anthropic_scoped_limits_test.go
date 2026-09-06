package api

import (
	"os"
	"testing"
	"time"
)

// quotaUtil returns the utilization for a key, failing if it is missing or null.
func quotaUtil(t *testing.T, resp *AnthropicQuotaResponse, key string) float64 {
	t.Helper()
	entry, ok := (*resp)[key]
	if !ok || entry == nil {
		t.Fatalf("quota %q missing from response", key)
	}
	if entry.Utilization == nil {
		t.Fatalf("quota %q has null utilization", key)
	}
	return *entry.Utilization
}

// TestParseAnthropicResponse_LiveWeeklyScoped exercises an unmodified capture of
// GET /api/oauth/usage taken on a Max account (2026-09-05). The per-model
// top-level keys are null there and the only per-model weekly data arrives as
// limits[].kind=weekly_scoped - the shape reported in issue #121.
func TestParseAnthropicResponse_LiveWeeklyScoped(t *testing.T) {
	data, err := os.ReadFile("testdata/anthropic_usage_weekly_scoped.json")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}

	resp, err := ParseAnthropicResponse(data)
	if err != nil {
		t.Fatalf("ParseAnthropicResponse() error = %v", err)
	}

	// The #82 / #84 hardening must still hold: limits[], spend and
	// member_dashboard_available cannot disturb the primary quotas.
	if got := quotaUtil(t, resp, "five_hour"); got != 32 {
		t.Errorf("five_hour utilization = %v, want 32", got)
	}
	if got := quotaUtil(t, resp, "seven_day"); got != 3 {
		t.Errorf("seven_day utilization = %v, want 3", got)
	}

	// The scoped bucket is the regression: Anthropic reports it only in
	// limits[], with seven_day_opus / seven_day_sonnet left null.
	if got := quotaUtil(t, resp, "seven_day_scoped_fable"); got != 0 {
		t.Errorf("seven_day_scoped_fable utilization = %v, want 0", got)
	}
	if entry := (*resp)["seven_day_sonnet"]; entry != nil {
		t.Errorf("seven_day_sonnet should stay null, got %+v", entry)
	}

	// session / weekly_all duplicate five_hour / seven_day and must not create
	// extra quotas.
	for _, key := range []string{"session", "weekly_all", "limits", "spend"} {
		if _, ok := (*resp)[key]; ok {
			t.Errorf("unexpected quota key %q promoted from metadata", key)
		}
	}

	// Experimental top-level keys stay filtered out of storage and the UI.
	for _, name := range resp.ActiveQuotaNames() {
		switch name {
		case "seven_day_omelette", "seven_day_cowork", "seven_day_oauth_apps",
			"nimbus_quill", "tangelo", "iguana_necktie", "omelette_promotional":
			t.Errorf("experimental key %q leaked into ActiveQuotaNames", name)
		}
	}

	snapshot := resp.ToSnapshot(time.Now().UTC())
	var found bool
	for _, q := range snapshot.Quotas {
		if q.Name == "seven_day_scoped_fable" {
			found = true
		}
	}
	if !found {
		t.Error("seven_day_scoped_fable did not reach the snapshot")
	}
}

// TestParseAnthropicResponse_ScopedLimitIsBinding uses the payload from issue
// #121, where the scoped weekly bucket (12%) is above both the 5-hour (9%) and
// the all-model weekly (7%) reading and is the only active limit.
func TestParseAnthropicResponse_ScopedLimitIsBinding(t *testing.T) {
	payload := []byte(`{
		"five_hour": {"utilization": 9, "resets_at": "2026-09-02T04:09:59Z", "is_enabled": null},
		"seven_day": {"utilization": 7, "resets_at": "2026-09-05T17:59:59Z", "is_enabled": null},
		"seven_day_opus": null,
		"seven_day_sonnet": null,
		"limits": [
			{"kind": "session", "group": "session", "percent": 9, "resets_at": "2026-09-02T04:09:59Z", "scope": null, "is_active": false},
			{"kind": "weekly_all", "group": "weekly", "percent": 7, "resets_at": "2026-09-05T17:59:59Z", "scope": null, "is_active": false},
			{"kind": "weekly_scoped", "group": "weekly", "percent": 12, "resets_at": "2026-09-05T17:59:59Z", "is_active": true,
			 "scope": {"model": {"id": null, "display_name": "Fable"}, "surface": null}}
		],
		"member_dashboard_available": false
	}`)

	resp, err := ParseAnthropicResponse(payload)
	if err != nil {
		t.Fatalf("ParseAnthropicResponse() error = %v", err)
	}

	if got := quotaUtil(t, resp, "seven_day_scoped_fable"); got != 12 {
		t.Errorf("seven_day_scoped_fable utilization = %v, want 12", got)
	}

	entry := (*resp)["seven_day_scoped_fable"]
	if entry.ResetsAt == nil || *entry.ResetsAt != "2026-09-05T17:59:59Z" {
		t.Errorf("scoped resets_at = %v, want 2026-09-05T17:59:59Z", entry.ResetsAt)
	}

	snapshot := resp.ToSnapshot(time.Now().UTC())
	var peak float64
	var peakName string
	for _, q := range snapshot.Quotas {
		if q.Utilization > peak {
			peak, peakName = q.Utilization, q.Name
		}
	}
	if peakName != "seven_day_scoped_fable" || peak != 12 {
		t.Errorf("binding quota = %s at %v, want seven_day_scoped_fable at 12", peakName, peak)
	}

	// The reset time must survive into the snapshot so cycle tracking works.
	for _, q := range snapshot.Quotas {
		if q.Name == "seven_day_scoped_fable" && q.ResetsAt == nil {
			t.Error("scoped quota reached the snapshot without a reset time")
		}
	}
}

// TestPromoteScopedWeeklyLimits_PrefersPopulatedTopLevelKey covers the caveat in
// issue #121: accounts that still get seven_day_sonnet must keep using it rather
// than gaining a duplicate card from limits[].
func TestPromoteScopedWeeklyLimits_PrefersPopulatedTopLevelKey(t *testing.T) {
	payload := []byte(`{
		"five_hour": {"utilization": 4, "resets_at": null, "is_enabled": null},
		"seven_day_sonnet": {"utilization": 41, "resets_at": "2026-09-05T17:59:59Z", "is_enabled": null},
		"limits": [
			{"kind": "weekly_scoped", "group": "weekly", "percent": 12, "resets_at": "2026-09-05T17:59:59Z", "is_active": true,
			 "scope": {"model": {"id": null, "display_name": "Sonnet"}}}
		]
	}`)

	resp, err := ParseAnthropicResponse(payload)
	if err != nil {
		t.Fatalf("ParseAnthropicResponse() error = %v", err)
	}

	if got := quotaUtil(t, resp, "seven_day_sonnet"); got != 41 {
		t.Errorf("seven_day_sonnet utilization = %v, want 41 (top-level wins)", got)
	}
	if _, ok := (*resp)["seven_day_scoped_sonnet"]; ok {
		t.Error("scoped key duplicated a populated top-level quota")
	}
}

// TestPromoteScopedWeeklyLimits_DynamicModelKeys checks that the key and label
// follow whatever model Anthropic scopes the bucket to - no enumeration.
func TestPromoteScopedWeeklyLimits_DynamicModelKeys(t *testing.T) {
	tests := []struct {
		displayName string
		wantKey     string
		wantLabel   string
	}{
		{"Fable", "seven_day_scoped_fable", "Weekly Fable"},
		{"Sonnet", "seven_day_scoped_sonnet", "Weekly Sonnet"},
		{"Opus", "seven_day_scoped_opus", "Weekly Opus"},
		{"Claude Fable", "seven_day_scoped_claude_fable", "Weekly Claude Fable"},
	}

	for _, tt := range tests {
		t.Run(tt.displayName, func(t *testing.T) {
			payload := []byte(`{
				"seven_day": {"utilization": 1, "resets_at": null, "is_enabled": null},
				"limits": [
					{"kind": "weekly_scoped", "group": "weekly", "percent": 55, "resets_at": null, "is_active": true,
					 "scope": {"model": {"id": null, "display_name": "` + tt.displayName + `"}}}
				]
			}`)

			resp, err := ParseAnthropicResponse(payload)
			if err != nil {
				t.Fatalf("ParseAnthropicResponse() error = %v", err)
			}
			if got := quotaUtil(t, resp, tt.wantKey); got != 55 {
				t.Errorf("%s utilization = %v, want 55", tt.wantKey, got)
			}
			if !IsKnownAnthropicQuota(tt.wantKey) {
				t.Errorf("IsKnownAnthropicQuota(%q) = false, want true", tt.wantKey)
			}
			if got := AnthropicDisplayName(tt.wantKey); got != tt.wantLabel {
				t.Errorf("AnthropicDisplayName(%q) = %q, want %q", tt.wantKey, got, tt.wantLabel)
			}

			var listed bool
			for _, name := range resp.ActiveQuotaNames() {
				if name == tt.wantKey {
					listed = true
				}
			}
			if !listed {
				t.Errorf("%s missing from ActiveQuotaNames", tt.wantKey)
			}
		})
	}
}

// TestPromoteScopedWeeklyLimits_MalformedArray verifies that the second parsing
// pass keeps the #82 / #84 guarantee: a broken limits array degrades to "no
// scoped quotas" instead of taking down five_hour / seven_day.
func TestPromoteScopedWeeklyLimits_MalformedArray(t *testing.T) {
	tests := []struct {
		name   string
		limits string
	}{
		{"object instead of array", `{"kind": "weekly_scoped"}`},
		{"scalar", `42`},
		{"string", `"nope"`},
		{"array of scalars", `[1, 2, 3]`},
		{"entry with wrong percent type", `[{"kind": "weekly_scoped", "percent": "high", "scope": {"model": {"display_name": "Fable"}}}]`},
		{"entry without scope", `[{"kind": "weekly_scoped", "percent": 12, "scope": null}]`},
		{"entry without model name", `[{"kind": "weekly_scoped", "percent": 12, "scope": {"model": {"id": null, "display_name": null}}}]`},
		{"blank model name", `[{"kind": "weekly_scoped", "percent": 12, "scope": {"model": {"display_name": "   "}}}]`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := []byte(`{
				"five_hour": {"utilization": 9, "resets_at": null, "is_enabled": null},
				"seven_day": {"utilization": 7, "resets_at": null, "is_enabled": null},
				"limits": ` + tt.limits + `
			}`)

			resp, err := ParseAnthropicResponse(payload)
			if err != nil {
				t.Fatalf("ParseAnthropicResponse() error = %v", err)
			}
			if got := quotaUtil(t, resp, "five_hour"); got != 9 {
				t.Errorf("five_hour utilization = %v, want 9", got)
			}
			if got := quotaUtil(t, resp, "seven_day"); got != 7 {
				t.Errorf("seven_day utilization = %v, want 7", got)
			}
			for key := range *resp {
				if IsAnthropicScopedQuota(key) {
					t.Errorf("malformed limits produced scoped quota %q", key)
				}
			}
		})
	}
}

// TestPromoteScopedWeeklyLimits_PartialArray verifies one unreadable entry does
// not discard the readable ones alongside it.
func TestPromoteScopedWeeklyLimits_PartialArray(t *testing.T) {
	payload := []byte(`{
		"seven_day": {"utilization": 7, "resets_at": null, "is_enabled": null},
		"limits": [
			"garbage",
			{"kind": "weekly_scoped", "group": "weekly", "percent": 12, "resets_at": null, "is_active": true,
			 "scope": {"model": {"id": null, "display_name": "Fable"}}}
		]
	}`)

	resp, err := ParseAnthropicResponse(payload)
	if err != nil {
		t.Fatalf("ParseAnthropicResponse() error = %v", err)
	}
	if got := quotaUtil(t, resp, "seven_day_scoped_fable"); got != 12 {
		t.Errorf("seven_day_scoped_fable utilization = %v, want 12", got)
	}
}

// TestIsKnownAnthropicQuota_ScopedPrefixIsNarrow guards the whitelist: only keys
// the promotion path can produce are accepted, so the experimental top-level
// keys that #82 filtered out stay filtered out.
func TestIsKnownAnthropicQuota_ScopedPrefixIsNarrow(t *testing.T) {
	allowed := []string{
		"five_hour", "seven_day", "seven_day_sonnet", "monthly_limit", "extra_usage",
		"seven_day_scoped_fable", "seven_day_scoped_claude_opus",
	}
	for _, key := range allowed {
		if !IsKnownAnthropicQuota(key) {
			t.Errorf("IsKnownAnthropicQuota(%q) = false, want true", key)
		}
	}

	rejected := []string{
		"seven_day_opus", "seven_day_omelette", "seven_day_cowork", "seven_day_oauth_apps",
		"omelette_promotional", "iguana_necktie", "nimbus_quill", "tangelo",
		"seven_day_scoped_", "seven_day_scoped__", "scoped_fable", "",
	}
	for _, key := range rejected {
		if IsKnownAnthropicQuota(key) {
			t.Errorf("IsKnownAnthropicQuota(%q) = true, want false", key)
		}
	}
}
