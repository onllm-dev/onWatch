package store

// Data-protection lifecycle: retention, export and erasure.
//
// These are the mechanisms behind the rights a deployer has to be able to
// honour - access and portability (GDPR Art. 15/20, DPDP s.11), erasure
// (GDPR Art. 17, DPDP s.12(3) and s.8(7)) and storage limitation
// (GDPR Art. 5(1)(e)). Everything here is driven by one declarative registry,
// privacyTables, so that adding a provider cannot quietly create a table that
// escapes all three: TestPrivacyTableRegistry_MatchesSchema fails the build if
// a table is left unclassified.
//
// On SQL identifiers: table and column names in this file are interpolated
// into statements, which the project otherwise forbids. They are safe because
// they come only from the compile-time constants below - never from a request,
// a setting or a provider response. EraseProvider is the one entry point that
// takes a caller-supplied name, and it resolves that name against the registry
// rather than using it in SQL.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ExportRedactedMarker replaces credential values in an export.
const ExportRedactedMarker = "[redacted-by-onwatch-export]"

// exportRowCap bounds how many rows per table an export streams. The project
// runs under a 40MB ceiling with a single SQLite connection, so an export
// walks tables one at a time and stops at this many rows each, noting the
// truncation in the output rather than silently dropping it.
const exportRowCap = 20000

// childTable is a table whose rows belong to a parent snapshot row.
type childTable struct {
	Name string
	// ParentKey is the column holding the parent's id.
	ParentKey string
}

// privacyTable classifies one table that holds personal data.
type privacyTable struct {
	Name string
	// TimeColumn is the column retention compares against. Empty means the
	// table has no age of its own and is only reachable through erasure.
	TimeColumn string
	// ScrubColumns lose their contents once a row is older than ScrubAfter.
	// These are the identifiers and the verbatim provider payloads; the
	// numeric quota columns beside them are deliberately left alone so that
	// long-term charts survive scrubbing.
	ScrubColumns []string
	// Children are deleted before the parent row so no orphan can be left.
	Children []childTable
	// Provider is the key EraseProvider matches, empty for cross-provider
	// tables that a targeted erasure must not touch.
	Provider string
	// OpenCycleColumn, when set, marks a cycle table: rows whose end column
	// is NULL are in flight and are never deleted by age.
	OpenCycleColumn string
}

