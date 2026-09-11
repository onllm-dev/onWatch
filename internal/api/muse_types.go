package api

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Muse coding-plan quota model.
//
// Meta exposes no documented aggregate usage or billing endpoint for the
// Muse coding plan (probed /v1/usage, /v1/billing, /v1/organization/usage,
// /v1/organization/costs - all 404). The same subscription snapshot the
// `muse` CLI `/usage` command shows is carried inline on a minimal streamed
// response: POST https://api.meta.ai/v1/responses with stream:true. Each
// poll therefore costs one tiny probe response (input "ping",
// max_output_tokens 16).
//
// The snapshot shape (verified against the live API):
//
//	{"subscription":{
//	  "tier":"27681527378179523",
//	  "weekly":{"resets_at":"1789344000","used_percent":"12.5"},
//	  "window":{"resets_at":"1789078632","used_percent":"34","window_duration_mins":"300"}}}
//
// Scalar values arrive as JSON strings; resets_at is epoch seconds (epoch
// millis are also accepted defensively). tier is an opaque account tier ID.

// Muse quota window names.
const (
	MuseQuotaWindow5H = "window_5h"
	MuseQuotaWeekly   = "weekly"
)

// MuseQuotaFormat mirrors the other quota-list providers.
type MuseQuotaFormat string

const (
	MuseQuotaFormatPercent MuseQuotaFormat = "percent"
)

// DefaultMuseModel is used when no model is configured and none can be read
// from the native Muse settings file.
const DefaultMuseModel = "muse-spark-1.3"

// MuseQuota is one tracked quota window (5h prompts or weekly).
type MuseQuota struct {
	Name        string
	Used        float64 // used percent, 0-100
	Limit       float64 // always 100 for percent windows
	Utilization float64 // percent used; equals Used, clamped to 0-100
	Format      MuseQuotaFormat
	ResetsAt    *time.Time
}

// MuseSnapshot is a point-in-time capture of Muse coding-plan usage.
type MuseSnapshot struct {
	ID         int64
	CapturedAt time.Time
	RawJSON    string

	Tier  string // opaque account tier ID; display only when human-readable
	Model string // model used for the usage probe

	WindowUsedPct      float64
	WindowResetsAt     *time.Time
	WindowDurationMins int

	WeeklyUsedPct  float64
	WeeklyResetsAt *time.Time

	Quotas []MuseQuota
}

// museFlexFloat accepts JSON numbers or numeric strings ("34", "12.5").
type museFlexFloat float64

func (f *museFlexFloat) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	if s[0] == '"' {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return err
		}
		str = strings.TrimSpace(str)
		if str == "" {
			*f = 0
			return nil
		}
		v, err := strconv.ParseFloat(strings.ReplaceAll(str, ",", ""), 64)
		if err != nil {
			return err
		}
		*f = museFlexFloat(v)
		return nil
	}
	var v float64
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*f = museFlexFloat(v)
	return nil
}

// museWindow is one quota window inside the subscription snapshot.
type museWindow struct {
	UsedPercent        float64
	ResetsAt           *time.Time
	WindowDurationMins int
}

// UnmarshalJSON decodes string-or-number scalars into parsed values.
func (w *museWindow) UnmarshalJSON(b []byte) error {
	var wire struct {
		UsedPercent        museFlexFloat `json:"used_percent"`
		ResetsAt           museFlexFloat `json:"resets_at"`
		WindowDurationMins museFlexFloat `json:"window_duration_mins"`
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		return err
	}
	w.UsedPercent = float64(wire.UsedPercent)
	w.ResetsAt = parseMuseEpoch(wire.ResetsAt)
	w.WindowDurationMins = int(wire.WindowDurationMins)
	return nil
}

// MuseSubscription is the parsed usage snapshot.
type MuseSubscription struct {
	Tier   string
	Window *museWindow
	Weekly *museWindow
}

type museSubscriptionWire struct {
	Tier   string      `json:"tier"`
	Plan   string      `json:"plan"`
	Window *museWindow `json:"window"`
	Weekly *museWindow `json:"weekly"`
}

func (w *museSubscriptionWire) subscription() *MuseSubscription {
	tier := strings.TrimSpace(w.Tier)
	if tier == "" {
		tier = strings.TrimSpace(w.Plan)
	}
	return &MuseSubscription{Tier: tier, Window: w.Window, Weekly: w.Weekly}
}

