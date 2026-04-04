package api

import (
	"encoding/json"
	"sort"
	"strconv"
	"time"
)

// MiniMaxBaseResp contains API status metadata.
type MiniMaxBaseResp struct {
	StatusCode int    `json:"status_code"`
	StatusMsg  string `json:"status_msg"`
}

// MiniMaxModelRemain represents quota remain data for one model.
type MiniMaxModelRemain struct {
	ModelName                 string      `json:"model_name"`
	StartTime                 interface{} `json:"start_time"`
	EndTime                   interface{} `json:"end_time"`
	RemainsTime               int64       `json:"remains_time"`
	CurrentIntervalTotalCount int         `json:"current_interval_total_count"`
	// Despite the field name, this endpoint returns remaining requests.
	CurrentIntervalUsageCount int `json:"current_interval_usage_count"`

	// Weekly quota fields - only present for accounts purchased from 2026-03-23 onwards.
	CurrentWeeklyTotalCount int         `json:"current_weekly_total_count"`
	CurrentWeeklyUsageCount int         `json:"current_weekly_usage_count"`
	WeeklyStartTime         interface{} `json:"weekly_start_time"`
	WeeklyEndTime           interface{} `json:"weekly_end_time"`
	WeeklyRemainsTime       int64       `json:"weekly_remains_time"`
}

// MiniMaxRemainsResponse is the full API response.
type MiniMaxRemainsResponse struct {
	BaseResp     MiniMaxBaseResp      `json:"base_resp"`
	ModelRemains []MiniMaxModelRemain `json:"model_remains"`
}

// MiniMaxModelQuota is normalized for storage.
type MiniMaxModelQuota struct {
	ModelName      string
	Total          int
	Remain         int
	Used           int
	UsedPercent    float64
	ResetAt        *time.Time
	WindowStart    *time.Time
	WindowEnd      *time.Time
	TimeUntilReset time.Duration

	// Weekly quota - zero values when not available (pre-March-23 accounts).
	WeeklyTotal          int
	WeeklyRemain         int
	WeeklyUsed           int
	WeeklyUsedPercent    float64
	WeeklyResetAt        *time.Time
	WeeklyWindowStart    *time.Time
	WeeklyWindowEnd      *time.Time
	WeeklyTimeUntilReset time.Duration
	HasWeeklyQuota       bool
}

// MiniMaxSnapshot is a point-in-time capture.
type MiniMaxSnapshot struct {
	ID         int64
	CapturedAt time.Time
	Models     []MiniMaxModelQuota
	RawJSON    string
}

// IsSharedQuota returns true when all active models report the same quota pool.
func (s *MiniMaxSnapshot) IsSharedQuota() bool {
	if s == nil || len(s.Models) <= 1 {
		return false
	}
	first := s.Models[0]
	for _, m := range s.Models[1:] {
		if m.Total != first.Total || m.Used != first.Used || m.Remain != first.Remain {
			return false
		}
		switch {
		case first.ResetAt == nil && m.ResetAt == nil:
		case first.ResetAt == nil || m.ResetAt == nil:
			return false
		default:
			if first.ResetAt.Sub(*m.ResetAt).Abs() > time.Second {
				return false
			}
		}
	}
	return true
}

// ActiveModels returns sorted model names from the snapshot.
func (s *MiniMaxSnapshot) ActiveModels() []string {
	if s == nil || len(s.Models) == 0 {
		return nil
	}
	names := make([]string, 0, len(s.Models))
	for _, m := range s.Models {
		if m.ModelName == "" {
			continue
		}
		names = append(names, m.ModelName)
	}
	sort.Strings(names)
	return names
}

// MergedQuota returns a single logical quota for a shared MiniMax pool.
func (s *MiniMaxSnapshot) MergedQuota() *MiniMaxModelQuota {
	if s == nil || len(s.Models) == 0 {
		return nil
	}
	first := s.Models[0]
	return &MiniMaxModelQuota{
		ModelName:            "MiniMax Coding Plan",
		Total:                first.Total,
		Remain:               first.Remain,
		Used:                 first.Used,
		UsedPercent:          first.UsedPercent,
		ResetAt:              first.ResetAt,
		WindowStart:          first.WindowStart,
		WindowEnd:            first.WindowEnd,
		TimeUntilReset:       first.TimeUntilReset,
		HasWeeklyQuota:       first.HasWeeklyQuota,
		WeeklyTotal:          first.WeeklyTotal,
		WeeklyRemain:         first.WeeklyRemain,
		WeeklyUsed:           first.WeeklyUsed,
		WeeklyUsedPercent:    first.WeeklyUsedPercent,
		WeeklyResetAt:        first.WeeklyResetAt,
		WeeklyWindowStart:    first.WeeklyWindowStart,
		WeeklyWindowEnd:      first.WeeklyWindowEnd,
		WeeklyTimeUntilReset: first.WeeklyTimeUntilReset,
	}
}

// ActiveModelNames returns sorted model names present in the response.
func (r MiniMaxRemainsResponse) ActiveModelNames() []string {
	if len(r.ModelRemains) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(r.ModelRemains))
	names := make([]string, 0, len(r.ModelRemains))
	for _, model := range r.ModelRemains {
		if model.ModelName == "" {
			continue
		}
		if _, exists := seen[model.ModelName]; exists {
			continue
		}
		seen[model.ModelName] = struct{}{}
		names = append(names, model.ModelName)
	}
	sort.Strings(names)
	return names
}

