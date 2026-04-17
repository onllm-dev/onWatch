# Custom API Integrations Setup Guide

Track Custom API Integrations usage in onWatch with local JSONL ingestion.

Ingest local JSONL files to monitor Custom API Integrations usage in onWatch. This is not for subscription or quota tracking, but for logging token usage in custom scripts and programs that make API calls. Wrap your integrations with telemetry to record per-call token usage, cost, and latency data, and track everything in onWatch.

## Prerequisites

- onWatch with the Custom API Integrations backend enabled
- A script or automation that already calls a supported provider API
- Ability to write a JSONL file locally

Supported v1 providers:

- `anthropic`
- `openai`
- `mistral`
- `openrouter`
- `gemini`

*This list is just getting started... feel free to add more providers as you need them!*

## How It Works

1. Your script calls the provider API.
2. Your script reads the usage fields from the API response.
3. Your script appends one normalised JSON object per line to a file in `~/.onwatch/api-integrations/`.
4. onWatch tails `*.jsonl` files in that directory and stores the events in SQLite.

The source files are just the ingest input. The canonical persisted data lives in `~/.onwatch/data/onwatch.db`.

## Default Paths

- API Integrations directory: `~/.onwatch/api-integrations`
- Database: `~/.onwatch/data/onwatch.db`
- Log file: `~/.onwatch/data/.onwatch.log`

In containers, the default API Integrations directory is `/data/api-integrations`.

## Configuration

Custom API Integrations ingestion is enabled by default.

Optional environment variables:

```env
ONWATCH_API_INTEGRATIONS_ENABLED=true
ONWATCH_API_INTEGRATIONS_DIR=~/.onwatch/api-integrations
ONWATCH_API_INTEGRATIONS_RETENTION=1440h
```

If you change `ONWATCH_API_INTEGRATIONS_DIR`, point your scripts and onWatch at the same directory.

Retention notes:

- `ONWATCH_API_INTEGRATIONS_RETENTION` controls how long ingested API Integrations events are kept in SQLite
- default retention is `1440h` which is 60 days
- set `ONWATCH_API_INTEGRATIONS_RETENTION=0` to disable database pruning
- pruning applies only to the SQLite table, not to the source `.jsonl` files

## Event Format

Write one JSON object per line.

Required fields:

```json
{
  "ts": "2026-04-03T12:00:00Z",
  "integration": "notes-organiser",
  "provider": "anthropic",
  "model": "claude-3-7-sonnet",
  "prompt_tokens": 1200,
  "completion_tokens": 300
}
```

Optional fields:

- `total_tokens`
- `cost_usd`
- `latency_ms`
- `account`
- `request_id`
- `metadata`

Full example:

```json
{
  "ts": "2026-04-03T12:00:00Z",
  "integration": "notes-organiser",
  "provider": "anthropic",
  "account": "personal",
  "model": "claude-3-7-sonnet",
  "request_id": "req_123",
  "prompt_tokens": 1200,
  "completion_tokens": 300,
  "total_tokens": 1500,
  "cost_usd": 0.0123,
  "latency_ms": 1840,
  "metadata": {
    "task": "weekly-meeting-notes"
  }
}
```

Notes:

- `ts` must be RFC3339 in UTC, for example `2026-04-03T12:00:00Z`
- `provider` must be one of the supported v1 provider names
- `metadata` must be a JSON object if present
- If `account` is omitted, onWatch stores it as `default`
- If `total_tokens` is omitted, onWatch computes `prompt_tokens + completion_tokens`

## Python Examples

Python-first examples are included here:

- `examples/api_integrations/python/onwatch_api_integrations.py`
- `examples/api_integrations/python/anthropic_example.py`
- `examples/api_integrations/python/openai_example.py`
- `examples/api_integrations/python/mistral_example.py`
- `examples/api_integrations/python/openrouter_example.py`
- `examples/api_integrations/python/gemini_example.py`

The helper in `examples/api_integrations/python/onwatch_api_integrations.py` appends normalised JSONL events to the API Integrations directory.

