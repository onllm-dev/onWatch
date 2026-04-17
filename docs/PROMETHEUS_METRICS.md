# Prometheus Metrics

onWatch exposes a Prometheus-compatible `/metrics` endpoint so quota, credit, and agent-health data can be scraped into Prometheus, Grafana, and Alertmanager alongside your other observability data.

> Status: Beta. Metric names and labels may evolve based on feedback before 1.0. Please open issues or PRs against `onllm-dev/onWatch` with suggestions.

## Enabling the Endpoint

The `/metrics` endpoint is always mounted. Authentication is optional and controlled by a single environment variable:

| Variable | Purpose | Default |
|---|---|---|
| `ONWATCH_METRICS_TOKEN` | Bearer token required on `/metrics` requests | unset (endpoint is open) |

- If `ONWATCH_METRICS_TOKEN` is **unset or empty**, `/metrics` responds without auth. This is the simplest setup for single-host or already-network-isolated deployments.
- If set, Prometheus must send `Authorization: Bearer <token>` on every scrape.

Unlike the dashboard (which uses HTTP Basic auth), the metrics endpoint has its own token so your scraper credentials stay separate from the admin credentials.

### Example

```bash
export ONWATCH_METRICS_TOKEN="$(openssl rand -hex 32)"
./onwatch --daemon
```

```bash
# manual verification
curl -H "Authorization: Bearer $ONWATCH_METRICS_TOKEN" http://localhost:8080/metrics
```

## Exposed Metrics

All onWatch-specific metrics share a common label set where applicable: `provider`, `quota_type`, and `account_id`. Standard Go runtime and process collectors (`go_*`, `process_*`) are also registered.

| Metric | Type | Labels | Description |
|---|---|---|---|
| `onwatch_quota_utilization_percent` | Gauge | `provider`, `quota_type`, `account_id` | Current quota utilization as a percentage (0-100). |
| `onwatch_quota_time_until_reset_seconds` | Gauge | `provider`, `quota_type`, `account_id` | Seconds until the quota resets (0 when already reset or unknown). |
| `onwatch_credits_balance` | Gauge | `provider`, `account_id` | Remaining credit balance (Codex credits, Antigravity prompt credits, OpenRouter balance). |
| `onwatch_auth_token_status` | Gauge | `provider`, `account_id` | `1` when the most recent poll succeeded within the stale threshold, `0` when stale or auth failure likely. Useful for "token expired" alerts. |
| `onwatch_agent_last_cycle_age_seconds` | Gauge | `provider`, `account_id` | Seconds since the last successful poll cycle for the provider/account. |

### Label Semantics

- `provider` - one of `anthropic`, `codex`, `copilot`, `zai`, `minimax`, `antigravity`, `gemini`, `openrouter`.
- `quota_type` - provider-specific quota identifier (e.g. `5h_limit`, model name, or `credits`/`tokens`/`time`).
- `account_id` - numeric account ID for multi-account providers (Codex, MiniMax). Empty string for single-account providers.

### Staleness Semantics

`onwatch_auth_token_status` and `onwatch_agent_last_cycle_age_seconds` are the canonical signals for "is onWatch still getting fresh data from the provider". The stale threshold is `2 * pollInterval`. Any cycle older than that flips the status to `0` - which usually indicates one of:

- OAuth/refresh token expired (the common Codex case).
- Provider API is down or rate-limiting persistently.
- onWatch was offline or backgrounded.

Alert on `onwatch_auth_token_status == 0` for fast detection.

## Example Prometheus Scrape Config

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

Write the token to `/etc/prometheus/onwatch_token` and ensure it is mode `0600`.

### Kubernetes (ServiceMonitor)

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: onwatch
  namespace: monitoring
spec:
  selector:
    matchLabels:
      app: onwatch
  endpoints:
    - port: http
      path: /metrics
      interval: 60s
      authorization:
        type: Bearer
        credentials:
          name: onwatch-metrics
          key: token
```

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: onwatch-metrics
  namespace: monitoring
type: Opaque
stringData:
  token: "<same value as ONWATCH_METRICS_TOKEN>"
```

## Example Alertmanager Rules

```yaml
groups:
  - name: onwatch
    rules:
      - alert: OnwatchProviderStale
        expr: onwatch_auth_token_status == 0
        for: 10m
        labels:
          severity: warning
        annotations:
          summary: "onWatch provider {{ $labels.provider }} has no fresh data"
          description: "{{ $labels.provider }}/{{ $labels.account_id }} has not produced a successful poll cycle in over 2x the poll interval. Likely an expired token."

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
        annotations:
          summary: "{{ $labels.provider }} quota {{ $labels.quota_type }} exhausted"
```

## Grafana

Any dashboard built on top of these gauges works - starting points:

- **Utilization heatmap**: `onwatch_quota_utilization_percent` grouped by `provider` + `quota_type`.
- **Time-to-reset countdown**: `onwatch_quota_time_until_reset_seconds` as a stat panel with `s` unit.
- **Credit balance trend**: `onwatch_credits_balance` as a time series.
- **Provider health matrix**: `onwatch_auth_token_status` as a state timeline.

## Notes & Limitations

- Metrics are regenerated on every scrape by querying the SQLite store. This keeps the gauges consistent with the dashboard and avoids double-counting but means scrape cost grows with the number of configured providers/accounts. At typical `scrape_interval` (30s-60s) this is negligible.
- All values use `prometheus.GaugeVec.Reset()` per scrape - metrics for a provider disappear if the provider becomes entirely unconfigured.
- The endpoint does not emit histograms or counters; all onWatch-specific series are gauges.
- `account_id` is a numeric ID, not a human-readable name. Cross-reference with the dashboard's Accounts view.

## Related

- README: [Prometheus metrics endpoint (Beta)](../README.md)
- Issue thread: [onllm-dev/onWatch#61](https://github.com/onllm-dev/onWatch/issues/61)
