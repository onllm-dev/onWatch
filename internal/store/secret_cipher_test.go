package store

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
)

// fakeCipher is a reversible stand-in for AES-GCM. The real cipher lives in
// internal/notify; the store only needs to know that it delegates correctly.
//
// It base64-encodes rather than merely tagging the value, so that a test
// asserting "the secret is not in the stored column" is actually testing
// something - a passthrough fake would let a broken implementation pass.
type fakeCipher struct {
	prefix string
}

func (c *fakeCipher) Encrypt(plaintext string) (string, error) {
	return c.prefix + base64.StdEncoding.EncodeToString([]byte(plaintext)), nil
}

func (c *fakeCipher) Decrypt(ciphertext string) (string, error) {
	if !strings.HasPrefix(ciphertext, c.prefix) {
		return "", errNotEncryptedForTest
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(ciphertext, c.prefix))
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

func (c *fakeCipher) IsEncrypted(value string) bool {
	return strings.HasPrefix(value, c.prefix)
}

var errNotEncryptedForTest = errTestSentinel("not encrypted with this key")

type errTestSentinel string

func (e errTestSentinel) Error() string { return string(e) }

// TestGeminiTokens_EncryptedAtRest asserts the OAuth access and refresh tokens
// are not sitting in the database in cleartext.
//
// A refresh token is a long-lived credential for the user's Google account, so
// it is the single most valuable thing onWatch stores (GDPR Art. 32(1)(a)).
func TestGeminiTokens_EncryptedAtRest(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	s.SetSecretCipher(&fakeCipher{prefix: "enc:"})

	if err := s.SaveGeminiTokens("ya29.ACCESS", "1//REFRESH", 1789000000); err != nil {
		t.Fatalf("SaveGeminiTokens: %v", err)
	}

	// What landed in the settings table must not contain the tokens. This reads
	// the column directly: GetSetting decrypts this key transparently, so going
	// through it would test nothing.
	var raw string
	if err := s.db.QueryRow(`SELECT value FROM settings WHERE key = 'gemini_tokens'`).Scan(&raw); err != nil {
		t.Fatalf("read gemini_tokens column: %v", err)
	}
	for _, secret := range []string{"ya29.ACCESS", "1//REFRESH"} {
		if strings.Contains(raw, secret) {
			t.Errorf("stored value contains %q in cleartext: %s", secret, raw)
		}
	}
	if raw == "" {
		t.Fatal("nothing was stored")
	}

	// It must still round-trip, or polling breaks.
	access, refresh, expires, err := s.LoadGeminiTokens()
	if err != nil {
		t.Fatalf("LoadGeminiTokens: %v", err)
	}
	if access != "ya29.ACCESS" || refresh != "1//REFRESH" || expires != 1789000000 {
		t.Errorf("round-trip = (%q, %q, %d), want the original values", access, refresh, expires)
	}
}

// TestGeminiTokens_ReadsLegacyPlaintext asserts an existing install keeps
// working. Tokens written by an earlier version are cleartext, and refusing to
// read them would silently break Gemini polling on upgrade.
func TestGeminiTokens_ReadsLegacyPlaintext(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	// Written the old way, before encryption existed.
	if err := s.SetSetting("gemini_tokens",
		`{"access_token":"ya29.OLD","refresh_token":"1//OLD","expires_at":1789000000}`); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	s.SetSecretCipher(&fakeCipher{prefix: "enc:"})

	access, refresh, _, err := s.LoadGeminiTokens()
	if err != nil {
		t.Fatalf("LoadGeminiTokens on legacy plaintext: %v", err)
	}
	if access != "ya29.OLD" || refresh != "1//OLD" {
		t.Errorf("legacy read = (%q, %q), want the stored values", access, refresh)
	}
}

// TestGeminiTokens_WorkWithoutCipher asserts the store still functions when no
// cipher is configured, which is the case before the admin password hash is
// known and in tests.
func TestGeminiTokens_WorkWithoutCipher(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	if err := s.SaveGeminiTokens("a", "r", 1); err != nil {
		t.Fatalf("SaveGeminiTokens: %v", err)
	}
	access, refresh, _, err := s.LoadGeminiTokens()
	if err != nil {
		t.Fatalf("LoadGeminiTokens: %v", err)
	}
	if access != "a" || refresh != "r" {
		t.Errorf("round-trip without a cipher = (%q, %q)", access, refresh)
	}
}

// TestProviderAccountMetadata_EncryptedAtRest covers the other cleartext
// credential: provider_accounts.metadata holds a MiniMax API key.
func TestProviderAccountMetadata_EncryptedAtRest(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	s.SetSecretCipher(&fakeCipher{prefix: "enc:"})

	acct, err := s.GetOrCreateProviderAccount("minimax", "default")
	if err != nil {
		t.Fatalf("GetOrCreateProviderAccount: %v", err)
	}
	id := acct.ID
	if err := s.UpdateProviderAccountMetadata(id, `{"api_key":"mm-SECRET","region":"global"}`); err != nil {
		t.Fatalf("UpdateProviderAccountMetadata: %v", err)
	}

	var stored string
	if err := s.db.QueryRow(`SELECT metadata FROM provider_accounts WHERE id = ?`, id).Scan(&stored); err != nil {
		t.Fatalf("read metadata column: %v", err)
	}
	if strings.Contains(stored, "mm-SECRET") {
		t.Errorf("metadata holds the API key in cleartext: %s", stored)
	}

	// Readers get the plaintext back.
	accounts, err := s.QueryActiveProviderAccounts("minimax")
	if err != nil {
		t.Fatalf("QueryActiveProviderAccounts: %v", err)
	}
	if len(accounts) != 1 {
		t.Fatalf("got %d accounts, want 1", len(accounts))
	}
	if !strings.Contains(accounts[0].Metadata, "mm-SECRET") {
		t.Errorf("metadata did not decrypt: %s", accounts[0].Metadata)
	}

	// The same for the other read path.
	all, err := s.QueryProviderAccounts("minimax")
	if err != nil {
		t.Fatalf("QueryProviderAccounts: %v", err)
	}
	if len(all) != 1 || !strings.Contains(all[0].Metadata, "mm-SECRET") {
		t.Errorf("QueryProviderAccounts metadata did not decrypt: %+v", all)
	}
}

// TestProviderAccountMetadata_ReadsLegacyPlaintext asserts existing rows still
// load after the upgrade.
func TestProviderAccountMetadata_ReadsLegacyPlaintext(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	acct, err := s.GetOrCreateProviderAccount("minimax", "legacy")
	if err != nil {
		t.Fatalf("GetOrCreateProviderAccount: %v", err)
	}
	id := acct.ID
	// Written before encryption existed.
	if _, err := s.db.Exec(`UPDATE provider_accounts SET metadata = ? WHERE id = ?`,
		`{"api_key":"mm-LEGACY"}`, id); err != nil {
		t.Fatalf("seed legacy metadata: %v", err)
	}

	s.SetSecretCipher(&fakeCipher{prefix: "enc:"})

	accounts, err := s.QueryActiveProviderAccounts("minimax")
	if err != nil {
		t.Fatalf("QueryActiveProviderAccounts: %v", err)
	}
	if len(accounts) != 1 || !strings.Contains(accounts[0].Metadata, "mm-LEGACY") {
		t.Errorf("legacy plaintext metadata was not returned: %+v", accounts)
	}
}

// TestExport_RedactsEncryptedSecretsToo asserts the export still redacts these
// values rather than handing back ciphertext that looks like data.
func TestExport_RedactsEncryptedSecretsToo(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	s.SetSecretCipher(&fakeCipher{prefix: "enc:"})
	if err := s.SaveGeminiTokens("ya29.ACCESS", "1//REFRESH", 1); err != nil {
		t.Fatalf("SaveGeminiTokens: %v", err)
	}

	var buf strings.Builder
	if err := s.ExportAll(context.Background(), &buf); err != nil {
		t.Fatalf("ExportAll: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "ya29.ACCESS") || strings.Contains(out, "1//REFRESH") {
		t.Error("export leaked the Gemini tokens")
	}
	if !strings.Contains(out, ExportRedactedMarker) {
		t.Error("export should mark the redacted setting")
	}
}

// TestReKeyProviderAccountMetadata_SurvivesAPasswordChange asserts the metadata
// is still readable after the dashboard password changes.
//
// This is the failure that would be worst in practice: the keys become
// undecryptable, polling stops, and nothing obviously points at the password
// change as the cause.
func TestReKeyProviderAccountMetadata_SurvivesAPasswordChange(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	oldCipher := &fakeCipher{prefix: "old:"}
	newCipher := &fakeCipher{prefix: "new:"}

	s.SetSecretCipher(oldCipher)
	acct, err := s.GetOrCreateProviderAccount("minimax", "default")
	if err != nil {
		t.Fatalf("GetOrCreateProviderAccount: %v", err)
	}
	if err := s.UpdateProviderAccountMetadata(acct.ID, `{"api_key":"mm-SECRET"}`); err != nil {
		t.Fatalf("UpdateProviderAccountMetadata: %v", err)
	}

	if err := s.ReKeyProviderAccountMetadata(newCipher); err != nil {
		t.Fatalf("ReKeyProviderAccountMetadata: %v", err)
	}

	// Stored under the new key...
	var stored string
	if err := s.db.QueryRow(`SELECT metadata FROM provider_accounts WHERE id = ?`, acct.ID).Scan(&stored); err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	if !strings.HasPrefix(stored, "new:") {
		t.Errorf("metadata was not re-keyed: %s", stored)
	}
	if strings.Contains(stored, "mm-SECRET") {
		t.Error("re-keyed metadata holds the key in cleartext")
	}

	// ...and still readable.
	accounts, err := s.QueryActiveProviderAccounts("minimax")
	if err != nil {
		t.Fatalf("QueryActiveProviderAccounts: %v", err)
	}
	if len(accounts) != 1 || !strings.Contains(accounts[0].Metadata, "mm-SECRET") {
		t.Errorf("metadata unreadable after re-key: %+v", accounts)
	}
}

// TestReKeyProviderAccountMetadata_HandlesLegacyPlaintext asserts a row written
// before encryption existed is picked up and encrypted by the first re-key.
func TestReKeyProviderAccountMetadata_HandlesLegacyPlaintext(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	acct, err := s.GetOrCreateProviderAccount("minimax", "legacy")
	if err != nil {
		t.Fatalf("GetOrCreateProviderAccount: %v", err)
	}
	if _, err := s.db.Exec(`UPDATE provider_accounts SET metadata = ? WHERE id = ?`,
		`{"api_key":"mm-LEGACY"}`, acct.ID); err != nil {
		t.Fatalf("seed legacy metadata: %v", err)
	}

	if err := s.ReKeyProviderAccountMetadata(&fakeCipher{prefix: "new:"}); err != nil {
		t.Fatalf("ReKeyProviderAccountMetadata: %v", err)
	}

	var stored string
	if err := s.db.QueryRow(`SELECT metadata FROM provider_accounts WHERE id = ?`, acct.ID).Scan(&stored); err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	if strings.Contains(stored, "mm-LEGACY") {
		t.Errorf("legacy plaintext was not encrypted by the re-key: %s", stored)
	}

	accounts, err := s.QueryActiveProviderAccounts("minimax")
	if err != nil {
		t.Fatalf("QueryActiveProviderAccounts: %v", err)
	}
	if len(accounts) != 1 || !strings.Contains(accounts[0].Metadata, "mm-LEGACY") {
		t.Errorf("metadata unreadable after re-key: %+v", accounts)
	}
}
