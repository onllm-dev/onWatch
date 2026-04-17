# Prometheus Metrics

onWatch exposes a Prometheus-compatible `/metrics` endpoint so quota, credit, and agent-health data can be scraped into Prometheus, Grafana, and Alertmanager alongside your other observability data.

> Status: Beta. Metric names and labels may evolve based on feedback before 1.0. Please open issues or PRs against `onllm-dev/onWatch` with suggestions.

## Migration notes (pre-1.0)

If you deployed onWatch 2.11.40's beta metrics, the following changed in the 1.0 correctness pass. Update dashboards/alerts before upgrading.

| Removed/renamed | Replacement |
|---|---|
| `onwatch_quota_time_until_reset_seconds` | `onwatch_quota_reset_timestamp_seconds` (absolute Unix seconds; compute remaining as `metric - time()`) |
| `onwatch_auth_token_status` | `onwatch_agent_healthy` (honest name; same `1`/`0` semantics, measures poll freshness, **not** real OAuth validity) |
| `account_id=""` | `account_id="default"` (sentinel for single-account providers) |
| `onwatch_credits_balance{provider,account_id}` | `onwatch_credits_balance{provider,account_id,unit}` (unit disambiguates `usd`, `credits`, `prompt_credits`) |

## Enabling the Endpoint

| Variable | Purpose | Default |
|---|---|---|
| `ONWATCH_METRICS_TOKEN` | Bearer token required on `/metrics` requests | unset (endpoint is open, logs a warning at startup) |

- If `ONWATCH_METRICS_TOKEN` is unset, `/metrics` responds without auth and onWatch emits `WARN metrics endpoint is unauthenticated; set ONWATCH_METRICS_TOKEN to restrict /metrics access` at startup.
- If set, Prometheus must send `Authorization: Bearer <token>` on every scrape.

```bash
export ONWATCH_METRICS_TOKEN="$(openssl rand -hex 32)"
./onwatch --daemon
curl -H "Authorization: Bearer $ONWATCH_METRICS_TOKEN" http://localhost:8080/metrics
```

## Exposed Metrics

Standard Go runtime / process collectors (`go_*`, `process_*`) are also registered.

### Gauges

| Metric | Labels | Description |
|---|---|---|
| `onwatch_quota_utilization_percent` | `provider`, `quota_type`, `account_id` | Current quota utilization as a percentage (0-100). |
| `onwatch_quota_reset_timestamp_seconds` | `provider`, `quota_type`, `account_id` | Unix timestamp (seconds) at which the quota next resets. Compute remaining: `metric - time()`. Series is omitted when no reset is scheduled. |
| `onwatch_credits_balance` | `provider`, `account_id`, `unit` | Remaining credit balance. `unit` is `usd` (OpenRouter), `credits` (Codex), or `prompt_credits` (Antigravity). |
| `onwatch_agent_healthy` | `provider`, `account_id` | `1` if the polling agent has recent successful data (within `2 * pollInterval`), `0` if stale. Reflects **poll freshness**, not real OAuth validity. Series is omitted until the provider has produced at least one snapshot, which prevents startup false-positives. |
| `onwatch_agent_last_cycle_age_seconds` | `provider`, `account_id` | Seconds since the last successful poll cycle. Companion to `onwatch_agent_healthy`. |
| `onwatch_build_info` | `version`, `go_version`, `commit` | Always `1`. Use for pinning alerts to a specific release. |
| `onwatch_account_info` | `provider`, `account_id`, `account_name` | Join-metric (always `1`) mapping numeric `account_id` to human-readable `account_name`. See "Joining on account_name" below. |
| `onwatch_api_integration_requests` | `integration` | Number of ingested API-integration usage events currently in the local DB, per integration. Not named `_total` because it's a DB snapshot, not an event-stream counter. |
| `onwatch_api_integration_spend_usd` | `integration` | Cumulative USD spend tracked by API-integration ingestion (from the local DB). |

### Counters

Counters live outside the per-scrape reset path, so `rate()` / `increase()` queries work correctly.

| Metric | Labels | Description |
|---|---|---|
| `onwatch_cycles_completed_total` | `provider`, `account_id` | Successful poll cycles. Use `rate(...)` for polling activity. |
| `onwatch_cycles_failed_total` | `provider`, `account_id`, `reason` | Failed poll cycles, labelled by reason. |
| `onwatch_scrape_errors_total` | `provider`, `error_type` | Errors while refreshing `/metrics` from the local store. Alert on `rate(...)` to detect broken metric collection itself. |