// privacyTables is the registry. Every table holding personal data, an
// identifier, or history a data subject could ask about belongs here.
var privacyTables = []privacyTable{
	// ── Provider snapshots ────────────────────────────────────────────────
	{
		Name: "anthropic_snapshots", TimeColumn: "captured_at", Provider: "anthropic",
		ScrubColumns: []string{"raw_json"},
		Children:     []childTable{{Name: "anthropic_quota_values", ParentKey: "snapshot_id"}},
	},
	{
		Name: "antigravity_snapshots", TimeColumn: "captured_at", Provider: "antigravity",
		ScrubColumns: []string{"email", "raw_json"},
		Children:     []childTable{{Name: "antigravity_model_values", ParentKey: "snapshot_id"}},
	},
	{
		Name: "codex_snapshots", TimeColumn: "captured_at", Provider: "codex",
		ScrubColumns: []string{"raw_json"},
		Children:     []childTable{{Name: "codex_quota_values", ParentKey: "snapshot_id"}},
	},
	{
		Name: "copilot_snapshots", TimeColumn: "captured_at", Provider: "copilot",
		ScrubColumns: []string{"raw_json"},
		Children:     []childTable{{Name: "copilot_quota_values", ParentKey: "snapshot_id"}},
	},
	{
		Name: "cursor_snapshots", TimeColumn: "captured_at", Provider: "cursor",
		ScrubColumns: []string{"raw_json"},
		Children:     []childTable{{Name: "cursor_quota_values", ParentKey: "snapshot_id"}},
	},
	{
		Name: "deepseek_snapshots", TimeColumn: "captured_at", Provider: "deepseek",
	},
	{
		Name: "gemini_snapshots", TimeColumn: "captured_at", Provider: "gemini",
		ScrubColumns: []string{"project_id", "raw_json"},
		Children:     []childTable{{Name: "gemini_quota_values", ParentKey: "snapshot_id"}},
	},
	{
		Name: "grok_snapshots", TimeColumn: "captured_at", Provider: "grok",
		ScrubColumns: []string{"email", "team_id", "login_method", "raw_json"},
		Children:     []childTable{{Name: "grok_quota_values", ParentKey: "snapshot_id"}},
	},
	{
		Name: "kimi_snapshots", TimeColumn: "captured_at", Provider: "kimi",
		ScrubColumns: []string{"user_id", "raw_json"},
		Children:     []childTable{{Name: "kimi_quota_values", ParentKey: "snapshot_id"}},
	},
	{
		Name: "minimax_snapshots", TimeColumn: "captured_at", Provider: "minimax",
		ScrubColumns: []string{"raw_json"},
		Children:     []childTable{{Name: "minimax_model_values", ParentKey: "snapshot_id"}},
	},
	{
		Name: "moonshot_snapshots", TimeColumn: "captured_at", Provider: "moonshot",
	},
	{
		Name: "ollama_snapshots", TimeColumn: "captured_at", Provider: "ollama",
		ScrubColumns: []string{"account_name", "account_email", "raw_json"},
		Children: []childTable{
			{Name: "ollama_quota_values", ParentKey: "snapshot_id"},
			{Name: "ollama_model_usage", ParentKey: "snapshot_id"},
		},
	},
	{
		Name: "opencode_snapshots", TimeColumn: "captured_at", Provider: "opencode",
		ScrubColumns: []string{"raw_json"},
		Children:     []childTable{{Name: "opencode_quota_values", ParentKey: "snapshot_id"}},
	},
	{
		Name: "openrouter_snapshots", TimeColumn: "captured_at", Provider: "openrouter",
		ScrubColumns: []string{"label"},
	},
	{
		Name: "zai_snapshots", TimeColumn: "captured_at", Provider: "zai",
	},
	{
		Name: "quota_snapshots", TimeColumn: "captured_at", Provider: "synthetic",
	},

	// ── Reset cycles: aggregates, but they are still usage history ────────
	{Name: "anthropic_reset_cycles", TimeColumn: "cycle_start", Provider: "anthropic", OpenCycleColumn: "cycle_end"},
	{Name: "antigravity_reset_cycles", TimeColumn: "cycle_start", Provider: "antigravity", OpenCycleColumn: "cycle_end"},
	{Name: "codex_reset_cycles", TimeColumn: "cycle_start", Provider: "codex", OpenCycleColumn: "cycle_end"},
	{Name: "copilot_reset_cycles", TimeColumn: "cycle_start", Provider: "copilot", OpenCycleColumn: "cycle_end"},
	{Name: "cursor_reset_cycles", TimeColumn: "cycle_start", Provider: "cursor", OpenCycleColumn: "cycle_end"},
	{Name: "deepseek_reset_cycles", TimeColumn: "cycle_start", Provider: "deepseek", OpenCycleColumn: "cycle_end"},
	{Name: "gemini_reset_cycles", TimeColumn: "cycle_start", Provider: "gemini", OpenCycleColumn: "cycle_end"},
	{Name: "grok_reset_cycles", TimeColumn: "cycle_start", Provider: "grok", OpenCycleColumn: "cycle_end"},
	{Name: "kimi_reset_cycles", TimeColumn: "cycle_start", Provider: "kimi", OpenCycleColumn: "cycle_end"},
	{Name: "minimax_reset_cycles", TimeColumn: "cycle_start", Provider: "minimax", OpenCycleColumn: "cycle_end"},
	{Name: "moonshot_reset_cycles", TimeColumn: "cycle_start", Provider: "moonshot", OpenCycleColumn: "cycle_end"},
	{Name: "ollama_reset_cycles", TimeColumn: "cycle_start", Provider: "ollama", OpenCycleColumn: "cycle_end"},
	{Name: "opencode_reset_cycles", TimeColumn: "cycle_start", Provider: "opencode", OpenCycleColumn: "cycle_end"},
	{Name: "openrouter_reset_cycles", TimeColumn: "cycle_start", Provider: "openrouter", OpenCycleColumn: "cycle_end"},
	{Name: "zai_reset_cycles", TimeColumn: "cycle_start", Provider: "zai", OpenCycleColumn: "cycle_end"},
	{Name: "reset_cycles", TimeColumn: "cycle_start", Provider: "synthetic", OpenCycleColumn: "cycle_end"},

	// ── Other history ─────────────────────────────────────────────────────
	{Name: "zai_hourly_usage", TimeColumn: "fetched_at", Provider: "zai"},
	{Name: "sessions", TimeColumn: "started_at"},
	{Name: "notification_log", TimeColumn: "sent_at"},
	{
		// The title, message and metadata quote provider account identifiers.
		Name: "system_alerts", TimeColumn: "created_at",
		ScrubColumns: []string{"message", "metadata"},
	},
	{
		Name: "api_integration_usage_events", TimeColumn: "captured_at",
		ScrubColumns: []string{"account_name", "request_id", "metadata_json", "source_path"},
	},
	{
		// source_path is an absolute path, so it usually carries the OS
		// username. updated_at moves on every ingest pass, so only state for
		// files that have stopped being written ages out.
		Name: "api_integration_ingest_state", TimeColumn: "updated_at",
		ScrubColumns: []string{"partial_line"},
	},

	// ── Identity and device records: no age, erasure only ─────────────────
	{Name: "push_subscriptions"},
	{Name: "provider_accounts"},
}