These are initial examples to show the general use-case, but the logic can be expanded to any API-driven custom integration you want.

Included example utilities currently include:

- `examples/api_integrations/python/generate_practice_dataset.py`

You can also build your own wrapper around the helper and write any number of integration-specific JSONL files, as long as they end in `.jsonl`.

## Dashboard and API

Once events are being ingested, open the `API Integrations` tab in the dashboard.

The dashboard shows:

- per-integration cards with request counts, token totals, providers, and optional cost
- all-time and recent usage insight panels
- a shared usage chart with metric modes for tokens per call, API calls, accumulated tokens, and cost
- ingest health, tailed files, and recent alerts

API Integrations can also be queried through the read-only backend API:

- `GET /api/api-integrations/current`
- `GET /api/api-integrations/history?range=6h`
- `GET /api/api-integrations/health`

Dashboard visibility is controlled through the normal settings API via `api_integrations_visibility`, but ingestion itself is controlled by `ONWATCH_API_INTEGRATIONS_ENABLED`.

## Start onWatch

Foreground mode is easiest for first-time verification:

```bash
onwatch --debug
```

You should see a log line showing that the API integrations ingester started.

## Verify File Output

Check that your script is writing JSONL events:

```bash
ls -la ~/.onwatch/api-integrations
tail -n 20 ~/.onwatch/api-integrations/*.jsonl
```

Each API call should append one valid JSON line.

## Verify Database Ingestion

Check recently ingested API integrations usage events:

```bash
sqlite3 ~/.onwatch/data/onwatch.db "select integration_name, provider, account_name, model, prompt_tokens, completion_tokens, total_tokens, captured_at from api_integration_usage_events order by id desc limit 20;"
```

Check ingest cursor state:

```bash
sqlite3 ~/.onwatch/data/onwatch.db "select source_path, offset_bytes, file_size, partial_line from api_integration_ingest_state;"
```

Expected result:

- `api_integration_usage_events` contains one row per ingested event
- `api_integration_ingest_state` contains one row per tailed file
- `offset_bytes` increases as the file grows

## Troubleshooting

### No rows appear in `api_integration_usage_events`

Check:

- onWatch is running
- `ONWATCH_API_INTEGRATIONS_ENABLED` is not set to `false`
- your script writes into the same directory that onWatch is tailing
- the file name ends with `.jsonl`
- each line is valid JSON

Run:

```bash
tail -f ~/.onwatch/data/.onwatch.log
```

### Invalid lines are skipped

onWatch skips malformed or schema-invalid lines and creates a system alert instead of stopping ingestion.

Check recent alerts:

```bash
sqlite3 ~/.onwatch/data/onwatch.db "select provider, alert_type, title, message, created_at from system_alerts where provider = 'api_integrations' order by id desc limit 20;"
```

### Duplicate rows

onWatch deduplicates ingested events using a derived fingerprint based on the source path and stable event fields.

This protects against:

- daemon restart
- file reread after truncation
- repeated scans of the same already-ingested lines

If you intentionally want two events, they must differ in at least one meaningful field such as timestamp or request id.

### Rotating source files

If you want to start a fresh source log for new events, move or rename the active `.jsonl` file and let your wrapper create a new one.

Notes:

- onWatch will treat the new file as a new ingest source
- previously ingested history remains in SQLite until you clear or replace the stored database
- rotating the source file changes future ingestion, but it does not erase existing chart history by itself

## Backend Storage

Custom API Integrations data is stored in separate SQLite tables from the existing subscription/quota tracking tables:

- `api_integration_usage_events`
- `api_integration_ingest_state`

This means Custom API Integrations telemetry is identifiable and queryable independently from provider quota snapshots and reset cycles.

Database retention behavior:

- onWatch automatically prunes old rows from `api_integration_usage_events`
- the pruning cutoff is controlled by `ONWATCH_API_INTEGRATIONS_RETENTION`
- the default is 60 days
- source `.jsonl` files are not pruned or compacted by onWatch
- if you want smaller source logs, rotate or remove the JSONL files manually