func parseMiniMaxTimestamp(v interface{}) *time.Time {
	switch ts := v.(type) {
	case nil:
		return nil
	case string:
		ts = stringsTrimSpace(ts)
		if ts == "" {
			return nil
		}
		if t, err := time.Parse(time.RFC3339, ts); err == nil {
			u := t.UTC()
			return &u
		}
		if n, err := strconv.ParseInt(ts, 10, 64); err == nil {
			t := time.UnixMilli(n).UTC()
			return &t
		}
	case float64:
		t := time.UnixMilli(int64(ts)).UTC()
		return &t
	case int64:
		t := time.UnixMilli(ts).UTC()
		return &t
	case int:
		t := time.UnixMilli(int64(ts)).UTC()
		return &t
	case json.Number:
		if n, err := ts.Int64(); err == nil {
			t := time.UnixMilli(n).UTC()
			return &t
		}
	}
	return nil
}

func stringsTrimSpace(s string) string {
	start, end := 0, len(s)
	for start < end {
		c := s[start]
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			break
		}
		start++
	}
	for end > start {
		c := s[end-1]
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			break
		}
		end--
	}
	return s[start:end]
}

// ToSnapshot converts API response to storage-friendly snapshot format.
func (r MiniMaxRemainsResponse) ToSnapshot(capturedAt time.Time) *MiniMaxSnapshot {
	snapshot := &MiniMaxSnapshot{CapturedAt: capturedAt.UTC()}

	for _, model := range r.ModelRemains {
		if model.ModelName == "" {
			continue
		}

		total := model.CurrentIntervalTotalCount
		// NOTE: The /coding_plan/remains endpoint reports what remains.
		// MiniMax names the field current_interval_usage_count, but it is
		// actually the remaining request count for the current window.
		remain := model.CurrentIntervalUsageCount
		used := total - remain
		if used < 0 {
			used = 0
		}
		if remain < 0 {
			remain = 0
		}

		windowStart := parseMiniMaxTimestamp(model.StartTime)
		windowEnd := parseMiniMaxTimestamp(model.EndTime)

		var resetAt *time.Time
		var untilReset time.Duration
		if model.RemainsTime > 0 {
			d := time.Duration(model.RemainsTime) * time.Millisecond
			r := snapshot.CapturedAt.Add(d)
			resetAt = &r
			untilReset = d
		} else if windowEnd != nil {
			resetAt = windowEnd
			untilReset = windowEnd.Sub(snapshot.CapturedAt)
			if untilReset < 0 {
				untilReset = 0
			}
		}

		usedPercent := 0.0
		if total > 0 {
			usedPercent = (float64(used) / float64(total)) * 100
		}

		quota := MiniMaxModelQuota{
			ModelName:      model.ModelName,
			Total:          total,
			Remain:         remain,
			Used:           used,
			UsedPercent:    usedPercent,
			ResetAt:        resetAt,
			WindowStart:    windowStart,
			WindowEnd:      windowEnd,
			TimeUntilReset: untilReset,
		}

		// Parse weekly quota fields if present.
		if model.CurrentWeeklyTotalCount > 0 || model.CurrentWeeklyUsageCount > 0 {
			quota.HasWeeklyQuota = true
			quota.WeeklyTotal = model.CurrentWeeklyTotalCount
			// Same naming quirk: current_weekly_usage_count is actually remaining.
			quota.WeeklyRemain = model.CurrentWeeklyUsageCount
			quota.WeeklyUsed = quota.WeeklyTotal - quota.WeeklyRemain
			if quota.WeeklyUsed < 0 {
				quota.WeeklyUsed = 0
			}
			if quota.WeeklyTotal > 0 {
				quota.WeeklyUsedPercent = (float64(quota.WeeklyUsed) / float64(quota.WeeklyTotal)) * 100
			}
			quota.WeeklyWindowStart = parseMiniMaxTimestamp(model.WeeklyStartTime)
			quota.WeeklyWindowEnd = parseMiniMaxTimestamp(model.WeeklyEndTime)
			if model.WeeklyRemainsTime > 0 {
				d := time.Duration(model.WeeklyRemainsTime) * time.Millisecond
				wr := snapshot.CapturedAt.Add(d)
				quota.WeeklyResetAt = &wr
				quota.WeeklyTimeUntilReset = d
			} else if quota.WeeklyWindowEnd != nil {
				quota.WeeklyResetAt = quota.WeeklyWindowEnd
				quota.WeeklyTimeUntilReset = quota.WeeklyWindowEnd.Sub(snapshot.CapturedAt)
				if quota.WeeklyTimeUntilReset < 0 {
					quota.WeeklyTimeUntilReset = 0
				}
			}
		}

		snapshot.Models = append(snapshot.Models, quota)
	}

	if raw, err := json.Marshal(r); err == nil {
		snapshot.RawJSON = string(raw)
	}

	return snapshot
}

// ParseMiniMaxResponse parses raw JSON bytes into MiniMaxRemainsResponse.
func ParseMiniMaxResponse(data []byte) (*MiniMaxRemainsResponse, error) {
	var resp MiniMaxRemainsResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// MiniMaxDisplayName returns a human-readable model label.
func MiniMaxDisplayName(key string) string {
	return key
}
