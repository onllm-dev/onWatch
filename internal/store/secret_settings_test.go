package store

import (
	"strings"
	"testing"
)

// TestSecretSettings_EncryptedTransparently asserts the settings blobs that
// hold provider credentials are ciphertext in the database but plaintext to
// every caller.
//
// settings.provider_settings is the largest credential store onWatch has: every
// provider API key set from the dashboard, plus the Copilot token, the
// Antigravity CSRF token and the OpenCode auth cookie. vapid_keys holds the Web
// Push private key. Both were cleartext.
func TestSecretSettings_EncryptedTransparently(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	s.SetSecretCipher(&fakeCipher{prefix: "enc:"})

	cases := map[string]string{
		"provider_settings": `{"minimax":{"api_key":"mm-SECRET"},"copilot":{"token":"ghp-SECRET"}}`,
		"vapid_keys":        `{"private_key":"VAPID-PRIVATE-SECRET"}`,
		"gemini_tokens":     `{"refresh_token":"1//SECRET"}`,
	}

	for key, value := range cases {
		if err := s.SetSetting(key, value); err != nil {
			t.Fatalf("SetSetting(%s): %v", key, err)
		}

		// The column must not hold the secret.
		var raw string
		if err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&raw); err != nil {
			t.Fatalf("read %s column: %v", key, err)
		}
		if strings.Contains(raw, "SECRET") {
			t.Errorf("%s is cleartext in the database: %s", key, raw)
		}

		// Callers see the original.
		got, err := s.GetSetting(key)
		if err != nil {
			t.Fatalf("GetSetting(%s): %v", key, err)
		}
		if got != value {
			t.Errorf("GetSetting(%s) = %q, want %q", key, got, value)
		}
	}
}

// TestSecretSettings_SMTPIsNotDoubleEncrypted asserts the smtp blob is left to
// the field-level encryption that already handles its password.
//
// Encrypting the whole blob here would hide the JSON from ConfigureSMTP and
// break mail delivery.
func TestSecretSettings_SMTPIsNotDoubleEncrypted(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	s.SetSecretCipher(&fakeCipher{prefix: "enc:"})

	const smtpJSON = `{"host":"smtp.example.com","username":"ops@example.com","password":"already-encrypted"}`
	if err := s.SetSetting("smtp", smtpJSON); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	var raw string
	if err := s.db.QueryRow(`SELECT value FROM settings WHERE key = 'smtp'`).Scan(&raw); err != nil {
		t.Fatalf("read smtp column: %v", err)
	}
	if raw != smtpJSON {
		t.Errorf("smtp blob was transformed: %s", raw)
	}
}

// TestSecretSettings_OrdinarySettingsUntouched asserts display preferences are
// stored as-is, so they remain greppable and debuggable.
func TestSecretSettings_OrdinarySettingsUntouched(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	s.SetSecretCipher(&fakeCipher{prefix: "enc:"})

	if err := s.SetSetting("timezone", "Asia/Kolkata"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	var raw string
	if err := s.db.QueryRow(`SELECT value FROM settings WHERE key = 'timezone'`).Scan(&raw); err != nil {
		t.Fatalf("read timezone: %v", err)
	}
	if raw != "Asia/Kolkata" {
		t.Errorf("ordinary setting was encrypted: %s", raw)
	}
}

// TestSecretSettings_ReadLegacyPlaintext asserts a database written before
// encryption keeps working.
func TestSecretSettings_ReadLegacyPlaintext(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	const legacy = `{"minimax":{"api_key":"mm-LEGACY"}}`
	if _, err := s.db.Exec(`INSERT INTO settings (key, value) VALUES ('provider_settings', ?)`, legacy); err != nil {
		t.Fatalf("seed: %v", err)
	}

	s.SetSecretCipher(&fakeCipher{prefix: "enc:"})

	got, err := s.GetSetting("provider_settings")
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}
	if got != legacy {
		t.Errorf("legacy plaintext = %q, want it returned unchanged", got)
	}
}

// TestReKeySecrets_SurvivesAPasswordChange asserts every stored secret is still
// readable after the dashboard password changes.
//
// This is the failure mode that would be worst in practice: the provider keys
// become undecryptable, polling stops, and nothing points at the password
// change as the cause.
func TestReKeySecrets_SurvivesAPasswordChange(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	s.SetSecretCipher(&fakeCipher{prefix: "old:"})

	const providerJSON = `{"minimax":{"api_key":"mm-SECRET"}}`
	if err := s.SetSetting("provider_settings", providerJSON); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if err := s.SaveGeminiTokens("ya29.ACCESS", "1//REFRESH", 42); err != nil {
		t.Fatalf("SaveGeminiTokens: %v", err)
	}
	acct, err := s.GetOrCreateProviderAccount("minimax", "default")
	if err != nil {
		t.Fatalf("GetOrCreateProviderAccount: %v", err)
	}
	if err := s.UpdateProviderAccountMetadata(acct.ID, `{"api_key":"mm-ACCOUNT"}`); err != nil {
		t.Fatalf("UpdateProviderAccountMetadata: %v", err)
	}

	if err := s.ReKeySecrets(&fakeCipher{prefix: "new:"}); err != nil {
		t.Fatalf("ReKeySecrets: %v", err)
	}

	// Everything re-keyed...
	for _, key := range []string{"provider_settings", "gemini_tokens"} {
		var raw string
		if err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&raw); err != nil {
			t.Fatalf("read %s: %v", key, err)
		}
		if !strings.HasPrefix(raw, "new:") {
			t.Errorf("%s was not re-keyed: %s", key, raw)
		}
	}

	// ...and still readable.
	if got, _ := s.GetSetting("provider_settings"); got != providerJSON {
		t.Errorf("provider_settings unreadable after re-key: %q", got)
	}
	access, refresh, _, err := s.LoadGeminiTokens()
	if err != nil || access != "ya29.ACCESS" || refresh != "1//REFRESH" {
		t.Errorf("gemini tokens unreadable after re-key: (%q, %q, %v)", access, refresh, err)
	}
	accounts, err := s.QueryActiveProviderAccounts("minimax")
	if err != nil {
		t.Fatalf("QueryActiveProviderAccounts: %v", err)
	}
	if len(accounts) != 1 || !strings.Contains(accounts[0].Metadata, "mm-ACCOUNT") {
		t.Errorf("account metadata unreadable after re-key: %+v", accounts)
	}
}

// TestReKeySecrets_LeavesSMTPAlone asserts the re-key does not touch the blob
// whose password is encrypted field-level by the web layer.
func TestReKeySecrets_LeavesSMTPAlone(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	s.SetSecretCipher(&fakeCipher{prefix: "old:"})
	const smtpJSON = `{"host":"smtp.example.com","password":"field-level-ciphertext"}`
	if err := s.SetSetting("smtp", smtpJSON); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	if err := s.ReKeySecrets(&fakeCipher{prefix: "new:"}); err != nil {
		t.Fatalf("ReKeySecrets: %v", err)
	}

	var raw string
	if err := s.db.QueryRow(`SELECT value FROM settings WHERE key = 'smtp'`).Scan(&raw); err != nil {
		t.Fatalf("read smtp: %v", err)
	}
	if raw != smtpJSON {
		t.Errorf("smtp blob was re-keyed: %s", raw)
	}
}
