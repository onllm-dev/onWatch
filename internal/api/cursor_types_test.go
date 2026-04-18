package api

import (
	"encoding/base64"
	"fmt"
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

func createTestJWT(sub string, exp int64) string {
	header := `{"alg":"RS256","typ":"JWT"}`
	payload := `{"sub":"` + sub + `","exp":` + fmt.Sprintf("%d", exp) + `}`
	return base64URLTestEncode([]byte(header)) + "." + base64URLTestEncode([]byte(payload)) + ".signature"
}

func base64URLTestEncode(data []byte) string {
	s := base64.URLEncoding.EncodeToString(data)
	return strings.TrimRight(s, "=")
}
