package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// TestChangePassword_KeepsStoredCredentialsReadable is the end-to-end guard on
// the riskiest path in the encryption work.
//
// Stored credentials are encrypted under a key derived from the dashboard
// password, so changing that password has to re-key them in the same request.
// If it does not, every provider API key becomes undecryptable, polling stops,
// and nothing points back at the password change as the cause. Unit tests cover
// the store's re-key; this covers the handler actually calling it, with the
// real cipher rather than a test double.
func TestChangePassword_KeepsStoredCredentialsReadable(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()

	oldHash := legacyHashPassword("oldpass")
	sessions := NewSessionStore("admin", oldHash, s)
	s.UpsertUser("admin", oldHash)

	// The cipher the daemon installs at start-up.
	s.SetSecretCipher(NewStoreSecretCipher(DeriveEncryptionKey(oldHash, nil)))

	const providerJSON = `{"minimax":{"api_key":"mm-SECRET"},"copilot":{"token":"ghp-SECRET"}}`
	if err := s.SetSetting("provider_settings", providerJSON); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if err := s.SaveGeminiTokens("ya29.ACCESS", "1//REFRESH", 99); err != nil {
		t.Fatalf("SaveGeminiTokens: %v", err)
	}
	acct, err := s.GetOrCreateProviderAccount("minimax", "default")
	if err != nil {
		t.Fatalf("GetOrCreateProviderAccount: %v", err)
	}
	if err := s.UpdateProviderAccountMetadata(acct.ID, `{"api_key":"mm-ACCOUNT"}`); err != nil {
		t.Fatalf("UpdateProviderAccountMetadata: %v", err)
	}

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, sessions, cfg)

	req := httptest.NewRequest(http.MethodPut, "/api/password",
		strings.NewReader(`{"current_password":"oldpass","new_password":"a-new-password"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ChangePassword(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("ChangePassword = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}

	// Everything must still be readable under the new key.
	got, err := s.GetSetting("provider_settings")
	if err != nil {
		t.Fatalf("GetSetting after password change: %v", err)
	}
	if got != providerJSON {
		t.Errorf("provider_settings after password change = %q, want it unchanged and readable", got)
	}

	access, refresh, _, err := s.LoadGeminiTokens()
	if err != nil || access != "ya29.ACCESS" || refresh != "1//REFRESH" {
		t.Errorf("gemini tokens after password change = (%q, %q, %v)", access, refresh, err)
	}

	accounts, err := s.QueryActiveProviderAccounts("minimax")
	if err != nil {
		t.Fatalf("QueryActiveProviderAccounts: %v", err)
	}
	if len(accounts) != 1 || !strings.Contains(accounts[0].Metadata, "mm-ACCOUNT") {
		t.Errorf("account metadata after password change: %+v", accounts)
	}
}

// TestChangePassword_StaleCipherCannotRead asserts the re-key actually changed
// the key, rather than appearing to work because nothing was encrypted.
func TestChangePassword_StaleCipherCannotRead(t *testing.T) {
	t.Parallel()
	s, _ := store.New(":memory:")
	defer s.Close()

	oldHash := legacyHashPassword("oldpass")
	sessions := NewSessionStore("admin", oldHash, s)
	s.UpsertUser("admin", oldHash)
	s.SetSecretCipher(NewStoreSecretCipher(DeriveEncryptionKey(oldHash, nil)))

	if err := s.SetSetting("provider_settings", `{"minimax":{"api_key":"mm-SECRET"}}`); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	cfg := createTestConfigWithSynthetic()
	h := NewHandler(s, nil, nil, sessions, cfg)

	req := httptest.NewRequest(http.MethodPut, "/api/password",
		strings.NewReader(`{"current_password":"oldpass","new_password":"a-new-password"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ChangePassword(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("ChangePassword = %d, want 200", rr.Code)
	}

	// Put the old cipher back: the stored value is now under the new key, so
	// this must fail to decrypt. If it succeeds, the value was never encrypted.
	s.SetSecretCipher(NewStoreSecretCipher(DeriveEncryptionKey(oldHash, nil)))
	got, _ := s.GetSetting("provider_settings")
	if strings.Contains(got, "mm-SECRET") {
		t.Error("the old key still decrypts the value, so the re-key did not change anything")
	}
}
