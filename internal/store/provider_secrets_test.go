package store

import (
	"strings"
	"testing"
)

func TestProviderSecretEncryptionRoundTrip(t *testing.T) {
	setOpenCodeTestKey(t)
	s, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ciphertext, err := s.EncryptProviderSecret("deepseek", "api_key", "sk-deepseek-secret")
	if err != nil {
		t.Fatalf("EncryptProviderSecret: %v", err)
	}
	if !strings.HasPrefix(ciphertext, "v1:") || strings.Contains(ciphertext, "sk-deepseek-secret") {
		t.Fatalf("provider secret was not safely encrypted: %q", ciphertext)
	}

	plaintext, err := s.DecryptProviderSecret("deepseek", "api_key", ciphertext)
	if err != nil {
		t.Fatalf("DecryptProviderSecret: %v", err)
	}
	if plaintext != "sk-deepseek-secret" {
		t.Fatalf("plaintext = %q, want original secret", plaintext)
	}
}

func TestProviderSecretAADPreventsCrossFieldOrProviderDecryption(t *testing.T) {
	setOpenCodeTestKey(t)
	s, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ciphertext, err := s.EncryptProviderSecret("deepseek", "api_key", "secret")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		provider string
		field    string
	}{
		{provider: "moonshot", field: "api_key"},
		{provider: "deepseek", field: "token"},
	} {
		if _, err := s.DecryptProviderSecret(tc.provider, tc.field, ciphertext); err == nil {
			t.Fatalf("ciphertext decrypted with AAD for %s.%s", tc.provider, tc.field)
		}
	}
}

func TestProviderSecretRejectsInvalidInputs(t *testing.T) {
	setOpenCodeTestKey(t)
	s, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if _, err := s.EncryptProviderSecret("", "api_key", "secret"); err == nil {
		t.Fatal("empty provider was accepted")
	}
	if _, err := s.EncryptProviderSecret("deepseek", "", "secret"); err == nil {
		t.Fatal("empty field was accepted")
	}
	if _, err := s.EncryptProviderSecret("deepseek", "api_key", ""); err == nil {
		t.Fatal("empty plaintext was accepted")
	}
	if _, err := s.DecryptProviderSecret("deepseek", "api_key", "plaintext"); err == nil {
		t.Fatal("plaintext credential format was accepted")
	}
}
