package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// OpenCodeGoUsageWindow represents a single usage window (rolling, weekly, monthly).
type OpenCodeGoUsageWindow struct {
	Name        string  // "rolling", "weekly", "monthly"
	UsagePercent float64 // 0-100
	ResetInSec   int     // seconds until reset
	Status       string  // "normal" or "rate-limited"
}

// OpenCodeGoUsageResponse holds parsed usage data from the dashboard page.
type OpenCodeGoUsageResponse struct {
	RollingUsage  *OpenCodeGoUsageWindow
	WeeklyUsage   *OpenCodeGoUsageWindow
	MonthlyUsage  *OpenCodeGoUsageWindow
	UseBalance    bool
}

// OpenCodeGoSnapshot is a point-in-time capture of OpenCode Go usage.
type OpenCodeGoSnapshot struct {
	ID         int64
	CapturedAt time.Time
	RawJSON    string
	Windows    []OpenCodeGoWindowValue
}

// OpenCodeGoWindowValue holds a single window's value at a snapshot.
type OpenCodeGoWindowValue struct {
	WindowName   string  // "rolling", "weekly", "monthly"
	UsagePercent float64 // 0-100
	ResetInSec   int     // seconds until reset
	Status       string  // "normal" or "rate-limited"
}

// OpenCodeGoUsagePoint represents a datapoint for time-series queries.
type OpenCodeGoUsagePoint struct {
	CapturedAt   time.Time
	UsagePercent float64
}

// OpenCodeGoResetCycle represents a reset cycle record.
type OpenCodeGoResetCycle struct {
	ID           int64
	WindowName   string
	CycleStart   time.Time
	CycleEnd     *time.Time
	ResetsAt     time.Time
	PeakUsage    float64
	TotalDelta   float64
}

// OpenCodeGoCycleOverviewRow is a cycle overview with cross-quota data.
type OpenCodeGoCycleOverviewRow struct {
	CycleID     int64
	WindowName  string
	CycleStart  time.Time
	CycleEnd    *time.Time
	PeakValue   float64
	TotalDelta  float64
	PeakTime    time.Time
	CrossQuotas []OpenCodeGoCrossQuota
}

// OpenCodeGoCrossQuota holds a single window's value at a point in time.
type OpenCodeGoCrossQuota struct {
	Name         string
	Value       float64
	Limit       float64
	Percent     float64
	StartPercent float64
	Delta       float64
}

// OpenCodeGoSummary contains computed usage statistics for a window.
type OpenCodeGoSummary struct {
	WindowName      string
	UsagePercent    float64
	ResetInSec      int
	TimeUntilReset  time.Duration
	CurrentRate     float64 // percent per hour
	ProjectedUsage  float64 // projected percent at reset
	CompletedCycles int
	PeakCycle       float64
	TotalTracked    float64
	TrackingSince   time.Time
}

var (
	ErrOpenCodeGoNotSignedIn   = errors.New("opencodego: not signed in")
	ErrOpenCodeGoParseError    = errors.New("opencodego: failed to parse usage data")
	ErrOpenCodeGoNetworkError  = errors.New("opencodego: network error")
)

// serovalRegex matches Seroval usage window patterns like:
// rollingUsage:$R[28]={status:"ok",resetInSec:17591,usagePercent:0}
var serovalRegex = regexp.MustCompile(`(rollingUsage|weeklyUsage|monthlyUsage)\s*:\s*\$R\s*\[\d+\]\s*=\s*\{([^}]+)\}`)

// serovalFieldRegex extracts named fields from a Seroval object.
var serovalFieldRegex = regexp.MustCompile(`(\w+)\s*:\s*([\d.]+|"[^"]*")`)

// simpleJSONUsageRegex is a fallback regex for parsing inline JS objects like:
// rollingUsage={status:"ok",resetInSec:17591,usagePercent:0}
var simpleJSONUsageRegex = regexp.MustCompile(`(rollingUsage|weeklyUsage|monthlyUsage)\s*[:=]\s*\{([^}]+(?:\{[^}]*\}[^}]*)*)\}`)

