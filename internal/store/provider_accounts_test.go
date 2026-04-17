package store

import (
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

func TestProviderAccountsLifecycleAndCodexAccountQueries(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Second)
	for _, accountID := range []int64{DefaultCodexAccountID, 2} {
		_, err := s.InsertCodexSnapshot(&api.CodexSnapshot{
			CapturedAt: now.Add(time.Duration(accountID) * time.Minute),
			AccountID:  accountID,
			PlanType:   "pro",
			Quotas: []api.CodexQuota{
				{Name: "five_hour", Utilization: 25},
			},
		})
		if err != nil {
			t.Fatalf("InsertCodexSnapshot(%d): %v", accountID, err)
		}
	}

	codexAccounts, err := s.QueryCodexAccounts()
	if err != nil {
		t.Fatalf("QueryCodexAccounts: %v", err)
	}
	if len(codexAccounts) != 2 || codexAccounts[0] != DefaultCodexAccountID || codexAccounts[1] != 2 {
		t.Fatalf("QueryCodexAccounts = %v, want [1 2]", codexAccounts)
	}

	defaultAcc, err := s.GetOrCreateProviderAccount("codex", "default")
	if err != nil {
		t.Fatalf("GetOrCreateProviderAccount(default): %v", err)
	}
	if defaultAcc == nil || defaultAcc.Name != "default" {
		t.Fatalf("default account = %+v", defaultAcc)
	}

	workAcc, err := s.GetOrCreateProviderAccount("codex", "work")
	if err != nil {
		t.Fatalf("GetOrCreateProviderAccount(work): %v", err)
	}
	if workAcc.ID != defaultAcc.ID || workAcc.Name != "work" {
		t.Fatalf("expected default account to be renamed to work, got default=%+v work=%+v", defaultAcc, workAcc)
	}

	workAccAgain, err := s.GetOrCreateProviderAccount("codex", "work")
	if err != nil {
		t.Fatalf("GetOrCreateProviderAccount(work again): %v", err)
	}
	if workAccAgain.ID != workAcc.ID {
		t.Fatalf("existing work account ID = %d, want %d", workAccAgain.ID, workAcc.ID)
	}

	personalAcc, err := s.GetOrCreateProviderAccount("codex", "personal")
	if err != nil {
		t.Fatalf("GetOrCreateProviderAccount(personal): %v", err)
	}
	if personalAcc.ID == workAcc.ID {
		t.Fatalf("expected personal account to be created separately, got shared ID %d", personalAcc.ID)
	}

	providerAccounts, err := s.QueryProviderAccounts("codex")
	if err != nil {
		t.Fatalf("QueryProviderAccounts: %v", err)
	}
	if len(providerAccounts) != 2 {
		t.Fatalf("provider account count = %d, want 2", len(providerAccounts))
	}
	if providerAccounts[0].Name != "work" || providerAccounts[1].Name != "personal" {
		t.Fatalf("provider accounts = %+v", providerAccounts)
	}

	byID, err := s.GetProviderAccountByID(personalAcc.ID)
	if err != nil {
		t.Fatalf("GetProviderAccountByID(existing): %v", err)
	}
	if byID == nil || byID.Name != "personal" || byID.Provider != "codex" {
		t.Fatalf("GetProviderAccountByID(existing) = %+v", byID)
	}

	missing, err := s.GetProviderAccountByID(999999)
	if err != nil {
		t.Fatalf("GetProviderAccountByID(missing): %v", err)
	}
	if missing != nil {
		t.Fatalf("expected nil for missing provider account, got %+v", missing)
	}
}
