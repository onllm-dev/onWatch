package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func mustExec(t *testing.T, s *Store, query string, args ...interface{}) {
	t.Helper()
	if _, err := s.db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func countRows(t *testing.T, s *Store, table string) int64 {
	t.Helper()
	var n int64
	if err := s.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// seedRetentionFixture writes one old and one recent snapshot into the tables
// that carry identifiers, each with a child row, so scrub and delete can be
// told apart.
func seedRetentionFixture(t *testing.T, s *Store, old, recent time.Time) {
	t.Helper()
	oldTS := old.UTC().Format(time.RFC3339)
	newTS := recent.UTC().Format(time.RFC3339)

	for _, ts := range []string{oldTS, newTS} {
		mustExec(t, s, `INSERT INTO grok_snapshots (captured_at, account_id, email, team_id, login_method, raw_json, quota_count)
			VALUES (?, 1, 'dev@example.com', 'team-42', 'sso', '{"email":"dev@example.com"}', 2)`, ts)
		var snapID int64
		if err := s.db.QueryRow(`SELECT id FROM grok_snapshots WHERE captured_at = ?`, ts).Scan(&snapID); err != nil {
			t.Fatalf("select grok snapshot id: %v", err)
		}
		mustExec(t, s, `INSERT INTO grok_quota_values (snapshot_id, quota_name, utilization, status)
			VALUES (?, 'credits', 42.5, 'ok')`, snapID)

		mustExec(t, s, `INSERT INTO ollama_snapshots (captured_at, raw_json, plan, account_name, account_email, monthly_used, monthly_limit, extra_cost, quota_count)
			VALUES (?, '{"Email":"dev@example.com"}', 'pro', 'Dev Person', 'dev@example.com', 12.5, 20, 0, 1)`, ts)
		var ollamaID int64
		if err := s.db.QueryRow(`SELECT id FROM ollama_snapshots WHERE captured_at = ?`, ts).Scan(&ollamaID); err != nil {
			t.Fatalf("select ollama snapshot id: %v", err)
		}
		mustExec(t, s, `INSERT INTO ollama_model_usage (snapshot_id, model, request_count, cost) VALUES (?, 'qwen', 3, 0.01)`, ollamaID)

		mustExec(t, s, `INSERT INTO antigravity_snapshots (captured_at, email, plan_name, prompt_credits, monthly_credits, raw_json, model_count, source)
			VALUES (?, 'dev@example.com', 'pro', 10, 100, '{"email":"dev@example.com"}', 1, 'ide')`, ts)
	}
}

// TestApplyRetention_ZeroPolicyIsNoOp is the upgrade-safety test: a version that
// introduces retention must not delete or scrub anything until the operator
// chooses a period.
func TestApplyRetention_ZeroPolicyIsNoOp(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	seedRetentionFixture(t, s, now.AddDate(-2, 0, 0), now.Add(-time.Hour))

	before := countRows(t, s, "grok_snapshots")
	res, err := s.ApplyRetention(context.Background(), RetentionPolicy{}, now)
	if err != nil {
		t.Fatalf("ApplyRetention: %v", err)
	}
	if res.TotalScrubbed() != 0 || res.TotalDeleted() != 0 {
		t.Errorf("zero policy changed rows: scrubbed=%d deleted=%d", res.TotalScrubbed(), res.TotalDeleted())
	}
	if got := countRows(t, s, "grok_snapshots"); got != before {
		t.Errorf("grok_snapshots = %d, want %d unchanged", got, before)
	}
	var email string
	if err := s.db.QueryRow(`SELECT email FROM grok_snapshots ORDER BY captured_at LIMIT 1`).Scan(&email); err != nil {
		t.Fatalf("select email: %v", err)
	}
	if email != "dev@example.com" {
		t.Errorf("email = %q, want it untouched by a zero policy", email)
	}
}

// TestApplyRetention_ScrubKeepsNumbersDropsIdentifiers covers the middle option:
// long-term usage charts survive, but the identifiers and the verbatim provider
// payloads behind them do not.
func TestApplyRetention_ScrubKeepsNumbersDropsIdentifiers(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	oldTS := now.AddDate(0, 0, -100)
	seedRetentionFixture(t, s, oldTS, now.Add(-time.Hour))

	res, err := s.ApplyRetention(context.Background(), RetentionPolicy{ScrubAfter: 30 * 24 * time.Hour}, now)
	if err != nil {
		t.Fatalf("ApplyRetention: %v", err)
	}
	if res.TotalScrubbed() == 0 {
		t.Fatal("expected rows to be scrubbed")
	}
	if res.TotalDeleted() != 0 {
		t.Errorf("scrub-only policy deleted %d rows", res.TotalDeleted())
	}

	// The old row keeps its shape and its numbers, loses its identifiers.
	var email, teamID, login, raw interface{}
	var quotaCount int
	err = s.db.QueryRow(`SELECT email, team_id, login_method, raw_json, quota_count FROM grok_snapshots WHERE captured_at = ?`,
		oldTS.UTC().Format(time.RFC3339)).Scan(&email, &teamID, &login, &raw, &quotaCount)
	if err != nil {
		t.Fatalf("select scrubbed grok row: %v", err)
	}
	for name, v := range map[string]interface{}{"email": email, "team_id": teamID, "login_method": login} {
		if v != nil && v != "" {
			t.Errorf("%s = %v, want NULL or empty after scrub", name, v)
		}
	}
	if raw != nil && raw != "" {
		t.Errorf("raw_json = %v, want cleared after scrub", raw)
	}
	if quotaCount != 2 {
		t.Errorf("quota_count = %d, want 2 preserved", quotaCount)
	}

	// The child usage row must survive a scrub: it holds no identifier.
	if got := countRows(t, s, "grok_quota_values"); got != 2 {
		t.Errorf("grok_quota_values = %d, want 2 (scrub must not delete children)", got)
	}

	// The recent row is untouched.
	var recentEmail string
	if err := s.db.QueryRow(`SELECT email FROM grok_snapshots WHERE captured_at > ?`,
		oldTS.UTC().Format(time.RFC3339)).Scan(&recentEmail); err != nil {
		t.Fatalf("select recent grok row: %v", err)
	}
	if recentEmail != "dev@example.com" {
		t.Errorf("recent email = %q, want it untouched", recentEmail)
	}

	// Every identifier-bearing table must be covered, not just grok.
	var ollamaEmail, ollamaName interface{}
	if err := s.db.QueryRow(`SELECT account_email, account_name FROM ollama_snapshots WHERE captured_at = ?`,
		oldTS.UTC().Format(time.RFC3339)).Scan(&ollamaEmail, &ollamaName); err != nil {
		t.Fatalf("select scrubbed ollama row: %v", err)
	}
	if (ollamaEmail != nil && ollamaEmail != "") || (ollamaName != nil && ollamaName != "") {
		t.Errorf("ollama identifiers survived scrub: %v / %v", ollamaEmail, ollamaName)
	}
	var agEmail interface{}
	if err := s.db.QueryRow(`SELECT email FROM antigravity_snapshots WHERE captured_at = ?`,
		oldTS.UTC().Format(time.RFC3339)).Scan(&agEmail); err != nil {
		t.Fatalf("select scrubbed antigravity row: %v", err)
	}
	if agEmail != nil && agEmail != "" {
		t.Errorf("antigravity email survived scrub: %v", agEmail)
	}
}

// TestApplyRetention_DeleteRemovesChildrenFirst asserts no orphaned child rows
// are left behind pointing at a deleted snapshot.
func TestApplyRetention_DeleteRemovesChildrenFirst(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	seedRetentionFixture(t, s, now.AddDate(0, 0, -100), now.Add(-time.Hour))

	res, err := s.ApplyRetention(context.Background(), RetentionPolicy{DeleteAfter: 30 * 24 * time.Hour}, now)
	if err != nil {
		t.Fatalf("ApplyRetention: %v", err)
	}
	if res.TotalDeleted() == 0 {
		t.Fatal("expected rows to be deleted")
	}

	if got := countRows(t, s, "grok_snapshots"); got != 1 {
		t.Errorf("grok_snapshots = %d, want 1 recent row left", got)
	}
	if got := countRows(t, s, "grok_quota_values"); got != 1 {
		t.Errorf("grok_quota_values = %d, want 1 - the old child row must go with its parent", got)
	}
	if got := countRows(t, s, "ollama_model_usage"); got != 1 {
		t.Errorf("ollama_model_usage = %d, want 1", got)
	}

	var orphans int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM grok_quota_values v
		LEFT JOIN grok_snapshots s ON s.id = v.snapshot_id WHERE s.id IS NULL`).Scan(&orphans); err != nil {
		t.Fatalf("orphan check: %v", err)
	}
	if orphans != 0 {
		t.Errorf("%d orphaned grok_quota_values rows", orphans)
	}
}

// TestApplyRetention_PrunesAlertsAndIngestState covers the two retention gaps
// that existed: ClearOldSystemAlerts was never called from anywhere, and
// api_integration_ingest_state was never pruned at all.
func TestApplyRetention_PrunesAlertsAndIngestState(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	oldTS := now.AddDate(0, 0, -100).UTC().Format(time.RFC3339)
	newTS := now.Add(-time.Hour).UTC().Format(time.RFC3339)

	mustExec(t, s, `INSERT INTO system_alerts (provider, alert_type, title, message, severity, created_at, metadata)
		VALUES ('grok', 'auth_error', 'Auth failed', 'token for dev@example.com expired', 'warning', ?, '{"email":"dev@example.com"}')`, oldTS)
	mustExec(t, s, `INSERT INTO system_alerts (provider, alert_type, title, message, severity, created_at, metadata)
		VALUES ('grok', 'auth_error', 'Auth failed', 'recent', 'warning', ?, '{}')`, newTS)
	mustExec(t, s, `INSERT INTO api_integration_ingest_state (source_path, offset_bytes, file_size, file_mod_time, partial_line, updated_at)
		VALUES ('/home/dev/.onwatch/api-integrations/old.jsonl', 10, 10, ?, '', ?)`, oldTS, oldTS)
	mustExec(t, s, `INSERT INTO api_integration_ingest_state (source_path, offset_bytes, file_size, file_mod_time, partial_line, updated_at)
		VALUES ('/home/dev/.onwatch/api-integrations/new.jsonl', 10, 10, ?, '', ?)`, newTS, newTS)
	mustExec(t, s, `INSERT INTO notification_log (provider, quota_key, notification_type, sent_at, utilization)
		VALUES ('grok', 'credits', 'warning', ?, 80)`, oldTS)

	if _, err := s.ApplyRetention(context.Background(), RetentionPolicy{DeleteAfter: 30 * 24 * time.Hour}, now); err != nil {
		t.Fatalf("ApplyRetention: %v", err)
	}

	if got := countRows(t, s, "system_alerts"); got != 1 {
		t.Errorf("system_alerts = %d, want 1 - old alerts carry provider identifiers in their message and metadata", got)
	}
	if got := countRows(t, s, "api_integration_ingest_state"); got != 1 {
		t.Errorf("api_integration_ingest_state = %d, want 1 - it holds absolute file paths with the OS username", got)
	}
	if got := countRows(t, s, "notification_log"); got != 0 {
		t.Errorf("notification_log = %d, want 0", got)
	}
}

// TestApplyRetention_KeepsOpenResetCycles asserts an in-flight cycle is never
// deleted, however old its start, since the dashboard still needs it.
func TestApplyRetention_KeepsOpenResetCycles(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	oldStart := now.AddDate(0, 0, -100).UTC().Format(time.RFC3339)
	oldEnd := now.AddDate(0, 0, -99).UTC().Format(time.RFC3339)

	mustExec(t, s, `INSERT INTO grok_reset_cycles (account_id, quota_name, cycle_start, cycle_end, peak_utilization, total_delta)
		VALUES (1, 'closed', ?, ?, 50, 10)`, oldStart, oldEnd)
	mustExec(t, s, `INSERT INTO grok_reset_cycles (account_id, quota_name, cycle_start, cycle_end, peak_utilization, total_delta)
		VALUES (1, 'open', ?, NULL, 50, 10)`, oldStart)

	if _, err := s.ApplyRetention(context.Background(), RetentionPolicy{DeleteAfter: 30 * 24 * time.Hour}, now); err != nil {
		t.Fatalf("ApplyRetention: %v", err)
	}

	var names []string
	rows, err := s.db.Query(`SELECT quota_name FROM grok_reset_cycles`)
	if err != nil {
		t.Fatalf("query cycles: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		rows.Scan(&n)
		names = append(names, n)
	}
	if len(names) != 1 || names[0] != "open" {
		t.Errorf("remaining cycles = %v, want only the open one", names)
	}
}

// TestExportAll_IsCompleteAndRedactsSecrets covers GDPR Art. 15/20 and DPDP
// s.11. The export must carry the personal data and must not carry live
// credentials: an export file is the easiest thing in the world to mislay.
func TestExportAll_IsCompleteAndRedactsSecrets(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	seedRetentionFixture(t, s, now.AddDate(0, 0, -10), now.Add(-time.Hour))
	if err := s.SetSetting("gemini_tokens", `{"access_token":"ya29.SECRET","refresh_token":"1//SECRET"}`); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if err := s.SetSetting("timezone", "Europe/Berlin"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if err := s.UpsertUser("admin", "$2a$10$abcdefghijklmnopqrstuv"); err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}

	var buf strings.Builder
	if err := s.ExportAll(context.Background(), &buf); err != nil {
		t.Fatalf("ExportAll: %v", err)
	}
	out := buf.String()

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("export is not valid JSON: %v", err)
	}
	if _, ok := parsed["exported_at"]; !ok {
		t.Error("export must record when it was produced")
	}
	tables, ok := parsed["tables"].(map[string]interface{})
	if !ok {
		t.Fatal("export must have a tables object")
	}
	for _, want := range []string{"grok_snapshots", "ollama_snapshots", "settings", "users"} {
		if _, ok := tables[want]; !ok {
			t.Errorf("export is missing table %s", want)
		}
	}

	// Personal data must be present - that is the point of an access request.
	if !strings.Contains(out, "dev@example.com") {
		t.Error("export should contain the stored account email")
	}
	if !strings.Contains(out, "Europe/Berlin") {
		t.Error("export should contain ordinary settings")
	}

	// Secrets must not be.
	for _, secret := range []string{"ya29.SECRET", "1//SECRET", "$2a$10$abcdefghijklmnopqrstuv"} {
		if strings.Contains(out, secret) {
			t.Errorf("export leaked a credential: %q", secret)
		}
	}
	if !strings.Contains(out, ExportRedactedMarker) {
		t.Errorf("export should mark redacted values with %q", ExportRedactedMarker)
	}
}

// TestEraseAll_ClearsPersonalDataAndKeepsSchema covers GDPR Art. 17 and DPDP
// s.12(3)/8(7).
func TestEraseAll_ClearsPersonalDataAndKeepsSchema(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	seedRetentionFixture(t, s, now.AddDate(0, 0, -10), now.Add(-time.Hour))
	mustExec(t, s, `INSERT INTO push_subscriptions (endpoint, p256dh, auth, created_at) VALUES ('https://fcm.googleapis.com/x', 'k', 'a', ?)`,
		now.Format(time.RFC3339))
	if err := s.SetSetting("timezone", "Europe/Berlin"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	counts, err := s.EraseAll(context.Background())
	if err != nil {
		t.Fatalf("EraseAll: %v", err)
	}
	if len(counts) == 0 {
		t.Error("EraseAll should report what it removed")
	}

	for _, table := range []string{
		"grok_snapshots", "grok_quota_values", "ollama_snapshots", "ollama_model_usage",
		"antigravity_snapshots", "push_subscriptions",
	} {
		if got := countRows(t, s, table); got != 0 {
			t.Errorf("%s = %d rows after EraseAll, want 0", table, got)
		}
	}

	// The install must still work afterwards: the schema itself has to survive
	// so the daemon does not have to rebuild it on the next poll.
	var tableCount int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`).Scan(&tableCount); err != nil {
		t.Fatalf("count tables: %v", err)
	}
	if tableCount < 50 {
		t.Errorf("table count = %d after EraseAll, want the full schema still present", tableCount)
	}
	if _, err := s.GetSetting("timezone"); err != nil {
		t.Errorf("settings table must still be usable: %v", err)
	}
}

// TestEraseProvider_LeavesOtherProvidersAlone asserts a targeted erasure is
// actually targeted.
func TestEraseProvider_LeavesOtherProvidersAlone(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	seedRetentionFixture(t, s, now.AddDate(0, 0, -10), now.Add(-time.Hour))

	counts, err := s.EraseProvider(context.Background(), "grok")
	if err != nil {
		t.Fatalf("EraseProvider: %v", err)
	}
	if counts["grok_snapshots"] != 2 {
		t.Errorf("grok_snapshots erased = %d, want 2", counts["grok_snapshots"])
	}
	if got := countRows(t, s, "grok_snapshots"); got != 0 {
		t.Errorf("grok_snapshots = %d, want 0", got)
	}
	if got := countRows(t, s, "grok_quota_values"); got != 0 {
		t.Errorf("grok_quota_values = %d, want 0", got)
	}
	if got := countRows(t, s, "ollama_snapshots"); got != 2 {
		t.Errorf("ollama_snapshots = %d, want 2 untouched", got)
	}
	if got := countRows(t, s, "antigravity_snapshots"); got != 2 {
		t.Errorf("antigravity_snapshots = %d, want 2 untouched", got)
	}
}

// TestEraseProvider_RejectsUnknownProvider guards against a typo silently
// reporting success, and against anything reaching SQL identifier construction
// from outside the fixed registry.
func TestEraseProvider_RejectsUnknownProvider(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	for _, bad := range []string{"", "nope", "grok; DROP TABLE users", "GROK"} {
		if _, err := s.EraseProvider(context.Background(), bad); err == nil {
			t.Errorf("EraseProvider(%q) should fail", bad)
		}
	}
}

// TestPrivacyTableRegistry_MatchesSchema is the guard that keeps this code
// honest as the schema grows: every table in the database must be classified,
// and every column the registry names must exist. A new provider added without
// a registry entry fails here rather than quietly escaping retention, export
// and erasure.
func TestPrivacyTableRegistry_MatchesSchema(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	rows, err := s.db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	defer rows.Close()

	live := map[string]bool{}
	for rows.Next() {
		var n string
		rows.Scan(&n)
		live[n] = true
	}

	classified := map[string]bool{}
	for _, tb := range privacyTables {
		if !live[tb.Name] {
			t.Errorf("registry names table %q which does not exist", tb.Name)
		}
		classified[tb.Name] = true
		for _, child := range tb.Children {
			if !live[child.Name] {
				t.Errorf("registry names child table %q which does not exist", child.Name)
			}
			classified[child.Name] = true
		}
		// Named columns must exist, or a scrub silently updates nothing.
		for _, col := range append(append([]string{}, tb.ScrubColumns...), tb.TimeColumn) {
			if col == "" {
				continue
			}
			var n int
			q := "SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?"
			if err := s.db.QueryRow(q, tb.Name, col).Scan(&n); err != nil {
				t.Fatalf("pragma_table_info(%s): %v", tb.Name, err)
			}
			if n != 1 {
				t.Errorf("registry names column %s.%s which does not exist", tb.Name, col)
			}
		}
	}

	for name := range live {
		if !classified[name] && !privacyExemptTables[name] {
			t.Errorf("table %q is neither in privacyTables nor privacyExemptTables - classify it so retention, export and erasure cover it", name)
		}
	}
}
