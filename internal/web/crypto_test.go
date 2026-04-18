package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/onllm-dev/onwatch/v2/internal/notify"
)

type memorySettingStore struct {
	settings map[string]string
	getErr   map[string]error
	setErr   map[string]error
	setCalls int
}

func newMemorySettingStore() *memorySettingStore {
	return &memorySettingStore{
		settings: make(map[string]string),
		getErr:   make(map[string]error),
		setErr:   make(map[string]error),
	}
}

func (m *memorySettingStore) GetSetting(key string) (string, error) {
	if err, ok := m.getErr[key]; ok {
		return "", err
	}
	return m.settings[key], nil
}

func (m *memorySettingStore) SetSetting(key, value string) error {
	m.setCalls++
	if err, ok := m.setErr[key]; ok {
		return err
	}
	m.settings[key] = value
	return nil
}

func setTestEncryptionSalt(t *testing.T, salt []byte) {
	t.Helper()
	original := GetEncryptionSalt()
	SetEncryptionSalt(salt)
	t.Cleanup(func() {
		SetEncryptionSalt(original)
	})
}

func TestSetAndGetEncryptionSalt(t *testing.T) {
	t.Parallel()
	setTestEncryptionSalt(t, []byte("abcdefghijklmnop"))

	got := GetEncryptionSalt()
	if !bytes.Equal(got, []byte("abcdefghijklmnop")) {
		t.Fatalf("GetEncryptionSalt() = %q, want %q", string(got), "abcdefghijklmnop")
	}
}

func TestGenerateEncryptionSalt_BasicBehavior(t *testing.T) {
	t.Parallel()
	salt, err := GenerateEncryptionSalt()
	if err != nil {
		t.Fatalf("GenerateEncryptionSalt() error = %v", err)
	}
	if len(salt) != 16 {
		t.Fatalf("GenerateEncryptionSalt() length = %d, want 16", len(salt))
	}
}

func TestDeriveEncryptionKey_WithExplicitSalt(t *testing.T) {
	t.Parallel()
	setTestEncryptionSalt(t, []byte("different-package-salt"))

	explicitSalt := []byte("1234567890abcdef")
	passwordHash := "bcrypt-like-password-hash"

	got1 := DeriveEncryptionKey(passwordHash, explicitSalt)
	got2 := DeriveEncryptionKey(passwordHash, explicitSalt)
	if got1 != got2 {
		t.Fatalf("DeriveEncryptionKey() with explicit salt should be deterministic, got %q and %q", got1, got2)
	}
	if len(got1) != 64 {
		t.Fatalf("DeriveEncryptionKey() length = %d, want 64", len(got1))
	}

	legacyRaw := sha256.Sum256([]byte(passwordHash))
	legacy := hex.EncodeToString(legacyRaw[:])
	if got1 == legacy {
		t.Fatalf("DeriveEncryptionKey() unexpectedly used legacy fallback with explicit salt")
	}
}

func TestDeriveEncryptionKey_WithNilSalt_UsesPackageSalt(t *testing.T) {
	t.Parallel()
	salt := []byte("abcdefghijklmnop")
	setTestEncryptionSalt(t, salt)

	passwordHash := "hash-value"
	gotWithNil := DeriveEncryptionKey(passwordHash, nil)
	gotWithExplicit := DeriveEncryptionKey(passwordHash, salt)

	if gotWithNil != gotWithExplicit {
		t.Fatalf("DeriveEncryptionKey(nil salt) = %q, want %q", gotWithNil, gotWithExplicit)
	}
}

func TestDeriveEncryptionKey_LegacyFallback_RawSHA256Hash(t *testing.T) {
	t.Parallel()
	setTestEncryptionSalt(t, nil)

	passwordHash := "not-a-64-char-hash"
	got := DeriveEncryptionKey(passwordHash, nil)

	wantRaw := sha256.Sum256([]byte(passwordHash))
	want := hex.EncodeToString(wantRaw[:])
	if got != want {
		t.Fatalf("DeriveEncryptionKey() = %q, want legacy SHA-256 %q", got, want)
	}
}

func TestDeriveEncryptionKey_LegacyFallback_Already64Chars(t *testing.T) {
	t.Parallel()
	setTestEncryptionSalt(t, nil)

	passwordHash := strings.Repeat("a", 64)
	got := DeriveEncryptionKey(passwordHash, nil)
	if got != passwordHash {
		t.Fatalf("DeriveEncryptionKey() = %q, want original 64-char hash", got)
	}
}

