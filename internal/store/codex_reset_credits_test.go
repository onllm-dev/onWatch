package store

import (
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

func rcTime(t *testing.T, raw string) *time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return &parsed
}

func TestSyncCodexResetCredits_UpsertAndGoneMarking(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	credit1Expiry := rcTime(t, "2026-10-19T12:00:00Z")
	credit2Expiry := rcTime(t, "2026-10-20T12:00:00Z")

	first := []CodexResetCreditRecord{
		{
			CreditID:  "credit-1",
			Status:    "available",
			ResetType: "codex_rate_limits",
			Title:     "Full reset",
			GrantedAt: rcTime(t, "2026-10-01T00:00:00Z"),
			ExpiresAt: credit1Expiry,
		},
		{
			CreditID:  "credit-2",
			Status:    "available",
			ResetType: "codex_rate_limits",
			GrantedAt: rcTime(t, "2026-10-02T00:00:00Z"),
		},
	}
	if err := s.SyncCodexResetCredits(DefaultCodexAccountID, first, now); err != nil {
		t.Fatalf("SyncCodexResetCredits: %v", err)
	}

	stored, err := s.QueryCodexResetCredits(DefaultCodexAccountID)
	if err != nil {
		t.Fatalf("QueryCodexResetCredits: %v", err)
	}
	if len(stored) != 2 {
		t.Fatalf("stored len = %d, want 2", len(stored))
	}
	if stored[0].CreditID != "credit-1" || stored[0].Status != "available" {
		t.Fatalf("first row = %#v", stored[0])
	}
	if stored[0].ExpiresAt == nil || !stored[0].ExpiresAt.Equal(*credit1Expiry) {
		t.Fatalf("first expiry = %v", stored[0].ExpiresAt)
	}
	if stored[1].ExpiresAt != nil {
		t.Fatalf("second expiry = %v, want nil (never expires)", stored[1].ExpiresAt)
	}
	if stored[1].GoneAt != nil {
		t.Fatalf("second GoneAt = %v, want nil", stored[1].GoneAt)
	}

	// Second fetch: credit-1 is still there and redeemed, credit-2 disappeared.
	later := now.Add(30 * time.Minute)
	second := []CodexResetCreditRecord{
		{
			CreditID:  "credit-1",
			Status:    "redeemed",
			ResetType: "codex_rate_limits",
			Title:     "Full reset",
			ExpiresAt: credit1Expiry,
		},
		{
			CreditID:  "credit-3",
			Status:    "available",
			ResetType: "codex_rate_limits",
			ExpiresAt: credit2Expiry,
		},
	}
	if err := s.SyncCodexResetCredits(DefaultCodexAccountID, second, later); err != nil {
		t.Fatalf("SyncCodexResetCredits second: %v", err)
	}

	stored, err = s.QueryCodexResetCredits(DefaultCodexAccountID)
	if err != nil {
		t.Fatalf("QueryCodexResetCredits second: %v", err)
	}
	byID := map[string]CodexResetCreditRecord{}
	for _, row := range stored {
		byID[row.CreditID] = row
	}
	if got := byID["credit-1"]; got.Status != "redeemed" || got.GoneAt != nil {
		t.Fatalf("credit-1 = %#v, want redeemed and not gone", got)
	}
	if got := byID["credit-2"]; got.GoneAt == nil {
		t.Fatalf("credit-2 = %#v, want marked gone", got)
	}
	if got := byID["credit-3"]; got.Status != "available" || got.GoneAt != nil {
		t.Fatalf("credit-3 = %#v, want available and not gone", got)
	}

	// A credit that reappears is revived rather than duplicated.
	if err := s.SyncCodexResetCredits(DefaultCodexAccountID, first, later.Add(time.Minute)); err != nil {
		t.Fatalf("SyncCodexResetCredits revive: %v", err)
	}
	stored, err = s.QueryCodexResetCredits(DefaultCodexAccountID)
	if err != nil {
		t.Fatalf("QueryCodexResetCredits revive: %v", err)
	}
	if len(stored) != 3 {
		t.Fatalf("stored len = %d, want 3 (no duplicates)", len(stored))
	}
	for _, row := range stored {
		if row.CreditID == "credit-2" && row.GoneAt != nil {
			t.Fatalf("credit-2 = %#v, want revived", row)
		}
	}
}