// privacyExemptTables hold no personal data and no usage history. They are
// listed explicitly so that the registry test can tell "deliberately exempt"
// from "forgotten".
var privacyExemptTables = map[string]bool{
	// Schema bookkeeping.
	"schema_version": true,
	// Child rows, covered through their parent's Children list.
	"anthropic_quota_values":   true,
	"antigravity_model_values": true,
	"codex_quota_values":       true,
	"copilot_quota_values":     true,
	"cursor_quota_values":      true,
	"gemini_quota_values":      true,
	"grok_quota_values":        true,
	"kimi_quota_values":        true,
	"minimax_model_values":     true,
	"ollama_model_usage":       true,
	"ollama_quota_values":      true,
	"opencode_quota_values":    true,
	// Handled directly rather than through the age-based registry: settings
	// and users are configuration and credentials, exported with redaction and
	// cleared by EraseAll, but never aged out - losing your own password or
	// timezone to a retention pass would be absurd.
	"settings":    true,
	"users":       true,
	"auth_tokens": true,
}

// identityTables are cleared by a full erasure but never by retention.
var identityTables = []string{"push_subscriptions", "provider_accounts", "auth_tokens"}

// RetentionPolicy says how long personal data may be kept.
//
// Both zero means keep everything, which is the default: introducing retention
// must never delete an existing install's history on upgrade. The operator
// chooses a period, and the choice is theirs to make - GDPR requires a
// controller to define one, it does not name it.
type RetentionPolicy struct {
	// ScrubAfter clears identifiers and verbatim provider payloads from rows
	// older than this, keeping the numeric usage history. Zero disables.
	ScrubAfter time.Duration
	// DeleteAfter removes rows older than this outright, children first.
	// Zero disables.
	DeleteAfter time.Duration
}

// IsZero reports whether the policy would change nothing.
func (p RetentionPolicy) IsZero() bool {
	return p.ScrubAfter <= 0 && p.DeleteAfter <= 0
}

// Setting keys for the retention policy, both in days. Absent or "0" means
// keep everything, which is the default for every existing install.
const (
	SettingRetentionScrubDays  = "retention_scrub_days"
	SettingRetentionDeleteDays = "retention_delete_days"
)

// RetentionPolicyFromSettings reads the operator's chosen policy. An unset,
// empty or unparseable value means "keep everything" rather than a guess: a
// retention pass is destructive, so an unreadable setting must never be read
// as permission to delete.
func (s *Store) RetentionPolicyFromSettings() RetentionPolicy {
	if s == nil {
		return RetentionPolicy{}
	}
	return RetentionPolicy{
		ScrubAfter:  s.retentionDays(SettingRetentionScrubDays),
		DeleteAfter: s.retentionDays(SettingRetentionDeleteDays),
	}
}

