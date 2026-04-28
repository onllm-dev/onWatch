package api

import (
	"encoding/json"
	"time"
)

// MoonshotBalanceData represents the balance data from Moonshot API.
type MoonshotBalanceData struct {
	AvailableBalance float64 `json:"available_balance"`
	VoucherBalance   float64 `json:"voucher_balance"`
	CashBalance      float64 `json:"cash_balance"`
}

// MoonshotBalanceResponse is the top-level response from GET /v1/users/me/balance.
type MoonshotBalanceResponse struct {
	Code int                 `json:"code"`
	Data MoonshotBalanceData `json:"data"`
}

// MoonshotSnapshot is the storage representation (flat, for SQLite).
type MoonshotSnapshot struct {
	ID               int64
	CapturedAt       time.Time
	AvailableBalance float64
	VoucherBalance   float64
	CashBalance      float64
}

// ToSnapshot converts MoonshotBalanceResponse to MoonshotSnapshot.
func (r *MoonshotBalanceResponse) ToSnapshot(capturedAt time.Time) *MoonshotSnapshot {
	return &MoonshotSnapshot{
		CapturedAt:       capturedAt.UTC(),
		AvailableBalance: r.Data.AvailableBalance,
		VoucherBalance:   r.Data.VoucherBalance,
		CashBalance:      r.Data.CashBalance,
	}
}

// ParseMoonshotResponse parses a Moonshot API response from JSON bytes.
func ParseMoonshotResponse(data []byte) (*MoonshotBalanceResponse, error) {
	var resp MoonshotBalanceResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
