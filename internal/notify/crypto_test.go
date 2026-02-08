package notify

import (
	"encoding/hex"
	"testing"
)

func TestGenerateEncryptionKey_Length(t *testing.T) {
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
