package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	OpenCodeAuthValid        = "valid"
	OpenCodeAuthPending      = "pending"
	OpenCodeAuthNeedsReauth  = "needs_reauth"
	OpenCodeAuthUnauthorized = "unauthorized"
	OpenCodeAuthError        = "error"
	OpenCodeAuthDisabled     = "disabled"
)

var validOpenCodeAuthStatuses = map[string]bool{
	OpenCodeAuthValid: true, OpenCodeAuthPending: true, OpenCodeAuthNeedsReauth: true,
	OpenCodeAuthUnauthorized: true, OpenCodeAuthError: true, OpenCodeAuthDisabled: true,
}

type OpenCodeAccount struct {
	AccountID           int64      `json:"account_id"`
	Name                string     `json:"name"`
	WorkspaceID         string     `json:"workspace_id"`
	Enabled             bool       `json:"enabled"`
	AuthStatus          string     `json:"auth_status"`
	HasAuthCookie       bool       `json:"has_auth_cookie"`
	CredentialVersion   int64      `json:"credential_version"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	LastPollAt          *time.Time `json:"last_poll_at,omitempty"`
	LastSuccessAt       *time.Time `json:"last_success_at,omitempty"`
	LastErrorAt         *time.Time `json:"last_error_at,omitempty"`
	LastErrorCode       string     `json:"last_error_code,omitempty"`
	NextPollAt          *time.Time `json:"next_poll_at,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
	DeletedAt           *time.Time `json:"deleted_at,omitempty"`
	AuthCookie          string     `json:"-"`
}

func loadOrCreateOpenCodeCredentialKey(dbPath string) ([]byte, error) {
	if encoded := strings.TrimSpace(os.Getenv("ONWATCH_CREDENTIAL_KEY")); encoded != "" {
		key, err := decodeOpenCodeCredentialKey(encoded)
		if err != nil {
			return nil, fmt.Errorf("ONWATCH_CREDENTIAL_KEY: %w", err)
		}
		return key, nil
	}

	if dbPath == ":memory:" || strings.Contains(strings.ToLower(dbPath), "mode=memory") {
		key := make([]byte, 32)
		_, err := io.ReadFull(rand.Reader, key)
		return key, err
	}

	keyPath := openCodeSidecarBasePath(dbPath) + ".credential-key"
	if raw, err := os.ReadFile(keyPath); err == nil {
		if chmodErr := os.Chmod(keyPath, 0600); chmodErr != nil {
			return nil, fmt.Errorf("restrict key file permissions: %w", chmodErr)
		}
		return decodeOpenCodeCredentialKey(strings.TrimSpace(string(raw)))
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read key file: %w", err)
	}

	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0700); err != nil {
		return nil, fmt.Errorf("create key directory: %w", err)
	}
	encoded := base64.StdEncoding.EncodeToString(key) + "\n"
	f, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			raw, readErr := os.ReadFile(keyPath)
			if readErr != nil {
				return nil, fmt.Errorf("read concurrently created key: %w", readErr)
			}
			if chmodErr := os.Chmod(keyPath, 0600); chmodErr != nil {
				return nil, fmt.Errorf("restrict concurrently created key permissions: %w", chmodErr)
			}
			return decodeOpenCodeCredentialKey(strings.TrimSpace(string(raw)))
		}
		return nil, fmt.Errorf("create key file: %w", err)
	}
	if _, err := f.WriteString(encoded); err != nil {
		f.Close()
		_ = os.Remove(keyPath)
		return nil, fmt.Errorf("write key file: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("close key file: %w", err)
	}
	return key, nil
}

func openCodeSidecarBasePath(dbPath string) string {
	if !strings.HasPrefix(strings.ToLower(dbPath), "file:") {
		return dbPath
	}
	parsed, err := url.Parse(dbPath)
	if err == nil {
		candidate := parsed.Path
		if candidate == "" {
			candidate = parsed.Opaque
		}
		if candidate != "" {
			if unescaped, unescapeErr := url.PathUnescape(candidate); unescapeErr == nil {
				candidate = unescaped
			}
			return candidate
		}
	}
	candidate := strings.TrimPrefix(dbPath, "file:")
	if idx := strings.Index(candidate, "?"); idx >= 0 {
		candidate = candidate[:idx]
	}
	return candidate
}

