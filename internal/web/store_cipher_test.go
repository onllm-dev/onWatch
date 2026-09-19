package web

import (
	"strings"
	"testing"
)

// TestStoreSecretCipher_MethodsAgree asserts Encrypt, Decrypt and IsEncrypted
// share one convention.
//
// They did not: Encrypt used the raw form while IsEncrypted looked for the
// "enc:" prefix, so every encrypted value was reported as plaintext. That
// double-encrypted on each write and returned raw ciphertext to callers on
// each read, which broke Gemini token loading outright. The three methods are
// only useful together, so they are tested together.
func TestStoreSecretCipher_MethodsAgree(t *testing.T) {
	t.Parallel()

	c := NewStoreSecretCipher(DeriveEncryptionKey(legacyHashPassword("a-password"), nil))
	const plaintext = `{"minimax":{"api_key":"mm-SECRET"}}`

	ciphertext, err := c.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if strings.Contains(ciphertext, "mm-SECRET") {
		t.Error("ciphertext still contains the secret")
	}
	if !c.IsEncrypted(ciphertext) {
		t.Fatal("IsEncrypted does not recognise this cipher's own output - the conventions disagree")
	}
	if c.IsEncrypted(plaintext) {
		t.Error("IsEncrypted reports plaintext as encrypted")
	}

	back, err := c.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if back != plaintext {
		t.Errorf("round-trip = %q, want %q", back, plaintext)
	}

	// Encrypting twice must not be mistaken for a fresh value: the store relies
	// on IsEncrypted to avoid double-encrypting.
	twice, err := c.Encrypt(ciphertext)
	if err != nil {
		t.Fatalf("Encrypt twice: %v", err)
	}
	if !c.IsEncrypted(twice) {
		t.Error("double-encrypted value is not recognised as encrypted")
	}
}

// TestStoreSecretCipher_WrongKeyFails asserts a different dashboard password
// cannot read the value, which is the whole point of deriving the key from it.
func TestStoreSecretCipher_WrongKeyFails(t *testing.T) {
	t.Parallel()

	good := NewStoreSecretCipher(DeriveEncryptionKey(legacyHashPassword("first"), nil))
	other := NewStoreSecretCipher(DeriveEncryptionKey(legacyHashPassword("second"), nil))

	ciphertext, err := good.Encrypt("mm-SECRET")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := other.Decrypt(ciphertext); err == nil {
		t.Error("a cipher keyed from a different password decrypted the value")
	}
}
