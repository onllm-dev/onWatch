package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestAgyRunner_Live exercises the full managed-agy pipeline against a real
// agy binary. It is skipped unless ONWATCH_AGY_LIVE=1 because it launches a
// real process and takes up to ~90s to reach readiness; CI never runs it.
func TestAgyRunner_Live(t *testing.T) {
	if os.Getenv("ONWATCH_AGY_LIVE") != "1" {
		t.Skip("set ONWATCH_AGY_LIVE=1 to run the live agy runner test")
	}
	r := NewAntigravityCLIRunner(nil)
	defer r.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	snap, err := r.Fetch(ctx)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if snap.Source != AntigravitySourceCLI {
		t.Errorf("source = %q, want %q", snap.Source, AntigravitySourceCLI)
	}
	if len(snap.Models) == 0 {
		t.Fatal("expected bucket rows from live agy")
	}
	for _, m := range snap.Models {
		t.Logf("bucket %s (%s): remaining=%.2f reset=%v", m.ModelID, m.Label, m.RemainingFraction, m.ResetTime)
	}

	// A second fetch should reuse the warm session quickly.
	if _, err := r.Fetch(ctx); err != nil {
		t.Fatalf("second Fetch (warm): %v", err)
	}
}

func TestParseAgyQuotaSummary_Fixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "agy_quota_summary.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	rows, err := ParseAgyQuotaSummary(data)
	if err != nil {
		t.Fatalf("ParseAgyQuotaSummary: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("expected 4 bucket rows, got %d", len(rows))
	}

	byID := map[string]AntigravityModelQuota{}
	for _, r := range rows {
		byID[r.ModelID] = r
	}
	for _, id := range []string{"gemini-weekly", "gemini-5h", "3p-weekly", "3p-5h"} {
		r, ok := byID[id]
		if !ok {
			t.Fatalf("missing bucket row %q", id)
		}
		if r.RemainingFraction != 1 {
			t.Errorf("%s: remaining fraction = %v, want 1", id, r.RemainingFraction)
		}
		if r.RemainingPercent != 100 {
			t.Errorf("%s: remaining percent = %v, want 100", id, r.RemainingPercent)
		}
		if r.IsExhausted {
			t.Errorf("%s: unexpectedly exhausted", id)
		}
		if r.ResetTime == nil {
			t.Errorf("%s: reset time not parsed", id)
		}
		if r.Label == "" {
			t.Errorf("%s: empty label", id)
		}
	}

	if got := byID["gemini-weekly"].Label; got != "Gemini Weekly" {
		t.Errorf("gemini-weekly label = %q, want %q", got, "Gemini Weekly")
	}
	if got := byID["3p-5h"].Label; got != "Claude + GPT 5h" {
		t.Errorf("3p-5h label = %q, want %q", got, "Claude + GPT 5h")
	}
}

func TestParseAgyQuotaSummary_Errors(t *testing.T) {
	if _, err := ParseAgyQuotaSummary([]byte("not json")); err == nil {
		t.Error("expected error on invalid JSON")
	}
	if _, err := ParseAgyQuotaSummary([]byte(`{}`)); err == nil {
		t.Error("expected error on missing response body")
	}
	if _, err := ParseAgyQuotaSummary([]byte(`{"response":{"groups":[]}}`)); err == nil {
		t.Error("expected error on empty groups")
	}
}

func TestParseAgyQuotaSummary_ClampsAndExhaustion(t *testing.T) {
	payload := `{"response":{"groups":[{"displayName":"Gemini Models","buckets":[
		{"bucketId":"gemini-5h","displayName":"Five Hour Limit","window":"5h","remainingFraction":0,"resetTime":"2026-06-15T13:00:00Z"},
		{"bucketId":"odd","displayName":"Odd Limit","window":"weekly","remainingFraction":1.5,"resetTime":""}
	]}]}}`
	rows, err := ParseAgyQuotaSummary([]byte(payload))
	if err != nil {
		t.Fatalf("ParseAgyQuotaSummary: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if !rows[0].IsExhausted {
		t.Error("gemini-5h with 0 fraction should be exhausted")
	}
	if rows[1].RemainingFraction != 1 {
		t.Errorf("over-1 fraction should clamp to 1, got %v", rows[1].RemainingFraction)
	}
	// Unknown bucket falls back to group-derived label.
	if rows[1].Label != "Gemini Odd Limit" {
		t.Errorf("unknown bucket label = %q, want %q", rows[1].Label, "Gemini Odd Limit")
	}
}

func TestNormalizeAntigravitySource(t *testing.T) {
	cases := map[string]string{
		"ide": "ide", "cli": "cli", "both": "both",
		"IDE": "ide", " Both ": "both", "": "both", "garbage": "both",
	}
	for in, want := range cases {
		if got := NormalizeAntigravitySource(in); got != want {
			t.Errorf("NormalizeAntigravitySource(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveAgyPath_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "agy")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake agy: %v", err)
	}
	t.Setenv("ANTIGRAVITY_CLI_PATH", fake)
	got, err := resolveAgyPath()
	if err != nil {
		t.Fatalf("resolveAgyPath: %v", err)
	}
	if got != fake {
		t.Errorf("resolveAgyPath = %q, want %q", got, fake)
	}
}

func TestResolveAgyPath_EnvMissingFileErrors(t *testing.T) {
	t.Setenv("ANTIGRAVITY_CLI_PATH", filepath.Join(t.TempDir(), "does-not-exist"))
	if _, err := resolveAgyPath(); err == nil {
		t.Error("expected error when ANTIGRAVITY_CLI_PATH points at a missing file")
	}
}
