package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Alibaba Cloud Model Studio Token Plan (Personal) provider model.
//
// The console has no public API-key based quota endpoint, so onWatch reads the
// same internal endpoints the web console uses, authenticated with the user's
// browser session cookies. The usage endpoint reports one 0-1 fraction per
// rolling window plus a reset time in epoch milliseconds:
//
//	{"per5HourPercentage":0.12,"per5HourResetTime":1790262790822,
//	 "per1WeekPercentage":0.4,"per1WeekResetTime":1790429616789,
//	 "per1MonthPercentage":0.07,"per1MonthResetTime":1792000000000}
//
// The subscription endpoint reports the plan itself:
//
//	{"remainingDays":21,"specCode":"lite","status":"VALID","startTime":...,
//	 "endTime":...,"autoRenewFlag":false,"instanceCode":"sfm_..."}

// Alibaba quota window names.
const (
	AlibabaQuotaFiveHour = "five_hour"
	AlibabaQuotaWeekly   = "weekly"
	AlibabaQuotaMonthly  = "monthly"
)

// AlibabaCommodityCode is the BSS commodity code of Token Plan Personal (intl).
const AlibabaCommodityCode = "sfm_tokenplansolo_public_intl"

// AlibabaQuota is one tracked quota window. Utilization is a 0-100 percent.
type AlibabaQuota struct {
	Name        string
	Utilization float64
	ResetsAt    *time.Time
}

// AlibabaSnapshot is a point-in-time capture of Token Plan usage.
type AlibabaSnapshot struct {
	ID         int64
	CapturedAt time.Time
	RawJSON    string

	SpecCode      string
	Status        string
	RemainingDays int
	AutoRenew     bool
	StartTime     *time.Time
	EndTime       *time.Time
	InstanceID    string

	// AccountLabel is a display label for the account the snapshot belongs to.
	// It is not persisted; the store keys rows by account id.
	AccountLabel string

	Quotas []AlibabaQuota
}

// AlibabaSubscription is the parsed subscription endpoint payload.
type AlibabaSubscription struct {
	RemainingDays int
	SpecCode      string
	Status        string
	StartTime     *time.Time
	EndTime       *time.Time
	AutoRenew     bool
	InstanceCode  string
}

// AlibabaInstance is the Token Plan instance found in a BSS
// QueryAvailableInstances response.
type AlibabaInstance struct {
	InstanceID string
	Status     string
	EndTime    *time.Time
}

// NormalizeAlibabaPercent converts an API percentage to 0-100. The usage
// endpoint reports fractions (0.25 = 25%), but a value above 1 is taken as an
// already-scaled percent. The result is clamped to 0-100.
func NormalizeAlibabaPercent(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	if v <= 1 {
		v *= 100
	}
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// alibabaFlexValue holds a JSON scalar that may arrive as a number, a numeric
// string, a bool or null, and records whether a value was present.
type alibabaFlexValue struct {
	raw   string
	valid bool
}

func (f *alibabaFlexValue) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "" || s == "null" {
		*f = alibabaFlexValue{}
		return nil
	}
	if s[0] == '"' {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return err
		}
		str = strings.TrimSpace(str)
		if str == "" {
			*f = alibabaFlexValue{}
			return nil
		}
		*f = alibabaFlexValue{raw: str, valid: true}
		return nil
	}
	if s[0] == '{' || s[0] == '[' {
		return fmt.Errorf("alibaba: unexpected composite value %q", alibabaTruncate(s))
	}
	*f = alibabaFlexValue{raw: s, valid: true}
	return nil
}

func alibabaTruncate(s string) string {
	if len(s) > 40 {
		return s[:40] + "..."
	}
	return s
}