// ParseOpenCodeGoUsageResponse parses the HTML response from the dashboard page.
// It tries multiple parsing strategies:
// 1. Direct JSON parse (look for standard JSON structure)
// 2. Seroval format extraction from script tags
// 3. Fallback regex extraction from raw text
func ParseOpenCodeGoUsageResponse(data []byte) (*OpenCodeGoUsageResponse, error) {
	if data == nil || len(data) == 0 {
		return nil, fmt.Errorf("%w: empty response", ErrOpenCodeGoParseError)
	}

	text := string(data)

	// Check for sign-out indicators
	if strings.Contains(text, "login") &&
		(strings.Contains(text, "sign in") || strings.Contains(text, "auth/authorize") ||
			strings.Contains(text, "not associated with an account")) {
		return nil, ErrOpenCodeGoNotSignedIn
	}

	// Strategy 1: Direct JSON parse
	resp, err := tryDirectJSONParse(text)
	if err == nil && resp != nil && len(resp) > 0 {
		return buildFromWindows(resp), nil
	}

	// Strategy 2: Seroval format extraction
	resp, err = trySerovalParse(text)
	if err == nil && resp != nil && len(resp) > 0 {
		return buildFromWindows(resp), nil
	}

	// Strategy 3: Simple JS object fallback
	resp, err = trySimpleJSParse(text)
	if err == nil && resp != nil && len(resp) > 0 {
		return buildFromWindows(resp), nil
	}

	// Strategy 4: Nested JSON scan (depth up to 3)
	resp, err = tryNestedJSONScan(text)
	if err == nil && resp != nil && len(resp) > 0 {
		return buildFromWindows(resp), nil
	}

	return nil, fmt.Errorf("%w: no usage data found in response", ErrOpenCodeGoParseError)
}

// DebugParse attempts to parse and returns diagnostic info about what was found.
func DebugParse(data []byte) (resp *OpenCodeGoUsageResponse, diag string) {
	if data == nil || len(data) == 0 {
		return nil, "empty response"
	}

	text := string(data)

	diag += fmt.Sprintf("len=%d", len(data))

	// Sign-out check
	if strings.Contains(text, "login") &&
		(strings.Contains(text, "sign in") || strings.Contains(text, "auth/authorize") ||
			strings.Contains(text, "not associated with an account")) {
		return nil, diag + " signout_detected"
	}

	// Check for seroval patterns
	sm := serovalRegex.FindAllString(text, -1)
	diag += fmt.Sprintf(" seroval_matches=%d", len(sm))

	// Check for simple JS objects
	jm := simpleJSONUsageRegex.FindAllString(text, -1)
	diag += fmt.Sprintf(" simplejs_matches=%d", len(jm))

	// Check for JSON
	braceCount := strings.Count(text, "{")
	diag += fmt.Sprintf(" braces=%d", braceCount)

	// Try actual parse
	parseResp, parseErr := ParseOpenCodeGoUsageResponse(data)
	resp = parseResp
	if parseErr != nil {
		diag += " parse_error=" + parseErr.Error()
	} else if resp != nil {
		c := 0
		if resp.RollingUsage != nil {
			c++
		}
		if resp.WeeklyUsage != nil {
			c++
		}
		if resp.MonthlyUsage != nil {
			c++
		}
		diag += fmt.Sprintf(" windows_found=%d", c)
	}

	return resp, diag
}

// tryDirectJSONParse attempts to parse the response as JSON.
func tryDirectJSONParse(text string) (map[string]*OpenCodeGoUsageWindow, error) {
	// Try to find JSON in the text
	idx := 0
	for {
		start := strings.Index(text[idx:], "{")
		if start == -1 {
			return nil, fmt.Errorf("no JSON object found")
		}
		start += idx

		end := findMatchingBrace(text, start)
		if end == -1 {
			idx = start + 1
			continue
		}

		jsonStr := text[start : end+1]

		// Try parsing directly
		windows := extractWindowsFromJSON(jsonStr)
		if windows != nil && len(windows) > 0 {
			return windows, nil
		}

		// Try parsing as nested object and look in common keys
		var root map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &root); err != nil {
			idx = end + 1
			continue
		}

		for _, key := range []string{"data", "result", "usage", "billing", "payload", "props", "pageProps"} {
			if sub, ok := root[key]; ok {
				if subMap, ok := sub.(map[string]interface{}); ok {
					windows := extractWindowsFromMap(subMap)
					if windows != nil && len(windows) > 0 {
						return windows, nil
					}
				}
			}
		}

		idx = end + 1
	}
}

