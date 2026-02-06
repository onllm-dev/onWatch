package api

import (
	"encoding/json"
	"testing"
	"time"
)

// realAPIResponse is the actual response captured from Synthetic API
const realAPIResponse = `{
  "subscription": {
    "limit": 1350,
    "requests": 154.3,
    "renewsAt": "2026-02-06T16:16:18.386Z"
  },
  "search": {
    "hourly": {
      "limit": 250,
      "requests": 0,
      "renewsAt": "2026-02-06T13:58:14.386Z"
    }
  },
  "toolCallDiscounts": {
    "limit": 16200,
    "requests": 7635,
    "renewsAt": "2026-02-06T15:26:41.390Z"
  }
}`

func TestQuotaResponse_UnmarshalJSON_RealData(t *testing.T) {
	var resp QuotaResponse
	if err := json.Unmarshal([]byte(realAPIResponse), &resp); err != nil {
		t.Fatalf("Failed to unmarshal real API response: %v", err)
	}

	// Verify subscription
	if resp.Subscription.Limit != 1350 {
		t.Errorf("Subscription.Limit = %v, want %v", resp.Subscription.Limit, 1350)
	}
	if resp.Subscription.Requests != 154.3 {
		t.Errorf("Subscription.Requests = %v, want %v", resp.Subscription.Requests, 154.3)
	}
	expectedRenew := time.Date(2026, 2, 6, 16, 16, 18, 386000000, time.UTC)
	if !resp.Subscription.RenewsAt.Equal(expectedRenew) {
		t.Errorf("Subscription.RenewsAt = %v, want %v", resp.Subscription.RenewsAt, expectedRenew)
	}

	// Verify search
	if resp.Search.Hourly.Limit != 250 {
		t.Errorf("Search.Hourly.Limit = %v, want %v", resp.Search.Hourly.Limit, 250)
	}
	if resp.Search.Hourly.Requests != 0 {
		t.Errorf("Search.Hourly.Requests = %v, want %v", resp.Search.Hourly.Requests, 0)
	}

	// Verify tool call discounts
	if resp.ToolCallDiscounts.Limit != 16200 {
		t.Errorf("ToolCallDiscounts.Limit = %v, want %v", resp.ToolCallDiscounts.Limit, 16200)
	}
	if resp.ToolCallDiscounts.Requests != 7635 {
		t.Errorf("ToolCallDiscounts.Requests = %v, want %v", resp.ToolCallDiscounts.Requests, 7635)
	}
}

func TestQuotaResponse_FloatRequests(t *testing.T) {
	jsonData := `{"subscription": {"limit": 100, "requests": 154.3, "renewsAt": "2026-02-06T16:16:18Z"}}`

	var resp QuotaResponse
	if err := json.Unmarshal([]byte(jsonData), &resp); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	// Should be exactly 154.3, not rounded
	if resp.Subscription.Requests != 154.3 {
		t.Errorf("Requests = %v, want %v", resp.Subscription.Requests, 154.3)
	}
}

func TestQuotaResponse_ISO8601_Parsing(t *testing.T) {
	jsonData := `{"subscription": {"limit": 100, "requests": 0, "renewsAt": "2026-02-06T16:16:18.386Z"}}`

	var resp QuotaResponse
	if err := json.Unmarshal([]byte(jsonData), &resp); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	expected := time.Date(2026, 2, 6, 16, 16, 18, 386000000, time.UTC)
	if !resp.Subscription.RenewsAt.Equal(expected) {
		t.Errorf("RenewsAt = %v, want %v", resp.Subscription.RenewsAt, expected)
	}

	// Should be UTC
	if resp.Subscription.RenewsAt.Location() != time.UTC {
		t.Errorf("Expected UTC timezone, got %v", resp.Subscription.RenewsAt.Location())
	}
}

func TestQuotaResponse_AllThreeQuotaTypes(t *testing.T) {
	var resp QuotaResponse
	if err := json.Unmarshal([]byte(realAPIResponse), &resp); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	// All three quota types should be present
	if resp.Subscription.Limit == 0 {
		t.Error("Subscription should be present")
	}
	if resp.Search.Hourly.Limit == 0 {
		t.Error("Search.Hourly should be present")
	}
	if resp.ToolCallDiscounts.Limit == 0 {
		t.Error("ToolCallDiscounts should be present")
	}
}