func decodeOpenCodeCredentialKey(encoded string) ([]byte, error) {
	if key, err := base64.StdEncoding.DecodeString(encoded); err == nil && len(key) == 32 {
		return key, nil
	}
	if key, err := hex.DecodeString(encoded); err == nil && len(key) == 32 {
		return key, nil
	}
	return nil, errors.New("must be a base64 or hex encoded 32-byte key")
}

func (s *Store) encryptOpenCodeCookie(accountID int64, plaintext string) (string, error) {
	return s.encryptCredential(fmt.Sprintf("opencode-account:%d", accountID), plaintext)
}

func (s *Store) decryptOpenCodeCookie(accountID int64, encoded string) (string, error) {
	return s.decryptCredential(fmt.Sprintf("opencode-account:%d", accountID), encoded)
}

func (s *Store) backupBeforeOpenCodeMigration() error {
	if strings.TrimSpace(s.dbPath) == "" {
		// Only direct Store test fixtures omit dbPath; Store.New rejects it.
		return nil
	}
	lower := strings.ToLower(s.dbPath)
	if s.dbPath == ":memory:" || strings.Contains(lower, "mode=memory") || strings.HasPrefix(lower, "file::memory:") {
		return nil
	}
	backupPath := fmt.Sprintf("%s.pre-opencode-multi-account-%s.bak", openCodeSidecarBasePath(s.dbPath), time.Now().UTC().Format("20060102T150405.000000000Z"))
	if _, err := os.Stat(backupPath); err == nil {
		return fmt.Errorf("backup already exists: %s", backupPath)
	}
	if _, err := s.db.Exec(`VACUUM INTO ?`, backupPath); err != nil {
		return fmt.Errorf("create consistent backup %s: %w", backupPath, err)
	}
	if err := os.Chmod(backupPath, 0600); err != nil {
		_ = os.Remove(backupPath)
		return fmt.Errorf("restrict backup permissions %s: %w", backupPath, err)
	}
	return nil
}