> Note: in 2.11.40 only the built-in `synthetic` agent emits `cycles_completed_total` / `cycles_failed_total`. Per-provider wiring for the remaining 8 agents is a follow-up; alerts that depend on these counters should gate on `absent_over_time(onwatch_cycles_completed_total{provider="..."}[1h])`.

### Label semantics

- `provider` - `anthropic`, `codex`, `copilot`, `zai`, `minimax`, `antigravity`, `gemini`, `openrouter`, `api_integrations`.
- `quota_type` - provider-specific quota identifier. For Gemini, Antigravity, and MiniMax this is the model ID (`gemini-2.5-pro`, etc.) so **cardinality grows as new models appear**; configure Prometheus retention accordingly.
- `account_id` - numeric account ID for multi-account providers (Codex, MiniMax); `"default"` for single-account providers.
- `account_name` - human-readable account name from `onwatch_account_info` (join-metric).
- `unit` - on `onwatch_credits_balance` only: `usd` | `credits` | `prompt_credits`.

## Example PromQL

**Minutes until quota reset:**
```promql
(onwatch_quota_reset_timestamp_seconds - time()) / 60
```

**Join numeric account_id with account_name for Grafana:**
```promql
onwatch_quota_utilization_percent * on(provider, account_id) group_left(account_name) onwatch_account_info
```

**Scrape-error rate:**
```promql
rate(onwatch_scrape_errors_total[5m])
```

## Example Scrape Config

```yaml
# prometheus.yml
scrape_configs:
  - job_name: onwatch
    metrics_path: /metrics
    scheme: http
    static_configs:
      - targets: ["onwatch.internal:8080"]
    authorization:
      type: Bearer
      credentials_file: /etc/prometheus/onwatch_token
```

## Example Alert Rules

```yaml
groups:
  - name: onwatch
    rules:
      # Suppress for 10m after process start to avoid startup false-positives.
      - alert: OnwatchAgentStale
        expr: |
          (onwatch_agent_healthy == 0)
          and on() (time() - process_start_time_seconds{job="onwatch"} > 600)
        for: 10m
        labels:
          severity: warning
        annotations:
          summary: "onWatch {{ $labels.provider }}/{{ $labels.account_id }} stale"
          description: "No successful poll in over 2x the poll interval. Common cause: expired OAuth refresh token."

      - alert: OnwatchQuotaNearLimit
        expr: onwatch_quota_utilization_percent >= 90
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "{{ $labels.provider }} quota {{ $labels.quota_type }} at {{ $value | printf \"%.0f\" }}%"

      - alert: OnwatchQuotaExhausted
        expr: onwatch_quota_utilization_percent >= 99
        for: 1m
        labels:
          severity: critical

      - alert: OnwatchMetricsCollectionBroken
        expr: rate(onwatch_scrape_errors_total[10m]) > 0
        for: 10m
        labels:
          severity: warning
        annotations:
          summary: "onWatch cannot refresh metrics from its own store"

      - alert: OnwatchQuotaResetMissed
        # Fires if a reset timestamp in the past persists, suggesting the agent
        # didn't pick up the new cycle.
        expr: (onwatch_quota_reset_timestamp_seconds - time()) < -900
        for: 10m
```

## Notes & limitations

- Metrics are refreshed on every scrape from the SQLite store. At typical 30-60s scrape intervals the cost is negligible.
- Most metrics are gauges that `Reset()` each scrape, so series for a provider disappear if it becomes unconfigured. Counters (`onwatch_cycles_*_total`, `onwatch_scrape_errors_total`) are preserved across scrapes.
- `onwatch_agent_healthy` reflects poll freshness, not real OAuth validity. A transient network blip or onWatch restart will flip it to 0. For true OAuth-expiry alerting, watch logs or the `/api/*/health` dashboard endpoints.
- `account_id` is a numeric ID. Use `onwatch_account_info` for Grafana panels that need human-readable labels.

## Related

- README: [Prometheus metrics endpoint (Beta)](../README.md)
- Issue thread: [onllm-dev/onWatch#61](https://github.com/onllm-dev/onWatch/issues/61)