func TestQuotaResponse_ToolCallDiscounts_IndependentRenewsAt(t *testing.T) {
	var resp QuotaResponse
	if err := json.Unmarshal([]byte(realAPIResponse), &resp); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	// Tool call renewsAt should be different from subscription
	subRenew := resp.Subscription.RenewsAt
	toolRenew := resp.ToolCallDiscounts.RenewsAt

	if subRenew.Equal(toolRenew) {
		t.Error("ToolCallDiscounts renewsAt should be independent from subscription")
	}

	// Expected values from real API
	subExpected := time.Date(2026, 2, 6, 16, 16, 18, 386000000, time.UTC)
	toolExpected := time.Date(2026, 2, 6, 15, 26, 41, 390000000, time.UTC)

	if !subRenew.Equal(subExpected) {
		t.Errorf("Subscription renewsAt = %v, want %v", subRenew, subExpected)
	}
	if !toolRenew.Equal(toolExpected) {
		t.Errorf("ToolCallDiscounts renewsAt = %v, want %v", toolRenew, toolExpected)
	}
}

func TestQuotaResponse_SearchNested(t *testing.T) {
	var resp QuotaResponse
	if err := json.Unmarshal([]byte(realAPIResponse), &resp); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	// Search is nested under "search.hourly"
	if resp.Search.Hourly.Limit != 250 {
		t.Errorf("Search.Hourly.Limit = %v, want %v", resp.Search.Hourly.Limit, 250)
	}
	if resp.Search.Hourly.Requests != 0 {
		t.Errorf("Search.Hourly.Requests = %v, want %v", resp.Search.Hourly.Requests, 0)
	}

	expectedRenew := time.Date(2026, 2, 6, 13, 58, 14, 386000000, time.UTC)
	if !resp.Search.Hourly.RenewsAt.Equal(expectedRenew) {
		t.Errorf("Search.Hourly.RenewsAt = %v, want %v", resp.Search.Hourly.RenewsAt, expectedRenew)
	}
}

func TestQuotaResponse_ZeroRequests(t *testing.T) {
	jsonData := `{
		"subscription": {"limit": 100, "requests": 0, "renewsAt": "2026-02-06T16:16:18Z"},
		"search": {"hourly": {"limit": 50, "requests": 0, "renewsAt": "2026-02-06T16:16:18Z"}},
		"toolCallDiscounts": {"limit": 200, "requests": 0, "renewsAt": "2026-02-06T16:16:18Z"}
	}`

	var resp QuotaResponse
	if err := json.Unmarshal([]byte(jsonData), &resp); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if resp.Subscription.Requests != 0 {
		t.Errorf("Subscription.Requests = %v, want 0", resp.Subscription.Requests)
	}
	if resp.Search.Hourly.Requests != 0 {
		t.Errorf("Search.Hourly.Requests = %v, want 0", resp.Search.Hourly.Requests)
	}
	if resp.ToolCallDiscounts.Requests != 0 {
		t.Errorf("ToolCallDiscounts.Requests = %v, want 0", resp.ToolCallDiscounts.Requests)
	}
}

func TestQuotaResponse_UnknownFields_Ignored(t *testing.T) {
	jsonData := `{
		"subscription": {"limit": 100, "requests": 50, "renewsAt": "2026-02-06T16:16:18Z", "unknownField": "ignored"},
		"search": {"hourly": {"limit": 50, "requests": 10, "renewsAt": "2026-02-06T16:16:18Z"}},
		"toolCallDiscounts": {"limit": 200, "requests": 100, "renewsAt": "2026-02-06T16:16:18Z"},
		"newFutureField": "also ignored"
	}`

	var resp QuotaResponse
	if err := json.Unmarshal([]byte(jsonData), &resp); err != nil {
		t.Fatalf("Failed to unmarshal with unknown fields: %v", err)
	}

	// Should still parse known fields correctly
	if resp.Subscription.Limit != 100 {
		t.Errorf("Subscription.Limit = %v, want 100", resp.Subscription.Limit)
	}
	if resp.Subscription.Requests != 50 {
		t.Errorf("Subscription.Requests = %v, want 50", resp.Subscription.Requests)
	}
}
