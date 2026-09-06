package api

import "testing"

// Weekly buckets can arrive under names nobody has seen yet - from limits[] on
// the API path, or straight from the statusline. A card titled
// "seven_day_fable" is a bug report waiting to happen, so the label is derived
// from the key rather than looked up in a table.
func TestAnthropicDisplayName_DerivesWeeklyLabels(t *testing.T) {
	cases := map[string]string{
		// Curated names keep their existing wording.
		"five_hour":        "5-Hour Limit",
		"seven_day":        "Weekly All-Model",
		"seven_day_sonnet": "Weekly Sonnet",
		"extra_usage":      "Extra Usage",

		// Synthesised from limits[] (issue #121).
		"seven_day_scoped_fable": "Weekly Fable",

		// Reported directly by the statusline under an unknown key.
		"seven_day_fable":      "Weekly Fable",
		"seven_day_opus":       "Weekly Opus",
		"seven_day_oauth_apps": "Weekly Oauth Apps",
	}
	for key, want := range cases {
		if got := AnthropicDisplayName(key); got != want {
			t.Errorf("AnthropicDisplayName(%q) = %q, want %q", key, got, want)
		}
	}
}

// Anything that is not a weekly bucket must fall through untouched rather than
// being dressed up as one.
func TestAnthropicDisplayName_LeavesUnrelatedKeysAlone(t *testing.T) {
	for _, key := range []string{"", "seven_day_", "nimbus_quill", "monthly"} {
		if got := AnthropicDisplayName(key); got != key {
			t.Errorf("AnthropicDisplayName(%q) = %q, want it unchanged", key, got)
		}
	}
}

// Per-model weekly buckets belong next to the other weekly cards, whichever
// route they arrived by: promoted from limits[] on the API path, or reported
// directly by the statusline under a name we have no entry for.
func TestIsAnthropicPerModelWeekly(t *testing.T) {
	weekly := []string{
		"seven_day_scoped_fable",
		"seven_day_scoped_claude_opus_4",
		"seven_day_fable",
		"seven_day_opus",
	}
	for _, key := range weekly {
		if !IsAnthropicPerModelWeekly(key) {
			t.Errorf("IsAnthropicPerModelWeekly(%q) = false, want true", key)
		}
	}

	other := []string{
		"five_hour",
		"seven_day",        // the all-model bucket, not per-model
		"seven_day_sonnet", // curated, has its own label and sort position
		"extra_usage",
		"seven_day_",
		"",
		"nimbus_quill",
	}
	for _, key := range other {
		if IsAnthropicPerModelWeekly(key) {
			t.Errorf("IsAnthropicPerModelWeekly(%q) = true, want false", key)
		}
	}
}