func TestSyncCodexResetCredits_PerAccountIsolation(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	credit := []CodexResetCreditRecord{{CreditID: "credit-1", Status: "available"}}
	if err := s.SyncCodexResetCredits(1, credit, now); err != nil {
		t.Fatalf("Sync account 1: %v", err)
	}

	rows, err := s.QueryCodexResetCredits(2)
	if err != nil {
		t.Fatalf("Query account 2: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("account 2 rows = %d, want 0", len(rows))
	}

	// Gone-marking for one account must not touch another account's rows.
	if err := s.SyncCodexResetCredits(2, nil, now); err != nil {
		t.Fatalf("Sync account 2 empty: %v", err)
	}
	rows, err = s.QueryCodexResetCredits(1)
	if err != nil {
		t.Fatalf("Query account 1: %v", err)
	}
	if len(rows) != 1 || rows[0].GoneAt != nil {
		t.Fatalf("account 1 rows = %#v, want the untouched credit", rows)
	}
}

func TestMarkCodexResetCreditNotified_DedupesByCredit(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	if err := s.SyncCodexResetCredits(1, []CodexResetCreditRecord{{
		CreditID:  "credit-1",
		Status:    "available",
		ExpiresAt: rcTime(t, "2026-10-19T06:00:00Z"),
	}}, now); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	pending, err := s.QueryCodexResetCreditsNeedingExpiryNotice(1, now, 24*time.Hour)
	if err != nil {
		t.Fatalf("Query needing notice: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending len = %d, want 1", len(pending))
	}

	marker := now.Add(time.Minute)
	if err := s.MarkCodexResetCreditNotified(1, "credit-1", marker); err != nil {
		t.Fatalf("MarkCodexResetCreditNotified: %v", err)
	}
	// A second mark must not move the timestamp.
	if err := s.MarkCodexResetCreditNotified(1, "credit-1", marker.Add(time.Hour)); err != nil {
		t.Fatalf("MarkCodexResetCreditNotified second: %v", err)
	}

	rows, err := s.QueryCodexResetCredits(1)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if rows[0].NotifiedAt == nil || !rows[0].NotifiedAt.Equal(marker) {
		t.Fatalf("NotifiedAt = %v, want %v", rows[0].NotifiedAt, marker)
	}

	pending, err = s.QueryCodexResetCreditsNeedingExpiryNotice(1, now.Add(2*time.Hour), 24*time.Hour)
	if err != nil {
		t.Fatalf("Query needing notice after mark: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending after mark = %d, want 0", len(pending))
	}
}

func TestCodexResetCreditCheckStatus_UpsertAndQuery(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	status, err := s.QueryCodexResetCreditCheckStatus(1)
	if err != nil {
		t.Fatalf("Query empty: %v", err)
	}
	if status != nil {
		t.Fatalf("status = %#v, want nil before any check", status)
	}

	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	count := int64(2)
	if err := s.SetCodexResetCreditCheckStatus(1, "ok", &count, nil, now); err != nil {
		t.Fatalf("Set ok: %v", err)
	}
	status, err = s.QueryCodexResetCreditCheckStatus(1)
	if err != nil {
		t.Fatalf("Query ok: %v", err)
	}
	if status.Status != "ok" || status.AvailableCount == nil || *status.AvailableCount != 2 {
		t.Fatalf("status = %#v", status)
	}
	if !status.LastCheckedAt.Equal(now) {
		t.Fatalf("LastCheckedAt = %v, want %v", status.LastCheckedAt, now)
	}

	if err := s.SetCodexResetCreditCheckStatus(1, "unavailable", nil, nil, now.Add(time.Hour)); err != nil {
		t.Fatalf("Set unavailable: %v", err)
	}
	status, err = s.QueryCodexResetCreditCheckStatus(1)
	if err != nil {
		t.Fatalf("Query unavailable: %v", err)
	}
	if status.Status != "unavailable" || status.AvailableCount != nil {
		t.Fatalf("status = %#v, want unavailable without a count", status)
	}
}

func TestClaimCodexResetCreditNotice_ExactlyOnce(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	if err := s.SyncCodexResetCredits(1, []CodexResetCreditRecord{{
		CreditID:  "credit-1",
		Status:    "available",
		ExpiresAt: rcTime(t, now.Add(6*time.Hour).Format(time.RFC3339)),
	}}, now); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	claimed, err := s.ClaimCodexResetCreditNotice(1, "credit-1", now)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if !claimed {
		t.Fatal("first claim = false, want true")
	}
	claimed, err = s.ClaimCodexResetCreditNotice(1, "credit-1", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Claim second: %v", err)
	}
	if claimed {
		t.Fatal("second claim = true, want false")
	}

	// Releasing the exact claim makes it claimable again; an older timestamp
	// does not release a newer claim.
	if err := s.ReleaseCodexResetCreditNotice(1, "credit-1", now.Add(time.Minute)); err != nil {
		t.Fatalf("Release mismatched: %v", err)
	}
	claimed, err = s.ClaimCodexResetCreditNotice(1, "credit-1", now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("Claim after mismatched release: %v", err)
	}
	if claimed {
		t.Fatal("claim after mismatched release = true, want false")
	}
	if err := s.ReleaseCodexResetCreditNotice(1, "credit-1", now); err != nil {
		t.Fatalf("Release: %v", err)
	}
	claimed, err = s.ClaimCodexResetCreditNotice(1, "credit-1", now.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("Claim after release: %v", err)
	}
	if !claimed {
		t.Fatal("claim after release = false, want true")
	}
}

func TestMergeCodexAccountData_MovesResetCreditsAndKeepsNotifiedMarker(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	if _, err := s.CreateOrRestoreProviderAccount("codex", "target"); err != nil {
		t.Fatalf("create target: %v", err)
	}
	if _, err := s.CreateOrRestoreProviderAccount("codex", "source"); err != nil {
		t.Fatalf("create source: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	if err := s.SyncCodexResetCredits(1, []CodexResetCreditRecord{{
		CreditID:  "shared",
		Status:    "available",
		ExpiresAt: rcTime(t, now.Add(4*time.Hour).Format(time.RFC3339)),
	}}, now); err != nil {
		t.Fatalf("Sync target: %v", err)
	}
	if err := s.SyncCodexResetCredits(2, []CodexResetCreditRecord{{
		CreditID:  "shared",
		Status:    "available",
		ExpiresAt: rcTime(t, now.Add(4*time.Hour).Format(time.RFC3339)),
	}}, now); err != nil {
		t.Fatalf("Sync source: %v", err)
	}
	if _, err := s.ClaimCodexResetCreditNotice(2, "shared", now); err != nil {
		t.Fatalf("Claim source: %v", err)
	}

	if err := s.MergeCodexAccountData(1, 2); err != nil {
		t.Fatalf("MergeCodexAccountData: %v", err)
	}

	rows, err := s.QueryCodexResetCredits(1)
	if err != nil {
		t.Fatalf("Query target: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("target rows = %d, want 1 after merge", len(rows))
	}
	if rows[0].NotifiedAt == nil {
		t.Fatal("notification marker lost in merge; a warning could re-send")
	}
	other, err := s.QueryCodexResetCredits(2)
	if err != nil {
		t.Fatalf("Query source: %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("source rows = %d, want 0 after merge", len(other))
	}
}

func TestMergeCodexAccountData_PreservesTargetMarkerFirstSeenAndGoneState(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	earliest := now.Add(-48 * time.Hour)
	expiry := now.Add(4 * time.Hour)

	// Target: live shared credit, notified, first seen earlier.
	if err := s.SyncCodexResetCredits(1, []CodexResetCreditRecord{{
		CreditID:  "shared",
		Status:    "available",
		ExpiresAt: &expiry,
	}}, earliest); err != nil {
		t.Fatalf("Sync target: %v", err)
	}
	targetMarker := now.Add(-time.Hour)
	if _, err := s.ClaimCodexResetCreditNotice(1, "shared", targetMarker); err != nil {
		t.Fatalf("Claim target: %v", err)
	}

	// Source: the same credit id, but gone (a stale disappeared row) and no
	// notification marker. UPDATE OR REPLACE would otherwise discard the
	// target's marker, first_seen and live state.
	if err := s.SyncCodexResetCredits(2, []CodexResetCreditRecord{{
		CreditID:  "shared",
		Status:    "available",
		ExpiresAt: &expiry,
	}}, now.Add(-24*time.Hour)); err != nil {
		t.Fatalf("Sync source: %v", err)
	}
	if err := s.SyncCodexResetCredits(2, nil, now.Add(-23*time.Hour)); err != nil {
		t.Fatalf("Sync source gone: %v", err)
	}

	if err := s.MergeCodexAccountData(1, 2); err != nil {
		t.Fatalf("MergeCodexAccountData: %v", err)
	}

	rows, err := s.QueryCodexResetCredits(1)
	if err != nil {
		t.Fatalf("Query target: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("target rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.NotifiedAt == nil || !row.NotifiedAt.Equal(targetMarker) {
		t.Fatalf("NotifiedAt = %v, want the target's marker %v (otherwise the warning re-sends)", row.NotifiedAt, targetMarker)
	}
	if !row.FirstSeen.Equal(earliest) {
		t.Fatalf("FirstSeen = %v, want the earliest %v", row.FirstSeen, earliest)
	}
	if row.GoneAt != nil {
		t.Fatalf("GoneAt = %v, want nil: one side was live, so the credit is live", row.GoneAt)
	}
}

// A credit that disappeared on the source, with no target row at all, must
// stay gone after the merge. The target-side subquery is empty, so the stale
// source's gone_at has to survive.
func TestMergeCodexAccountData_SourceOnlyGoneCreditStaysGone(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	expiry := now.Add(4 * time.Hour)
	if err := s.SyncCodexResetCredits(2, []CodexResetCreditRecord{{
		CreditID:  "source-gone",
		Status:    "available",
		ExpiresAt: &expiry,
	}}, now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("Sync source: %v", err)
	}
	goneAt := now.Add(-time.Hour)
	if err := s.SyncCodexResetCredits(2, nil, goneAt); err != nil {
		t.Fatalf("Sync source gone: %v", err)
	}

	if err := s.MergeCodexAccountData(1, 2); err != nil {
		t.Fatalf("MergeCodexAccountData: %v", err)
	}

	rows, err := s.QueryCodexResetCredits(1)
	if err != nil {
		t.Fatalf("Query target: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("target rows = %d, want 1", len(rows))
	}
	if rows[0].CreditID != "source-gone" {
		t.Fatalf("credit = %q", rows[0].CreditID)
	}
	if rows[0].GoneAt == nil || !rows[0].GoneAt.Equal(goneAt) {
		t.Fatalf("GoneAt = %v, want the source's %v: a gone credit must not revive", rows[0].GoneAt, goneAt)
	}
}

// The earlier first_seen wins regardless of which account holds it.
func TestMergeCodexAccountData_EarliestFirstSeenWinsBothDirections(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	expiry := now.Add(4 * time.Hour)
	earlier := now.Add(-72 * time.Hour)
	later := now.Add(-24 * time.Hour)

	cases := []struct {
		name          string
		targetSeen    time.Time
		sourceSeen    time.Time
		wantFirstSeen time.Time
	}{
		{name: "target earlier", targetSeen: earlier, sourceSeen: later, wantFirstSeen: earlier},
		{name: "source earlier", targetSeen: later, sourceSeen: earlier, wantFirstSeen: earlier},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := New(":memory:")
			if err != nil {
				t.Fatalf("store.New: %v", err)
			}
			defer s.Close()

			credit := []CodexResetCreditRecord{{CreditID: "shared", Status: "available", ExpiresAt: &expiry}}
			if err := s.SyncCodexResetCredits(1, credit, tc.targetSeen); err != nil {
				t.Fatalf("Sync target: %v", err)
			}
			if err := s.SyncCodexResetCredits(2, credit, tc.sourceSeen); err != nil {
				t.Fatalf("Sync source: %v", err)
			}
			if err := s.MergeCodexAccountData(1, 2); err != nil {
				t.Fatalf("MergeCodexAccountData: %v", err)
			}

			rows, err := s.QueryCodexResetCredits(1)
			if err != nil {
				t.Fatalf("Query target: %v", err)
			}
			if len(rows) != 1 {
				t.Fatalf("target rows = %d, want 1", len(rows))
			}
			if !rows[0].FirstSeen.Equal(tc.wantFirstSeen) {
				t.Fatalf("FirstSeen = %v, want the earlier %v", rows[0].FirstSeen, tc.wantFirstSeen)
			}
		})
	}
}

func TestSyncCodexResetCredits_StoresSupportedAndExpiryUnknown(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	expiry := rcTime(t, now.Add(3*time.Hour).Format(time.RFC3339))
	if err := s.SyncCodexResetCredits(1, []CodexResetCreditRecord{
		{CreditID: "usable", Status: "available", ExpiresAt: expiry},
		{CreditID: "unsupported", Status: "available", ExpiresAt: expiry, UnsupportedByPlan: true},
		{CreditID: "unknown-expiry", Status: "available", ExpiryUnknown: true},
	}, now); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	rows, err := s.QueryCodexResetCredits(1)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	byID := map[string]CodexResetCreditRecord{}
	for _, row := range rows {
		byID[row.CreditID] = row
	}
	if byID["unsupported"].UnsupportedByPlan != true {
		t.Fatalf("unsupported flag not stored: %#v", byID["unsupported"])
	}
	if byID["usable"].UnsupportedByPlan {
		t.Fatalf("usable flag = true, want false: %#v", byID["usable"])
	}
	if !byID["unknown-expiry"].ExpiryUnknown || byID["unknown-expiry"].ExpiresAt != nil {
		t.Fatalf("unknown-expiry row = %#v", byID["unknown-expiry"])
	}

	// Only plan-usable credits may trigger an expiry warning.
	pending, err := s.QueryCodexResetCreditsNeedingExpiryNotice(1, now, 24*time.Hour)
	if err != nil {
		t.Fatalf("Query needing notice: %v", err)
	}
	if len(pending) != 1 || pending[0].CreditID != "usable" {
		t.Fatalf("pending = %#v, want only the usable credit", pending)
	}
}

func TestCodexResetCreditCheckStatus_StoresApplicableCount(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	available, applicable := int64(3), int64(1)
	if err := s.SetCodexResetCreditCheckStatus(1, "ok", &available, &applicable, now); err != nil {
		t.Fatalf("Set: %v", err)
	}
	status, err := s.QueryCodexResetCreditCheckStatus(1)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if status.ApplicableCount == nil || *status.ApplicableCount != 1 {
		t.Fatalf("ApplicableCount = %v, want 1", status.ApplicableCount)
	}

	// An absent applicable count must round-trip as unknown, not 0.
	if err := s.SetCodexResetCreditCheckStatus(1, "ok", &available, nil, now); err != nil {
		t.Fatalf("Set nil applicable: %v", err)
	}
	status, err = s.QueryCodexResetCreditCheckStatus(1)
	if err != nil {
		t.Fatalf("Query second: %v", err)
	}
	if status.ApplicableCount != nil {
		t.Fatalf("ApplicableCount = %v, want nil when absent", *status.ApplicableCount)
	}
}

func TestInsertCodexSnapshot_RoundTripsApplicableCount(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	if _, err := s.InsertCodexSnapshot(&api.CodexSnapshot{
		CapturedAt:                  now,
		AccountID:                   1,
		PlanType:                    "plus",
		ResetCreditsAvailableCount:  api.CodexOptionalInt64{Value: 3, Valid: true},
		ResetCreditsApplicableCount: api.CodexOptionalInt64{Value: 1, Valid: true},
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	latest, err := s.QueryLatestCodex(1)
	if err != nil {
		t.Fatalf("QueryLatest: %v", err)
	}
	if !latest.ResetCreditsApplicableCount.Valid || latest.ResetCreditsApplicableCount.Value != 1 {
		t.Fatalf("applicable = %#v, want known 1", latest.ResetCreditsApplicableCount)
	}
}

func TestInsertCodexSnapshot_RoundTripsResetCreditsCount(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	known := &api.CodexSnapshot{
		CapturedAt:                 now.Add(-time.Minute),
		AccountID:                  1,
		PlanType:                   "plus",
		ResetCreditsAvailableCount: api.CodexOptionalInt64{Value: 0, Valid: true},
	}
	if _, err := s.InsertCodexSnapshot(known); err != nil {
		t.Fatalf("Insert known: %v", err)
	}
	// A known zero must come back as known zero, not unknown.
	latest, err := s.QueryLatestCodex(1)
	if err != nil {
		t.Fatalf("QueryLatest: %v", err)
	}
	if !latest.ResetCreditsAvailableCount.Valid || latest.ResetCreditsAvailableCount.Value != 0 {
		t.Fatalf("count = %#v, want known 0", latest.ResetCreditsAvailableCount)
	}

	unknown := &api.CodexSnapshot{
		CapturedAt: now,
		AccountID:  1,
		PlanType:   "plus",
	}
	if _, err := s.InsertCodexSnapshot(unknown); err != nil {
		t.Fatalf("Insert unknown: %v", err)
	}
	latest, err = s.QueryLatestCodex(1)
	if err != nil {
		t.Fatalf("QueryLatest second: %v", err)
	}
	if latest.ResetCreditsAvailableCount.Valid {
		t.Fatalf("count = %#v, want unknown when absent", latest.ResetCreditsAvailableCount)
	}

	rangeRows, err := s.QueryCodexRange(1, now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatalf("QueryCodexRange: %v", err)
	}
	if len(rangeRows) != 2 {
		t.Fatalf("range rows = %d, want 2", len(rangeRows))
	}
	if !rangeRows[0].ResetCreditsAvailableCount.Valid || rangeRows[0].ResetCreditsAvailableCount.Value != 0 {
		t.Fatalf("range first count = %#v", rangeRows[0].ResetCreditsAvailableCount)
	}
	if rangeRows[1].ResetCreditsAvailableCount.Valid {
		t.Fatalf("range second count = %#v, want unknown", rangeRows[1].ResetCreditsAvailableCount)
	}
}

func TestQueryCodexResetCreditsNeedingExpiryNotice_SkipsNeverExpiringAndGone(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	expiringAt := now.Add(3 * time.Hour)
	tooFar := now.Add(72 * time.Hour)

	if err := s.SyncCodexResetCredits(1, []CodexResetCreditRecord{
		{CreditID: "soon", Status: "available", ExpiresAt: &expiringAt},
		{CreditID: "never", Status: "available"},
		{CreditID: "later", Status: "available", ExpiresAt: &tooFar},
		{CreditID: "redeemed", Status: "redeemed", ExpiresAt: &expiringAt},
	}, now); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	// Mark "gone" as disappeared.
	if err := s.SyncCodexResetCredits(1, []CodexResetCreditRecord{
		{CreditID: "soon", Status: "available", ExpiresAt: &expiringAt},
		{CreditID: "never", Status: "available"},
		{CreditID: "later", Status: "available", ExpiresAt: &tooFar},
	}, now.Add(time.Minute)); err != nil {
		t.Fatalf("Sync gone: %v", err)
	}

	pending, err := s.QueryCodexResetCreditsNeedingExpiryNotice(1, now, 24*time.Hour)
	if err != nil {
		t.Fatalf("Query needing notice: %v", err)
	}
	if len(pending) != 1 || pending[0].CreditID != "soon" {
		t.Fatalf("pending = %#v, want only 'soon'", pending)
	}
}

// A database created before the applicable/unsupported columns existed gains
// them through the same idempotent migration path, and existing rows keep their
// data with safe defaults.
func TestMigrateSchema_AddsResetCreditColumns(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	// Simulate the pre-follow-up shape. Snapshots have no dependent data here
	// beyond the quota values table, which is empty in this test.
	for _, stmt := range []string{
		`DROP TABLE codex_quota_values`,
		`DROP TABLE codex_snapshots`,
		`CREATE TABLE codex_snapshots (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			captured_at TEXT NOT NULL,
			account_id INTEGER NOT NULL DEFAULT 1,
			plan_type TEXT,
			credits_balance REAL,
			reset_credits_count INTEGER,
			raw_json TEXT,
			quota_count INTEGER DEFAULT 0
		)`,
		`DROP TABLE codex_reset_credit_checks`,
		`CREATE TABLE codex_reset_credit_checks (
			account_id INTEGER PRIMARY KEY,
			status TEXT NOT NULL,
			available_count INTEGER,
			last_checked_at TEXT NOT NULL
		)`,
		`DROP TABLE codex_reset_credits`,
		`CREATE TABLE codex_reset_credits (
			account_id INTEGER NOT NULL DEFAULT 1,
			credit_id TEXT NOT NULL,
			status TEXT NOT NULL,
			reset_type TEXT NOT NULL DEFAULT '',
			title TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL DEFAULT '',
			granted_at TEXT,
			expires_at TEXT,
			first_seen TEXT NOT NULL,
			last_seen TEXT NOT NULL,
			gone_at TEXT,
			notified_at TEXT,
			PRIMARY KEY (account_id, credit_id)
		)`,
	} {
		if _, err := s.db.Exec(stmt); err != nil {
			t.Fatalf("setup %q: %v", stmt, err)
		}
	}

	for i := 0; i < 2; i++ {
		if err := s.migrateSchema(); err != nil {
			t.Fatalf("migrateSchema run %d: %v", i+1, err)
		}
	}

	for _, tc := range []struct{ table, column string }{
		{"codex_reset_credits", "unsupported_by_plan"},
		{"codex_reset_credits", "expiry_unknown"},
		{"codex_reset_credit_checks", "applicable_count"},
		{"codex_snapshots", "applicable_credits_count"},
	} {
		has, err := s.tableHasColumn(tc.table, tc.column)
		if err != nil {
			t.Fatalf("tableHasColumn(%s, %s): %v", tc.table, tc.column, err)
		}
		if !has {
			t.Fatalf("migration did not add %s.%s", tc.table, tc.column)
		}
	}
}

// The parser degrades an expiry it cannot trust (already past on a fast local
// clock, or equal to granted_at) to "unknown". A later sync must not overwrite
// a stored, valid expiry with NULL and flip expiry_unknown on.
func TestSyncCodexResetCredits_KeepsStoredExpiryWhenIncomingIsUnknown(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	expiry := rcTime(t, "2026-10-19T09:00:00Z")
	if err := s.SyncCodexResetCredits(1, []CodexResetCreditRecord{{
		CreditID:  "credit-1",
		Status:    "available",
		ExpiresAt: expiry,
	}}, now); err != nil {
		t.Fatalf("first sync: %v", err)
	}

	// The next fetch reported an available credit with an expiry the parser
	// could not trust, which arrives as nil + ExpiryUnknown.
	if err := s.SyncCodexResetCredits(1, []CodexResetCreditRecord{{
		CreditID:      "credit-1",
		Status:        "available",
		ExpiryUnknown: true,
	}}, now.Add(30*time.Minute)); err != nil {
		t.Fatalf("second sync: %v", err)
	}

	rows, err := s.QueryCodexResetCredits(1)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.ExpiresAt == nil || !row.ExpiresAt.Equal(*expiry) {
		t.Fatalf("ExpiresAt = %v, want the stored valid %v", row.ExpiresAt, expiry)
	}
	if row.ExpiryUnknown {
		t.Fatal("ExpiryUnknown = true, want false: a known expiry was downgraded")
	}
}

// A credit the backend genuinely reports as never-expiring clears the stored
// expiry: only an unknown (untrustworthy) value keeps it.
func TestSyncCodexResetCredits_NeverExpiringClearsStoredExpiry(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	if err := s.SyncCodexResetCredits(1, []CodexResetCreditRecord{{
		CreditID:  "credit-1",
		Status:    "available",
		ExpiresAt: rcTime(t, "2026-10-19T09:00:00Z"),
	}}, now); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if err := s.SyncCodexResetCredits(1, []CodexResetCreditRecord{{
		CreditID: "credit-1",
		Status:   "available",
	}}, now.Add(30*time.Minute)); err != nil {
		t.Fatalf("second sync: %v", err)
	}

	rows, err := s.QueryCodexResetCredits(1)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 1 || rows[0].ExpiresAt != nil || rows[0].ExpiryUnknown {
		t.Fatalf("row = %#v, want a cleared, known never-expiring expiry", rows)
	}
}

// On a merge, UPDATE OR REPLACE keeps the source row's status and last_seen.
// When the target's row is newer, the merged row must keep the target's
// last_seen and that row's status instead.
func TestMergeCodexAccountData_NewerRowWinsStatusAndLastSeen(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	expiry := now.Add(4 * time.Hour)
	targetSeen := now
	sourceSeen := now.Add(-2 * time.Hour)

	if err := s.SyncCodexResetCredits(1, []CodexResetCreditRecord{{
		CreditID:  "shared",
		Status:    "redeemed",
		ExpiresAt: &expiry,
	}}, targetSeen); err != nil {
		t.Fatalf("Sync target: %v", err)
	}
	if err := s.SyncCodexResetCredits(2, []CodexResetCreditRecord{{
		CreditID:  "shared",
		Status:    "available",
		ExpiresAt: &expiry,
	}}, sourceSeen); err != nil {
		t.Fatalf("Sync source: %v", err)
	}

	if err := s.MergeCodexAccountData(1, 2); err != nil {
		t.Fatalf("MergeCodexAccountData: %v", err)
	}

	rows, err := s.QueryCodexResetCredits(1)
	if err != nil {
		t.Fatalf("Query target: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if !row.LastSeen.Equal(targetSeen) {
		t.Fatalf("LastSeen = %v, want the newer target %v", row.LastSeen, targetSeen)
	}
	if row.Status != "redeemed" {
		t.Fatalf("Status = %q, want the status of the newer row (%q)", row.Status, "redeemed")
	}
}

// The rule is symmetric: when the source row is newer, its status and
// last_seen win.
func TestMergeCodexAccountData_NewerSourceRowWins(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	expiry := now.Add(4 * time.Hour)
	targetSeen := now.Add(-2 * time.Hour)
	sourceSeen := now

	if err := s.SyncCodexResetCredits(1, []CodexResetCreditRecord{{
		CreditID:  "shared",
		Status:    "available",
		ExpiresAt: &expiry,
	}}, targetSeen); err != nil {
		t.Fatalf("Sync target: %v", err)
	}
	if err := s.SyncCodexResetCredits(2, []CodexResetCreditRecord{{
		CreditID:  "shared",
		Status:    "redeemed",
		ExpiresAt: &expiry,
	}}, sourceSeen); err != nil {
		t.Fatalf("Sync source: %v", err)
	}

	if err := s.MergeCodexAccountData(1, 2); err != nil {
		t.Fatalf("MergeCodexAccountData: %v", err)
	}

	rows, err := s.QueryCodexResetCredits(1)
	if err != nil {
		t.Fatalf("Query target: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if !rows[0].LastSeen.Equal(sourceSeen) || rows[0].Status != "redeemed" {
		t.Fatalf("row = %#v, want the newer source's redeemed/%v", rows[0], sourceSeen)
	}
}