func (s *Store) migrateOpenCodeMultiAccount() error {
	var tableCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='opencode_snapshots'`).Scan(&tableCount); err != nil {
		return err
	}
	if tableCount == 0 {
		return nil
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='opencode_accounts'`).Scan(&tableCount); err != nil {
		return err
	}
	if tableCount == 0 {
		return nil
	}
	hasAccountID, err := s.tableHasColumn("opencode_snapshots", "account_id")
	if err != nil {
		return err
	}
	if hasAccountID {
		for _, stmt := range []string{
			`CREATE INDEX IF NOT EXISTS idx_opencode_accounts_poll ON opencode_accounts(enabled, auth_status, next_poll_at, account_id)`,
			`CREATE INDEX IF NOT EXISTS idx_opencode_snapshots_account_captured ON opencode_snapshots(account_id, captured_at DESC, id DESC)`,
			`CREATE INDEX IF NOT EXISTS idx_opencode_quota_values_account_snapshot ON opencode_quota_values(account_id, snapshot_id)`,
			`CREATE INDEX IF NOT EXISTS idx_opencode_cycles_account_name_start ON opencode_reset_cycles(account_id, quota_name, cycle_start DESC)`,
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_opencode_cycles_account_name_active ON opencode_reset_cycles(account_id, quota_name) WHERE cycle_end IS NULL`,
		} {
			if _, err := s.db.Exec(stmt); err != nil {
				return err
			}
		}
		return nil
	}
	if err := s.backupBeforeOpenCodeMigration(); err != nil {
		return err
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var accountID int64
	err = tx.QueryRow(`SELECT id FROM provider_accounts WHERE provider='opencode' AND name='default'`).Scan(&accountID)
	if err == sql.ErrNoRows {
		result, insertErr := tx.Exec(`INSERT INTO provider_accounts(provider,name,created_at) VALUES('opencode','default',?)`, now)
		if insertErr != nil {
			return insertErr
		}
		accountID, err = result.LastInsertId()
	}
	if err != nil {
		return err
	}

	stmts := []string{
		`INSERT OR IGNORE INTO opencode_accounts(account_id, workspace_id, enabled, auth_status, credential_version, created_at, updated_at) VALUES (?, 'legacy-default', 0, 'disabled', 0, ?, ?)`,
		`CREATE TABLE opencode_snapshots_v2 (id INTEGER PRIMARY KEY AUTOINCREMENT, account_id INTEGER NOT NULL, captured_at TEXT NOT NULL, raw_json TEXT NOT NULL DEFAULT '', account_type TEXT NOT NULL DEFAULT '', plan_name TEXT NOT NULL DEFAULT '', quota_count INTEGER NOT NULL DEFAULT 0, FOREIGN KEY(account_id) REFERENCES opencode_accounts(account_id), UNIQUE(id, account_id), UNIQUE(account_id, captured_at))`,
		`CREATE TABLE opencode_quota_values_v2 (id INTEGER PRIMARY KEY AUTOINCREMENT, account_id INTEGER NOT NULL, snapshot_id INTEGER NOT NULL, quota_name TEXT NOT NULL, used REAL NOT NULL DEFAULT 0, limit_value REAL NOT NULL DEFAULT 0, utilization REAL NOT NULL DEFAULT 0, format TEXT NOT NULL DEFAULT 'percent', resets_at TEXT, FOREIGN KEY(snapshot_id, account_id) REFERENCES opencode_snapshots_v2(id, account_id) ON DELETE CASCADE, UNIQUE(account_id, snapshot_id, quota_name))`,
		`CREATE TABLE opencode_reset_cycles_v2 (id INTEGER PRIMARY KEY AUTOINCREMENT, account_id INTEGER NOT NULL, quota_name TEXT NOT NULL, cycle_start TEXT NOT NULL, cycle_end TEXT, resets_at TEXT, peak_utilization REAL NOT NULL DEFAULT 0, total_delta REAL NOT NULL DEFAULT 0, FOREIGN KEY(account_id) REFERENCES opencode_accounts(account_id))`,
		`INSERT INTO opencode_snapshots_v2(id, account_id, captured_at, raw_json, account_type, plan_name, quota_count) SELECT id, ?, captured_at, raw_json, account_type, plan_name, quota_count FROM opencode_snapshots`,
		`INSERT INTO opencode_quota_values_v2(id, account_id, snapshot_id, quota_name, used, limit_value, utilization, format, resets_at) SELECT id, ?, snapshot_id, quota_name, used, limit_value, utilization, format, resets_at FROM opencode_quota_values`,
		`INSERT INTO opencode_reset_cycles_v2(id, account_id, quota_name, cycle_start, cycle_end, resets_at, peak_utilization, total_delta) SELECT id, ?, quota_name, cycle_start, cycle_end, resets_at, peak_utilization, total_delta FROM opencode_reset_cycles`,
	}
	for i, stmt := range stmts {
		var args []any
		switch i {
		case 0:
			args = []any{accountID, now, now}
		case 4, 5, 6:
			args = []any{accountID}
		}
		if _, err := tx.Exec(stmt, args...); err != nil {
			return fmt.Errorf("migration statement %d: %w", i+1, err)
		}
	}
	if _, err := tx.Exec(`UPDATE opencode_reset_cycles_v2 AS old SET cycle_end = (SELECT MIN(newer.cycle_start) FROM opencode_reset_cycles_v2 newer WHERE newer.account_id = old.account_id AND newer.quota_name = old.quota_name AND newer.cycle_end IS NULL AND newer.id > old.id) WHERE old.cycle_end IS NULL AND EXISTS (SELECT 1 FROM opencode_reset_cycles_v2 newer WHERE newer.account_id = old.account_id AND newer.quota_name = old.quota_name AND newer.cycle_end IS NULL AND newer.id > old.id)`); err != nil {
		return err
	}
	for _, stmt := range []string{
		`DROP TABLE opencode_quota_values`, `DROP TABLE opencode_reset_cycles`, `DROP TABLE opencode_snapshots`,
		`ALTER TABLE opencode_snapshots_v2 RENAME TO opencode_snapshots`,
		`ALTER TABLE opencode_quota_values_v2 RENAME TO opencode_quota_values`,
		`ALTER TABLE opencode_reset_cycles_v2 RENAME TO opencode_reset_cycles`,
		`CREATE INDEX idx_opencode_snapshots_account_captured ON opencode_snapshots(account_id, captured_at DESC, id DESC)`,
		`CREATE INDEX idx_opencode_quota_values_account_snapshot ON opencode_quota_values(account_id, snapshot_id)`,
		`CREATE INDEX idx_opencode_cycles_account_name_start ON opencode_reset_cycles(account_id, quota_name, cycle_start DESC)`,
		`CREATE UNIQUE INDEX idx_opencode_cycles_account_name_active ON opencode_reset_cycles(account_id, quota_name) WHERE cycle_end IS NULL`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) CreateOpenCodeAccount(name, workspaceID, authCookie string, enabled bool) (*OpenCodeAccount, error) {
	name, workspaceID, authCookie = strings.TrimSpace(name), strings.TrimSpace(workspaceID), strings.TrimSpace(authCookie)
	if name == "" || workspaceID == "" || authCookie == "" {
		return nil, errors.New("name, workspace_id, and auth_cookie are required")
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var accountID int64
	var deletedAt sql.NullString
	err = tx.QueryRow(`SELECT id, deleted_at FROM provider_accounts WHERE provider = 'opencode' AND external_id = ?`, workspaceID).Scan(&accountID, &deletedAt)
	if err == sql.ErrNoRows {
		result, execErr := tx.Exec(`INSERT INTO provider_accounts(provider, name, created_at, external_id) VALUES ('opencode', ?, ?, ?)`, name, time.Now().UTC().Format(time.RFC3339Nano), workspaceID)
		if execErr != nil {
			return nil, fmt.Errorf("create provider account: %w", execErr)
		}
		accountID, err = result.LastInsertId()
	} else if err == nil {
		_, err = tx.Exec(`UPDATE provider_accounts SET name = ?, deleted_at = NULL WHERE id = ?`, name, accountID)
	}
	if err != nil {
		return nil, err
	}
	ciphertext, err := s.encryptOpenCodeCookie(accountID, authCookie)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	status := OpenCodeAuthPending
	if !enabled {
		status = OpenCodeAuthDisabled
	}
	_, err = tx.Exec(`INSERT INTO opencode_accounts(account_id, workspace_id, auth_cookie_ciphertext, enabled, auth_status, credential_version, created_at, updated_at) VALUES (?, ?, ?, ?, ?, 1, ?, ?) ON CONFLICT(account_id) DO UPDATE SET workspace_id=excluded.workspace_id, auth_cookie_ciphertext=excluded.auth_cookie_ciphertext, enabled=excluded.enabled, auth_status=excluded.auth_status, credential_version=opencode_accounts.credential_version+1, consecutive_failures=0, last_error_code='', next_poll_at=NULL, updated_at=excluded.updated_at`, accountID, workspaceID, ciphertext, enabled, status, now, now)
	if err != nil {
		return nil, fmt.Errorf("save OpenCode account: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetOpenCodeAccount(accountID, false)
}

func (s *Store) UpdateOpenCodeAccount(accountID int64, name, workspaceID string, authCookie *string, enabled bool) (*OpenCodeAccount, error) {
	if accountID <= 0 || strings.TrimSpace(name) == "" || strings.TrimSpace(workspaceID) == "" {
		return nil, errors.New("valid account_id, name, and workspace_id are required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE provider_accounts SET name = ?, external_id = ? WHERE id = ? AND provider = 'opencode' AND deleted_at IS NULL`, strings.TrimSpace(name), strings.TrimSpace(workspaceID), accountID); err != nil {
		return nil, err
	}
	status := OpenCodeAuthDisabled
	if enabled {
		status = OpenCodeAuthPending
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if authCookie != nil && strings.TrimSpace(*authCookie) != "" {
		ciphertext, err := s.encryptOpenCodeCookie(accountID, strings.TrimSpace(*authCookie))
		if err != nil {
			return nil, err
		}
		_, err = tx.Exec(`UPDATE opencode_accounts SET workspace_id=?, auth_cookie_ciphertext=?, enabled=?, auth_status=?, credential_version=credential_version+1, consecutive_failures=0, last_error_code='', next_poll_at=NULL, updated_at=? WHERE account_id=?`, strings.TrimSpace(workspaceID), ciphertext, enabled, status, now, accountID)
	} else {
		_, err = tx.Exec(`UPDATE opencode_accounts SET workspace_id=?, enabled=?, auth_status=CASE WHEN ?=0 THEN 'disabled' WHEN auth_status='disabled' THEN 'pending' ELSE auth_status END, updated_at=? WHERE account_id=?`, strings.TrimSpace(workspaceID), enabled, enabled, now, accountID)
	}
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetOpenCodeAccount(accountID, false)
}

func (s *Store) DeleteOpenCodeAccount(accountID int64) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE opencode_accounts SET auth_cookie_ciphertext='', enabled=0, auth_status='disabled', credential_version=credential_version+1, next_poll_at=NULL, updated_at=? WHERE account_id=?`, now, accountID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE provider_accounts SET deleted_at=? WHERE id=? AND provider='opencode'`, now, accountID); err != nil {
		return err
	}
	return tx.Commit()
}