// trySerovalParse extracts usage data from Seroval format.
func trySerovalParse(text string) (map[string]*OpenCodeGoUsageWindow, error) {
	matches := serovalRegex.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		// Try the simple js object regex as fallback within the seroval path
		matches = simpleJSONUsageRegex.FindAllStringSubmatch(text, -1)
		if len(matches) == 0 {
			return nil, fmt.Errorf("no seroval patterns found")
		}
	}

	windows := make(map[string]*OpenCodeGoUsageWindow)
	for _, match := range matches {
		name := classifyWindowName(match[1])
		fields := match[2]

		fieldMatches := serovalFieldRegex.FindAllStringSubmatch(fields, -1)
		fieldsMap := make(map[string]string)
		for _, fm := range fieldMatches {
			val := fm[2]
			val = strings.Trim(val, `"`)
			fieldsMap[fm[1]] = val
		}

		usagePercentStr := fieldsMap["usagePercent"]
		usagePercent, _ := strconv.ParseFloat(usagePercentStr, 64)
		// Handle 0-1 fractional values (e.g., 0.02 -> 2%) without
		// affecting integer values like 1 meaning 1%.
		if usagePercent > 0 && usagePercent < 1 && strings.Contains(usagePercentStr, ".") {
			usagePercent *= 100
		}
		resetInSec, _ := strconv.Atoi(fieldsMap["resetInSec"])
		status := fieldsMap["status"]
		if status == "" {
			status = "normal"
		}

		windows[name] = &OpenCodeGoUsageWindow{
			Name:        name,
			UsagePercent: math.Round(usagePercent*10) / 10,
			ResetInSec:   resetInSec,
			Status:       status,
		}
	}

	return windows, nil
}

// trySimpleJSParse extracts usage data from simple JS object notation.
func trySimpleJSParse(text string) (map[string]*OpenCodeGoUsageWindow, error) {
	matches := simpleJSONUsageRegex.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil, fmt.Errorf("no simple JS objects found")
	}

	windows := make(map[string]*OpenCodeGoUsageWindow)
	for _, match := range matches {
		name := classifyWindowName(match[1])
		fields := match[2]

		fieldMatches := serovalFieldRegex.FindAllStringSubmatch(fields, -1)
		fieldsMap := make(map[string]string)
		for _, fm := range fieldMatches {
			val := fm[2]
			val = strings.Trim(val, `"`)
			fieldsMap[fm[1]] = val
		}

		usagePercentStr := fieldsMap["usagePercent"]
		usagePercent, _ := strconv.ParseFloat(usagePercentStr, 64)
		if usagePercent > 0 && usagePercent < 1 && strings.Contains(usagePercentStr, ".") {
			usagePercent *= 100
		}
		resetInSec, _ := strconv.Atoi(fieldsMap["resetInSec"])
		status := fieldsMap["status"]
		if status == "" {
			status = "normal"
		}

		windows[name] = &OpenCodeGoUsageWindow{
			Name:        name,
			UsagePercent: math.Round(usagePercent*10) / 10,
			ResetInSec:   resetInSec,
			Status:       status,
		}
	}

	return windows, nil
}

// tryNestedJSONScan scans nested JSON for usage window data.
func tryNestedJSONScan(text string) (map[string]*OpenCodeGoUsageWindow, error) {
	idx := 0
	for {
		start := strings.Index(text[idx:], "{")
		if start == -1 {
			return nil, fmt.Errorf("no JSON objects found")
		}
		start += idx

		end := findMatchingBrace(text, start)
		if end == -1 {
			idx = start + 1
			continue
		}

		jsonStr := text[start : end+1]
		var root map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &root); err != nil {
			idx = end + 1
			continue
		}

		// Scan all sub-objects at depth <= 3
		windows := scanForWindows(root, 3)
		if windows != nil && len(windows) > 0 {
			return windows, nil
		}

		idx = end + 1
	}
}

