package store

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

// --- UpdateCycle ---

func TestStore_UpdateCycle(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err = s.CreateCycle("subscription", base, base.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("CreateCycle failed: %v", err)
	}

	err = s.UpdateCycle("subscription", 500, 300)
	if err != nil {
		t.Fatalf("UpdateCycle failed: %v", err)
	}

	cycle, err := s.QueryActiveCycle("subscription")
	if err != nil {
		t.Fatalf("QueryActiveCycle failed: %v", err)
	}
	if cycle == nil {
		t.Fatal("Expected active cycle")
	}
	if cycle.PeakRequests != 500 {
		t.Errorf("PeakRequests = %v, want 500", cycle.PeakRequests)
	}
	if cycle.TotalDelta != 300 {
		t.Errorf("TotalDelta = %v, want 300", cycle.TotalDelta)
	}
}

// --- DeleteAuthToken ---

func TestStore_DeleteAuthToken(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	token := "token-to-delete"
	err = s.SaveAuthToken(token, time.Now().UTC().Add(24*time.Hour))
	if err != nil {
		t.Fatalf("SaveAuthToken failed: %v", err)
	}

	// Verify it exists
	_, found, err := s.GetAuthTokenExpiry(token)
	if err != nil {
		t.Fatalf("GetAuthTokenExpiry failed: %v", err)
	}
	if !found {
		t.Fatal("Token should exist before delete")
	}

	// Delete it
	err = s.DeleteAuthToken(token)
	if err != nil {
		t.Fatalf("DeleteAuthToken failed: %v", err)
	}

	// Verify it's gone
	_, found, err = s.GetAuthTokenExpiry(token)
	if err != nil {
		t.Fatalf("GetAuthTokenExpiry failed: %v", err)
	}
	if found {
		t.Error("Token should not exist after delete")
	}
}

func TestStore_DeleteAuthToken_NonExistent(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	// Should not error on non-existent token
	err = s.DeleteAuthToken("nonexistent")
	if err != nil {
		t.Fatalf("DeleteAuthToken on nonexistent failed: %v", err)
	}
}

// --- Push Subscriptions ---