func museHasQuota(sub *MuseSubscription) bool {
	if sub == nil {
		return false
	}
	return sub.Window != nil || sub.Weekly != nil
}

// parseMuseSubscriptionData extracts a usage snapshot from one SSE data
// payload (without the "data:" prefix). Empty / "[DONE]" / non-JSON lines
// yield nil.
func parseMuseSubscriptionData(data string) *MuseSubscription {
	data = strings.TrimSpace(data)
	if data == "" || data == "[DONE]" {
		return nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(data), &obj); err != nil {
		return nil
	}
	raw, ok := obj["subscription"]
	if !ok {
		return nil
	}
	var wire museSubscriptionWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil
	}
	candidate := wire.subscription()
	if !museHasQuota(candidate) {
		return nil
	}
	return candidate
}

// ParseMuseSubscriptionEvents returns the last subscription snapshot seen in
// SSE data lines (payloads without the "data:" prefix). Lines that are empty,
// "[DONE]", or not JSON objects are skipped.
func ParseMuseSubscriptionEvents(dataLines []string) (*MuseSubscription, error) {
	var snapshot *MuseSubscription
	for _, data := range dataLines {
		if candidate := parseMuseSubscriptionData(data); candidate != nil {
			snapshot = candidate
		}
	}
	if snapshot == nil {
		return nil, fmt.Errorf("muse: stream carried no subscription usage")
	}
	return snapshot, nil
}

// parseMuseEpoch converts an epoch seconds (or millis) value to UTC.
// Zero or negative values yield nil.
func parseMuseEpoch(v museFlexFloat) *time.Time {
	f := float64(v)
	if f <= 0 {
		return nil
	}
	if f > 1e12 {
		f /= 1000
	}
	t := time.Unix(int64(f), 0).UTC()
	return &t
}

func clampMusePercent(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// BuildMuseSnapshot combines a subscription snapshot into storage form.
func BuildMuseSnapshot(sub *MuseSubscription, model, rawJSON string, capturedAt time.Time) *MuseSnapshot {
	capturedAt = capturedAt.UTC()
	snap := &MuseSnapshot{
		CapturedAt: capturedAt,
		RawJSON:    rawJSON,
		Model:      strings.TrimSpace(model),
	}
	if sub == nil {
		return snap
	}
	snap.Tier = sub.Tier
	if sub.Window != nil {
		used := clampMusePercent(sub.Window.UsedPercent)
		resetsAt := sub.Window.ResetsAt
		snap.WindowUsedPct = used
		snap.WindowResetsAt = resetsAt
		snap.WindowDurationMins = sub.Window.WindowDurationMins
		snap.Quotas = append(snap.Quotas, MuseQuota{
			Name:        MuseQuotaWindow5H,
			Used:        used,
			Limit:       100,
			Utilization: used,
			Format:      MuseQuotaFormatPercent,
			ResetsAt:    resetsAt,
		})
	}
	if sub.Weekly != nil {
		used := clampMusePercent(sub.Weekly.UsedPercent)
		resetsAt := sub.Weekly.ResetsAt
		snap.WeeklyUsedPct = used
		snap.WeeklyResetsAt = resetsAt
		snap.Quotas = append(snap.Quotas, MuseQuota{
			Name:        MuseQuotaWeekly,
			Used:        used,
			Limit:       100,
			Utilization: used,
			Format:      MuseQuotaFormatPercent,
			ResetsAt:    resetsAt,
		})
	}
	return snap
}

// MuseDisplayTier returns the tier for display. The API reports an opaque
// numeric account tier ID; only human-readable values are shown.
func MuseDisplayTier(tier string) string {
	tier = strings.TrimSpace(tier)
	if tier == "" {
		return ""
	}
	for _, r := range tier {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return tier
		}
	}
	return ""
}

// MuseWindowLabel renders a window duration (minutes) like "5h prompts".
func MuseWindowLabel(durationMins int) string {
	if durationMins <= 0 {
		return "window prompts"
	}
	if durationMins < 60 {
		return fmt.Sprintf("%dm prompts", durationMins)
	}
	hours := float64(durationMins) / 60
	if hours == float64(int(hours)) {
		return fmt.Sprintf("%dh prompts", int(hours))
	}
	return fmt.Sprintf("%gh prompts", hours)
}