func (s *Store) retentionDays(key string) time.Duration {
	d, _ := s.retentionDaysSetting(key)
	return d
}

// retentionDaysSetting reports the stored period and whether the operator has
// made a choice at all. The distinction matters for the env default: an
// explicit 0 means "keep everything" and must not fall back to a default,
// while an absent setting means "never asked".
func (s *Store) retentionDaysSetting(key string) (time.Duration, bool) {
	val, err := s.GetSetting(key)
	if err != nil || strings.TrimSpace(val) == "" {
		return 0, false
	}
	days, err := strconv.Atoi(strings.TrimSpace(val))
	if err != nil {
		return 0, false
	}
	if days <= 0 {
		return 0, true
	}
	return time.Duration(days) * 24 * time.Hour, true
}

// RetentionPolicyWithDefault resolves the effective policy, using def for any
// period the operator has not set.
//
// The default comes from ONWATCH_RETENTION_SCRUB_DAYS and
// ONWATCH_RETENTION_DELETE_DAYS, which exist so a container deployment can
// arrive with a policy already in force. Once a period is chosen in the
// dashboard, the dashboard wins - including a deliberate 0 - so the operator's
// own choice is never silently overridden by the environment it runs in.
func (s *Store) RetentionPolicyWithDefault(def RetentionPolicy) RetentionPolicy {
	if s == nil {
		return def
	}
	policy := def
	if d, ok := s.retentionDaysSetting(SettingRetentionScrubDays); ok {
		policy.ScrubAfter = d
	}
	if d, ok := s.retentionDaysSetting(SettingRetentionDeleteDays); ok {
		policy.DeleteAfter = d
	}
	return policy
}

// PruneResult reports what a retention pass changed, per table.
type PruneResult struct {
	Scrubbed map[string]int64
	Deleted  map[string]int64
}

// TotalScrubbed returns the number of rows whose identifiers were cleared.
func (r PruneResult) TotalScrubbed() int64 { return sumCounts(r.Scrubbed) }

// TotalDeleted returns the number of rows removed.
func (r PruneResult) TotalDeleted() int64 { return sumCounts(r.Deleted) }

func sumCounts(m map[string]int64) int64 {
	var total int64
	for _, v := range m {
		total += v
	}
	return total
}

// ApplyRetention enforces policy against rows older than the cutoffs measured
// from now. Scrubbing runs before deletion so a row that is due for both ends
// up deleted rather than scrubbed twice.
//
// Timestamps are stored as RFC3339 strings and compared as strings, which is
// correct for UTC values of the same shape and can be off by under a second
// where a writer used nanosecond precision. At day-scale retention that does
// not matter.
func (s *Store) ApplyRetention(ctx context.Context, policy RetentionPolicy, now time.Time) (PruneResult, error) {
	res := PruneResult{Scrubbed: map[string]int64{}, Deleted: map[string]int64{}}
	if s == nil || policy.IsZero() {
		return res, nil
	}

	if policy.ScrubAfter > 0 {
		cutoff := now.UTC().Add(-policy.ScrubAfter).Format(time.RFC3339)
		for _, tb := range privacyTables {
			if tb.TimeColumn == "" || len(tb.ScrubColumns) == 0 {
				continue
			}
			n, err := s.scrubTable(ctx, tb, cutoff)
			if err != nil {
				return res, err
			}
			if n > 0 {
				res.Scrubbed[tb.Name] = n
			}
		}
	}

	if policy.DeleteAfter > 0 {
		cutoff := now.UTC().Add(-policy.DeleteAfter).Format(time.RFC3339)
		for _, tb := range privacyTables {
			if tb.TimeColumn == "" {
				continue
			}
			counts, err := s.deleteAgedRows(ctx, tb, cutoff)
			if err != nil {
				return res, err
			}
			for name, n := range counts {
				if n > 0 {
					res.Deleted[name] += n
				}
			}
		}
	}

	return res, nil
}

