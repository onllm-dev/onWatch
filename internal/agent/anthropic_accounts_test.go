package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

func TestNormalizeAnthropicAccountName(t *testing.T) {
	cases := map[string]struct {
		want string
		ok   bool
	}{
		"WORK":       {"work", true},
		" personal ": {"personal", true},
		"team_2":     {"team_2", true},
		"a-b":        {"a-b", true},
		"":           {"", false},
		"default":    {"", false},
		"DEFAULT":    {"", false},
		"../x":       {"", false},
		"with space": {"", false},
		"_lead":      {"", false},
	}
	for in, tc := range cases {
		got, ok := NormalizeAnthropicAccountName(in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("NormalizeAnthropicAccountName(%q) = %q,%v want %q,%v", in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestSeedAnthropicProfilesFromEnv_WritesAndPreservesRotation(t *testing.T) {
	dir := t.TempDir()
	env := []string{
		"ANTHROPIC_TOKEN=primary-should-be-ignored",
		"ANTHROPIC_TOKEN_WORK=work-access",
		"ANTHROPIC_REFRESH_TOKEN_WORK=work-refresh",
		"ANTHROPIC_TOKEN_SIDE=side-access",
		"ANTHROPIC_TOKEN_BAD NAME=x",
		"ANTHROPIC_TOKEN_EMPTY=",
		"ANTHROPIC_SOURCE=auto",
	}
	names := SeedAnthropicProfilesFromEnv(dir, env, nil)
	if len(names) != 2 || names[0] != "side" || names[1] != "work" {
		t.Fatalf("names = %v, want [side work]", names)
	}

	workPath := filepath.Join(dir, "work.json")
	creds := ReadAnthropicProfileCredentials(workPath)
	if creds == nil || creds.AccessToken != "work-access" || creds.RefreshToken != "work-refresh" {
		t.Fatalf("unexpected work creds: %+v", creds)
	}
	if !creds.ExpiresAt.IsZero() {
		t.Fatalf("seeded expiry should be unknown, got %v", creds.ExpiresAt)
	}
	if side := ReadAnthropicProfileCredentials(filepath.Join(dir, "side.json")); side == nil || side.RefreshToken != "" {
		t.Fatalf("unexpected side creds: %+v", side)
	}

	// Simulate an OAuth rotation, then a restart with the same env.
	if err := WriteAnthropicProfileCredentials(workPath, "rotated-access", "rotated-refresh", 3600); err != nil {
		t.Fatal(err)
	}
	SeedAnthropicProfilesFromEnv(dir, env, nil)
	creds = ReadAnthropicProfileCredentials(workPath)
	if creds.AccessToken != "rotated-access" || creds.RefreshToken != "rotated-refresh" {
		t.Fatalf("restart must keep rotated tokens, got %+v", creds)
	}
	if creds.ExpiresAt.IsZero() || creds.ExpiresIn <= 0 {
		t.Fatalf("rotated expiry should be set, got %v", creds.ExpiresAt)
	}

	// Changing the env value re-seeds the file.
	env[1] = "ANTHROPIC_TOKEN_WORK=new-access"
	SeedAnthropicProfilesFromEnv(dir, env, nil)
	creds = ReadAnthropicProfileCredentials(workPath)
	if creds.AccessToken != "new-access" || creds.RefreshToken != "work-refresh" {
		t.Fatalf("changed env should re-seed, got %+v", creds)
	}
}

func TestWriteAnthropicProfileCredentials_PreservesOtherFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "acct.json")
	orig := `{"claudeAiOauth":{"accessToken":"a","refreshToken":"r","expiresAt":1,"scopes":["user:inference"],"subscriptionType":"max"},"other":42}`
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteAnthropicProfileCredentials(path, "a2", "r2", 60); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["other"].(float64) != 42 {
		t.Fatalf("top-level field lost: %s", data)
	}
	oauth := raw["claudeAiOauth"].(map[string]interface{})
	if oauth["accessToken"] != "a2" || oauth["refreshToken"] != "r2" || oauth["subscriptionType"] != "max" {
		t.Fatalf("oauth not updated correctly: %s", data)
	}
}

func TestLoadAnthropicProfiles_FiltersInvalid(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("work.json", `{"claudeAiOauth":{"accessToken":"x"}}`)
	write("alpha.json", `{"claudeAiOauth":{"accessToken":"y"}}`)
	write("empty.json", `{"claudeAiOauth":{"accessToken":""}}`)
	write("Upper.json", `{"claudeAiOauth":{"accessToken":"z"}}`)
	write("default.json", `{"claudeAiOauth":{"accessToken":"z"}}`)
	write("broken.json", `{`)
	write("notes.txt", `x`)

	profiles := LoadAnthropicProfiles(dir, nil)
	if len(profiles) != 2 || profiles[0].Name != "alpha" || profiles[1].Name != "work" {
		t.Fatalf("profiles = %+v", profiles)
	}
	if LoadAnthropicProfiles(filepath.Join(dir, "missing"), nil) != nil {
		t.Fatal("missing dir should yield nil")
	}
}

// TestAnthropicAgent_ExtraAccountStoresSeparately verifies that an agent scoped
// to an extra account writes snapshots and cycles under its own account_id and
// never touches the primary account's data.
func TestAnthropicAgent_ExtraAccountStoresSeparately(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"five_hour":{"utilization":42,"resets_at":"2099-01-01T00:00:00Z"}}`))
	}))
	defer srv.Close()

	db, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	acc, err := db.CreateOrRestoreProviderAccount("anthropic", "work")
	if err != nil {
		t.Fatal(err)
	}

	client := api.NewAnthropicClient("work-token", slog.Default(), api.WithAnthropicBaseURL(srv.URL))
	tr := tracker.NewAnthropicTrackerForAccount(db, nil, acc.ID)
	ag := NewAnthropicAgent(client, db, tr, time.Minute, nil, nil)
	ag.SetAccount(acc.ID, "work")
	ag.poll(context.Background())

	got, err := db.QueryLatestAnthropicForAccount(acc.ID)
	if err != nil || got == nil || len(got.Quotas) != 1 || got.Quotas[0].Utilization != 42 {
		t.Fatalf("extra account snapshot missing: %+v err=%v", got, err)
	}
	if primary, _ := db.QueryLatestAnthropic(); primary != nil {
		t.Fatalf("primary account must stay empty, got %+v", primary)
	}
	if c, _ := db.QueryActiveAnthropicCycleForAccount(acc.ID, "five_hour"); c == nil {
		t.Fatal("extra account cycle missing")
	}
	if c, _ := db.QueryActiveAnthropicCycle("five_hour"); c != nil {
		t.Fatal("primary account must not get a cycle")
	}
}

// TestAnthropicAgent_ExtraAccountPersistsRotatedTokensToOwnWriter checks that
// an OAuth refresh for an extra account goes to its own writer.
func TestAnthropicAgent_ExtraAccountPersistsRotatedTokensToOwnWriter(t *testing.T) {
	ag := NewAnthropicAgent(api.NewAnthropicClient("old", slog.Default()), nil, nil, time.Minute, nil, nil)
	var gotAccess, gotRefresh string
	ag.SetCredentialsWriter(func(a, r string, _ int) error {
		gotAccess, gotRefresh = a, r
		return nil
	})
	ag.applyRefreshedTokens(&api.OAuthTokenResponse{AccessToken: "new-a", RefreshToken: "new-r", ExpiresIn: 3600}, "old")
	if gotAccess != "new-a" || gotRefresh != "new-r" {
		t.Fatalf("writer got %q/%q", gotAccess, gotRefresh)
	}
	if ag.lastToken != "new-a" {
		t.Fatalf("in-memory token not updated")
	}
}

func TestAnthropicAccountsRunner_RunsAllAndStops(t *testing.T) {
	db, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mk := func() *AnthropicAgent {
		ag := NewAnthropicAgent(api.NewAnthropicClient("t", slog.Default()), db, nil, time.Hour, nil, nil)
		ag.SetPollingCheck(func() bool { return false })
		return ag
	}
	r := &AnthropicAccountsRunner{Primary: nil, Extras: []*AnthropicAgent{mk(), mk()}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = r.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not stop")
	}
}