func TestIsEncryptedValue_Wrapper(t *testing.T) {
	t.Parallel()
	if !IsEncryptedValue("enc:abc") {
		t.Fatal("IsEncryptedValue() should return true for notify encrypted prefix")
	}
	if IsEncryptedValue("SGVsbG8gV29ybGQ=") {
		t.Fatal("IsEncryptedValue() should return false for base64 without enc: prefix")
	}
}

func TestReEncryptAllData_SameKeySkips(t *testing.T) {
	t.Parallel()
	setTestEncryptionSalt(t, []byte("abcdefghijklmnop"))
	store := newMemorySettingStore()
	store.settings["smtp"] = `{"password":"plain"}`

	errs := ReEncryptAllData(store, "same-password-hash", "same-password-hash")
	if len(errs) != 0 {
		t.Fatalf("ReEncryptAllData() errors = %v, want none", errs)
	}
	if store.setCalls != 0 {
		t.Fatalf("SetSetting calls = %d, want 0 when keys are equal", store.setCalls)
	}
}

func TestReEncryptAllData_CollectsSMTPError(t *testing.T) {
	t.Parallel()
	setTestEncryptionSalt(t, []byte("abcdefghijklmnop"))
	store := newMemorySettingStore()
	store.settings["smtp"] = "not-json"

	errs := ReEncryptAllData(store, "old-hash", "new-hash")
	msg, ok := errs["smtp"]
	if !ok {
		t.Fatalf("ReEncryptAllData() should include smtp error, got %v", errs)
	}
	if !strings.Contains(msg, "failed to parse SMTP settings") {
		t.Fatalf("smtp error = %q, want parse message", msg)
	}
}

func TestReEncryptAllData_Success(t *testing.T) {
	t.Parallel()
	setTestEncryptionSalt(t, []byte("abcdefghijklmnop"))
	store := newMemorySettingStore()

	newKey := DeriveEncryptionKey("new-hash", nil)

	store.settings["smtp"] = `{"host":"smtp.example.com","password":"smtp-secret"}`
	errs := ReEncryptAllData(store, "old-hash", "new-hash")
	if len(errs) != 0 {
		t.Fatalf("ReEncryptAllData() errors = %v, want none", errs)
	}

	if store.setCalls != 1 {
		t.Fatalf("SetSetting calls = %d, want 1", store.setCalls)
	}

	var gotSMTP map[string]any
	if err := json.Unmarshal([]byte(store.settings["smtp"]), &gotSMTP); err != nil {
		t.Fatalf("failed to parse updated smtp setting: %v", err)
	}
	ciphertext, _ := gotSMTP["password"].(string)
	plaintext, err := notify.Decrypt(ciphertext, newKey)
	if err != nil {
		t.Fatalf("notify.Decrypt() with new key error = %v", err)
	}
	if plaintext != "smtp-secret" {
		t.Fatalf("decrypted password = %q, want smtp-secret", plaintext)
	}
}

