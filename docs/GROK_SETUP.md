# Grok Setup Guide

Track Grok (xAI) credits usage in onWatch.

Grok (SuperGrok / Grok Build CLI) does not expose a simple public REST quota API for consumer plans. onWatch reads usage from the same sources used by local tooling:

- `~/.grok/auth.json` (or `$GROK_HOME/auth.json`) — primary for identity + bearer
- `grok agent stdio` ACP JSON-RPC `x.ai/billing` (best-effort; frequently "method not found" in current grok builds)
- grok.com gRPC-web billing probe using the bearer from auth.json (most reliable for the daemon)
- Local `~/.grok/sessions/**/signals.json` (informational token/session stats only)

---

## Prerequisites

- Run `grok login` (install the official `grok` CLI from https://x.ai if needed)
- `~/.grok/auth.json` exists with a valid `key` (bearer) entry
- onWatch installed

Verify:

```bash
ls -l ~/.grok/auth.json
# or with custom home
ls -l "$GROK_HOME/auth.json"
```

---

## Configuration

Add to `~/.onwatch/.env` (or project `.env`):

```bash
# Explicit bearer (recommended for Docker / headless). Optional if auth.json is present.
GROK_TOKEN=your_bearer_from_auth_json

# Optional: force enable/disable (default: auto when token or auth file present)
GROK_ENABLED=true

# Optional: custom Grok home (defaults to ~/.grok)
GROK_HOME=/path/to/grok/home
```

- If another provider is already configured, onWatch will auto-detect the grok auth file at startup and during runtime (token rotation from `grok login` is picked up).
- For Docker-only / no file: set `GROK_TOKEN` (mount the auth dir if you prefer the file path).
- Set `GROK_ENABLED=false` to disable even if a file is present.

---

## How Detection Works

1. `GROK_TOKEN` from environment (highest precedence for explicit/Docker).
2. `~/.grok/auth.json` (or `$GROK_HOME/auth.json`) — onWatch prefers the OIDC/SuperGrok scope (`https://auth.x.ai::...`) and falls back to legacy session scope.
3. Runtime re-detection on every poll (like Codex / Cursor).

The `grok` CLI itself manages refresh/rotation of tokens stored in auth.json. onWatch does not write the file.

---

## Dashboard

After starting onWatch you will see a **Grok** tab (if enabled). It shows:

- Credits utilization bar (primary window against your plan limit)
- Live countdown to the billing period end (resetsAt)
- Historical chart, cycle overview, sessions, and insights
- Identity (email + team) from the auth file

The quota label is "Credits" (with dynamic Weekly/Monthly hints in the UI when the cycle length matches common periods).

---

## Limitations & Notes

- RPC (`grok agent stdio`) path is best-effort. Many grok builds only wire billing for the interactive TUI; onWatch silently falls back to the web probe.
- Browser cookie import (used by some desktop tools) is not performed by the onWatch daemon for cross-platform and privacy reasons. The bearer from `grok login` is sufficient for the gRPC-web path.
- Local session signals (`~/.grok/sessions`) are aggregated for the last 30 days and attached as supplemental info only (they do not drive the main quota cards).
- Status page link points to https://status.x.ai (no public feed yet).

---

## Troubleshooting

- "unauthorized" or empty data: re-run `grok login`, confirm `key` exists and is not expired in auth.json, restart onWatch.
- No Grok tab: check `GROK_ENABLED` / presence of token or auth file in logs (`--debug`).
- Docker: the container has no `grok` binary; rely on `GROK_TOKEN` (or bind-mount the auth dir and set `GROK_HOME`).
- High poll interval or rate behavior: the web probe is lightweight; RPC spawns a short-lived process (guarded by timeouts + kill).

See also the main README for environment variable reference and dashboard features.
