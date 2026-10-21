package api

import (
	"encoding/base64"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"testing"
	"time"
)

func TestParseCursorUsageResponse(t *testing.T) {
	raw := `{
		"billingCycleStart": "1768399334000",
		"billingCycleEnd": "1771077734000",
		"planUsage": {
			"totalSpend": 23222,
			"includedSpend": 23222,
			"bonusSpend": 0,
			"remaining": 16778,
			"limit": 40000,
			"remainingBonus": false,
			"autoPercentUsed": 0,
			"apiPercentUsed": 46.444,
			"totalPercentUsed": 15.48
		},
		"spendLimitUsage": {
			"totalSpend": 0,
			"pooledLimit": 50000,
			"pooledUsed": 0,
			"pooledRemaining": 50000,
			"individualLimit": 10000,
			"individualUsed": 0,
			"individualRemaining": 10000,
			"limitType": "team"
		},
		"enabled": true,
		"displayThreshold": 200,
		"displayMessage": "You've used 46% of your usage limit"
	}`

	resp, err := ParseCursorUsageResponse([]byte(raw))
	if err != nil {
		t.Fatalf("ParseCursorUsageResponse: %v", err)
	}

	if resp.BillingCycleStart != "1768399334000" {
		t.Errorf("BillingCycleStart = %q, want %q", resp.BillingCycleStart, "1768399334000")
	}
	if resp.PlanUsage == nil {
		t.Fatal("PlanUsage should not be nil")
	}
	if resp.PlanUsage.TotalSpend != 23222 {
		t.Errorf("TotalSpend = %d, want 23222", resp.PlanUsage.TotalSpend)
	}
	if resp.PlanUsage.TotalPercentUsed != 15.48 {
		t.Errorf("TotalPercentUsed = %f, want 15.48", resp.PlanUsage.TotalPercentUsed)
	}
	if resp.SpendLimitUsage == nil {
		t.Fatal("SpendLimitUsage should not be nil")
	}
	if resp.SpendLimitUsage.LimitType != "team" {
		t.Errorf("LimitType = %q, want %q", resp.SpendLimitUsage.LimitType, "team")
	}
	if !resp.Enabled {
		t.Error("Enabled should be true")
	}
}

func TestParseCursorUsageResponse_InvalidJSON(t *testing.T) {
	_, err := ParseCursorUsageResponse([]byte(`{invalid`))
	if err == nil {
		t.Error("Expected error for invalid JSON")
	}
}

func TestParseCursorPlanInfoResponse(t *testing.T) {
	raw := `{
		"planInfo": {
			"planName": "Ultra",
			"includedAmountCents": 40000,
			"price": "$200/mo",
			"billingCycleEnd": "1771077734000"
		}
	}`

	resp, err := ParseCursorPlanInfoResponse([]byte(raw))
	if err != nil {
		t.Fatalf("ParseCursorPlanInfoResponse: %v", err)
	}
	if resp.PlanInfo.PlanName != "Ultra" {
		t.Errorf("PlanName = %q, want %q", resp.PlanInfo.PlanName, "Ultra")
	}
	if resp.PlanInfo.IncludedAmountCents != 40000 {
		t.Errorf("IncludedAmountCents = %d, want 40000", resp.PlanInfo.IncludedAmountCents)
	}
}

func TestParseCursorCreditGrantsResponse(t *testing.T) {
	raw := `{
		"hasCreditGrants": true,
		"totalCents": "5000",
		"usedCents": "2000"
	}`

	resp, err := ParseCursorCreditGrantsResponse([]byte(raw))
	if err != nil {
		t.Fatalf("ParseCursorCreditGrantsResponse: %v", err)
	}
	if !resp.HasCreditGrants {
		t.Error("HasCreditGrants should be true")
	}
	if resp.TotalCents != "5000" {
		t.Errorf("TotalCents = %q, want %q", resp.TotalCents, "5000")
	}
	if resp.UsedCents != "2000" {
		t.Errorf("UsedCents = %q, want %q", resp.UsedCents, "2000")
	}
}

func TestParseCursorStripeResponse(t *testing.T) {
	raw := `{
		"membershipType": "ultra",
		"subscriptionStatus": "active",
		"customerBalance": -123456
	}`

	resp, err := ParseCursorStripeResponse([]byte(raw))
	if err != nil {
		t.Fatalf("ParseCursorStripeResponse: %v", err)
	}
	if resp.MembershipType != "ultra" {
		t.Errorf("MembershipType = %q, want %q", resp.MembershipType, "ultra")
	}
	if resp.CustomerBalance != -123456 {
		t.Errorf("CustomerBalance = %d, want -123456", resp.CustomerBalance)
	}
}

func TestParseCursorOAuthResponse(t *testing.T) {
	raw := `{
		"access_token": "new_access_token",
		"id_token": "new_id_token",
		"refresh_token": "new_refresh_token",
		"shouldLogout": false
	}`

	resp, err := ParseCursorOAuthResponse([]byte(raw))
	if err != nil {
		t.Fatalf("ParseCursorOAuthResponse: %v", err)
	}
	if resp.AccessToken != "new_access_token" {
		t.Errorf("AccessToken = %q, want %q", resp.AccessToken, "new_access_token")
	}
	if resp.ShouldLogout {
		t.Error("ShouldLogout should be false")
	}
}

func TestParseCursorOAuthResponse_SessionExpired(t *testing.T) {
	raw := `{
		"access_token": "",
		"id_token": "",
		"shouldLogout": true
	}`

	resp, err := ParseCursorOAuthResponse([]byte(raw))
	if err != nil {
		t.Fatalf("ParseCursorOAuthResponse: %v", err)
	}
	if !resp.ShouldLogout {
		t.Error("ShouldLogout should be true")
	}
}

func TestParseCursorRequestUsageResponse(t *testing.T) {
	raw := `{
		"startOfMonth": "2026-03-01",
		"gpt-4": {
			"numRequests": 150,
			"maxRequestUsage": 500
		},
		"claude-3.5-sonnet": {
			"numRequests": 50,
			"maxRequestUsage": 200
		}
	}`

	resp, err := ParseCursorRequestUsageResponse([]byte(raw))
	if err != nil {
		t.Fatalf("ParseCursorRequestUsageResponse: %v", err)
	}
	if resp.StartOfMonth != "2026-03-01" {
		t.Errorf("StartOfMonth = %q, want %q", resp.StartOfMonth, "2026-03-01")
	}
	if len(resp.Models) != 2 {
		t.Fatalf("Models len = %d, want 2", len(resp.Models))
	}
	if resp.Models["gpt-4"].NumRequests != 150 {
		t.Errorf("gpt-4 NumRequests = %d, want 150", resp.Models["gpt-4"].NumRequests)
	}
	if resp.Models["claude-3.5-sonnet"].MaxRequestUsage != 200 {
		t.Errorf("claude-3.5-sonnet MaxRequestUsage = %d, want 200", resp.Models["claude-3.5-sonnet"].MaxRequestUsage)
	}
}