// scanForWindows recursively scans a map for usage window data.
func scanForWindows(m map[string]interface{}, maxDepth int) map[string]*OpenCodeGoUsageWindow {
	if maxDepth <= 0 {
		return nil
	}

	windows := make(map[string]*OpenCodeGoUsageWindow)

	// Check if this map itself is a window
	for key, val := range m {
		lowerKey := strings.ToLower(key)
		if window, ok := tryExtractWindow(val); ok {
			switch {
			case strings.Contains(lowerKey, "rolling") || strings.Contains(lowerKey, "5h") || strings.Contains(lowerKey, "5-hour"):
				window.Name = "rolling"
				windows["rolling"] = window
			case strings.Contains(lowerKey, "weekly") || strings.Contains(lowerKey, "week"):
				window.Name = "weekly"
				windows["weekly"] = window
			case strings.Contains(lowerKey, "monthly") || strings.Contains(lowerKey, "month"):
				window.Name = "monthly"
				windows["monthly"] = window
			}
		}

		if subMap, ok := val.(map[string]interface{}); ok {
			subWindows := scanForWindows(subMap, maxDepth-1)
			for k, v := range subWindows {
				if _, exists := windows[k]; !exists {
					windows[k] = v
				}
			}
		}
	}

	if len(windows) == 0 {
		return nil
	}
	return windows
}

// tryExtractWindow attempts to extract a usage window from an interface{}.
func tryExtractWindow(val interface{}) (*OpenCodeGoUsageWindow, bool) {
	m, ok := val.(map[string]interface{})
	if !ok {
		return nil, false
	}

	var usagePercent float64
	var resetInSec int
	var status string
	hasData := false

	for _, key := range []string{"usagePercent", "usedPercent", "percentUsed", "percent", "usage_percent", "used_percent", "utilization", "utilizationPercent", "utilization_percent", "usage"} {
		if v, ok := m[key]; ok {
			usagePercent = toFloat64(v)
			hasData = true
			break
		}
	}

	// Try computing from used/limit
	if !hasData {
		used, hasUsed := m["used"].(float64)
		limit, hasLimit := m["limit"].(float64)
		if hasUsed && hasLimit && limit > 0 {
			usagePercent = (used / limit) * 100
			hasData = true
		}
	}

	for _, key := range []string{"resetInSec", "resetInSeconds", "resetSeconds", "reset_sec", "reset_in_sec", "resetsInSec", "resetsInSeconds", "resetIn", "resetSec"} {
		if v, ok := m[key]; ok {
			resetInSec = int(toFloat64(v))
			break
		}
	}

	if s, ok := m["status"].(string); ok {
		status = s
	}
	if status == "" {
		status = "normal"
	}

	if !hasData {
		return nil, false
	}

	if usagePercent > 0 && usagePercent <= 1 {
		usagePercent *= 100
	}

	return &OpenCodeGoUsageWindow{
		UsagePercent: math.Round(usagePercent*10) / 10,
		ResetInSec:   resetInSec,
		Status:       status,
	}, true
}

// toFloat64 converts various numeric types to float64.
func toFloat64(v interface{}) float64 {
	switch val := v.(type) {
	case float64:
		return val
	case float32:
		return float64(val)
	case int:
		return float64(val)
	case int64:
		return float64(val)
	case string:
		f, _ := strconv.ParseFloat(val, 64)
		return f
	case json.Number:
		f, _ := val.Float64()
		return f
	default:
		return 0
	}
}