func (f alibabaFlexValue) float() (float64, bool) {
	if !f.valid {
		return 0, false
	}
	v, err := strconv.ParseFloat(f.raw, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

func (f alibabaFlexValue) text() string {
	if !f.valid {
		return ""
	}
	return f.raw
}

func (f alibabaFlexValue) boolean() bool {
	if !f.valid {
		return false
	}
	switch strings.ToLower(f.raw) {
	case "true", "1", "yes", "y", "on":
		return true
	}
	if v, ok := f.float(); ok {
		return v != 0
	}
	return false
}

// millis converts epoch milliseconds (or seconds) or a formatted date string
// to a time. Zero and unparseable values yield nil.
func (f alibabaFlexValue) millis() *time.Time {
	if !f.valid {
		return nil
	}
	if v, ok := f.float(); ok {
		return alibabaEpochTime(v)
	}
	return parseAlibabaTimeString(f.raw)
}

// alibabaEpochTime treats values below 1e11 as epoch seconds and everything
// else as epoch milliseconds.
func alibabaEpochTime(v float64) *time.Time {
	if v <= 0 {
		return nil
	}
	var t time.Time
	if v < 1e11 {
		t = time.Unix(int64(v), 0).UTC()
	} else {
		t = time.UnixMilli(int64(v)).UTC()
	}
	return &t
}

func parseAlibabaTimeString(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05Z0700",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			t = t.UTC()
			return &t
		}
	}
	return nil
}

type alibabaUsagePayload struct {
	Per5HourPercentage  alibabaFlexValue `json:"per5HourPercentage"`
	Per5HourResetTime   alibabaFlexValue `json:"per5HourResetTime"`
	Per1WeekPercentage  alibabaFlexValue `json:"per1WeekPercentage"`
	Per1WeekResetTime   alibabaFlexValue `json:"per1WeekResetTime"`
	Per1MonthPercentage alibabaFlexValue `json:"per1MonthPercentage"`
	Per1MonthResetTime  alibabaFlexValue `json:"per1MonthResetTime"`
}

// alibabaIsJSONObject reports whether raw holds a non-empty JSON object.
func alibabaIsJSONObject(raw []byte) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 1 && raw[0] == '{'
}

// ParseAlibabaUsage extracts the quota windows from the usage endpoint
// payload. Only windows that report a percentage are returned, ordered
// five_hour, weekly, monthly.
func ParseAlibabaUsage(raw []byte) ([]AlibabaQuota, error) {
	if !alibabaIsJSONObject(raw) {
		return nil, fmt.Errorf("alibaba: usage payload is not a JSON object")
	}
	var p alibabaUsagePayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("alibaba: parsing usage: %w", err)
	}

	windows := []struct {
		name  string
		pct   alibabaFlexValue
		reset alibabaFlexValue
	}{
		{AlibabaQuotaFiveHour, p.Per5HourPercentage, p.Per5HourResetTime},
		{AlibabaQuotaWeekly, p.Per1WeekPercentage, p.Per1WeekResetTime},
		{AlibabaQuotaMonthly, p.Per1MonthPercentage, p.Per1MonthResetTime},
	}

	var quotas []AlibabaQuota
	for _, w := range windows {
		v, ok := w.pct.float()
		if !ok {
			continue
		}
		quotas = append(quotas, AlibabaQuota{
			Name:        w.name,
			Utilization: NormalizeAlibabaPercent(v),
			ResetsAt:    w.reset.millis(),
		})
	}
	return quotas, nil
}

type alibabaSubscriptionPayload struct {
	RemainingDays alibabaFlexValue `json:"remainingDays"`
	SpecCode      alibabaFlexValue `json:"specCode"`
	Status        alibabaFlexValue `json:"status"`
	StartTime     alibabaFlexValue `json:"startTime"`
	EndTime       alibabaFlexValue `json:"endTime"`
	AutoRenewFlag alibabaFlexValue `json:"autoRenewFlag"`
	InstanceCode  alibabaFlexValue `json:"instanceCode"`
}

// ParseAlibabaSubscription parses the subscription endpoint payload. An empty
// payload returns nil without an error.
func ParseAlibabaSubscription(raw []byte) (*AlibabaSubscription, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" || string(raw) == "{}" {
		return nil, nil
	}
	if !alibabaIsJSONObject(raw) {
		return nil, fmt.Errorf("alibaba: subscription payload is not a JSON object")
	}
	var p alibabaSubscriptionPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("alibaba: parsing subscription: %w", err)
	}
	sub := &AlibabaSubscription{
		SpecCode:     p.SpecCode.text(),
		Status:       p.Status.text(),
		StartTime:    p.StartTime.millis(),
		EndTime:      p.EndTime.millis(),
		AutoRenew:    p.AutoRenewFlag.boolean(),
		InstanceCode: p.InstanceCode.text(),
	}
	if v, ok := p.RemainingDays.float(); ok {
		sub.RemainingDays = int(math.Round(v))
	}
	return sub, nil
}