func TestStore_SavePushSubscription_RoundTrip(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	// Generate valid base64url-encoded values
	// p256dh: 65 bytes (uncompressed P-256 point)
	p256dhBytes := make([]byte, 65)
	p256dhBytes[0] = 0x04 // Uncompressed point prefix
	for i := 1; i < 65; i++ {
		p256dhBytes[i] = byte(i)
	}
	p256dh := base64.RawURLEncoding.EncodeToString(p256dhBytes)

	// auth: 16 bytes
	authBytes := make([]byte, 16)
	for i := range authBytes {
		authBytes[i] = byte(i + 100)
	}
	auth := base64.RawURLEncoding.EncodeToString(authBytes)

	endpoint := "https://push.example.com/subscription/123"

	err = s.SavePushSubscription(endpoint, p256dh, auth)
	if err != nil {
		t.Fatalf("SavePushSubscription failed: %v", err)
	}

	// Verify it was stored
	subs, err := s.GetPushSubscriptions()
	if err != nil {
		t.Fatalf("GetPushSubscriptions failed: %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("Expected 1 subscription, got %d", len(subs))
	}
	if subs[0].Endpoint != endpoint {
		t.Errorf("Endpoint = %q, want %q", subs[0].Endpoint, endpoint)
	}
	if subs[0].P256dh != p256dh {
		t.Errorf("P256dh mismatch")
	}
	if subs[0].Auth != auth {
		t.Errorf("Auth mismatch")
	}
}

func TestStore_SavePushSubscription_InvalidEndpoint(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	err = s.SavePushSubscription("http://insecure.example.com", "abc", "def")
	if err == nil {
		t.Error("Expected error for non-HTTPS endpoint")
	}
}

func TestStore_SavePushSubscription_InvalidP256dh(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	authBytes := make([]byte, 16)
	auth := base64.RawURLEncoding.EncodeToString(authBytes)

	// p256dh too short (only 10 bytes instead of 65)
	p256dh := base64.RawURLEncoding.EncodeToString(make([]byte, 10))

	err = s.SavePushSubscription("https://push.example.com/sub", p256dh, auth)
	if err == nil {
		t.Error("Expected error for invalid p256dh length")
	}
}

func TestStore_SavePushSubscription_InvalidAuth(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	p256dhBytes := make([]byte, 65)
	p256dhBytes[0] = 0x04
	p256dh := base64.RawURLEncoding.EncodeToString(p256dhBytes)

	// auth too short (only 5 bytes instead of 16)
	auth := base64.RawURLEncoding.EncodeToString(make([]byte, 5))

	err = s.SavePushSubscription("https://push.example.com/sub", p256dh, auth)
	if err == nil {
		t.Error("Expected error for invalid auth length")
	}
}

func TestStore_SavePushSubscription_Upsert(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	p256dhBytes := make([]byte, 65)
	p256dhBytes[0] = 0x04
	p256dh := base64.RawURLEncoding.EncodeToString(p256dhBytes)

	authBytes := make([]byte, 16)
	auth := base64.RawURLEncoding.EncodeToString(authBytes)

	endpoint := "https://push.example.com/sub/456"

	err = s.SavePushSubscription(endpoint, p256dh, auth)
	if err != nil {
		t.Fatalf("First SavePushSubscription failed: %v", err)
	}

	// Update with new auth
	newAuthBytes := make([]byte, 16)
	for i := range newAuthBytes {
		newAuthBytes[i] = 0xFF
	}
	newAuth := base64.RawURLEncoding.EncodeToString(newAuthBytes)

	err = s.SavePushSubscription(endpoint, p256dh, newAuth)
	if err != nil {
		t.Fatalf("Upsert SavePushSubscription failed: %v", err)
	}

	subs, err := s.GetPushSubscriptions()
	if err != nil {
		t.Fatalf("GetPushSubscriptions failed: %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("Expected 1 subscription after upsert, got %d", len(subs))
	}
	if subs[0].Auth != newAuth {
		t.Errorf("Auth not updated after upsert")
	}
}

func TestStore_DeletePushSubscription(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	p256dhBytes := make([]byte, 65)
	p256dhBytes[0] = 0x04
	p256dh := base64.RawURLEncoding.EncodeToString(p256dhBytes)
	authBytes := make([]byte, 16)
	auth := base64.RawURLEncoding.EncodeToString(authBytes)

	endpoint := "https://push.example.com/sub/789"
	err = s.SavePushSubscription(endpoint, p256dh, auth)
	if err != nil {
		t.Fatalf("SavePushSubscription failed: %v", err)
	}

	err = s.DeletePushSubscription(endpoint)
	if err != nil {
		t.Fatalf("DeletePushSubscription failed: %v", err)
	}

	subs, err := s.GetPushSubscriptions()
	if err != nil {
		t.Fatalf("GetPushSubscriptions failed: %v", err)
	}
	if len(subs) != 0 {
		t.Errorf("Expected 0 subscriptions after delete, got %d", len(subs))
	}
}

func TestStore_GetPushSubscriptions_Empty(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	subs, err := s.GetPushSubscriptions()
	if err != nil {
		t.Fatalf("GetPushSubscriptions failed: %v", err)
	}
	if len(subs) != 0 {
		t.Errorf("Expected 0 subscriptions, got %d", len(subs))
	}
}

// --- MigrateSessionsToUsageBased ---

func TestStore_MigrateSessionsToUsageBased_Empty(t *testing.T) {
	t.Parallel()
	tmpFile := t.TempDir() + "/migrate_empty.db"
	s, err := New(tmpFile)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	err = s.MigrateSessionsToUsageBased(30 * time.Minute)
	if err != nil {
		t.Fatalf("MigrateSessionsToUsageBased failed: %v", err)
	}

	// Verify migration flag set
	val, err := s.GetSetting("session_migration_v2")
	if err != nil {
		t.Fatalf("GetSetting failed: %v", err)
	}
	if val != "done" {
		t.Errorf("Expected 'done', got %q", val)
	}
}

func TestStore_MigrateSessionsToUsageBased_AlreadyDone(t *testing.T) {
	t.Parallel()
	tmpFile := t.TempDir() + "/migrate_done.db"
	s, err := New(tmpFile)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	// Pre-set the flag
	if err := s.SetSetting("session_migration_v2", "done"); err != nil {
		t.Fatalf("SetSetting failed: %v", err)
	}

	err = s.MigrateSessionsToUsageBased(30 * time.Minute)
	if err != nil {
		t.Fatalf("MigrateSessionsToUsageBased failed: %v", err)
	}
}

func TestStore_MigrateSessionsToUsageBased_WithSyntheticData(t *testing.T) {
	t.Parallel()
	tmpFile := t.TempDir() + "/migrate_synthetic.db"
	s, err := New(tmpFile)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Insert snapshots with changing values (simulates activity)
	for i := 0; i < 5; i++ {
		snapshot := &api.Snapshot{
			CapturedAt: base.Add(time.Duration(i) * time.Minute),
			Sub:        api.QuotaInfo{Limit: 100, Requests: float64(i * 10), RenewsAt: base},
			Search:     api.QuotaInfo{Limit: 50, Requests: float64(i * 5), RenewsAt: base},
			ToolCall:   api.QuotaInfo{Limit: 200, Requests: float64(i * 20), RenewsAt: base},
		}
		_, err := s.InsertSnapshot(snapshot)
		if err != nil {
			t.Fatalf("InsertSnapshot failed: %v", err)
		}
	}

	err = s.MigrateSessionsToUsageBased(30 * time.Minute)
	if err != nil {
		t.Fatalf("MigrateSessionsToUsageBased failed: %v", err)
	}

	// Should have created at least one session
	sessions, err := s.QuerySessionHistory("synthetic")
	if err != nil {
		t.Fatalf("QuerySessionHistory failed: %v", err)
	}
	if len(sessions) == 0 {
		t.Error("Expected at least 1 migrated session")
	}
}

func TestStore_MigrateSessionsToUsageBased_WithZaiData(t *testing.T) {
	t.Parallel()
	tmpFile := t.TempDir() + "/migrate_zai.db"
	s, err := New(tmpFile)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Insert Z.ai snapshots with changing values
	for i := 0; i < 5; i++ {
		snapshot := &api.ZaiSnapshot{
			CapturedAt:         base.Add(time.Duration(i) * time.Minute),
			TimeLimit:          100,
			TimeUnit:           1,
			TimeNumber:         100,
			TimeUsage:          float64(i * 10),
			TimeCurrentValue:   float64(i * 10),
			TimeRemaining:      float64(100 - i*10),
			TimePercentage:     i * 10,
			TokensLimit:        1000,
			TokensUnit:         1,
			TokensNumber:       1000,
			TokensUsage:        float64(i * 100),
			TokensCurrentValue: float64(i * 100),
			TokensRemaining:    float64(1000 - i*100),
			TokensPercentage:   i * 10,
		}
		_, err := s.InsertZaiSnapshot(snapshot)
		if err != nil {
			t.Fatalf("InsertZaiSnapshot failed: %v", err)
		}
	}

	err = s.MigrateSessionsToUsageBased(30 * time.Minute)
	if err != nil {
		t.Fatalf("MigrateSessionsToUsageBased failed: %v", err)
	}

	sessions, err := s.QuerySessionHistory("zai")
	if err != nil {
		t.Fatalf("QuerySessionHistory failed: %v", err)
	}
	if len(sessions) == 0 {
		t.Error("Expected at least 1 migrated zai session")
	}
}

func TestStore_MigrateSessionsToUsageBased_WithAnthropicData(t *testing.T) {
	t.Parallel()
	tmpFile := t.TempDir() + "/migrate_anthropic.db"
	s, err := New(tmpFile)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	resetsAt := base.Add(5 * time.Hour)

	// Insert Anthropic snapshots with changing values
	for i := 0; i < 5; i++ {
		snapshot := &api.AnthropicSnapshot{
			CapturedAt: base.Add(time.Duration(i) * time.Minute),
			RawJSON:    "{}",
			Quotas: []api.AnthropicQuota{
				{Name: "five_hour", Utilization: float64(i) * 10, ResetsAt: &resetsAt},
			},
		}
		_, err := s.InsertAnthropicSnapshot(snapshot)
		if err != nil {
			t.Fatalf("InsertAnthropicSnapshot failed: %v", err)
		}
	}

	err = s.MigrateSessionsToUsageBased(30 * time.Minute)
	if err != nil {
		t.Fatalf("MigrateSessionsToUsageBased failed: %v", err)
	}

	sessions, err := s.QuerySessionHistory("anthropic")
	if err != nil {
		t.Fatalf("QuerySessionHistory failed: %v", err)
	}
	if len(sessions) == 0 {
		t.Error("Expected at least 1 migrated anthropic session")
	}
}

func TestStore_MigrateSessionsToUsageBased_IdleTimeout(t *testing.T) {
	t.Parallel()
	tmpFile := t.TempDir() + "/migrate_idle.db"
	s, err := New(tmpFile)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Insert snapshots with gap to trigger idle timeout
	for i := 0; i < 3; i++ {
		snapshot := &api.Snapshot{
			CapturedAt: base.Add(time.Duration(i) * time.Minute),
			Sub:        api.QuotaInfo{Limit: 100, Requests: float64(i * 10), RenewsAt: base},
			Search:     api.QuotaInfo{Limit: 50, Requests: float64(i * 5), RenewsAt: base},
			ToolCall:   api.QuotaInfo{Limit: 200, Requests: float64(i * 20), RenewsAt: base},
		}
		_, err := s.InsertSnapshot(snapshot)
		if err != nil {
			t.Fatalf("InsertSnapshot failed: %v", err)
		}
	}

	// Gap of 2 hours with no value change
	for i := 0; i < 3; i++ {
		snapshot := &api.Snapshot{
			CapturedAt: base.Add(2*time.Hour + time.Duration(i)*time.Minute),
			Sub:        api.QuotaInfo{Limit: 100, Requests: 20, RenewsAt: base}, // Same values
			Search:     api.QuotaInfo{Limit: 50, Requests: 10, RenewsAt: base},
			ToolCall:   api.QuotaInfo{Limit: 200, Requests: 40, RenewsAt: base},
		}
		_, err := s.InsertSnapshot(snapshot)
		if err != nil {
			t.Fatalf("InsertSnapshot failed: %v", err)
		}
	}

	// Then new activity
	for i := 0; i < 3; i++ {
		snapshot := &api.Snapshot{
			CapturedAt: base.Add(3*time.Hour + time.Duration(i)*time.Minute),
			Sub:        api.QuotaInfo{Limit: 100, Requests: float64(30 + i*10), RenewsAt: base},
			Search:     api.QuotaInfo{Limit: 50, Requests: float64(15 + i*5), RenewsAt: base},
			ToolCall:   api.QuotaInfo{Limit: 200, Requests: float64(60 + i*20), RenewsAt: base},
		}
		_, err := s.InsertSnapshot(snapshot)
		if err != nil {
			t.Fatalf("InsertSnapshot failed: %v", err)
		}
	}

	// Short idle timeout so the gap creates separate sessions
	err = s.MigrateSessionsToUsageBased(5 * time.Minute)
	if err != nil {
		t.Fatalf("MigrateSessionsToUsageBased failed: %v", err)
	}

	sessions, err := s.QuerySessionHistory("synthetic")
	if err != nil {
		t.Fatalf("QuerySessionHistory failed: %v", err)
	}
	// Should create multiple sessions due to idle gap
	if len(sessions) < 2 {
		t.Errorf("Expected at least 2 sessions with idle gap, got %d", len(sessions))
	}
}
