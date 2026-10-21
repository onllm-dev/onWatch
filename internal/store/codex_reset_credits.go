package store

import (
	"database/sql"
	"fmt"
	"time"
)

// CodexResetCreditRecord is one banked reset credit row for an account.
type CodexResetCreditRecord struct {
	AccountID   int64
	CreditID    string
	Status      string
	ResetType   string
	Title       string
	Description string
	GrantedAt   *time.Time
	ExpiresAt   *time.Time // nil means the credit never expires
	// UnsupportedByPlan mirrors an explicit is_supported_by_plan=false. Such
	// credits are excluded from the next-expiry display and expiry warnings.
	UnsupportedByPlan bool
	// ExpiryUnknown marks an expiry the backend reported but that is not
	// trustworthy (equal to granted_at, or already past while available).
	ExpiryUnknown bool
	FirstSeen     time.Time
	LastSeen      time.Time
	GoneAt        *time.Time
	NotifiedAt    *time.Time
}

// SyncCodexResetCredits upserts the credits from one successful detail fetch
// and marks the ones that disappeared as gone (never deletes them, so the
// expiry-notification dedupe survives). first_seen is preserved across
// updates; gone_at is cleared when a credit reappears.
func (s *Store) SyncCodexResetCredits(accountID int64, credits []CodexResetCreditRecord, seenAt time.Time) error {
	if accountID == 0 {
		accountID = DefaultCodexAccountID
	}
	seenAt = seenAt.UTC()

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("codex reset credits: begin: %w", err)
	}
	defer tx.Rollback()

	seen := make(map[string]bool, len(credits))
	for _, c := range credits {
		if c.CreditID == "" {
			continue
		}
		seen[c.CreditID] = true
		var grantedAt, expiresAt interface{}
		if c.GrantedAt != nil {
			grantedAt = c.GrantedAt.UTC().Format(time.RFC3339)
		}
		if c.ExpiresAt != nil {
			expiresAt = c.ExpiresAt.UTC().Format(time.RFC3339)
		}
		if _, err := tx.Exec(
			`INSERT INTO codex_reset_credits
				(account_id, credit_id, status, reset_type, title, description, granted_at, expires_at,
				 unsupported_by_plan, expiry_unknown, first_seen, last_seen, gone_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)
			ON CONFLICT(account_id, credit_id) DO UPDATE SET
				status = excluded.status,
				reset_type = excluded.reset_type,
				title = excluded.title,
				description = excluded.description,
				granted_at = excluded.granted_at,
				expires_at = excluded.expires_at,
				unsupported_by_plan = excluded.unsupported_by_plan,
				expiry_unknown = excluded.expiry_unknown,
				last_seen = excluded.last_seen,
				gone_at = NULL`,
			accountID,
			c.CreditID,
			c.Status,
			c.ResetType,
			c.Title,
			c.Description,
			grantedAt,
			expiresAt,
			c.UnsupportedByPlan,
			c.ExpiryUnknown,
			seenAt.Format(time.RFC3339),
			seenAt.Format(time.RFC3339),
		); err != nil {
			return fmt.Errorf("codex reset credits: upsert %q: %w", c.CreditID, err)
		}
	}

	// Mark every credit this account previously had but the fetch did not
	// return. A bounded, parameterized update over the account's rows only.
	rows, err := tx.Query(
		`SELECT credit_id FROM codex_reset_credits WHERE account_id = ? AND gone_at IS NULL LIMIT 500`,
		accountID,
	)
	if err != nil {
		return fmt.Errorf("codex reset credits: list existing: %w", err)
	}
	var missing []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("codex reset credits: scan existing: %w", err)
		}
		if !seen[id] {
			missing = append(missing, id)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("codex reset credits: iterate existing: %w", err)
	}
	rows.Close()

	for _, id := range missing {
		if _, err := tx.Exec(
			`UPDATE codex_reset_credits SET gone_at = ? WHERE account_id = ? AND credit_id = ?`,
			seenAt.Format(time.RFC3339), accountID, id,
		); err != nil {
			return fmt.Errorf("codex reset credits: mark gone %q: %w", id, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("codex reset credits: commit: %w", err)
	}
	return nil
}

// QueryCodexResetCredits returns the account's tracked credits, bounded and
// ordered by expiry (never-expiring last) then ID for a stable display.
func (s *Store) QueryCodexResetCredits(accountID int64) ([]CodexResetCreditRecord, error) {
	if accountID == 0 {
		accountID = DefaultCodexAccountID
	}
	rows, err := s.db.Query(
		`SELECT account_id, credit_id, status, reset_type, title, description, granted_at, expires_at,
			unsupported_by_plan, expiry_unknown, first_seen, last_seen, gone_at, notified_at
		FROM codex_reset_credits
		WHERE account_id = ?
		ORDER BY (expires_at IS NULL), expires_at ASC, credit_id ASC
		LIMIT 200`,
		accountID,
	)
	if err != nil {
		return nil, fmt.Errorf("codex reset credits: query: %w", err)
	}
	defer rows.Close()

	var out []CodexResetCreditRecord
	for rows.Next() {
		record, err := scanCodexResetCredit(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

// QueryCodexResetCreditsNeedingExpiryNotice returns available credits that
// expire within lead time, have not been notified yet, and have not
// disappeared. Never-expiring credits are never returned.
func (s *Store) QueryCodexResetCreditsNeedingExpiryNotice(accountID int64, now time.Time, lead time.Duration) ([]CodexResetCreditRecord, error) {
	if accountID == 0 {
		accountID = DefaultCodexAccountID
	}
	now = now.UTC()
	cutoff := now.Add(lead).UTC()

	rows, err := s.db.Query(
		`SELECT account_id, credit_id, status, reset_type, title, description, granted_at, expires_at,
			unsupported_by_plan, expiry_unknown, first_seen, last_seen, gone_at, notified_at
		FROM codex_reset_credits
		WHERE account_id = ?
			AND status = 'available'
			AND gone_at IS NULL
			AND notified_at IS NULL
			AND unsupported_by_plan = 0
			AND expiry_unknown = 0
			AND expires_at IS NOT NULL
			AND expires_at > ?
			AND expires_at <= ?
		ORDER BY expires_at ASC, credit_id ASC
		LIMIT 50`,
		accountID,
		now.Format(time.RFC3339),
		cutoff.Format(time.RFC3339),
	)
	if err != nil {
		return nil, fmt.Errorf("codex reset credits: query expiry notices: %w", err)
	}
	defer rows.Close()

	var out []CodexResetCreditRecord
	for rows.Next() {
		record, err := scanCodexResetCredit(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

// MarkCodexResetCreditNotified records the expiry-notification timestamp. The
// first marker wins; a repeated call leaves it untouched.
func (s *Store) MarkCodexResetCreditNotified(accountID int64, creditID string, notifiedAt time.Time) error {
	if accountID == 0 {
		accountID = DefaultCodexAccountID
	}
	_, err := s.db.Exec(
		`UPDATE codex_reset_credits SET notified_at = ?
		WHERE account_id = ? AND credit_id = ? AND notified_at IS NULL`,
		notifiedAt.UTC().Format(time.RFC3339), accountID, creditID,
	)
	if err != nil {
		return fmt.Errorf("codex reset credits: mark notified: %w", err)
	}
	return nil
}

// ClaimCodexResetCreditNotice atomically claims the one notification slot for a
// credit. It reports true only for the caller that set the marker, so a second
// poller (or a restart racing the first send) never re-sends.
func (s *Store) ClaimCodexResetCreditNotice(accountID int64, creditID string, claimedAt time.Time) (bool, error) {
	if accountID == 0 {
		accountID = DefaultCodexAccountID
	}
	result, err := s.db.Exec(
		`UPDATE codex_reset_credits SET notified_at = ?
		WHERE account_id = ? AND credit_id = ? AND notified_at IS NULL`,
		claimedAt.UTC().Format(time.RFC3339), accountID, creditID,
	)
	if err != nil {
		return false, fmt.Errorf("codex reset credits: claim notice: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("codex reset credits: claim rows: %w", err)
	}
	return affected > 0, nil
}

// ReleaseCodexResetCreditNotice clears a claim made by this exact timestamp,
// so a delivery that reached no channel can be retried on the next fetch.
func (s *Store) ReleaseCodexResetCreditNotice(accountID int64, creditID string, claimedAt time.Time) error {
	if accountID == 0 {
		accountID = DefaultCodexAccountID
	}
	_, err := s.db.Exec(
		`UPDATE codex_reset_credits SET notified_at = NULL
		WHERE account_id = ? AND credit_id = ? AND notified_at = ?`,
		accountID, creditID, claimedAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("codex reset credits: release notice: %w", err)
	}
	return nil
}

// CodexResetCreditCheckStatus is the last detail-fetch outcome for an account.
type CodexResetCreditCheckStatus struct {
	AccountID      int64
	Status         string // "ok", "unavailable"
	AvailableCount *int64
	// ApplicableCount is the plan-usable subset, nil when the payload omitted it.
	ApplicableCount *int64
	LastCheckedAt   time.Time
}

// SetCodexResetCreditCheckStatus upserts the last detail-fetch outcome. A nil
// count is stored as NULL, so "unknown" never becomes 0.
func (s *Store) SetCodexResetCreditCheckStatus(accountID int64, status string, availableCount, applicableCount *int64, checkedAt time.Time) error {
	if accountID == 0 {
		accountID = DefaultCodexAccountID
	}
	var count, applicable interface{}
	if availableCount != nil {
		count = *availableCount
	}
	if applicableCount != nil {
		applicable = *applicableCount
	}
	_, err := s.db.Exec(
		`INSERT INTO codex_reset_credit_checks (account_id, status, available_count, applicable_count, last_checked_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(account_id) DO UPDATE SET
			status = excluded.status,
			available_count = excluded.available_count,
			applicable_count = excluded.applicable_count,
			last_checked_at = excluded.last_checked_at`,
		accountID, status, count, applicable, checkedAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("codex reset credits: set check status: %w", err)
	}
	return nil
}

// QueryCodexResetCreditCheckStatus returns the last detail-fetch outcome, or
// nil when the account has never been checked successfully or otherwise.
func (s *Store) QueryCodexResetCreditCheckStatus(accountID int64) (*CodexResetCreditCheckStatus, error) {
	if accountID == 0 {
		accountID = DefaultCodexAccountID
	}
	var status CodexResetCreditCheckStatus
	var availableCount, applicableCount sql.NullInt64
	var checkedAt string
	err := s.db.QueryRow(
		`SELECT account_id, status, available_count, applicable_count, last_checked_at
		FROM codex_reset_credit_checks WHERE account_id = ?`,
		accountID,
	).Scan(&status.AccountID, &status.Status, &availableCount, &applicableCount, &checkedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("codex reset credits: query check status: %w", err)
	}
	if availableCount.Valid {
		count := availableCount.Int64
		status.AvailableCount = &count
	}
	if applicableCount.Valid {
		count := applicableCount.Int64
		status.ApplicableCount = &count
	}
	if parsed, parseErr := time.Parse(time.RFC3339, checkedAt); parseErr == nil {
		status.LastCheckedAt = parsed
	}
	return &status, nil
}

func scanCodexResetCredit(rows *sql.Rows) (CodexResetCreditRecord, error) {
	var rec CodexResetCreditRecord
	var grantedAt, expiresAt, firstSeen, lastSeen, goneAt, notifiedAt sql.NullString
	if err := rows.Scan(
		&rec.AccountID, &rec.CreditID, &rec.Status, &rec.ResetType,
		&rec.Title, &rec.Description,
		&grantedAt, &expiresAt,
		&rec.UnsupportedByPlan, &rec.ExpiryUnknown,
		&firstSeen, &lastSeen, &goneAt, &notifiedAt,
	); err != nil {
		return rec, fmt.Errorf("codex reset credits: scan: %w", err)
	}
	rec.GrantedAt = parseNullableCodexTime(grantedAt)
	rec.ExpiresAt = parseNullableCodexTime(expiresAt)
	if firstSeen.Valid {
		rec.FirstSeen, _ = time.Parse(time.RFC3339Nano, firstSeen.String)
	}
	if lastSeen.Valid {
		rec.LastSeen, _ = time.Parse(time.RFC3339Nano, lastSeen.String)
	}
	rec.GoneAt = parseNullableCodexTime(goneAt)
	rec.NotifiedAt = parseNullableCodexTime(notifiedAt)
	return rec, nil
}

func parseNullableCodexTime(value sql.NullString) *time.Time {
	if !value.Valid || value.String == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value.String)
	if err != nil {
		return nil
	}
	return &parsed
}
