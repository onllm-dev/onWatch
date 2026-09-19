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

	// Re-encrypt webhook bearer token
	if err := reEncryptWebhookToken(store, oldKey, newKey); err != nil {
		errors["webhook"] = err.Error()
	}

	return errors
}

// reEncryptWebhookToken re-keys the webhook bearer token when the admin
// password changes. The token is stored with the "enc:" prefix, so an
// unprefixed value is plaintext and is simply encrypted with the new key.
func reEncryptWebhookToken(store interface {
	GetSetting(key string) (string, error)
	SetSetting(key, value string) error
}, oldKey, newKey string) error {
	raw, err := store.GetSetting("webhook")
	if err != nil || raw == "" {
		return nil // No webhook settings to re-encrypt
	}

	var settings map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		return fmt.Errorf("failed to parse webhook settings: %w", err)
	}

	token, _ := settings["bearer_token"].(string)
	if token == "" {
		return nil // No token to re-encrypt
	}

	plaintext := token
	if IsEncryptedValue(token) {
		plaintext, err = notify.DecryptFromStorage(token, oldKey)
		if err != nil {
			// Already re-keyed (e.g. a retried password change) - leave it alone.
			if _, tryNewErr := notify.DecryptFromStorage(token, newKey); tryNewErr == nil {
				return nil
			}
			return fmt.Errorf("failed to decrypt webhook token with old key: %w", err)
		}
	}

	reEncrypted, err := notify.EncryptForStorage(plaintext, newKey)
	if err != nil {
		return fmt.Errorf("failed to encrypt webhook token: %w", err)
	}
	settings["bearer_token"] = reEncrypted

	updated, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("failed to encode webhook settings: %w", err)
	}
	if err := store.SetSetting("webhook", string(updated)); err != nil {
		return fmt.Errorf("failed to save re-encrypted webhook token: %w", err)
	}
	return nil
}

// decryptStoredSecret decrypts a stored secret with key, accepting both storage
// shapes in use: values written with the "enc:" prefix (webhook bearer tokens)
// and bare ciphertext written by notify.Encrypt (SMTP passwords).
func decryptStoredSecret(value, key string) (string, error) {
	if IsEncryptedValue(value) {
		return notify.DecryptFromStorage(value, key)
	}
	return notify.Decrypt(value, key)
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

	// Detect encryption by attempting decryption, not by inspecting the value.
	// UpdateSettings stores the password as bare notify.Encrypt output with no
	// "enc:" prefix, and ConfigureSMTP reads it back the same way, so the
	// prefix check used here previously reported stored ciphertext as plaintext
	// and encrypted it a second time - after which ConfigureSMTP decrypted only
	// the outer layer and handed the inner ciphertext to the mail server.
	plaintext, err := decryptStoredSecret(encryptedPass, oldKey)
	if err != nil {
		// Unreadable with the old key: either already re-keyed by a previous
		// (perhaps retried) password change, or never encrypted at all.
		if _, newErr := decryptStoredSecret(encryptedPass, newKey); newErr == nil {
			return nil // Already encrypted with the new key, nothing to do
		}
		if IsEncryptedValue(encryptedPass) {
			// Marked as ciphertext but readable with neither key - do not
			// silently encrypt a corrupt value and bury the original.
			return fmt.Errorf("failed to decrypt SMTP password with old key: %w", err)
		}
		plaintext = encryptedPass // Legacy plaintext password
	}

	// Always write back in the bare form ConfigureSMTP expects.
	newEncrypted, err := notify.Encrypt(plaintext, newKey)
	if err != nil {
		return fmt.Errorf("failed to encrypt SMTP password: %w", err)
	}
	smtpSettings["password"] = newEncrypted

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
