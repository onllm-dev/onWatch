package notify

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestGenerateEncryptionKey_Length(t *testing.T) {
	t.Parallel()
	key, err := GenerateEncryptionKey()
	if err != nil {
		t.Fatalf("GenerateEncryptionKey failed: %v", err)
	}

	// 32 bytes = 64 hex chars
	if len(key) != 64 {
		t.Errorf("Expected 64 hex chars, got %d", len(key))
	}

	// Must be valid hex
	decoded, err := hex.DecodeString(key)
	if err != nil {
		t.Fatalf("Key is not valid hex: %v", err)
	}
	if len(decoded) != 32 {
		t.Errorf("Expected 32 bytes, got %d", len(decoded))
	}
}

func TestGenerateEncryptionKey_Unique(t *testing.T) {
	t.Parallel()
	keys := make(map[string]bool)
	for i := 0; i < 10; i++ {
		key, err := GenerateEncryptionKey()
		if err != nil {
			t.Fatalf("GenerateEncryptionKey failed: %v", err)
		}
		if keys[key] {
			t.Fatalf("Duplicate key generated on iteration %d", i)
		}
		keys[key] = true
	}
}

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	t.Parallel()
	key, err := GenerateEncryptionKey()
	if err != nil {
		t.Fatalf("GenerateEncryptionKey failed: %v", err)
	}

	tests := []struct {
		name      string
		plaintext string
	}{
		{"simple password", "mypassword123"},
		{"empty string", ""},
		{"unicode", "p@$$w0rd-with-unicode-\u00e9\u00e8\u00ea"},
		{"long string", "this is a much longer string that might be used as a complex password with spaces and special chars !@#$%^&*()"},
		{"special chars only", "!@#$%^&*()_+-=[]{}|;':\",./<>?"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ciphertext, err := Encrypt(tt.plaintext, key)
			if err != nil {
				t.Fatalf("Encrypt failed: %v", err)
			}

			if ciphertext == tt.plaintext {
				t.Error("Ciphertext should not equal plaintext")
			}

			decrypted, err := Decrypt(ciphertext, key)
			if err != nil {
				t.Fatalf("Decrypt failed: %v", err)
			}

			if decrypted != tt.plaintext {
				t.Errorf("Decrypt = %q, want %q", decrypted, tt.plaintext)
			}
		})
	}
}

func TestEncrypt_DifferentCiphertexts(t *testing.T) {
	t.Parallel()
	key, err := GenerateEncryptionKey()
	if err != nil {
		t.Fatalf("GenerateEncryptionKey failed: %v", err)
	}

	plaintext := "same-plaintext"
	ct1, err := Encrypt(plaintext, key)
	if err != nil {
		t.Fatalf("Encrypt 1 failed: %v", err)
	}
	ct2, err := Encrypt(plaintext, key)
	if err != nil {
		t.Fatalf("Encrypt 2 failed: %v", err)
	}

	// Random nonce means different ciphertexts each time
	if ct1 == ct2 {
		t.Error("Same plaintext should produce different ciphertexts (random nonce)")
	}

	// But both should decrypt to the same value
	d1, _ := Decrypt(ct1, key)
	d2, _ := Decrypt(ct2, key)
	if d1 != d2 {
		t.Error("Both ciphertexts should decrypt to same plaintext")
	}
}

func TestDecrypt_WrongKey(t *testing.T) {
	t.Parallel()
	key1, _ := GenerateEncryptionKey()
	key2, _ := GenerateEncryptionKey()

	ciphertext, err := Encrypt("secret", key1)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	_, err = Decrypt(ciphertext, key2)
	if err == nil {
		t.Error("Expected error when decrypting with wrong key")
	}
}

func TestDecrypt_InvalidCiphertext(t *testing.T) {
	t.Parallel()
	key, _ := GenerateEncryptionKey()

	tests := []struct {
		name       string
		ciphertext string
	}{
		{"empty string", ""},
		{"not base64", "!!!not-base64!!!"},
		{"too short", "AQID"},
		{"valid base64 but garbage", "dGhpcyBpcyBub3QgYSB2YWxpZCBjaXBoZXJ0ZXh0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Decrypt(tt.ciphertext, key)
			if err == nil {
				t.Error("Expected error for invalid ciphertext")
			}
		})
	}
}

func TestEncrypt_InvalidKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		key  string
	}{
		{"empty key", ""},
		{"too short", "abcdef"},
		{"not hex", "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"},
		{"wrong length hex", "aabbccdd"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Encrypt("test", tt.key)
			if err == nil {
				t.Errorf("Expected error for key %q", tt.key)
			}
		})
	}
}