// scrubTable clears the identifier columns of rows older than cutoff. Rows
// already scrubbed are skipped so the reported count means "rows changed by
// this pass" rather than "rows matching the cutoff".
func (s *Store) scrubTable(ctx context.Context, tb privacyTable, cutoff string) (int64, error) {
	// Scrubbed columns are set to the empty string rather than NULL: several
	// of them are declared NOT NULL DEFAULT '' (grok_snapshots.raw_json, for
	// one), so NULL would fail the constraint. Every scrub target is TEXT.
	sets := make([]string, 0, len(tb.ScrubColumns))
	alreadyClear := make([]string, 0, len(tb.ScrubColumns))
	for _, col := range tb.ScrubColumns {
		sets = append(sets, fmt.Sprintf("%s = ''", col))
		alreadyClear = append(alreadyClear, fmt.Sprintf("(%s IS NULL OR %s = '')", col, col))
	}

	query := fmt.Sprintf(
		"UPDATE %s SET %s WHERE %s < ? AND NOT (%s)",
		tb.Name, strings.Join(sets, ", "), tb.TimeColumn, strings.Join(alreadyClear, " AND "),
	)
	result, err := s.db.ExecContext(ctx, query, cutoff)
	if err != nil {
		return 0, fmt.Errorf("store.ApplyRetention: scrub %s: %w", tb.Name, err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, nil
	}
	return n, nil
}

// deleteAgedRows removes rows older than cutoff, deleting child rows first so
// nothing is orphaned. An in-flight reset cycle is kept whatever its age.
func (s *Store) deleteAgedRows(ctx context.Context, tb privacyTable, cutoff string) (map[string]int64, error) {
	counts := map[string]int64{}

	// A cycle that has not ended is still in use by the dashboard.
	where := fmt.Sprintf("%s < ?", tb.TimeColumn)
	if tb.OpenCycleColumn != "" {
		where = fmt.Sprintf("%s < ? AND %s IS NOT NULL", tb.TimeColumn, tb.OpenCycleColumn)
	}

	for _, child := range tb.Children {
		query := fmt.Sprintf(
			"DELETE FROM %s WHERE %s IN (SELECT id FROM %s WHERE %s)",
			child.Name, child.ParentKey, tb.Name, where,
		)
		result, err := s.db.ExecContext(ctx, query, cutoff)
		if err != nil {
			return counts, fmt.Errorf("store.ApplyRetention: delete %s: %w", child.Name, err)
		}
		if n, err := result.RowsAffected(); err == nil {
			counts[child.Name] += n
		}
	}

	query := fmt.Sprintf("DELETE FROM %s WHERE %s", tb.Name, where)
	result, err := s.db.ExecContext(ctx, query, cutoff)
	if err != nil {
		return counts, fmt.Errorf("store.ApplyRetention: delete %s: %w", tb.Name, err)
	}
	if n, err := result.RowsAffected(); err == nil {
		counts[tb.Name] += n
	}
	return counts, nil
}

// redactedSettingKeys name settings whose values are credentials or key
// material. An export replaces them with ExportRedactedMarker: the values
// belong to the operator, but an export file is trivially mislaid and there is
// no access-request purpose in carrying live secrets around.
var redactedSettingKeys = map[string]bool{
	"smtp":              true,
	"gemini_tokens":     true,
	"provider_settings": true,
	"vapid_keys":        true,
}

// ExportAll streams a complete, machine-readable export of everything onWatch
// stores about the operator, for GDPR Art. 15/20 and DPDP s.11.
//
// It writes JSON directly to w, one table at a time with a bounded row cursor,
// so a large history does not have to be held in memory at once. Credentials
// and password hashes are redacted; personal data is not.
func (s *Store) ExportAll(ctx context.Context, w io.Writer) error {
	if s == nil {
		return fmt.Errorf("store.ExportAll: nil store")
	}

	tables, err := s.listTables(ctx)
	if err != nil {
		return err
	}

	if _, err := io.WriteString(w, "{\n"); err != nil {
		return err
	}
	header := fmt.Sprintf("  %q: %q,\n  %q: %q,\n  %q: %d,\n  %q: {\n",
		"export_format", "onwatch-data-export-v1",
		"exported_at", time.Now().UTC().Format(time.RFC3339),
		"row_cap_per_table", exportRowCap,
		"tables",
	)
	if _, err := io.WriteString(w, header); err != nil {
		return err
	}

	for i, table := range tables {
		if err := ctx.Err(); err != nil {
			return err
		}
		sep := ","
		if i == len(tables)-1 {
			sep = ""
		}
		if _, err := io.WriteString(w, fmt.Sprintf("    %q: ", table)); err != nil {
			return err
		}
		if err := s.exportTable(ctx, w, table); err != nil {
			return err
		}
		if _, err := io.WriteString(w, sep+"\n"); err != nil {
			return err
		}
	}

	_, err = io.WriteString(w, "  }\n}\n")
	return err
}

func (s *Store) listTables(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("store.ExportAll: list tables: %w", err)
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("store.ExportAll: scan table name: %w", err)
		}
		tables = append(tables, name)
	}
	return tables, rows.Err()
}