func scanOpenCodeAccount(scanner interface{ Scan(...any) error }) (*OpenCodeAccount, string, error) {
	var a OpenCodeAccount
	var enabled int
	var ciphertext string
	var lastPoll, lastSuccess, lastError, nextPoll, deleted sql.NullString
	var created, updated string
	err := scanner.Scan(&a.AccountID, &a.Name, &a.WorkspaceID, &ciphertext, &enabled, &a.AuthStatus, &a.CredentialVersion, &a.ConsecutiveFailures, &lastPoll, &lastSuccess, &lastError, &a.LastErrorCode, &nextPoll, &created, &updated, &deleted)
	if err != nil {
		return nil, "", err
	}
	a.Enabled = enabled != 0
	a.HasAuthCookie = ciphertext != ""
	a.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	a.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	parseNullableTime := func(src sql.NullString) *time.Time {
		if !src.Valid || src.String == "" {
			return nil
		}
		t, err := time.Parse(time.RFC3339Nano, src.String)
		if err != nil {
			return nil
		}
		return &t
	}
	a.LastPollAt, a.LastSuccessAt, a.LastErrorAt, a.NextPollAt, a.DeletedAt = parseNullableTime(lastPoll), parseNullableTime(lastSuccess), parseNullableTime(lastError), parseNullableTime(nextPoll), parseNullableTime(deleted)
	return &a, ciphertext, nil
}

