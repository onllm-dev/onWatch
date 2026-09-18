package store

// Encryption at rest for the credentials onWatch holds in SQLite.
//
// The SMTP password has been encrypted since v2.x, but two credentials were
// still written in cleartext: the Gemini OAuth pair in settings.gemini_tokens
// (a refresh token is a long-lived credential for the user's Google account)
// and the provider API key in provider_accounts.metadata. Both are now put
// through the same AES-256-GCM path, which is what GDPR Art. 32(1)(a) asks of
// a credential at rest.
//
// The cipher is injected rather than imported so the store keeps no dependency
// on the notify or web packages. main.go supplies an adapter over the existing
// key derivation, which means a dashboard password change re-keys these
// secrets through the same path that already re-keys the SMTP password.

import (
	"fmt"
	"sync"
)

// SecretCipher encrypts and decrypts values held at rest.
type SecretCipher interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(ciphertext string) (string, error)
	// IsEncrypted reports whether a stored value is ciphertext. It is what
	// lets a database written by an earlier version keep working.
	IsEncrypted(value string) bool
}

// secretCipherHolder guards the cipher, which is set during start-up and read
// from provider agent goroutines.
type secretCipherHolder struct {
	mu     sync.RWMutex
	cipher SecretCipher
}

func (h *secretCipherHolder) set(c SecretCipher) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cipher = c
}

func (h *secretCipherHolder) get() SecretCipher {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.cipher
}

// SetSecretCipher installs the cipher used for credentials at rest. Passing nil
// disables encryption, which is the state before the dashboard password hash is
// known.
func (s *Store) SetSecretCipher(c SecretCipher) {
	if s == nil {
		return
	}
	s.secrets.set(c)
}

// protectSecret returns the value to write to the database.
//
// With no cipher configured the value is stored as-is: refusing to store it
// would break polling, and the caller has no better place to put it. That is
// the same trade the SMTP path already makes.
func (s *Store) protectSecret(plaintext string) string {
	if s == nil || plaintext == "" {
		return plaintext
	}
	cipher := s.secrets.get()
	if cipher == nil {
		return plaintext
	}
	if cipher.IsEncrypted(plaintext) {
		return plaintext // already ciphertext, do not double-encrypt
	}
	encrypted, err := cipher.Encrypt(plaintext)
	if err != nil {
		// Losing the credential would stop the provider working, so the
		// cleartext is kept. The privacy notice says which values are
		// encrypted; this path is the documented exception.
		return plaintext
	}
	return encrypted
}

// revealSecret returns the usable value for a stored one.
//
// A value that is not ciphertext is returned unchanged, which is what makes
// this safe to deploy over an existing database: rows written by an earlier
// version are read as they always were, and are re-encrypted the next time
// they are written.
func (s *Store) revealSecret(stored string) string {
	if s == nil || stored == "" {
		return stored
	}
	cipher := s.secrets.get()
	if cipher == nil || !cipher.IsEncrypted(stored) {
		return stored
	}
	plaintext, err := cipher.Decrypt(stored)
	if err != nil {
		// Most likely the dashboard password changed without re-keying. The
		// caller sees an unusable value and re-authenticates, which is better
		// than handing back ciphertext that looks like a token.
		return ""
	}
	return plaintext
}

// ReKeyProviderAccountMetadata re-encrypts every provider account's metadata
// under a new cipher, and installs that cipher.
//
// Ordering matters and is why this lives here rather than in the web layer: the
// rows have to be read while the OLD cipher is still installed, because reads
// decrypt, and written back only after the new one is in place. Doing it the
// other way round leaves the provider API keys unreadable and silently stops
// polling.
func (s *Store) ReKeyProviderAccountMetadata(newCipher SecretCipher) error {
	if s == nil {
		return nil
	}

	type account struct {
		id       int64
		metadata string
	}

	rows, err := s.db.Query(`SELECT id, COALESCE(metadata, '') FROM provider_accounts`)
	if err != nil {
		return fmt.Errorf("store.ReKeyProviderAccountMetadata: read accounts: %w", err)
	}
	var accounts []account
	for rows.Next() {
		var a account
		if err := rows.Scan(&a.id, &a.metadata); err != nil {
			rows.Close()
			return fmt.Errorf("store.ReKeyProviderAccountMetadata: scan: %w", err)
		}
		// Decrypt with the cipher currently installed.
		a.metadata = s.revealSecret(a.metadata)
		accounts = append(accounts, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("store.ReKeyProviderAccountMetadata: iterate: %w", err)
	}

	s.SetSecretCipher(newCipher)

	for _, a := range accounts {
		if a.metadata == "" {
			continue
		}
		if _, err := s.db.Exec(`UPDATE provider_accounts SET metadata = ? WHERE id = ?`,
			s.protectSecret(a.metadata), a.id); err != nil {
			return fmt.Errorf("store.ReKeyProviderAccountMetadata: write account %d: %w", a.id, err)
		}
	}
	return nil
}