func TestExtractJWTSubject(t *testing.T) {
	tests := []struct {
		name     string
		token    string
		expected string
	}{
		{
			name:     "valid token with pipe",
			token:    createTestJWT("google-oauth2|user_abc", 1735689600),
			expected: "user_abc",
		},
		{
			name:     "valid token without pipe",
			token:    createTestJWT("user123", 1735689600),
			expected: "user123",
		},
		{
			name:     "empty token",
			token:    "",
			expected: "",
		},
		{
			name:     "invalid token",
			token:    "not.a.valid.token",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractJWTSubject(tt.token)
			if got != tt.expected {
				t.Errorf("ExtractJWTSubject() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestExtractJWTExpiry(t *testing.T) {
	token := createTestJWT("user123", 1735689600)
	exp := ExtractJWTExpiry(token)
	if exp != 1735689600 {
		t.Errorf("ExtractJWTExpiry() = %d, want 1735689600", exp)
	}

	emptyExp := ExtractJWTExpiry("")
	if emptyExp != 0 {
		t.Errorf("ExtractJWTExpiry(empty) = %d, want 0", emptyExp)
	}
}

func TestNormalizeCursorPlanName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Pro", "pro"},
		{"Ultra", "ultra"},
		{"Team", "team"},
		{"Business", "team"},
		{"Enterprise", "enterprise"},
		{"Free", "free"},
		{"free trial", "free"},
		{"  PRO  ", "pro"},
		{"Unknown", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := NormalizeCursorPlanName(tt.input)
			if got != tt.expected {
				t.Errorf("NormalizeCursorPlanName(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestParseUnixMsString(t *testing.T) {
	result, err := ParseUnixMsString("1768399334000")
	if err != nil {
		t.Fatalf("ParseUnixMsString: %v", err)
	}
	expected := time.UnixMilli(1768399334000)
	if !result.Equal(expected) {
		t.Errorf("ParseUnixMsString = %v, want %v", result, expected)
	}
}

func TestParseUnixMsString_Empty(t *testing.T) {
	_, err := ParseUnixMsString("")
	if err == nil {
		t.Error("Expected error for empty string")
	}
}

func TestParseIntString(t *testing.T) {
	tests := []struct {
		input    string
		expected int64
	}{
		{"12345", 12345},
		{"0", 0},
		{"", 0},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseIntString(tt.input)
			if err != nil {
				t.Fatalf("ParseIntString(%q): %v", tt.input, err)
			}
			if got != tt.expected {
				t.Errorf("ParseIntString(%q) = %d, want %d", tt.input, got, tt.expected)
			}
		})
	}
}

func TestParseIntString_Invalid(t *testing.T) {
	_, err := ParseIntString("abc")
	if err == nil {
		t.Error("Expected error for non-numeric string")
	}
}

func TestParseIntString_LargeValue(t *testing.T) {
	got, err := ParseIntString("1768399334000")
	if err != nil {
		t.Fatalf("ParseIntString large value: %v", err)
	}
	if got != 1768399334000 {
		t.Fatalf("ParseIntString large value = %d, want 1768399334000", got)
	}
}

func TestToCursorSnapshot_Individual(t *testing.T) {
	usage := &CursorUsageResponse{
		BillingCycleStart: "1768399334000",
		BillingCycleEnd:   "1771077734000",
		PlanUsage: &CursorPlanUsage{
			TotalSpend:       23222,
			IncludedSpend:    23222,
			BonusSpend:       0,
			Remaining:        16778,
			Limit:            40000,
			TotalPercentUsed: 15.48,
			AutoPercentUsed:  5.2,
			ApiPercentUsed:   46.444,
		},
		Enabled: true,
	}

	planInfo := &CursorPlanInfoResponse{
		PlanInfo: CursorPlanInfo{
			PlanName: "Pro",
		},
	}

	snapshot := ToCursorSnapshot(usage, planInfo, nil, nil, nil, false)

	if snapshot.AccountType != CursorAccountIndividual {
		t.Errorf("AccountType = %q, want %q", snapshot.AccountType, CursorAccountIndividual)
	}
	if snapshot.PlanName != "Pro" {
		t.Errorf("PlanName = %q, want %q", snapshot.PlanName, "Pro")
	}

	totalUsageFound := false
	autoFound := false
	apiFound := false
	for _, q := range snapshot.Quotas {
		switch q.Name {
		case "total_usage":
			totalUsageFound = true
			if q.Format != CursorFormatPercent {
				t.Errorf("total_usage Format = %q, want %q", q.Format, CursorFormatPercent)
			}
			if q.Utilization != 15.48 {
				t.Errorf("total_usage Utilization = %f, want 15.48", q.Utilization)
			}
		case "auto_usage":
			autoFound = true
			if q.Utilization != 5.2 {
				t.Errorf("auto_usage Utilization = %f, want 5.2", q.Utilization)
			}
		case "api_usage":
			apiFound = true
			if q.Utilization != 46.444 {
				t.Errorf("api_usage Utilization = %f, want 46.444", q.Utilization)
			}
		}
	}
	if !totalUsageFound {
		t.Error("total_usage quota not found")
	}
	if !autoFound {
		t.Error("auto_usage quota not found")
	}
	if !apiFound {
		t.Error("api_usage quota not found")
	}
}

func TestToCursorSnapshot_IndividualIncludesZeroBreakdownQuotas(t *testing.T) {
	usage := &CursorUsageResponse{
		BillingCycleStart: "1768399334000",
		BillingCycleEnd:   "1771077734000",
		PlanUsage: &CursorPlanUsage{
			TotalSpend:       100,
			Remaining:        39900,
			Limit:            40000,
			TotalPercentUsed: 0.25,
			AutoPercentUsed:  0,
			ApiPercentUsed:   0,
		},
		Enabled: true,
	}

	planInfo := &CursorPlanInfoResponse{
		PlanInfo: CursorPlanInfo{
			PlanName: "Pro+",
		},
	}

	snapshot := ToCursorSnapshot(usage, planInfo, nil, nil, nil, false)

	autoFound := false
	apiFound := false
	for _, q := range snapshot.Quotas {
		switch q.Name {
		case "auto_usage":
			autoFound = true
			if q.Utilization != 0 {
				t.Errorf("auto_usage Utilization = %f, want 0", q.Utilization)
			}
		case "api_usage":
			apiFound = true
			if q.Utilization != 0 {
				t.Errorf("api_usage Utilization = %f, want 0", q.Utilization)
			}
		}
	}

	if !autoFound {
		t.Error("auto_usage quota should still be present when utilization is zero")
	}
	if !apiFound {
		t.Error("api_usage quota should still be present when utilization is zero")
	}
}

func TestToCursorSnapshot_Team(t *testing.T) {
	usage := &CursorUsageResponse{
		BillingCycleStart: "1768399334000",
		BillingCycleEnd:   "1771077734000",
		PlanUsage: &CursorPlanUsage{
			TotalSpend: 5000,
			Remaining:  35000,
			Limit:      40000,
		},
		SpendLimitUsage: &CursorSpendLimitUsage{
			PooledLimit:     50000,
			PooledUsed:      0,
			PooledRemaining: 50000,
			LimitType:       "team",
		},
		Enabled: true,
	}

	planInfo := &CursorPlanInfoResponse{
		PlanInfo: CursorPlanInfo{
			PlanName: "Team",
		},
	}

	snapshot := ToCursorSnapshot(usage, planInfo, nil, nil, nil, false)

	if snapshot.AccountType != CursorAccountTeam {
		t.Errorf("AccountType = %q, want %q", snapshot.AccountType, CursorAccountTeam)
	}

	totalUsageFound := false
	for _, q := range snapshot.Quotas {
		if q.Name == "total_usage" {
			totalUsageFound = true
			if q.Format != CursorFormatDollars {
				t.Errorf("total_usage Format = %q, want %q", q.Format, CursorFormatDollars)
			}
			if q.Used != 50.0 {
				t.Errorf("total_usage Used = %f, want 50.0", q.Used)
			}
			if q.Limit != 400.0 {
				t.Errorf("total_usage Limit = %f, want 400.0", q.Limit)
			}
		}
	}
	if !totalUsageFound {
		t.Error("total_usage quota not found for team")
	}
}

func TestToCursorSnapshot_Credits(t *testing.T) {
	usage := &CursorUsageResponse{
		PlanUsage: &CursorPlanUsage{
			TotalSpend:       1000,
			Remaining:        9000,
			Limit:            10000,
			TotalPercentUsed: 10.0,
		},
		Enabled: true,
	}

	planInfo := &CursorPlanInfoResponse{
		PlanInfo: CursorPlanInfo{
			PlanName: "Pro",
		},
	}

	creditGrants := &CursorCreditGrantsResponse{
		HasCreditGrants: true,
		TotalCents:      "3000",
		UsedCents:       "1500",
	}

	stripeResp := &CursorStripeResponse{
		MembershipType:  "pro",
		CustomerBalance: -2000,
	}

	snapshot := ToCursorSnapshot(usage, planInfo, creditGrants, stripeResp, nil, false)

	creditsFound := false
	for _, q := range snapshot.Quotas {
		if q.Name == "credits" {
			creditsFound = true
			if q.Format != CursorFormatDollars {
				t.Errorf("credits Format = %q, want %q", q.Format, CursorFormatDollars)
			}
			if q.Used != 15.0 {
				t.Errorf("credits Used = %f, want 15.0", q.Used)
			}
			if q.Limit != 50.0 {
				t.Errorf("credits Limit = %f, want 50.0 (3000 + 2000 = 5000 cents = $50)", q.Limit)
			}
		}
	}
	if !creditsFound {
		t.Error("credits quota not found")
	}
}

func TestToCursorSnapshot_Enterprise(t *testing.T) {
	usage := &CursorUsageResponse{
		Enabled: true,
	}

	planInfo := &CursorPlanInfoResponse{
		PlanInfo: CursorPlanInfo{
			PlanName: "Enterprise",
		},
	}

	requestUsage := &CursorRequestUsageResponse{
		StartOfMonth: "2026-03-01",
		Models: map[string]CursorModelUsage{
			"gpt-4": {NumRequests: 150, MaxRequestUsage: 500},
		},
	}

	snapshot := ToCursorSnapshot(usage, planInfo, nil, nil, requestUsage, true)

	if snapshot.AccountType != CursorAccountEnterprise {
		t.Errorf("AccountType = %q, want %q", snapshot.AccountType, CursorAccountEnterprise)
	}

	requestsFound := false
	for _, q := range snapshot.Quotas {
		if q.Name == "requests_gpt-4" {
			requestsFound = true
			if q.Format != CursorFormatCount {
				t.Errorf("requests Format = %q, want %q", q.Format, CursorFormatCount)
			}
			if q.Used != 150 {
				t.Errorf("requests Used = %f, want 150", q.Used)
			}
			if q.Limit != 500 {
				t.Errorf("requests Limit = %f, want 500", q.Limit)
			}
		}
	}
	if !requestsFound {
		t.Error("requests quota not found for enterprise")
	}
}

func TestToCursorSnapshot_OnDemand(t *testing.T) {
	usage := &CursorUsageResponse{
		PlanUsage: &CursorPlanUsage{
			TotalSpend:       1000,
			Remaining:        9000,
			Limit:            10000,
			TotalPercentUsed: 10.0,
		},
		SpendLimitUsage: &CursorSpendLimitUsage{
			IndividualLimit:     10000,
			IndividualUsed:      2500,
			IndividualRemaining: 7500,
		},
		Enabled: true,
	}

	planInfo := &CursorPlanInfoResponse{
		PlanInfo: CursorPlanInfo{
			PlanName: "Pro",
		},
	}

	snapshot := ToCursorSnapshot(usage, planInfo, nil, nil, nil, false)

	ondemandFound := false
	for _, q := range snapshot.Quotas {
		if q.Name == "on_demand" {
			ondemandFound = true
			if q.Format != CursorFormatDollars {
				t.Errorf("on_demand Format = %q, want %q", q.Format, CursorFormatDollars)
			}
			if q.Used != 25.0 {
				t.Errorf("on_demand Used = %f, want 25.0", q.Used)
			}
			if q.Limit != 100.0 {
				t.Errorf("on_demand Limit = %f, want 100.0", q.Limit)
			}
		}
	}
	if !ondemandFound {
		t.Error("on_demand quota not found")
	}
}

func TestToCursorSnapshot_NilUsage(t *testing.T) {
	snapshot := ToCursorSnapshot(nil, nil, nil, nil, nil, false)

	if snapshot.AccountType != CursorAccountIndividual {
		t.Errorf("AccountType = %q, want %q", snapshot.AccountType, CursorAccountIndividual)
	}
	if len(snapshot.Quotas) != 0 {
		t.Errorf("Quotas len = %d, want 0", len(snapshot.Quotas))
	}
}

func TestDetermineCursorAccountType_RequestBasedPromotesToEnterprise(t *testing.T) {
	usage := &CursorUsageResponse{
		Enabled:   true,
		PlanUsage: &CursorPlanUsage{Limit: 0},
	}
	accountType := DetermineCursorAccountType("pro", usage, true)
	if accountType != CursorAccountEnterprise {
		t.Fatalf("DetermineCursorAccountType() = %q, want %q", accountType, CursorAccountEnterprise)
	}
}

func TestToCursorSnapshot_TeamZeroLimitDoesNotProduceInfiniteUtilization(t *testing.T) {
	usage := &CursorUsageResponse{
		Enabled: true,
		PlanUsage: &CursorPlanUsage{
			TotalSpend: 1234,
			Limit:      0,
		},
	}
	planInfo := &CursorPlanInfoResponse{
		PlanInfo: CursorPlanInfo{
			PlanName: "Team",
		},
	}

	snapshot := ToCursorSnapshot(usage, planInfo, nil, nil, nil, false)
	if len(snapshot.Quotas) == 0 {
		t.Fatal("expected at least one quota")
	}

	var totalUsage *CursorQuota
	for i := range snapshot.Quotas {
		if snapshot.Quotas[i].Name == "total_usage" {
			totalUsage = &snapshot.Quotas[i]
			break
		}
	}
	if totalUsage == nil {
		t.Fatal("expected total_usage quota")
	}
	if math.IsInf(totalUsage.Utilization, 0) || math.IsNaN(totalUsage.Utilization) {
		t.Fatalf("utilization = %v, want finite value", totalUsage.Utilization)
	}
	if totalUsage.Utilization != 0 {
		t.Fatalf("utilization = %v, want 0 when plan limit is unavailable", totalUsage.Utilization)
	}
}

func TestCursorCredentials_IsExpiringSoon(t *testing.T) {
	tests := []struct {
		name      string
		creds     *CursorCredentials
		threshold time.Duration
		expected  bool
	}{
		{
			name: "expiring soon",
			creds: &CursorCredentials{
				AccessToken: "test",
				ExpiresAt:   time.Now().Add(2 * time.Minute),
				ExpiresIn:   2 * time.Minute,
			},
			threshold: 5 * time.Minute,
			expected:  true,
		},
		{
			name: "not expiring soon",
			creds: &CursorCredentials{
				AccessToken: "test",
				ExpiresAt:   time.Now().Add(30 * time.Minute),
				ExpiresIn:   30 * time.Minute,
			},
			threshold: 5 * time.Minute,
			expected:  false,
		},
		{
			name: "zero expires at",
			creds: &CursorCredentials{
				AccessToken: "test",
			},
			threshold: 5 * time.Minute,
			expected:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.creds.IsExpiringSoon(tt.threshold)
			if got != tt.expected {
				t.Errorf("IsExpiringSoon() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestNeedsCursorRefresh(t *testing.T) {
	tests := []struct {
		name     string
		creds    *CursorCredentials
		expected bool
	}{
		{
			name:     "nil credentials",
			creds:    nil,
			expected: true,
		},
		{
			name: "empty access token",
			creds: &CursorCredentials{
				AccessToken: "",
			},
			expected: true,
		},
		{
			name: "not expiring soon",
			creds: &CursorCredentials{
				AccessToken: "test",
				ExpiresAt:   time.Now().Add(30 * time.Minute),
				ExpiresIn:   30 * time.Minute,
			},
			expected: false,
		},
		{
			name: "expiring soon",
			creds: &CursorCredentials{
				AccessToken: "test",
				ExpiresAt:   time.Now().Add(2 * time.Minute),
				ExpiresIn:   2 * time.Minute,
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NeedsCursorRefresh(tt.creds)
			if got != tt.expected {
				t.Errorf("NeedsCursorRefresh() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestIsCursorAuthError(t *testing.T) {
	if !IsCursorAuthError(ErrCursorUnauthorized) {
		t.Error("IsCursorAuthError(ErrCursorUnauthorized) should be true")
	}
	if !IsCursorAuthError(ErrCursorForbidden) {
		t.Error("IsCursorAuthError(ErrCursorForbidden) should be true")
	}
	if IsCursorAuthError(ErrCursorServerError) {
		t.Error("IsCursorAuthError(ErrCursorServerError) should be false")
	}
}

func TestIsCursorSessionExpired(t *testing.T) {
	if !IsCursorSessionExpired(ErrCursorSessionExpired) {
		t.Error("IsCursorSessionExpired(ErrCursorSessionExpired) should be true")
	}
	if IsCursorSessionExpired(ErrCursorUnauthorized) {
		t.Error("IsCursorSessionExpired(ErrCursorUnauthorized) should be false")
	}
}

func TestCursorQuotaFormat_String(t *testing.T) {
	tests := []struct {
		format   CursorQuotaFormat
		expected string
	}{
		{CursorFormatPercent, "percent"},
		{CursorFormatDollars, "dollars"},
		{CursorFormatCount, "count"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if string(tt.format) != tt.expected {
				t.Errorf("CursorQuotaFormat(%q).String() = %q, want %q", tt.format, string(tt.format), tt.expected)
			}
		})
	}
}

// Issue #151: Cursor began returning spend fields as JSON strings. The whole
// decode used to abort on the first string, hiding Cursor from the dashboard.
func TestParseCursorUsageResponse_StringNumbers(t *testing.T) {
	raw := `{
		"billingCycleStart": "1768399334000",
		"billingCycleEnd": "1771077734000",
		"planUsage": {
			"totalSpend": "23222",
			"includedSpend": "23222",
			"bonusSpend": "0",
			"remaining": "16778",
			"limit": "40000",
			"remainingBonus": false,
			"autoPercentUsed": "0",
			"apiPercentUsed": "46.444",
			"totalPercentUsed": "15.48"
		},
		"spendLimitUsage": {
			"totalSpend": "0",
			"pooledLimit": "50000",
			"pooledUsed": "0",
			"pooledRemaining": "50000",
			"individualLimit": "10000",
			"individualUsed": "0",
			"individualRemaining": "10000",
			"limitType": "team"
		},
		"enabled": true,
		"displayThreshold": "200",
		"displayMessage": "You've used 46% of your usage limit"
	}`

	resp, err := ParseCursorUsageResponse([]byte(raw))
	if err != nil {
		t.Fatalf("ParseCursorUsageResponse rejected string numbers: %v", err)
	}

	if resp.PlanUsage == nil || resp.SpendLimitUsage == nil {
		t.Fatal("planUsage/spendLimitUsage should not be nil")
	}
	if resp.PlanUsage.TotalSpend != 23222 {
		t.Errorf("planUsage.TotalSpend = %d, want 23222", resp.PlanUsage.TotalSpend)
	}
	if resp.PlanUsage.Limit != 40000 {
		t.Errorf("planUsage.Limit = %d, want 40000", resp.PlanUsage.Limit)
	}
	if resp.PlanUsage.ApiPercentUsed != 46.444 {
		t.Errorf("planUsage.ApiPercentUsed = %v, want 46.444", resp.PlanUsage.ApiPercentUsed)
	}
	if resp.SpendLimitUsage.PooledLimit != 50000 {
		t.Errorf("spendLimitUsage.PooledLimit = %d, want 50000", resp.SpendLimitUsage.PooledLimit)
	}
	if resp.SpendLimitUsage.IndividualRemaining != 10000 {
		t.Errorf("spendLimitUsage.IndividualRemaining = %d, want 10000", resp.SpendLimitUsage.IndividualRemaining)
	}
	if resp.DisplayThreshold != 200 {
		t.Errorf("DisplayThreshold = %d, want 200", resp.DisplayThreshold)
	}
}

func TestParseCursorUsageResponse_MixedNumbers(t *testing.T) {
	raw := `{
		"planUsage": {
			"totalSpend": 23222,
			"limit": "40000",
			"remaining": 16778,
			"totalPercentUsed": "15.48",
			"autoPercentUsed": 2.5,
			"apiPercentUsed": "46.444"
		},
		"spendLimitUsage": {
			"totalSpend": "0",
			"pooledLimit": 50000,
			"pooledRemaining": "50000",
			"individualLimit": 10000,
			"individualRemaining": "10000"
		},
		"enabled": true
	}`

	resp, err := ParseCursorUsageResponse([]byte(raw))
	if err != nil {
		t.Fatalf("ParseCursorUsageResponse rejected mixed numbers: %v", err)
	}
	if resp.PlanUsage.TotalSpend != 23222 || resp.PlanUsage.Limit != 40000 {
		t.Errorf("planUsage spend = %d/%d, want 23222/40000", resp.PlanUsage.TotalSpend, resp.PlanUsage.Limit)
	}
	if resp.PlanUsage.TotalPercentUsed != 15.48 || resp.PlanUsage.ApiPercentUsed != 46.444 {
		t.Errorf("planUsage percents = %v/%v, want 15.48/46.444", resp.PlanUsage.TotalPercentUsed, resp.PlanUsage.ApiPercentUsed)
	}
	if resp.SpendLimitUsage.PooledLimit != 50000 || resp.SpendLimitUsage.PooledRemaining != 50000 {
		t.Errorf("pooled = %d/%d, want 50000/50000", resp.SpendLimitUsage.PooledLimit, resp.SpendLimitUsage.PooledRemaining)
	}
	if resp.SpendLimitUsage.IndividualLimit != 10000 || resp.SpendLimitUsage.IndividualRemaining != 10000 {
		t.Errorf("individual = %d/%d, want 10000/10000", resp.SpendLimitUsage.IndividualLimit, resp.SpendLimitUsage.IndividualRemaining)
	}
}

func TestParseCursorUsageResponse_EmptyAndNullNumbers(t *testing.T) {
	raw := `{
		"planUsage": {
			"totalSpend": "",
			"limit": null,
			"remaining": "40000",
			"totalPercentUsed": "",
			"autoPercentUsed": null,
			"apiPercentUsed": 1.5
		},
		"spendLimitUsage": {
			"pooledLimit": "",
			"pooledRemaining": null,
			"individualLimit": "10000",
			"individualRemaining": ""
		},
		"enabled": true
	}`

	resp, err := ParseCursorUsageResponse([]byte(raw))
	if err != nil {
		t.Fatalf("ParseCursorUsageResponse rejected empty/null numbers: %v", err)
	}
	if resp.PlanUsage.TotalSpend != 0 || resp.PlanUsage.Limit != 0 {
		t.Errorf("empty/null plan spend = %d/%d, want 0/0", resp.PlanUsage.TotalSpend, resp.PlanUsage.Limit)
	}
	if resp.PlanUsage.Remaining != 40000 {
		t.Errorf("Remaining = %d, want 40000", resp.PlanUsage.Remaining)
	}
	if resp.PlanUsage.TotalPercentUsed != 0 || resp.PlanUsage.AutoPercentUsed != 0 {
		t.Errorf("empty/null percents = %v/%v, want 0/0", resp.PlanUsage.TotalPercentUsed, resp.PlanUsage.AutoPercentUsed)
	}
	if resp.PlanUsage.ApiPercentUsed != 1.5 {
		t.Errorf("ApiPercentUsed = %v, want 1.5", resp.PlanUsage.ApiPercentUsed)
	}
	if resp.SpendLimitUsage.PooledLimit != 0 || resp.SpendLimitUsage.PooledRemaining != 0 {
		t.Errorf("empty/null pooled = %d/%d, want 0/0", resp.SpendLimitUsage.PooledLimit, resp.SpendLimitUsage.PooledRemaining)
	}
	if resp.SpendLimitUsage.IndividualLimit != 10000 {
		t.Errorf("IndividualLimit = %d, want 10000", resp.SpendLimitUsage.IndividualLimit)
	}
}

func TestParseCursorUsageResponse_GarbageFieldDegrades(t *testing.T) {
	raw := `{
		"planUsage": {
			"totalSpend": "not-a-number",
			"limit": "40000",
			"totalPercentUsed": "also-bad",
			"apiPercentUsed": "46.444"
		},
		"spendLimitUsage": {
			"pooledLimit": {},
			"individualLimit": "10000"
		},
		"enabled": true
	}`

	resp, err := ParseCursorUsageResponse([]byte(raw))
	if err != nil {
		t.Fatalf("one bad field must not fail the whole decode: %v", err)
	}
	if resp.PlanUsage.TotalSpend != 0 {
		t.Errorf("garbage TotalSpend = %d, want 0", resp.PlanUsage.TotalSpend)
	}
	if resp.PlanUsage.Limit != 40000 {
		t.Errorf("Limit = %d, want 40000 (other fields must still decode)", resp.PlanUsage.Limit)
	}
	if resp.PlanUsage.TotalPercentUsed != 0 {
		t.Errorf("garbage TotalPercentUsed = %v, want 0", resp.PlanUsage.TotalPercentUsed)
	}
	if resp.PlanUsage.ApiPercentUsed != 46.444 {
		t.Errorf("ApiPercentUsed = %v, want 46.444", resp.PlanUsage.ApiPercentUsed)
	}
	if resp.SpendLimitUsage.PooledLimit != 0 {
		t.Errorf("garbage PooledLimit = %d, want 0", resp.SpendLimitUsage.PooledLimit)
	}
	if resp.SpendLimitUsage.IndividualLimit != 10000 {
		t.Errorf("IndividualLimit = %d, want 10000", resp.SpendLimitUsage.IndividualLimit)
	}
}

func TestParseCursorUsageResponse_GarbageLogsOneWarning(t *testing.T) {
	var buf strings.Builder
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(old)

	raw := `{
		"planUsage": {"totalSpend": "secret-token-looking-value"},
		"spendLimitUsage": {"pooledLimit": "also-bad"},
		"enabled": true
	}`
	if _, err := ParseCursorUsageResponse([]byte(raw)); err != nil {
		t.Fatalf("ParseCursorUsageResponse: %v", err)
	}

	logs := buf.String()
	if n := strings.Count(logs, "cursor:"); n != 1 {
		t.Errorf("got %d cursor log lines, want exactly 1; logs: %q", n, logs)
	}
	if !strings.Contains(logs, "level=WARN") {
		t.Errorf("expected a WARN log, got: %q", logs)
	}
	if strings.Contains(logs, "secret-token-looking-value") || strings.Contains(logs, "also-bad") {
		t.Errorf("log leaked raw field values: %q", logs)
	}
}

// A free plan now sends spendLimitUsage.pooledLimit too, so its presence must
// not promote the account to Team.
func TestDetermineCursorAccountType_FreePlanWithStringPooledLimit(t *testing.T) {
	raw := `{
		"planUsage": {"totalSpend": "0", "limit": "2000", "remaining": "2000", "totalPercentUsed": "0"},
		"spendLimitUsage": {"pooledLimit": "50000", "pooledRemaining": "50000", "limitType": "user"},
		"enabled": true
	}`
	usage, err := ParseCursorUsageResponse([]byte(raw))
	if err != nil {
		t.Fatalf("ParseCursorUsageResponse: %v", err)
	}

	if got := DetermineCursorAccountType("free", usage, false); got != CursorAccountIndividual {
		t.Errorf("DetermineCursorAccountType(free) = %q, want %q", got, CursorAccountIndividual)
	}

	snapshot := ToCursorSnapshot(usage, &CursorPlanInfoResponse{PlanInfo: CursorPlanInfo{PlanName: "Free"}}, nil, nil, nil, false)
	if snapshot.AccountType != CursorAccountIndividual {
		t.Errorf("snapshot AccountType = %q, want %q", snapshot.AccountType, CursorAccountIndividual)
	}
	for _, q := range snapshot.Quotas {
		if q.Name == "total_usage" && q.Format != CursorFormatPercent {
			t.Errorf("free plan total_usage Format = %q, want %q", q.Format, CursorFormatPercent)
		}
	}
}

// Plan info is non-fatal in the client, so a free account must not be
// promoted to Team when only limitType identifies it as individual.
func TestDetermineCursorAccountType_UserLimitTypeWithoutPlanName(t *testing.T) {
	raw := `{
		"planUsage": {"limit": "2000", "remaining": "2000"},
		"spendLimitUsage": {"pooledLimit": "50000", "limitType": "user"},
		"enabled": true
	}`
	usage, err := ParseCursorUsageResponse([]byte(raw))
	if err != nil {
		t.Fatalf("ParseCursorUsageResponse: %v", err)
	}
	if got := DetermineCursorAccountType("", usage, false); got != CursorAccountIndividual {
		t.Errorf("DetermineCursorAccountType with user limitType = %q, want %q", got, CursorAccountIndividual)
	}
}

// Plan info is non-fatal in the client. With no plan name and no limitType,
// a pooledLimit must not promote the account to Team: the issue's free-plan
// user would otherwise be misclassified whenever plan info fails.
func TestDetermineCursorAccountType_EmptyPlanPooledLimitIsIndividual(t *testing.T) {
	raw := `{
		"planUsage": {"limit": "2000", "remaining": "2000"},
		"spendLimitUsage": {"pooledLimit": "50000", "pooledRemaining": "50000"},
		"enabled": true
	}`
	usage, err := ParseCursorUsageResponse([]byte(raw))
	if err != nil {
		t.Fatalf("ParseCursorUsageResponse: %v", err)
	}
	if got := DetermineCursorAccountType("", usage, false); got != CursorAccountIndividual {
		t.Errorf(`DetermineCursorAccountType("") = %q, want %q`, got, CursorAccountIndividual)
	}
}

// A Team payload still resolves to Team after the inference change.
func TestDetermineCursorAccountType_TeamLimitTypeStillWins(t *testing.T) {
	raw := `{"spendLimitUsage": {"pooledLimit": "50000", "limitType": "team"}, "enabled": true}`
	usage, err := ParseCursorUsageResponse([]byte(raw))
	if err != nil {
		t.Fatalf("ParseCursorUsageResponse: %v", err)
	}
	if got := DetermineCursorAccountType("", usage, false); got != CursorAccountTeam {
		t.Errorf("DetermineCursorAccountType with team limitType = %q, want %q", got, CursorAccountTeam)
	}
}

func TestParseCursorUsageResponse_GarbageValueDoesNotFailPoll(t *testing.T) {
	var buf strings.Builder
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(old)

	// Requirement 3: even a value the tolerant type cannot parse must degrade
	// to zero rather than fail the poll.
	raw := `{
		"planUsage": {"totalSpend": "not-a-number", "limit": 40000},
		"spendLimitUsage": {"pooledLimit": {}, "individualLimit": 10000},
		"enabled": true
	}`
	resp, err := ParseCursorUsageResponse([]byte(raw))
	if err != nil {
		t.Fatalf("ParseCursorUsageResponse: %v", err)
	}
	if resp.PlanUsage == nil || resp.SpendLimitUsage == nil {
		t.Fatal("sections should still decode")
	}
	if resp.PlanUsage.TotalSpend != 0 || resp.PlanUsage.Limit != 40000 {
		t.Errorf("planUsage = %d/%d, want 0/40000", resp.PlanUsage.TotalSpend, resp.PlanUsage.Limit)
	}
	if resp.SpendLimitUsage.PooledLimit != 0 || resp.SpendLimitUsage.IndividualLimit != 10000 {
		t.Errorf("spendLimitUsage = %d/%d, want 0/10000", resp.SpendLimitUsage.PooledLimit, resp.SpendLimitUsage.IndividualLimit)
	}
}

// A failed enabled decode, or a section that is not an object, must also be
// counted so the single warning reflects silently dropped data.
func TestParseCursorUsageResponse_MalformedSectionsWarn(t *testing.T) {
	var buf strings.Builder
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(old)

	raw := `{
		"enabled": "yes",
		"planUsage": 5,
		"spendLimitUsage": "oops"
	}`
	resp, err := ParseCursorUsageResponse([]byte(raw))
	if err != nil {
		t.Fatalf("ParseCursorUsageResponse: %v", err)
	}
	if resp.PlanUsage != nil || resp.SpendLimitUsage != nil {
		t.Errorf("malformed sections should be nil, got %+v/%+v", resp.PlanUsage, resp.SpendLimitUsage)
	}

	logs := buf.String()
	if n := strings.Count(logs, "cursor:"); n != 1 {
		t.Errorf("got %d cursor log lines, want exactly 1; logs: %q", n, logs)
	}
	if !strings.Contains(logs, "field_count=3") {
		t.Errorf("expected field_count=3 in warning, got: %q", logs)
	}
}

func TestParseCursorUsageResponse_NullSections(t *testing.T) {
	raw := `{"planUsage": null, "spendLimitUsage": null, "enabled": true}`
	resp, err := ParseCursorUsageResponse([]byte(raw))
	if err != nil {
		t.Fatalf("ParseCursorUsageResponse: %v", err)
	}
	if resp.PlanUsage != nil || resp.SpendLimitUsage != nil {
		t.Errorf("null sections should stay nil, got %+v/%+v", resp.PlanUsage, resp.SpendLimitUsage)
	}
}

func TestParseCursorPlanInfoResponse_StringNumbers(t *testing.T) {
	raw := `{"planInfo": {"planName": "Pro", "includedAmountCents": "2000"}}`
	resp, err := ParseCursorPlanInfoResponse([]byte(raw))
	if err != nil {
		t.Fatalf("ParseCursorPlanInfoResponse: %v", err)
	}
	if resp.PlanInfo.IncludedAmountCents != 2000 {
		t.Errorf("IncludedAmountCents = %d, want 2000", resp.PlanInfo.IncludedAmountCents)
	}
}

func TestParseCursorCreditGrantsResponse_NumericCents(t *testing.T) {
	raw := `{"hasCreditGrants": true, "totalCents": 5000, "usedCents": "2000"}`
	resp, err := ParseCursorCreditGrantsResponse([]byte(raw))
	if err != nil {
		t.Fatalf("ParseCursorCreditGrantsResponse: %v", err)
	}
	if resp.TotalCents != "5000" {
		t.Errorf("TotalCents = %q, want %q", resp.TotalCents, "5000")
	}
	if resp.UsedCents != "2000" {
		t.Errorf("UsedCents = %q, want %q", resp.UsedCents, "2000")
	}
}

func TestParseCursorStripeResponse_StringBalance(t *testing.T) {
	raw := `{"membershipType": "pro", "customerBalance": "-123456"}`
	resp, err := ParseCursorStripeResponse([]byte(raw))
	if err != nil {
		t.Fatalf("ParseCursorStripeResponse: %v", err)
	}
	if resp.CustomerBalance != -123456 {
		t.Errorf("CustomerBalance = %d, want -123456", resp.CustomerBalance)
	}
}

func TestParseCursorRequestUsageResponse_StringNumbers(t *testing.T) {
	raw := `{
		"startOfMonth": "2026-03-01",
		"gpt-4": {"numRequests": "150", "maxRequestUsage": 500},
		"bad-model": {"numRequests": {}, "maxRequestUsage": "200"}
	}`
	resp, err := ParseCursorRequestUsageResponse([]byte(raw))
	if err != nil {
		t.Fatalf("ParseCursorRequestUsageResponse: %v", err)
	}
	if resp.Models["gpt-4"].NumRequests != 150 || resp.Models["gpt-4"].MaxRequestUsage != 500 {
		t.Errorf("gpt-4 = %d/%d, want 150/500", resp.Models["gpt-4"].NumRequests, resp.Models["gpt-4"].MaxRequestUsage)
	}
	if resp.Models["bad-model"].MaxRequestUsage != 200 {
		t.Errorf("bad-model MaxRequestUsage = %d, want 200", resp.Models["bad-model"].MaxRequestUsage)
	}
}

func TestCursorFlexInt_Unmarshal(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    cursorFlexInt
		wantErr bool
	}{
		{"number", `50000`, 50000, false},
		{"negative number", `-1234`, -1234, false},
		{"string integer", `"50000"`, 50000, false},
		{"string with spaces", `" 50000 "`, 50000, false},
		{"string decimal rounds down", `"12.4"`, 12, false},
		{"string decimal rounds up", `"12.5"`, 13, false},
		{"decimal number rounds", `12.6`, 13, false},
		{"empty string", `""`, 0, false},
		{"null", `null`, 0, false},
		{"garbage string", `"abc"`, 0, true},
		{"array", `[1,2]`, 0, true},
		{"object", `{"v":1}`, 0, true},
		{"bool", `true`, 0, true},
		{"NaN", `"NaN"`, 0, true},
		{"Infinity", `"Inf"`, 0, true},
		{"overflow", `1e300`, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got cursorFlexInt
			err := got.UnmarshalJSON([]byte(tt.input))
			if tt.wantErr != (err != nil) {
				t.Fatalf("UnmarshalJSON(%s) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("UnmarshalJSON(%s) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestCursorFlexFloat_Unmarshal(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    cursorFlexFloat
		wantErr bool
	}{
		{"number", `46.444`, 46.444, false},
		{"string", `"46.444"`, 46.444, false},
		{"string integer", `"0"`, 0, false},
		{"empty string", `""`, 0, false},
		{"null", `null`, 0, false},
		{"garbage", `"nope"`, 0, true},
		{"object", `{}`, 0, true},
		{"bool", `true`, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got cursorFlexFloat
			err := got.UnmarshalJSON([]byte(tt.input))
			if tt.wantErr != (err != nil) {
				t.Fatalf("UnmarshalJSON(%s) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("UnmarshalJSON(%s) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func createTestJWT(sub string, exp int64) string {
	header := `{"alg":"RS256","typ":"JWT"}`
	payload := `{"sub":"` + sub + `","exp":` + fmt.Sprintf("%d", exp) + `}`
	return base64URLTestEncode([]byte(header)) + "." + base64URLTestEncode([]byte(payload)) + ".signature"
}

func base64URLTestEncode(data []byte) string {
	s := base64.URLEncoding.EncodeToString(data)
	return strings.TrimRight(s, "=")
}

// A non-numeric value in a quota-defining field ("N/A") used to become 0 and
// was stored as a fresh 0% or no-limit reading. Drop that quota instead so the
// last good card is kept; a single bad field still must not fail the poll.
func TestToCursorSnapshot_DropsQuotaWhoseDefiningFieldIsGarbage(t *testing.T) {
	raw := `{
		"planUsage": {
			"totalSpend": "N/A",
			"remaining": "N/A",
			"limit": "N/A",
			"totalPercentUsed": "N/A",
			"autoPercentUsed": "N/A",
			"apiPercentUsed": 12.5
		},
		"spendLimitUsage": {"individualLimit": "N/A", "individualRemaining": "N/A"},
		"enabled": true
	}`
	usage, err := ParseCursorUsageResponse([]byte(raw))
	if err != nil {
		t.Fatalf("one bad field must not fail the poll: %v", err)
	}
	snapshot := ToCursorSnapshot(usage, &CursorPlanInfoResponse{PlanInfo: CursorPlanInfo{PlanName: "Pro"}}, nil, nil, nil, false)

	names := map[string]CursorQuota{}
	for _, q := range snapshot.Quotas {
		names[q.Name] = q
	}
	if _, ok := names["total_usage"]; ok {
		t.Errorf("total_usage present with garbage limit/percent; want it dropped: %#v", names["total_usage"])
	}
	if _, ok := names["auto_usage"]; ok {
		t.Errorf("auto_usage present with a garbage percent; want it dropped: %#v", names["auto_usage"])
	}
	if _, ok := names["on_demand"]; ok {
		t.Errorf("on_demand present with a garbage limit; want it dropped: %#v", names["on_demand"])
	}
	api, ok := names["api_usage"]
	if !ok {
		t.Fatal("api_usage (the only sound percent) must still be present")
	}
	if api.Utilization != 12.5 {
		t.Fatalf("api_usage = %v, want 12.5", api.Utilization)
	}
}

// Absent, null and empty-string defining fields stay a legitimate zero, per
// #151's contract, not a dropped quota.
func TestToCursorSnapshot_EmptyDefiningFieldsStillDecode(t *testing.T) {
	raw := `{
		"planUsage": {"limit": "", "totalPercentUsed": null, "autoPercentUsed": "", "apiPercentUsed": ""},
		"spendLimitUsage": {"individualLimit": null, "individualRemaining": ""},
		"enabled": true
	}`
	usage, err := ParseCursorUsageResponse([]byte(raw))
	if err != nil {
		t.Fatalf("ParseCursorUsageResponse: %v", err)
	}
	snapshot := ToCursorSnapshot(usage, &CursorPlanInfoResponse{PlanInfo: CursorPlanInfo{PlanName: "Pro"}}, nil, nil, nil, false)
	names := map[string]bool{}
	for _, q := range snapshot.Quotas {
		names[q.Name] = true
	}
	if !names["auto_usage"] || !names["api_usage"] {
		t.Fatalf("empty percentages must decode to a known zero; quotas = %v", names)
	}
}

// A garbage non-defining field (remaining) must not drop the quota: the limit
// and percent still describe it.
func TestToCursorSnapshot_NonDefiningGarbageKeepsQuota(t *testing.T) {
	raw := `{
		"planUsage": {"limit": "40000", "remaining": "N/A", "totalPercentUsed": "10"},
		"enabled": true
	}`
	usage, err := ParseCursorUsageResponse([]byte(raw))
	if err != nil {
		t.Fatalf("ParseCursorUsageResponse: %v", err)
	}
	snapshot := ToCursorSnapshot(usage, &CursorPlanInfoResponse{PlanInfo: CursorPlanInfo{PlanName: "Pro"}}, nil, nil, nil, false)
	var found bool
	for _, q := range snapshot.Quotas {
		if q.Name == "total_usage" {
			found = true
			if q.Utilization != 10 {
				t.Fatalf("total_usage = %v, want 10", q.Utilization)
			}
		}
	}
	if !found {
		t.Fatal("total_usage dropped because of a garbage non-defining field")
	}
}

// A garbage maxRequestUsage drops that model's request quota, not the others.
func TestToCursorSnapshot_EnterpriseDropsModelWithGarbageLimit(t *testing.T) {
	raw := `{
		"startOfMonth": "2026-03-01",
		"gpt-4": {"numRequests": 10, "maxRequestUsage": "N/A"},
		"o3": {"numRequests": 5, "maxRequestUsage": 500}
	}`
	usage := &CursorUsageResponse{Enabled: true}
	requestUsage, err := ParseCursorRequestUsageResponse([]byte(raw))
	if err != nil {
		t.Fatalf("ParseCursorRequestUsageResponse: %v", err)
	}
	snapshot := ToCursorSnapshot(usage,
		&CursorPlanInfoResponse{PlanInfo: CursorPlanInfo{PlanName: "Enterprise"}},
		nil, nil, requestUsage, true)

	names := map[string]bool{}
	for _, q := range snapshot.Quotas {
		names[q.Name] = true
	}
	if names["requests_gpt-4"] {
		t.Error("requests_gpt-4 present with a garbage limit; want it dropped")
	}
	if !names["requests_o3"] {
		t.Error("requests_o3 must still be present")
	}
}
