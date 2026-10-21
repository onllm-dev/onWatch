package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ErrCodexResetCreditsUnavailable marks a detail fetch that cannot produce
// reset-credit data (404, rejected auth, malformed body). It is informational:
// reset-credit failures must never affect usage polling or pause state.
var ErrCodexResetCreditsUnavailable = errors.New("codex: reset credits unavailable")

// CodexResetCreditStatus is the normalized status of one banked reset credit.
// Unknown wire values map to CodexResetCreditStatusUnknown instead of failing.
type CodexResetCreditStatus int

const (
	CodexResetCreditStatusUnknown CodexResetCreditStatus = iota
	CodexResetCreditStatusAvailable
	CodexResetCreditStatusRedeeming
	CodexResetCreditStatusRedeemed
)

// String returns the lowercase wire-style name used in the database and API.
func (s CodexResetCreditStatus) String() string {
	switch s {
	case CodexResetCreditStatusAvailable:
		return "available"
	case CodexResetCreditStatusRedeeming:
		return "redeeming"
	case CodexResetCreditStatusRedeemed:
		return "redeemed"
	default:
		return "unknown"
	}
}

// ParseCodexResetCreditStatus normalizes a wire status. Unknown values are
// tolerated and reported as CodexResetCreditStatusUnknown.
func ParseCodexResetCreditStatus(raw string) CodexResetCreditStatus {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "available":
		return CodexResetCreditStatusAvailable
	case "redeeming":
		return CodexResetCreditStatusRedeeming
	case "redeemed":
		return CodexResetCreditStatusRedeemed
	default:
		return CodexResetCreditStatusUnknown
	}
}

// CodexResetTypeUnknown is stored for reset_type values onWatch does not know.
const CodexResetTypeUnknown = "unknown"

// CodexResetCredit is one banked reset credit from the detail endpoint.
type CodexResetCredit struct {
	ID          string
	ResetType   string
	Status      CodexResetCreditStatus
	GrantedAt   *time.Time
	ExpiresAt   *time.Time // nil means no usable expiry (never expires or unknown)
	Title       string
	Description string
	// UnsupportedByPlan is true only when the wire is_supported_by_plan flag
	// was explicitly false. The zero value means usable, so a credit built
	// directly (or a response that omits the flag) is never silently hidden;
	// unsupported credits are excluded from plan-usable counts and warnings.
	UnsupportedByPlan bool
	// ExpiryUnknown marks an expiry the backend reported but that cannot be
	// trusted (equal to granted_at, or already past while still available).
	ExpiryUnknown bool
}

// CodexResetCreditsResponse is the parsed reset-credits detail payload.
type CodexResetCreditsResponse struct {
	Credits []CodexResetCredit
	// AvailableCount is nil when the payload omits the field: unknown, not 0.
	AvailableCount *int64
	// ApplicableAvailableCount is the plan-usable subset, nil when absent.
	ApplicableAvailableCount *int64
	TotalEarnedCount         int64
}

// codexResetCreditsWire mirrors the wire shape. Optional fields stay pointers
// so a missing value is never confused with a zero/default.
type codexResetCreditsWire struct {
	Credits []struct {
		ID                string  `json:"id"`
		ResetType         string  `json:"reset_type"`
		Status            string  `json:"status"`
		GrantedAt         *string `json:"granted_at"`
		ExpiresAt         *string `json:"expires_at"`
		Title             *string `json:"title"`
		Description       *string `json:"description"`
		IsSupportedByPlan *bool   `json:"is_supported_by_plan"`
	} `json:"credits"`
	AvailableCount           *int64 `json:"available_count"`
	ApplicableAvailableCount *int64 `json:"applicable_available_count"`
	TotalEarnedCount         *int64 `json:"total_earned_count"`
}

// ParseCodexResetCreditsResponse parses a reset-credits detail payload using
// the wall clock to sanity-check expiries. A missing credits array is rejected
// as unavailable.
func ParseCodexResetCreditsResponse(data []byte) (*CodexResetCreditsResponse, error) {
	return ParseCodexResetCreditsResponseAt(data, time.Now())
}

