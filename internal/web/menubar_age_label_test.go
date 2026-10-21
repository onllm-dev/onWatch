package web

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// The per-card age label must count from the provider's own last successful
// data time against the client wall clock, so it keeps growing both when the
// client cannot reach the daemon and when the daemon is up but that provider
// is not syncing. It must not jump in steps on its scale.
func TestMenubarProviderAgeLabelCountsFromTimestamp(t *testing.T) {
	page, err := staticFS.ReadFile("static/menubar.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(page)

	start := strings.Index(html, "function compactUpdatedLabel(")
	end := strings.Index(html, "// quotaLabelSemantic maps")
	if start < 0 || end < start {
		t.Fatal("age label helpers not found in menubar.html")
	}
	slice := html[start:end]

	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node required for menubar age label test")
	}

	test := `
const assert = require('node:assert/strict');
` + slice + `
const nowMs = 1_700_000_000_000;
const realNow = Date.now;
Date.now = () => nowMs;
const iso = (msAgo) => new Date(nowMs - msAgo).toISOString();
const min = 60 * 1000, hour = 60 * min, day = 24 * hour;

assert.equal(providerAgeLabel({updated_at_time: iso(0)}), "now");
assert.equal(providerAgeLabel({updated_at_time: iso(90 * 1000)}), "1m");
assert.equal(providerAgeLabel({updated_at_time: iso(2 * min)}), "2m");
assert.equal(providerAgeLabel({updated_at_time: iso(59 * min)}), "59m");
assert.equal(providerAgeLabel({updated_at_time: iso(hour)}), "1h");
assert.equal(providerAgeLabel({updated_at_time: iso(23 * hour)}), "23h");
assert.equal(providerAgeLabel({updated_at_time: iso(day)}), "1d");
assert.equal(providerAgeLabel({updated_at_time: iso(2 * day)}), "2d");

// The label keeps growing as the wall clock advances, with no API call.
const provider = {updated_at_time: iso(0)};
assert.equal(providerAgeLabel(provider), "now");
Date.now = () => nowMs + 5 * min;
assert.equal(providerAgeLabel(provider), "5m");
Date.now = () => nowMs + 3 * hour;
assert.equal(providerAgeLabel(provider), "3h");

// No timestamp: fall back to the server's pre-rendered string.
assert.equal(providerAgeLabel({updated_at: "7m ago"}), "7m");
assert.equal(providerAgeLabel(null), "--");
// A zero Go time.Time ("0001-01-01T00:00:00Z") and other absurd ages must fall
// back rather than render hundreds of thousands of days.
assert.equal(ageLabelFromTimestamp("0001-01-01T00:00:00Z", "5m ago"), "5m");
assert.equal(ageLabelFromTimestamp("1999-12-31T23:59:59Z", "5m ago"), "5m");
assert.equal(ageLabelFromTimestamp("not-a-date", "5m ago"), "5m");
Date.now = realNow;
`
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node)
	cmd.Stdin = strings.NewReader(test)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
}