const openCodeAccountSelect = `SELECT oa.account_id, pa.name, oa.workspace_id, oa.auth_cookie_ciphertext, oa.enabled, oa.auth_status, oa.credential_version, oa.consecutive_failures, oa.last_poll_at, oa.last_success_at, oa.last_error_at, oa.last_error_code, oa.next_poll_at, oa.created_at, oa.updated_at, pa.deleted_at FROM opencode_accounts oa JOIN provider_accounts pa ON pa.id=oa.account_id`

func (s *Store) GetOpenCodeAccount(accountID int64, withSecret bool) (*OpenCodeAccount, error) {
	a, ciphertext, err := scanOpenCodeAccount(s.db.QueryRow(openCodeAccountSelect+` WHERE oa.account_id=?`, accountID))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if withSecret && ciphertext != "" {
		a.AuthCookie, err = s.decryptOpenCodeCookie(accountID, ciphertext)
		if err != nil {
			return nil, err
		}
	}
	return a, nil
}

func (s *Store) QueryOpenCodeAccounts(includeDeleted bool) ([]OpenCodeAccount, error) {
	query := openCodeAccountSelect
	if !includeDeleted {
		query += ` WHERE pa.deleted_at IS NULL`
	}
	query += ` ORDER BY oa.account_id`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OpenCodeAccount
	for rows.Next() {
		a, _, err := scanOpenCodeAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

func (s *Store) UpdateOpenCodeAccountPollState(accountID, credentialVersion int64, status, errorCode string, success bool, nextPollAt time.Time) (bool, error) {
	if !validOpenCodeAuthStatuses[status] {
		return false, errors.New("invalid OpenCode auth status")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var result sql.Result
	var err error
	var nextPoll any
	if !nextPollAt.IsZero() {
		nextPoll = nextPollAt.UTC().Format(time.RFC3339Nano)
	}
	if success {
		result, err = s.db.Exec(`UPDATE opencode_accounts SET auth_status=?, consecutive_failures=0, last_poll_at=?, last_success_at=?, last_error_code='', next_poll_at=?, updated_at=? WHERE account_id=? AND credential_version=?`, status, now, now, nextPoll, now, accountID, credentialVersion)
	} else {
		result, err = s.db.Exec(`UPDATE opencode_accounts SET auth_status=?, consecutive_failures=consecutive_failures+1, last_poll_at=?, last_error_at=?, last_error_code=?, next_poll_at=?, updated_at=? WHERE account_id=? AND credential_version=?`, status, now, now, errorCode, nextPoll, now, accountID, credentialVersion)
	}
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n > 0, err
}

func (s *Store) BootstrapLegacyOpenCodeAccount(envWorkspaceID, envAuthCookie string) error {
	raw, _ := s.GetSetting("provider_settings")
	var settings map[string]map[string]any
	var dbWorkspaceID, dbAuthCookie, name string
	var hadLegacyFields bool
	name = "default"
	if raw != "" && json.Unmarshal([]byte(raw), &settings) == nil {
		if oc := settings["opencode"]; oc != nil {
			_, hadWorkspaceID := oc["workspace_id"]
			_, hadAuthCookie := oc["auth_cookie"]
			hadLegacyFields = hadWorkspaceID || hadAuthCookie
			dbWorkspaceID, _ = oc["workspace_id"].(string)
			dbAuthCookie, _ = oc["auth_cookie"].(string)
			if n, _ := oc["name"].(string); strings.TrimSpace(n) != "" {
				name = strings.TrimSpace(n)
			}
		}
	}
	scrubbedSettings := ""
	if settings != nil && settings["opencode"] != nil {
		delete(settings["opencode"], "workspace_id")
		delete(settings["opencode"], "auth_cookie")
		clean, err := json.Marshal(settings)
		if err != nil {
			return err
		}
		scrubbedSettings = string(clean)
	}

	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM opencode_accounts oa JOIN provider_accounts pa ON pa.id=oa.account_id WHERE pa.deleted_at IS NULL AND oa.workspace_id != 'legacy-default'`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		// Encrypted database accounts take precedence. Remove any stale legacy
		// plaintext without importing or overwriting an existing account.
		if scrubbedSettings != "" && hadLegacyFields {
			if err := s.SetSetting("provider_settings", scrubbedSettings); err != nil {
				return err
			}
			_, err := s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
			return err
		}
		return nil
	}
	workspaceID, authCookie := dbWorkspaceID, dbAuthCookie
	if strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(authCookie) == "" {
		workspaceID, authCookie = envWorkspaceID, envAuthCookie
	}
	if strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(authCookie) == "" {
		if scrubbedSettings != "" && hadLegacyFields {
			if err := s.SetSetting("provider_settings", scrubbedSettings); err != nil {
				return err
			}
			_, err := s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
			return err
		}
		return nil
	}

	workspaceID, authCookie = strings.TrimSpace(workspaceID), strings.TrimSpace(authCookie)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var accountID int64
	err = tx.QueryRow(`SELECT account_id FROM opencode_accounts WHERE workspace_id='legacy-default'`).Scan(&accountID)
	if err == sql.ErrNoRows {
		err = tx.QueryRow(`SELECT id FROM provider_accounts WHERE provider='opencode' AND external_id=?`, workspaceID).Scan(&accountID)
	}
	if err == sql.ErrNoRows {
		result, insertErr := tx.Exec(`INSERT INTO provider_accounts(provider,name,created_at,external_id) VALUES('opencode',?,?,?)`, name, time.Now().UTC().Format(time.RFC3339Nano), workspaceID)
		if insertErr != nil {
			return insertErr
		}
		accountID, err = result.LastInsertId()
	}
	if err != nil {
		return err
	}
	ciphertext, err := s.encryptOpenCodeCookie(accountID, authCookie)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.Exec(`UPDATE provider_accounts SET name=?,external_id=?,deleted_at=NULL WHERE id=?`, name, workspaceID, accountID); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO opencode_accounts(account_id,workspace_id,auth_cookie_ciphertext,enabled,auth_status,credential_version,created_at,updated_at) VALUES(?,?,?,1,'pending',1,?,?) ON CONFLICT(account_id) DO UPDATE SET workspace_id=excluded.workspace_id,auth_cookie_ciphertext=excluded.auth_cookie_ciphertext,enabled=1,auth_status='pending',credential_version=opencode_accounts.credential_version+1,consecutive_failures=0,last_error_code='',next_poll_at=NULL,updated_at=excluded.updated_at`, accountID, workspaceID, ciphertext, now, now); err != nil {
		return err
	}
	if scrubbedSettings != "" && hadLegacyFields {
		if _, err := tx.Exec(`INSERT INTO settings(key,value) VALUES('provider_settings',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, scrubbedSettings); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	_, err = s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	return err
}
