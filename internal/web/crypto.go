package web

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"

	"github.com/onllm-dev/onwatch/v2/internal/notify"
	"golang.org/x/crypto/hkdf"
)

// encryptionSalt is the package-level salt for HKDF key derivation.
// Set during initialization via SetEncryptionSalt.
var encryptionSalt []byte

// SetEncryptionSalt sets the salt used for HKDF key derivation.
// Called once during application startup.
func SetEncryptionSalt(salt []byte) {
	encryptionSalt = salt
}

// GetEncryptionSalt returns the current encryption salt.
func GetEncryptionSalt() []byte {
	return encryptionSalt
}

// GenerateEncryptionSalt generates a new random 16-byte salt.
func GenerateEncryptionSalt() ([]byte, error) {
	salt := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("failed to generate encryption salt: %w", err)
	}
	return salt, nil
}

// DeriveEncryptionKey derives a 32-byte encryption key from the admin password hash using HKDF-SHA256.
// The password hash is expected to be a bcrypt hash or SHA-256 hex string.
// Uses HKDF with the provided salt for secure key derivation.
// Returns a hex-encoded 32-byte key suitable for AES-256-GCM.
func DeriveEncryptionKey(passwordHash string, salt []byte) string {
	// Use HKDF-SHA256 for secure key derivation
	// secret = passwordHash bytes
	// salt = stored salt from database (or nil to use package-level salt)
	// info = "onwatch-smtp-encryption" (domain separation)

	// Use package-level salt if none provided
	if salt == nil {
		salt = encryptionSalt
	}

	if salt == nil {
		// Legacy fallback: use raw SHA-256 of password hash
		// This maintains backward compatibility during migration
		if len(passwordHash) == 64 {
			return passwordHash
		}
		h := sha256.Sum256([]byte(passwordHash))
		return hex.EncodeToString(h[:])
	}

	hkdfReader := hkdf.New(sha256.New, []byte(passwordHash), salt, []byte("onwatch-smtp-encryption"))
	key := make([]byte, 32)
	if _, err := io.ReadFull(hkdfReader, key); err != nil {
		// Fallback to legacy on error
		if len(passwordHash) == 64 {
			return passwordHash
		}
		h := sha256.Sum256([]byte(passwordHash))
		return hex.EncodeToString(h[:])
	}
	return hex.EncodeToString(key)
}

// IsEncryptedValue checks if a string has the encrypted prefix marker.
// Delegates to notify.IsEncryptedValue for the actual check.
func IsEncryptedValue(value string) bool {
	return notify.IsEncryptedValue(value)
}

// ReEncryptAllData re-encrypts all encrypted data in the database when password changes.
// It uses the old key to decrypt and the new key to re-encrypt.
// Returns a map of any errors that occurred (key = setting name, value = error message).
func ReEncryptAllData(store interface {
	GetSetting(key string) (string, error)
	SetSetting(key, value string) error
}, oldPasswordHash, newPasswordHash string) map[string]string {
	errors := make(map[string]string)

	oldKey := DeriveEncryptionKey(oldPasswordHash, nil)
	newKey := DeriveEncryptionKey(newPasswordHash, nil)

	// If keys are the same (shouldn't happen, but safety check), skip
	if oldKey == newKey {
		return errors
	}

	// Re-encrypt SMTP password
	if err := reEncryptSMTPPassword(store, oldKey, newKey); err != nil {
		errors["smtp"] = err.Error()
	}

	// Re-encrypt the Gemini OAuth pair. Without this the tokens become
	// unreadable after a password change and Gemini polling silently stops.
	if err := reEncryptSetting(store, "gemini_tokens", oldKey, newKey); err != nil {
		errors["gemini_tokens"] = err.Error()
	}

	return errors
}