func TestDecrypt_InvalidKey(t *testing.T) {
	t.Parallel()
	// First encrypt with a valid key
	validKey, _ := GenerateEncryptionKey()
	ciphertext, _ := Encrypt("test", validKey)

	tests := []struct {
		name string
		key  string
	}{
		{"empty key", ""},
		{"too short", "abcdef"},
		{"not hex", "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Decrypt(ciphertext, tt.key)
			if err == nil {
				t.Errorf("Expected error for key %q", tt.key)
			}
		})
	}
}

func TestEncryptForStorage_RoundTrip(t *testing.T) {
	t.Parallel()
	key, err := GenerateEncryptionKey()
	if err != nil {
		t.Fatalf("GenerateEncryptionKey failed: %v", err)
	}

	plaintext := "my-secret-password"
	stored, err := EncryptForStorage(plaintext, key)
	if err != nil {
		t.Fatalf("EncryptForStorage failed: %v", err)
	}

	// Should have the encrypted prefix
	if !strings.HasPrefix(stored, "enc:") {
		t.Errorf("Expected 'enc:' prefix, got %q", stored[:10])
	}

	// Should round-trip via DecryptFromStorage
	decrypted, err := DecryptFromStorage(stored, key)
	if err != nil {
		t.Fatalf("DecryptFromStorage failed: %v", err)
	}
	if decrypted != plaintext {
		t.Errorf("DecryptFromStorage = %q, want %q", decrypted, plaintext)
	}
}

func TestDecryptFromStorage_PlaintextPassthrough(t *testing.T) {
	t.Parallel()
	key, _ := GenerateEncryptionKey()

	// Value without "enc:" prefix should be returned as-is
	plaintext := "my-plain-password"
	result, err := DecryptFromStorage(plaintext, key)
	if err != nil {
		t.Fatalf("DecryptFromStorage failed: %v", err)
	}
	if result != plaintext {
		t.Errorf("DecryptFromStorage = %q, want %q", result, plaintext)
	}
}

func TestDecryptFromStorage_EmptyString(t *testing.T) {
	t.Parallel()
	key, _ := GenerateEncryptionKey()

	result, err := DecryptFromStorage("", key)
	if err != nil {
		t.Fatalf("DecryptFromStorage failed for empty string: %v", err)
	}
	if result != "" {
		t.Errorf("DecryptFromStorage = %q, want empty", result)
	}
}

func TestIsEncryptedValue(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{"encrypted value", "enc:base64ciphertext", true},
		{"plain value", "my-password", false},
		{"empty string", "", false},
		{"just prefix", "enc:", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsEncryptedValue(tt.value)
			if got != tt.want {
				t.Errorf("IsEncryptedValue(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

func TestEncryptForStorage_InvalidKey(t *testing.T) {
	t.Parallel()
	_, err := EncryptForStorage("test", "bad-key")
	if err == nil {
		t.Error("Expected error for invalid key")
	}
}

func TestDecryptFromStorage_InvalidEncrypted(t *testing.T) {
	t.Parallel()
	key, _ := GenerateEncryptionKey()

	// Has the prefix but not valid ciphertext
	_, err := DecryptFromStorage("enc:not-valid-base64!!!", key)
	if err == nil {
		t.Error("Expected error for invalid encrypted value")
	}
}

func TestEncryptDecrypt_WithAAD(t *testing.T) {
	t.Parallel()
	key, err := GenerateEncryptionKey()
	if err != nil {
		t.Fatalf("GenerateEncryptionKey failed: %v", err)
	}

	plaintext := "secret-with-aad"
	aad := "context-binding"

	ciphertext, err := Encrypt(plaintext, key, aad)
	if err != nil {
		t.Fatalf("Encrypt with AAD failed: %v", err)
	}

	// Decrypt with same AAD should succeed
	decrypted, err := Decrypt(ciphertext, key, aad)
	if err != nil {
		t.Fatalf("Decrypt with correct AAD failed: %v", err)
	}
	if decrypted != plaintext {
		t.Errorf("Decrypt = %q, want %q", decrypted, plaintext)
	}

	// Decrypt with wrong AAD should fail
	_, err = Decrypt(ciphertext, key, "wrong-aad")
	if err == nil {
		t.Error("Expected error when decrypting with wrong AAD")
	}

	// Decrypt with no AAD should fail
	_, err = Decrypt(ciphertext, key)
	if err == nil {
		t.Error("Expected error when decrypting without AAD")
	}
}