func TestReEncryptSMTPPassword_Branches(t *testing.T) {
	t.Parallel()
	setTestEncryptionSalt(t, []byte("abcdefghijklmnop"))
	oldKey := DeriveEncryptionKey("old-hash", nil)
	newKey := DeriveEncryptionKey("new-hash", nil)

	tests := []struct {
		name           string
		setupStore     func(*memorySettingStore)
		oldKey         string
		newKey         string
		wantErrSubstr  string
		wantSetCalls   int
		validateResult func(*testing.T, *memorySettingStore)
	}{
		{
			name: "get setting error returns nil",
			setupStore: func(s *memorySettingStore) {
				s.getErr["smtp"] = errors.New("db read failed")
			},
			oldKey:       oldKey,
			newKey:       newKey,
			wantSetCalls: 0,
		},
		{
			name: "empty smtp returns nil",
			setupStore: func(s *memorySettingStore) {
				s.settings["smtp"] = ""
			},
			oldKey:       oldKey,
			newKey:       newKey,
			wantSetCalls: 0,
		},
		{
			name: "invalid json returns parse error",
			setupStore: func(s *memorySettingStore) {
				s.settings["smtp"] = "invalid-json"
			},
			oldKey:        oldKey,
			newKey:        newKey,
			wantErrSubstr: "failed to parse SMTP settings",
			wantSetCalls:  0,
		},
		{
			name: "missing password returns nil",
			setupStore: func(s *memorySettingStore) {
				s.settings["smtp"] = `{"host":"smtp.example.com"}`
			},
			oldKey:       oldKey,
			newKey:       newKey,
			wantSetCalls: 0,
		},
		{
			name: "non-string password returns nil",
			setupStore: func(s *memorySettingStore) {
				s.settings["smtp"] = `{"password":123}`
			},
			oldKey:       oldKey,
			newKey:       newKey,
			wantSetCalls: 0,
		},
		{
			name: "empty password returns nil",
			setupStore: func(s *memorySettingStore) {
				s.settings["smtp"] = `{"password":""}`
			},
			oldKey:       oldKey,
			newKey:       newKey,
			wantSetCalls: 0,
		},
		{
			name: "plaintext password gets encrypted with new key",
			setupStore: func(s *memorySettingStore) {
				s.settings["smtp"] = `{"password":"plain-secret"}`
			},
			oldKey:       oldKey,
			newKey:       newKey,
			wantSetCalls: 1,
			validateResult: func(t *testing.T, s *memorySettingStore) {
				t.Helper()
				var smtp map[string]any
				if err := json.Unmarshal([]byte(s.settings["smtp"]), &smtp); err != nil {
					t.Fatalf("json.Unmarshal() error = %v", err)
				}
				ciphertext, _ := smtp["password"].(string)
				plaintext, err := notify.Decrypt(ciphertext, newKey)
				if err != nil {
					t.Fatalf("notify.Decrypt() error = %v", err)
				}
				if plaintext != "plain-secret" {
					t.Fatalf("decrypted password = %q, want plain-secret", plaintext)
				}
			},
		},
		{
			name: "plaintext encryption failure returns error",
			setupStore: func(s *memorySettingStore) {
				s.settings["smtp"] = `{"password":"plain-secret"}`
			},
			oldKey:        oldKey,
			newKey:        "invalid-key",
			wantErrSubstr: "failed to encrypt SMTP password",
			wantSetCalls:  0,
		},
		{
			name: "prefixed decrypt failure with both keys returns error",
			setupStore: func(s *memorySettingStore) {
				s.settings["smtp"] = `{"password":"enc:AAAAAAAAAAAAAAAAAAAAAAAA"}`
			},
			oldKey:        oldKey,
			newKey:        newKey,
			wantErrSubstr: "failed to decrypt SMTP password with old key",
			wantSetCalls:  0,
		},
		{
			name: "plaintext encryption with invalid new key returns error",
			setupStore: func(s *memorySettingStore) {
				s.settings["smtp"] = `{"password":"plain-secret"}`
			},
			oldKey:        oldKey,
			newKey:        "invalid-key",
			wantErrSubstr: "failed to encrypt SMTP password",
			wantSetCalls:  0,
		},
		{
			name: "save failure returns error",
			setupStore: func(s *memorySettingStore) {
				s.settings["smtp"] = `{"password":"plain-secret"}`
				s.setErr["smtp"] = errors.New("write failed")
			},
			oldKey:        oldKey,
			newKey:        newKey,
			wantErrSubstr: "failed to save SMTP settings",
			wantSetCalls:  1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newMemorySettingStore()
			tc.setupStore(store)

			err := reEncryptSMTPPassword(store, tc.oldKey, tc.newKey)
			if tc.wantErrSubstr == "" {
				if err != nil {
					t.Fatalf("reEncryptSMTPPassword() unexpected error = %v", err)
				}
			} else {
				if err == nil {
					t.Fatalf("reEncryptSMTPPassword() error = nil, want substring %q", tc.wantErrSubstr)
				}
				if !strings.Contains(err.Error(), tc.wantErrSubstr) {
					t.Fatalf("reEncryptSMTPPassword() error = %q, want substring %q", err.Error(), tc.wantErrSubstr)
				}
			}

			if store.setCalls != tc.wantSetCalls {
				t.Fatalf("SetSetting call count = %d, want %d", store.setCalls, tc.wantSetCalls)
			}

			if tc.validateResult != nil {
				tc.validateResult(t, store)
			}
		})
	}
}