// reEncryptSetting re-keys a settings value that is stored as ciphertext.
// A plaintext value (written before encryption existed) is encrypted with the
// new key, and a value already under the new key is left alone.
func reEncryptSetting(store interface {
	GetSetting(key string) (string, error)
	SetSetting(key, value string) error
}, key, oldKey, newKey string) error {
	stored, err := store.GetSetting(key)
	if err != nil || stored == "" {
		return nil
	}

	plaintext := stored
	if IsEncryptedValue(stored) {
		decrypted, err := notify.Decrypt(stored, oldKey)
		if err != nil {
			if _, newErr := notify.Decrypt(stored, newKey); newErr == nil {
				return nil // already re-keyed
			}
			return fmt.Errorf("failed to decrypt %s with the old key: %w", key, err)
		}
		plaintext = decrypted
	}

	reEncrypted, err := notify.Encrypt(plaintext, newKey)
	if err != nil {
		return fmt.Errorf("failed to re-encrypt %s: %w", key, err)
	}
	return store.SetSetting(key, reEncrypted)
}

// reEncryptSMTPPassword re-encrypts the SMTP password when admin password changes.
func reEncryptSMTPPassword(store interface {
	GetSetting(key string) (string, error)
	SetSetting(key, value string) error
}, oldKey, newKey string) error {
	smtpJSON, err := store.GetSetting("smtp")
	if err != nil || smtpJSON == "" {
		return nil // No SMTP settings to re-encrypt
	}

	// Parse SMTP settings
	var smtpSettings map[string]interface{}
	if err := json.Unmarshal([]byte(smtpJSON), &smtpSettings); err != nil {
		return fmt.Errorf("failed to parse SMTP settings: %w", err)
	}

	passwordVal, ok := smtpSettings["password"]
	if !ok || passwordVal == nil {
		return nil // No password to re-encrypt
	}

	encryptedPass, ok := passwordVal.(string)
	if !ok || encryptedPass == "" {
		return nil // No password to re-encrypt
	}

	// Check if the password is already encrypted
	if !IsEncryptedValue(encryptedPass) {
		// It's plaintext, encrypt it with the new key
		newEncrypted, err := notify.Encrypt(encryptedPass, newKey)
		if err != nil {
			return fmt.Errorf("failed to encrypt SMTP password: %w", err)
		}
		smtpSettings["password"] = newEncrypted
	} else {
		// It's encrypted, decrypt with old key and re-encrypt with new key
		plaintext, err := notify.Decrypt(encryptedPass, oldKey)
		if err != nil {
			// If decryption fails with old key, try with new key (might already be re-encrypted)
			_, tryNewErr := notify.Decrypt(encryptedPass, newKey)
			if tryNewErr == nil {
				// Already encrypted with new key, nothing to do
				return nil
			}
			return fmt.Errorf("failed to decrypt SMTP password with old key: %w", err)
		}

		// Re-encrypt with new key
		newEncrypted, err := notify.Encrypt(plaintext, newKey)
		if err != nil {
			return fmt.Errorf("failed to re-encrypt SMTP password: %w", err)
		}
		smtpSettings["password"] = newEncrypted
	}

	// Save updated settings
	newJSON, err := json.Marshal(smtpSettings)
	if err != nil {
		return fmt.Errorf("failed to marshal SMTP settings: %w", err)
	}

	if err := store.SetSetting("smtp", string(newJSON)); err != nil {
		return fmt.Errorf("failed to save SMTP settings: %w", err)
	}

	return nil
}

// storeSecretCipher adapts the AES-256-GCM helpers to the store's SecretCipher
// interface, so internal/store can encrypt credentials at rest without
// depending on this package or on internal/notify.
type storeSecretCipher struct {
	key string
}

// NewStoreSecretCipher returns a cipher for the store, keyed by a value from
// DeriveEncryptionKey. A password change produces a different key, which is
// why the store's metadata is re-keyed at the same time.
func NewStoreSecretCipher(key string) *storeSecretCipher {
	return &storeSecretCipher{key: key}
}

func (c *storeSecretCipher) Encrypt(plaintext string) (string, error) {
	return notify.Encrypt(plaintext, c.key)
}

func (c *storeSecretCipher) Decrypt(ciphertext string) (string, error) {
	return notify.Decrypt(ciphertext, c.key)
}

func (c *storeSecretCipher) IsEncrypted(value string) bool {
	return notify.IsEncryptedValue(value)
}
