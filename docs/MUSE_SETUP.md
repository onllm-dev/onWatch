# Muse Setup Guide

Track your **Meta Muse coding-plan** quota (5-hour prompts + weekly usage) in onWatch.

Meta publishes no aggregate usage or billing endpoint for the coding plan, so onWatch reads the same subscription snapshot the `muse` CLI `/usage` command shows: each poll sends one minimal streamed probe (`POST https://api.meta.ai/v1/responses` with `stream: true`, input `"ping"`, `max_output_tokens: 16`) and extracts the inline `subscription` object. The probe is a few tokens and never touches your files.

---

## Prerequisites

- The [`muse` CLI](https://github.com/meta) installed and logged in (`muse login`), **or** a Meta API key
- onWatch installed ([Quick Start](../README.md#quick-start))

---

## How It Works

The probe response carries a Server-Sent Events stream. onWatch scans its `data:` lines and keeps the last event containing a `subscription` snapshot:

```json
{"subscription":{
  "tier":"pro",
  "weekly":{"resets_at":"1789344000","used_percent":"12.5"},
  "window":{"resets_at":"1789078632","used_percent":"34","window_duration_mins":"300"}}}
```

- `window` is the rolling prompt window (usually 300 minutes = 5 hours).
- `weekly` is the weekly quota.
- `used_percent` values arrive as strings and are clamped to 0-100.
- `resets_at` is an epoch timestamp in seconds.
- `tier` is an opaque account tier ID; onWatch stores it but only displays it when it is human-readable.

Both windows are tracked as percent quotas (`window_5h`, `weekly`) with reset-cycle detection, burn-rate insights, history, and notifications, like every other provider. Snapshots are stored locally in SQLite.

---

## 1. Log in to Muse

```bash
muse login
```

`META_API_KEY` always takes priority over the login session when both exist.

---

## 2. Configure onWatch

Use any one of the following.

### Option A - auto-detect (recommended)

No configuration needed. On startup onWatch resolves the key in this order:

1. `META_API_KEY` from the environment / `.env`
2. `providers.meta.api_key` in `~/.config/muse/auth.json` (must not be group/other-readable)
3. System credential store: macOS Keychain (`ai.meta.dev.credentials` / `meta`, written by `muse login`) or the Linux secret-service keyring

The probe model resolves as `META_MUSE_MODEL`, then the `model` in `~/.config/muse/settings.json`, then `muse-spark-1.3`.

To force-enable auto-detect (for example when the key only appears later), set in `~/.onwatch/.env`:

```bash
MUSE_ENABLED=true
```

### Option B - `.env` with explicit key

Add to `~/.onwatch/.env` (best for Docker / headless hosts where no login session exists):

```bash
META_API_KEY=your_meta_api_key
# Optional: pin the probe model
# META_MUSE_MODEL=muse-spark-1.3
```

### Option C - setup wizard

```bash
onwatch setup
```

Choose "Muse (Meta) only" (or add it under "Multiple" / "All available"). When no login is detected the wizard accepts an optional key and verifies it with a single probe.

### Option D - dashboard settings

Open Settings, pick the Muse provider, and save an API key and/or probe model. Changes take effect after daemon restart.

---

## 3. Verify

```bash
# Poll once in the foreground and watch for the Muse agent
onwatch --debug 2>&1 | grep -i muse
```

Then open the dashboard and select the **Muse** tab. You should see the 5h Prompts and Weekly cards with reset countdowns.

---

## Environment variables

| Variable | Purpose |
|---|---|
| `META_API_KEY` | Meta API key. Overrides the `muse login` session. |
| `META_MUSE_MODEL` | Probe model (default: your Muse settings model, else `muse-spark-1.3`). |
| `MUSE_ENABLED` | Set `true` to enable auto-detect, `false` to disable Muse tracking entirely. |
| `MUSE_AUTH_PATH` | Override the Muse login file path (default: `~/.config/muse/auth.json`). |

---

## Cost and polling notes

- Each poll costs exactly one minimal probe response (a `"ping"` input with at most 16 output tokens). At the default 120 s interval that is about 720 tiny probes per day.
- If you want fewer probes, raise `ONWATCH_POLL_INTERVAL` or pause the provider from the dashboard (telemetry toggle); polling and history resume when re-enabled.
- onWatch only sends `GET`-style usage reads plus the probe above. It never writes to your Muse files or keychain entries, and it never logs keys (values are redacted as `...` in logs and config dumps).

---

## Troubleshooting

| Symptom | Fix |
|---|---|
| Muse tab missing | Run `muse login`, or set `META_API_KEY` / `MUSE_ENABLED=true`, then restart the daemon. Check `onwatch --debug` for `Muse API client configured`. |
| Slow start / Muse missing after upgrading the binary | macOS shows a Keychain approval dialog for the new binary. Click **Always Allow**, or set `META_API_KEY` to skip the Keychain lookup (the daemon gives up after 15 s and starts without Muse rather than hanging). |
| `401 invalid_api_key` | The stored login key is stale. Re-run `muse login`, or export a fresh `META_API_KEY`. |
| `429 rate limited` | Back off and let the next poll retry; the daemon logs the event and keeps the last known values. |
| Model errors | Set `META_MUSE_MODEL` to a model your plan can access (for example `muse-spark-1.3`). |
| Windows hosts | No system-credential lookup is available; use `META_API_KEY` (Option B). |
