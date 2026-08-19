package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

func (s *Store) encryptCredential(aad, plaintext string) (string, error) {
	block, err := aes.NewCipher(s.credentialKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), []byte(aad))
	return "v1:" + base64.RawStdEncoding.EncodeToString(sealed), nil
}

func (s *Store) decryptCredential(aad, encoded string) (string, error) {
	if !strings.HasPrefix(encoded, "v1:") {
		return "", errors.New("unsupported credential format")
	}
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(encoded, "v1:"))
	if err != nil {
		return "", errors.New("invalid encrypted credential")
	}
	block, err := aes.NewCipher(s.credentialKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("invalid encrypted credential")
	}
	plaintext, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], []byte(aad))
	if err != nil {
		return "", errors.New("credential authentication failed")
	}
	return string(plaintext), nil
}

// EncryptProviderSecret encrypts a provider setting with authenticated-data
// binding so ciphertext cannot be moved to a different provider or field.
func (s *Store) EncryptProviderSecret(provider, field, plaintext string) (string, error) {
	if strings.TrimSpace(provider) == "" || strings.TrimSpace(field) == "" {
		return "", errors.New("provider and field are required")
	}
	if plaintext == "" {
		return "", errors.New("provider secret is required")
	}
	return s.encryptCredential(fmt.Sprintf("provider-setting:%s:%s", provider, field), plaintext)
}

// DecryptProviderSecret decrypts a provider setting encrypted by
// EncryptProviderSecret. The provider and field must match the original AAD.
func (s *Store) DecryptProviderSecret(provider, field, ciphertext string) (string, error) {
	if strings.TrimSpace(provider) == "" || strings.TrimSpace(field) == "" {
		return "", errors.New("provider and field are required")
	}
	return s.decryptCredential(fmt.Sprintf("provider-setting:%s:%s", provider, field), ciphertext)
}