// ParseAlibabaInstance finds the Token Plan Personal instance in a BSS
// QueryAvailableInstances Data payload ({"InstanceList":[...]}). It returns
// nil when no matching instance is listed.
func ParseAlibabaInstance(raw []byte) (*AlibabaInstance, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var payload struct {
		InstanceList []map[string]alibabaFlexValue `json:"InstanceList"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("alibaba: parsing instances: %w", err)
	}
	for _, item := range payload.InstanceList {
		if lookupAlibabaField(item, "ProductType").text() != AlibabaCommodityCode {
			continue
		}
		inst := &AlibabaInstance{
			InstanceID: lookupAlibabaField(item, "InstanceId").text(),
			Status:     lookupAlibabaField(item, "Status").text(),
			EndTime:    lookupAlibabaField(item, "EndTime").millis(),
		}
		return inst, nil
	}
	return nil, nil
}

// lookupAlibabaField reads a field ignoring key case ("InstanceId" is also
// spelled "InstanceID" in some responses).
func lookupAlibabaField(m map[string]alibabaFlexValue, key string) alibabaFlexValue {
	if v, ok := m[key]; ok {
		return v
	}
	for k, v := range m {
		if strings.EqualFold(k, key) {
			return v
		}
	}
	return alibabaFlexValue{}
}

// BuildAlibabaSnapshot merges the usage, subscription and optional BSS
// instance payloads into a snapshot. subscriptionJSON and instanceJSON may be
// empty. It fails when neither usage windows nor a subscription were found,
// since that means the response was not a Token Plan payload.
func BuildAlibabaSnapshot(usageJSON, subscriptionJSON, instanceJSON []byte, capturedAt time.Time) (*AlibabaSnapshot, error) {
	quotas, err := ParseAlibabaUsage(usageJSON)
	if err != nil {
		return nil, err
	}

	sub, err := ParseAlibabaSubscription(subscriptionJSON)
	if err != nil {
		return nil, err
	}

	inst, err := ParseAlibabaInstance(instanceJSON)
	if err != nil {
		inst = nil
	}

	if len(quotas) == 0 && sub == nil {
		return nil, fmt.Errorf("alibaba: no usage windows or subscription in response")
	}

	snap := &AlibabaSnapshot{
		CapturedAt: capturedAt.UTC(),
		Quotas:     quotas,
	}
	if sub != nil {
		snap.SpecCode = sub.SpecCode
		snap.Status = sub.Status
		snap.RemainingDays = sub.RemainingDays
		snap.AutoRenew = sub.AutoRenew
		snap.StartTime = sub.StartTime
		snap.EndTime = sub.EndTime
		snap.InstanceID = sub.InstanceCode
	}
	if inst != nil {
		if snap.InstanceID == "" {
			snap.InstanceID = inst.InstanceID
		}
		if snap.Status == "" {
			snap.Status = inst.Status
		}
		if snap.EndTime == nil {
			snap.EndTime = inst.EndTime
		}
	}

	snap.RawJSON = buildAlibabaRawJSON(usageJSON, subscriptionJSON, instanceJSON)
	return snap, nil
}

// buildAlibabaRawJSON stores the raw payloads side by side for debugging.
// Payloads that are not valid JSON are dropped so the result stays valid.
func buildAlibabaRawJSON(usage, subscription, instance []byte) string {
	out := make(map[string]json.RawMessage, 3)
	for key, raw := range map[string][]byte{"usage": usage, "subscription": subscription, "instance": instance} {
		raw = bytes.TrimSpace(raw)
		if len(raw) > 0 && json.Valid(raw) {
			out[key] = json.RawMessage(raw)
		}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return ""
	}
	return string(b)
}
