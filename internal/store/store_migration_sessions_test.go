package store

import (
	"strings"
	"testing"
	"time"
)

func TestStore_MigrateSessionsToUsageBased_DoneFlagShortCircuits(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	if err := s.CreateSession("keep-me", time.Now().UTC(), 60, "synthetic"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := s.SetSetting("session_migration_v2", "done"); err != nil {
		t.Fatalf("SetSetting(done): %v", err)
	}

	if err := s.MigrateSessionsToUsageBased(5 * time.Minute); err != nil {
		t.Fatalf("MigrateSessionsToUsageBased(done): %v", err)
	}

	history, err := s.QuerySessionHistory("synthetic")
	if err != nil {
		t.Fatalf("QuerySessionHistory: %v", err)
	}
	if len(history) != 1 || history[0].ID != "keep-me" {
		t.Fatalf("expected original session to remain, got %+v", history)
	}
}

func TestStore_MigrateSessionsToUsageBased_ProviderMigrationErrors(t *testing.T) {
	t.Parallel()
	t.Run("synthetic migration error", func(t *testing.T) {
		s, err := New(":memory:")
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		defer s.Close()

		if _, err := s.db.Exec(`DROP TABLE quota_snapshots`); err != nil {
			t.Fatalf("drop quota_snapshots: %v", err)
		}

		err = s.MigrateSessionsToUsageBased(5 * time.Minute)
		if err == nil || !strings.Contains(err.Error(), "synthetic") {
			t.Fatalf("MigrateSessionsToUsageBased(synthetic) = %v", err)
		}
	})

	t.Run("zai migration error", func(t *testing.T) {
		s, err := New(":memory:")
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		defer s.Close()

		if _, err := s.db.Exec(`DROP TABLE zai_snapshots`); err != nil {
			t.Fatalf("drop zai_snapshots: %v", err)
		}

		err = s.MigrateSessionsToUsageBased(5 * time.Minute)
		if err == nil || !strings.Contains(err.Error(), "zai") {
			t.Fatalf("MigrateSessionsToUsageBased(zai) = %v", err)
		}
	})

	t.Run("anthropic migration error", func(t *testing.T) {
		s, err := New(":memory:")
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		defer s.Close()

		if _, err := s.db.Exec(`DROP TABLE anthropic_snapshots`); err != nil {
			t.Fatalf("drop anthropic_snapshots: %v", err)
		}

		err = s.MigrateSessionsToUsageBased(5 * time.Minute)
		if err == nil || !strings.Contains(err.Error(), "anthropic") {
			t.Fatalf("MigrateSessionsToUsageBased(anthropic) = %v", err)
		}
	})
}

func TestStore_MigrateProviderSessions_ClosedDB(t *testing.T) {
	t.Parallel()
	s := closedStore(t)

	if err := s.migrateSyntheticSessions(5 * time.Minute); err == nil {
		t.Fatal("expected error from migrateSyntheticSessions on closed DB")
	}
	if err := s.migrateZaiSessions(5 * time.Minute); err == nil {
		t.Fatal("expected error from migrateZaiSessions on closed DB")
	}
	if err := s.migrateAnthropicSessions(5 * time.Minute); err == nil {
		t.Fatal("expected error from migrateAnthropicSessions on closed DB")
	}
}