// ParseCodexResetCreditsResponseAt is ParseCodexResetCreditsResponse with an
// injectable clock for deterministic expiry sanitization.
//
// The backend can report an available credit whose expires_at is already
// reached (the real payload observed has expires_at == granted_at to the
// microsecond). Taken literally that would put a past expiry on the card and
// fire the lead-time warning immediately with a wrong time, so such an expiry
// is reported as unknown and the credit sorts with the non-expiring ones.
func ParseCodexResetCreditsResponseAt(data []byte, now time.Time) (*CodexResetCreditsResponse, error) {
	var wire codexResetCreditsWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil, err
	}
	if wire.Credits == nil {
		return nil, fmt.Errorf("%w: credits field missing", ErrCodexResetCreditsUnavailable)
	}

	resp := &CodexResetCreditsResponse{
		Credits: make([]CodexResetCredit, 0, len(wire.Credits)),
	}
	if wire.AvailableCount != nil {
		count := *wire.AvailableCount
		resp.AvailableCount = &count
	}
	if wire.ApplicableAvailableCount != nil {
		count := *wire.ApplicableAvailableCount
		resp.ApplicableAvailableCount = &count
	}
	if wire.TotalEarnedCount != nil {
		resp.TotalEarnedCount = *wire.TotalEarnedCount
	}

	now = now.UTC()
	for _, c := range wire.Credits {
		credit := CodexResetCredit{
			ID:                strings.TrimSpace(c.ID),
			ResetType:         normalizeCodexResetType(c.ResetType),
			Status:            ParseCodexResetCreditStatus(c.Status),
			UnsupportedByPlan: c.IsSupportedByPlan != nil && !*c.IsSupportedByPlan,
		}
		credit.GrantedAt = parseCodexOptionalRC3339(c.GrantedAt)
		credit.ExpiresAt = parseCodexOptionalRC3339(c.ExpiresAt)
		if c.Title != nil {
			credit.Title = strings.TrimSpace(*c.Title)
		}
		if c.Description != nil {
			credit.Description = strings.TrimSpace(*c.Description)
		}
		if credit.ID == "" {
			continue // a credit without an ID cannot be tracked
		}
		credit.sanitizeExpiry(now)
		resp.Credits = append(resp.Credits, credit)
	}

	return resp, nil
}

// sanitizeExpiry replaces an untrustworthy expiry on an available credit with
// "no usable expiry". Available credits already past expiry are never valid:
// the list would not still offer them. Redeemed/redeeming credits keep their
// real timestamps, since they are not shown as upcoming.
func (c *CodexResetCredit) sanitizeExpiry(now time.Time) {
	if c.Status != CodexResetCreditStatusAvailable || c.ExpiresAt == nil {
		return
	}
	notAfterGrant := c.GrantedAt != nil && !c.ExpiresAt.After(*c.GrantedAt)
	if notAfterGrant || !c.ExpiresAt.After(now) {
		c.ExpiresAt = nil
		c.ExpiryUnknown = true
	}
}

func normalizeCodexResetType(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return CodexResetTypeUnknown
	}
	// The only reset type the CLI defines today. Anything else is kept as an
	// opaque string so the database never gains an unconstrained enum.
	if trimmed == "codex_rate_limits" {
		return trimmed
	}
	return CodexResetTypeUnknown
}

func parseCodexOptionalRC3339(raw *string) *time.Time {
	if raw == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*raw)
	if trimmed == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		return nil
	}
	utc := parsed.UTC()
	return &utc
}

// AvailableCodexResetCredits returns only available, plan-usable credits,
// sorted the way the Codex CLI sorts them: expiry ascending, never-expiring
// credits last.
func AvailableCodexResetCredits(credits []CodexResetCredit) []CodexResetCredit {
	available := make([]CodexResetCredit, 0, len(credits))
	for _, c := range credits {
		if c.Status == CodexResetCreditStatusAvailable && !c.UnsupportedByPlan {
			available = append(available, c)
		}
	}
	sort.SliceStable(available, func(i, j int) bool {
		left, right := available[i].ExpiresAt, available[j].ExpiresAt
		switch {
		case left == nil && right == nil:
			return false
		case left == nil:
			return false // never-expiring sorts last
		case right == nil:
			return true
		default:
			return left.Before(*right)
		}
	})
	return available
}