// findMatchingBrace finds the matching closing brace for a JSON object.
func findMatchingBrace(s string, start int) int {
	if start >= len(s) || s[start] != '{' {
		return -1
	}
	depth := 0
	inString := false
	escapeNext := false
	for i := start; i < len(s); i++ {
		if escapeNext {
			escapeNext = false
			continue
		}
		if s[i] == '\\' {
			escapeNext = true
			continue
		}
		if s[i] == '"' || s[i] == '\'' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// extractWindowsFromJSON tries to extract usage windows from a JSON string.
func extractWindowsFromJSON(jsonStr string) map[string]*OpenCodeGoUsageWindow {
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		return nil
	}
	return extractWindowsFromMap(data)
}

// extractWindowsFromMap extracts usage windows from a parsed JSON map.
func extractWindowsFromMap(data map[string]interface{}) map[string]*OpenCodeGoUsageWindow {
	for _, key := range []string{"rollingUsage", "weeklyUsage", "monthlyUsage", "rolling", "weekly", "monthly"} {
		val, ok := data[key]
		if !ok {
			continue
		}
		if window, ok := tryExtractWindow(val); ok {
			window.Name = classifyWindowName(key)
			result := make(map[string]*OpenCodeGoUsageWindow)
			result[window.Name] = window
			// Check for sibling windows
			for _, otherKey := range []string{"rollingUsage", "weeklyUsage", "monthlyUsage", "rolling", "weekly", "monthly"} {
				if otherKey == key {
					continue
				}
				if otherVal, ok := data[otherKey]; ok {
					if otherWindow, ok := tryExtractWindow(otherVal); ok {
						otherWindow.Name = classifyWindowName(otherKey)
						result[otherWindow.Name] = otherWindow
					}
				}
			}
			return result
		}
	}

	// Check nested structures: usage.rollingUsage, windows.primaryWindow, etc.
	for _, containerKey := range []string{"usage", "windows", "data", "result"} {
		if container, ok := data[containerKey]; ok {
			if containerMap, ok := container.(map[string]interface{}); ok {
				mapped := make(map[string]*OpenCodeGoUsageWindow)
				for _, windowKey := range []string{"rollingUsage", "weeklyUsage", "monthlyUsage", "rolling", "weekly", "monthly", "primaryWindow", "weeklyQuota", "monthlyBucket"} {
					if val, ok := containerMap[windowKey]; ok {
						if window, ok := tryExtractWindow(val); ok {
							name := classifyWindowName(windowKey)
							window.Name = name
							mapped[name] = window
						}
					}
				}
				if len(mapped) > 0 {
					return mapped
				}
			}
		}
	}

	return nil
}

// classifyWindowName maps a raw key to a standard window name.
func classifyWindowName(key string) string {
	lower := strings.ToLower(key)
	switch {
	case strings.Contains(lower, "rolling") || strings.Contains(lower, "primary") || strings.Contains(lower, "5h"):
		return "rolling"
	case strings.Contains(lower, "weekly") || strings.Contains(lower, "week"):
		return "weekly"
	case strings.Contains(lower, "monthly") || strings.Contains(lower, "month"):
		return "monthly"
	default:
		return key
	}
}

// buildFromWindows builds an OpenCodeGoUsageResponse from parsed windows.
func buildFromWindows(windows map[string]*OpenCodeGoUsageWindow) *OpenCodeGoUsageResponse {
	resp := &OpenCodeGoUsageResponse{}

	if w, ok := windows["rolling"]; ok {
		resp.RollingUsage = w
	}
	if w, ok := windows["weekly"]; ok {
		resp.WeeklyUsage = w
	}
	if w, ok := windows["monthly"]; ok {
		resp.MonthlyUsage = w
	}

	return resp
}

// ToSnapshot converts the response to a snapshot for storage.
func (r *OpenCodeGoUsageResponse) ToSnapshot(capturedAt time.Time) *OpenCodeGoSnapshot {
	snapshot := &OpenCodeGoSnapshot{
		CapturedAt: capturedAt,
	}

	var windows []OpenCodeGoWindowValue

	addWindow := func(w *OpenCodeGoUsageWindow) {
		if w == nil {
			return
		}
		windows = append(windows, OpenCodeGoWindowValue{
			WindowName:   w.Name,
			UsagePercent: w.UsagePercent,
			ResetInSec:   w.ResetInSec,
			Status:       w.Status,
		})
	}

	addWindow(r.RollingUsage)
	addWindow(r.WeeklyUsage)
	addWindow(r.MonthlyUsage)

	snapshot.Windows = windows

	if raw, err := json.Marshal(r); err == nil {
		snapshot.RawJSON = string(raw)
	}

	return snapshot
}

// HasMonthlyWindow returns true if monthly usage data is available.
func (s *OpenCodeGoSnapshot) HasMonthlyWindow() bool {
	for _, w := range s.Windows {
		if w.WindowName == "monthly" {
			return true
		}
	}
	return false
}

// GetWindow returns a specific window value by name.
func (s *OpenCodeGoSnapshot) GetWindow(name string) *OpenCodeGoWindowValue {
	for i := range s.Windows {
		if s.Windows[i].WindowName == name {
			return &s.Windows[i]
		}
	}
	return nil
}

// resetAt computes the time when this window resets (now + resetInSec).
func (w *OpenCodeGoWindowValue) resetAt(now time.Time) time.Time {
	if w.ResetInSec <= 0 {
		return now.Add(24 * time.Hour)
	}
	return now.Add(time.Duration(w.ResetInSec) * time.Second)
}