// exportTable writes one table as a JSON array of objects.
func (s *Store) exportTable(ctx context.Context, w io.Writer, table string) error {
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf("SELECT * FROM %s LIMIT %d", table, exportRowCap))
	if err != nil {
		return fmt.Errorf("store.ExportAll: read %s: %w", table, err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return fmt.Errorf("store.ExportAll: columns of %s: %w", table, err)
	}

	enc := json.NewEncoder(w)
	if _, err := io.WriteString(w, "[\n"); err != nil {
		return err
	}

	first := true
	for rows.Next() {
		holders := make([]interface{}, len(cols))
		for i := range holders {
			holders[i] = new(interface{})
		}
		if err := rows.Scan(holders...); err != nil {
			return fmt.Errorf("store.ExportAll: scan %s: %w", table, err)
		}

		record := make(map[string]interface{}, len(cols))
		for i, col := range cols {
			value := *(holders[i].(*interface{}))
			if b, ok := value.([]byte); ok {
				value = string(b)
			}
			record[col] = value
		}
		redactExportRecord(table, record)

		if !first {
			if _, err := io.WriteString(w, ",\n"); err != nil {
				return err
			}
		}
		first = false
		if _, err := io.WriteString(w, "      "); err != nil {
			return err
		}
		if err := enc.Encode(record); err != nil {
			return fmt.Errorf("store.ExportAll: encode %s: %w", table, err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("store.ExportAll: iterate %s: %w", table, err)
	}

	_, err = io.WriteString(w, "    ]")
	return err
}

// redactExportRecord removes credentials and key material from one exported row.
func redactExportRecord(table string, record map[string]interface{}) {
	switch table {
	case "users":
		if _, ok := record["password_hash"]; ok {
			record["password_hash"] = ExportRedactedMarker
		}
	case "auth_tokens":
		if _, ok := record["token"]; ok {
			record["token"] = ExportRedactedMarker
		}
	case "settings":
		key, _ := record["key"].(string)
		if redactedSettingKeys[key] {
			record["value"] = ExportRedactedMarker
		}
	case "provider_accounts":
		// metadata can hold a provider API key.
		if _, ok := record["metadata"]; ok {
			record["metadata"] = ExportRedactedMarker
		}
	case "push_subscriptions":
		// p256dh and auth are the browser's encryption key material.
		for _, col := range []string{"p256dh", "auth"} {
			if _, ok := record[col]; ok {
				record[col] = ExportRedactedMarker
			}
		}
	}
}

// EraseAll removes every row of personal data and usage history, for GDPR
// Art. 17 and DPDP s.12(3). The schema and the operator's own configuration
// survive, so the install keeps working: what goes is the data about people.
//
// Settings are cleared selectively - credentials and provider state go, while
// display preferences stay - because wiping the dashboard password out of a
// running install would lock the operator out of the tool they just used to
// ask for the erasure.
func (s *Store) EraseAll(ctx context.Context) (map[string]int64, error) {
	counts := map[string]int64{}
	if s == nil {
		return counts, fmt.Errorf("store.EraseAll: nil store")
	}

	for _, tb := range privacyTables {
		for _, child := range tb.Children {
			if err := s.eraseTable(ctx, child.Name, counts); err != nil {
				return counts, err
			}
		}
		if err := s.eraseTable(ctx, tb.Name, counts); err != nil {
			return counts, err
		}
	}
	for _, name := range identityTables {
		if err := s.eraseTable(ctx, name, counts); err != nil {
			return counts, err
		}
	}

	// Provider credentials and OAuth tokens held in settings.
	for key := range redactedSettingKeys {
		result, err := s.db.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key)
		if err != nil {
			return counts, fmt.Errorf("store.EraseAll: delete setting %s: %w", key, err)
		}
		if n, err := result.RowsAffected(); err == nil && n > 0 {
			counts["settings"] += n
		}
	}

	return counts, nil
}

func (s *Store) eraseTable(ctx context.Context, table string, counts map[string]int64) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM "+table)
	if err != nil {
		return fmt.Errorf("store.EraseAll: clear %s: %w", table, err)
	}
	if n, err := result.RowsAffected(); err == nil && n > 0 {
		counts[table] += n
	}
	return nil
}

