package api

import (
	"encoding/json"
	"strconv"
	"time"
)

// DeepSeekBalanceInfo represents the balance data from DeepSeek API.
type DeepSeekBalanceInfo struct {
	Currency       string `json:"currency"`
	TotalBalance   string `json:"total_balance"`
	GrantedBalance string `json:"granted_balance"`
	ToppedUpBalance string `json:"topped_up_balance"`
}

// DeepSeekBalanceResponse is the top-level response from GET /user/balance.
type DeepSeekBalanceResponse struct {
	IsAvailable  bool                  `json:"is_available"`
	BalanceInfos []DeepSeekBalanceInfo `json:"balance_infos"`
}

// DeepSeekSnapshot is the storage representation (flat, for SQLite).
type DeepSeekSnapshot struct {
	ID              int64
	CapturedAt      time.Time
	IsAvailable     bool
	Currency        string
	TotalBalance    float64
	GrantedBalance  float64
	ToppedUpBalance float64
}

// ToSnapshot converts DeepSeekBalanceResponse to DeepSeekSnapshot.
func (r *DeepSeekBalanceResponse) ToSnapshot(capturedAt time.Time) *DeepSeekSnapshot {
	snapshot := &DeepSeekSnapshot{
		CapturedAt:  capturedAt.UTC(),
		IsAvailable: r.IsAvailable,
	}

	if len(r.BalanceInfos) > 0 {
		// Priority: CNY over USD if multiple, though usually it's just one or the other.
		var info *DeepSeekBalanceInfo
		for i := range r.BalanceInfos {
			if r.BalanceInfos[i].Currency == "CNY" {
				info = &r.BalanceInfos[i]
				break
			}
		}
		if info == nil {
			info = &r.BalanceInfos[0]
		}

		snapshot.Currency = info.Currency
		if v, err := strconv.ParseFloat(info.TotalBalance, 64); err == nil {
			snapshot.TotalBalance = v
		}
		if v, err := strconv.ParseFloat(info.GrantedBalance, 64); err == nil {
			snapshot.GrantedBalance = v
		}
		if v, err := strconv.ParseFloat(info.ToppedUpBalance, 64); err == nil {
			snapshot.ToppedUpBalance = v
		}
	}

	return snapshot
}

// ParseDeepSeekResponse parses a DeepSeek API response from JSON bytes.
func ParseDeepSeekResponse(data []byte) (*DeepSeekBalanceResponse, error) {
	var resp DeepSeekBalanceResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
