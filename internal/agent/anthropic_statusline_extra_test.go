package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeStatusline writes a payload to a temp file and returns its path.
func writeStatusline(t *testing.T, payload string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "statusline.json")
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatalf("write statusline file: %v", err)
	}
	return path
}

// Claude Code reports five_hour and seven_day today. A per-model window would
// arrive under a key we have never seen, and the old struct dropped it without
// a trace - the same hardcoded-shape failure as issue #121.
func TestReadStatuslineData_KeepsUnknownWindow(t *testing.T) {
	path := writeStatusline(t, `{
		"rate_limits": {
			"five_hour": {"used_percentage": 42.5, "resets_at": 1766000000},
			"seven_day": {"used_percentage": 15.2, "resets_at": 1766500000},
			"seven_day_fable": {"used_percentage": 61.0, "resets_at": 1766500000}
		}
	}`)

	rl, err := readStatuslineData(path)
	if err != nil {
		t.Fatalf("readStatuslineData: %v", err)
	}
	if rl.FiveHour == nil || rl.FiveHour.UsedPercentage != 42.5 {
		t.Fatalf("five_hour not decoded: %+v", rl.FiveHour)
	}
	w := rl.Extra["seven_day_fable"]
	if w == nil {
		t.Fatalf("seven_day_fable dropped; Extra = %+v", rl.Extra)
	}
	if w.UsedPercentage != 61.0 {
		t.Errorf("UsedPercentage = %v, want 61.0", w.UsedPercentage)
	}
	if w.ResetsAt != 1766500000 {
		t.Errorf("ResetsAt = %d, want 1766500000", w.ResetsAt)
	}
}

// Accepting any key means non-window values have to be rejected on shape, or a
// future scalar field under rate_limits becomes a bogus quota card.
func TestReadStatuslineData_IgnoresNonWindowValues(t *testing.T) {
	path := writeStatusline(t, `{
		"rate_limits": {
			"five_hour": {"used_percentage": 10, "resets_at": 1766000000},
			"note": "some string",
			"count": 7,
			"nested": {"unrelated": true},
			"nulled": null
		}
	}`)

	rl, err := readStatuslineData(path)
	if err != nil {
		t.Fatalf("readStatuslineData: %v", err)
	}
	for _, key := range []string{"note", "count", "nested", "nulled"} {
		if _, ok := rl.Extra[key]; ok {
			t.Errorf("%q was accepted as a window", key)
		}
	}
	if rl.FiveHour == nil {
		t.Error("five_hour should still decode alongside junk keys")
	}
}

// The two-window payload every current user has must behave exactly as before.
func TestStatuslineToSnapshot_TwoWindowOutputUnchanged(t *testing.T) {
	path := writeStatusline(t, `{
		"rate_limits": {
			"five_hour": {"used_percentage": 42.5, "resets_at": 1766000000},
			"seven_day": {"used_percentage": 15.2, "resets_at": 1766500000}
		}
	}`)
	rl, err := readStatuslineData(path)
	if err != nil {
		t.Fatalf("readStatuslineData: %v", err)
	}

	snap := statuslineToSnapshot(rl, time.Unix(1766000100, 0).UTC())
	if len(snap.Quotas) != 2 {
		t.Fatalf("quota count = %d, want 2", len(snap.Quotas))
	}
	if snap.Quotas[0].Name != "five_hour" || snap.Quotas[1].Name != "seven_day" {
		t.Fatalf("names = %q, %q; want five_hour, seven_day",
			snap.Quotas[0].Name, snap.Quotas[1].Name)
	}
	if snap.Quotas[0].Utilization != 42.5 {
		t.Errorf("five_hour utilization = %v, want 42.5", snap.Quotas[0].Utilization)
	}
}

func TestStatuslineToSnapshot_IncludesUnknownWindow(t *testing.T) {
	path := writeStatusline(t, `{
		"rate_limits": {
			"five_hour": {"used_percentage": 10, "resets_at": 1766000000},
			"seven_day_fable": {"used_percentage": 61, "resets_at": 1766500000}
		}
	}`)
	rl, err := readStatuslineData(path)
	if err != nil {
		t.Fatalf("readStatuslineData: %v", err)
	}

	snap := statuslineToSnapshot(rl, time.Unix(1766000100, 0).UTC())

	var found bool
	for _, q := range snap.Quotas {
		if q.Name == "seven_day_fable" {
			found = true
			if q.Utilization != 61 {
				t.Errorf("utilization = %v, want 61", q.Utilization)
			}
			if q.ResetsAt == nil || q.ResetsAt.Unix() != 1766500000 {
				t.Errorf("ResetsAt = %v, want 1766500000", q.ResetsAt)
			}
		}
	}
	if !found {
		t.Fatalf("seven_day_fable missing from snapshot quotas: %+v", snap.Quotas)
	}

	// The audit trail must keep it too. Re-marshalling through a struct that
	// only knows two fields is exactly how the API path lost limits[].
	var round struct {
		RateLimits map[string]json.RawMessage `json:"rate_limits"`
	}
	if err := json.Unmarshal([]byte(snap.RawJSON), &round); err != nil {
		t.Fatalf("RawJSON not parseable: %v (%s)", err, snap.RawJSON)
	}
	if _, ok := round.RateLimits["seven_day_fable"]; !ok {
		t.Errorf("RawJSON dropped seven_day_fable: %s", snap.RawJSON)
	}
}

// Quota order must not depend on Go's randomised map iteration, or cards would
// reshuffle between polls.
func TestStatuslineToSnapshot_StableOrder(t *testing.T) {
	path := writeStatusline(t, `{
		"rate_limits": {
			"five_hour": {"used_percentage": 10, "resets_at": 1766000000},
			"seven_day": {"used_percentage": 20, "resets_at": 1766500000},
			"seven_day_opus": {"used_percentage": 30, "resets_at": 1766500000},
			"seven_day_fable": {"used_percentage": 40, "resets_at": 1766500000}
		}
	}`)
	rl, err := readStatuslineData(path)
	if err != nil {
		t.Fatalf("readStatuslineData: %v", err)
	}

	var first []string
	for i := 0; i < 20; i++ {
		snap := statuslineToSnapshot(rl, time.Unix(1766000100, 0).UTC())
		var names []string
		for _, q := range snap.Quotas {
			names = append(names, q.Name)
		}
		if first == nil {
			first = names
			continue
		}
		for j := range names {
			if names[j] != first[j] {
				t.Fatalf("order changed between runs: %v vs %v", first, names)
			}
		}
	}
}

func TestIsValidStatuslineData_UnknownWindowAlone(t *testing.T) {
	rl := &StatuslineRateLimits{
		Extra: map[string]*StatuslineWindow{
			"seven_day_fable": {UsedPercentage: 61, ResetsAt: 1766500000},
		},
	}
	if !isValidStatuslineData(rl) {
		t.Error("a lone unknown window should count as valid data")
	}
}

func TestIsValidStatuslineData_RejectsImplausibleUnknownWindow(t *testing.T) {
	rl := &StatuslineRateLimits{
		FiveHour: &StatuslineWindow{UsedPercentage: 10, ResetsAt: 1766000000},
		Extra: map[string]*StatuslineWindow{
			"seven_day_fable": {UsedPercentage: 4000, ResetsAt: 1766500000},
		},
	}
	if isValidStatuslineData(rl) {
		t.Error("out-of-range unknown window should invalidate the payload")
	}
}