// KnownEraseProviders lists the provider keys EraseProvider accepts.
func KnownEraseProviders() []string {
	seen := map[string]bool{}
	var names []string
	for _, tb := range privacyTables {
		if tb.Provider != "" && !seen[tb.Provider] {
			seen[tb.Provider] = true
			names = append(names, tb.Provider)
		}
	}
	sort.Strings(names)
	return names
}

// EraseProvider removes everything stored for one provider, leaving the others
// untouched. This is the proportionate answer to "stop processing my Grok
// account" and to DPDP s.8(7), where withdrawing consent for one purpose
// requires erasing the data collected for it.
//
// The provider name is resolved against the registry and never reaches SQL.
func (s *Store) EraseProvider(ctx context.Context, provider string) (map[string]int64, error) {
	counts := map[string]int64{}
	if s == nil {
		return counts, fmt.Errorf("store.EraseProvider: nil store")
	}

	// Resolve the name against the registry before touching anything. Without
	// this, an empty string would match every cross-provider table - the ones
	// carrying Provider: "" - and a targeted erasure would wipe sessions,
	// notification_log and provider_accounts for every provider at once.
	known := false
	for _, name := range KnownEraseProviders() {
		if name == provider {
			known = true
			break
		}
	}
	if !known {
		return counts, fmt.Errorf("store.EraseProvider: unknown provider %q (known: %s)",
			provider, strings.Join(KnownEraseProviders(), ", "))
	}

	matched := false
	for _, tb := range privacyTables {
		if tb.Provider != provider {
			continue
		}
		matched = true
		for _, child := range tb.Children {
			query := fmt.Sprintf("DELETE FROM %s WHERE %s IN (SELECT id FROM %s)", child.Name, child.ParentKey, tb.Name)
			result, err := s.db.ExecContext(ctx, query)
			if err != nil {
				return counts, fmt.Errorf("store.EraseProvider: clear %s: %w", child.Name, err)
			}
			if n, err := result.RowsAffected(); err == nil && n > 0 {
				counts[child.Name] += n
			}
		}
		if err := s.eraseProviderTable(ctx, tb.Name, counts); err != nil {
			return counts, err
		}
	}

	if !matched {
		return counts, fmt.Errorf("store.EraseProvider: unknown provider %q (known: %s)",
			provider, strings.Join(KnownEraseProviders(), ", "))
	}

	// Cross-provider rows that name this provider.
	for _, table := range []string{"notification_log", "system_alerts", "provider_accounts", "sessions"} {
		result, err := s.db.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE provider = ?", table), provider)
		if err != nil {
			return counts, fmt.Errorf("store.EraseProvider: clear %s: %w", table, err)
		}
		if n, err := result.RowsAffected(); err == nil && n > 0 {
			counts[table] += n
		}
	}

	return counts, nil
}

func (s *Store) eraseProviderTable(ctx context.Context, table string, counts map[string]int64) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM "+table)
	if err != nil {
		return fmt.Errorf("store.EraseProvider: clear %s: %w", table, err)
	}
	if n, err := result.RowsAffected(); err == nil && n > 0 {
		counts[table] += n
	}
	return nil
}
