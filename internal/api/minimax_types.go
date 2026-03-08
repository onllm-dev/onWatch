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
	CurrentIntervalUsageCount int         `json:"current_interval_usage_count"`
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
}

// MiniMaxSnapshot is a point-in-time capture.
type MiniMaxSnapshot struct {
	ID         int64
	CapturedAt time.Time
	Models     []MiniMaxModelQuota
	RawJSON    string
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
		used := model.CurrentIntervalUsageCount
		remain := total - used
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

		snapshot.Models = append(snapshot.Models, MiniMaxModelQuota{
			ModelName:      model.ModelName,
			Total:          total,
			Remain:         remain,
			Used:           used,
			UsedPercent:    usedPercent,
			ResetAt:        resetAt,
			WindowStart:    windowStart,
			WindowEnd:      windowEnd,
			TimeUntilReset: untilReset,
		})
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
