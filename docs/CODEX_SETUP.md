# Codex Setup Guide

Track Codex quota usage in onWatch (`v2.11.12`).

---

## Prerequisites

- Codex account access with a valid OAuth auth state
- Codex auth file present at `~/.codex/auth.json` (or `$CODEX_HOME/auth.json`)
- onWatch installed ([Quick Start](../README.md#quick-start))

---

## Step 1: Confirm Codex Auth File Exists

macOS / Linux:

```bash
ls -la ~/.codex/auth.json
```

If you use a custom Codex home:

```bash
ls -la "$CODEX_HOME/auth.json"
```

Windows (PowerShell):

```powershell
Get-Item "$env:USERPROFILE\.codex\auth.json"
```

---

## Step 2: Get the Access Token

macOS / Linux (default path):

```bash
python3 -c "import json,os; p=os.path.expanduser('~/.codex/auth.json'); print(json.load(open(p))['tokens']['access_token'])"
```

Custom `CODEX_HOME`:

```bash
python3 -c "import json,os; p=os.path.join(os.environ['CODEX_HOME'],'auth.json'); print(json.load(open(p))['tokens']['access_token'])"
```

Windows (PowerShell):

```powershell
(Get-Content "$env:USERPROFILE\.codex\auth.json" | ConvertFrom-Json).tokens.access_token
```

---

## Step 3: Configure onWatch

Add token to `.env`:

```bash
cd ~/.onwatch
```

Set:

```bash
CODEX_TOKEN=your_codex_oauth_access_token
```

Notes:
- If Codex is your only provider, `CODEX_TOKEN` must be set so startup validation passes.
- If another provider is already configured, onWatch can auto-detect Codex auth when `CODEX_TOKEN` is omitted.
- While running, onWatch re-reads Codex credentials and can pick up token rotation from `auth.json`.

---

## How Codex Auth Resolution Works

onWatch follows this order:

1. Use `CODEX_TOKEN` from `.env` (or environment) if set.
2. If missing, try Codex auth state from:
   - `CODEX_HOME/auth.json` (when `CODEX_HOME` is set)
   - `~/.codex/auth.json` (default)
3. If still missing, try the opencode-codex auth state (see below).
4. During runtime, keep checking auth state and refresh token usage automatically when credentials change.

This is aligned with the Anthropic provider behavior: explicit env token first, local auth-state detection as fallback, and runtime refresh for credential changes.

---

## OpenCode (opencode-codex) ChatGPT Login

If you sign in to ChatGPT through OpenCode (the opencode-codex auth flow) instead of the Codex CLI, onWatch can track the same ChatGPT quota - it feeds the existing Codex provider (same backend, same dashboard card).

OpenCode stores its credentials at `~/.local/share/opencode/auth.json` in a different shape than Codex:

```json
{ "openai": { "type": "oauth", "access": "...", "refresh": "...", "expires": 1234567890123, "accountId": "..." } }
```

`expires` is a Unix timestamp in milliseconds. onWatch reads, refreshes, and writes back this file in its native format, so your OpenCode login keeps working (one-time-use refresh tokens are preserved).

### Enabling

Pick whichever fits your workflow:

- **Setup wizard**: run `onwatch setup` (or the installer) and choose "OpenCode (opencode-codex)".
- **Env flag**: set `OPENCODE_ENABLED=true` in `.env`. This enables the Codex provider using the opencode-codex credentials, even when no `CODEX_TOKEN` is set.
- **Dashboard**: persist `provider_settings.opencode.enabled = true` via the settings API.

### Paths and overrides

onWatch resolves the opencode-codex auth file in this order:

1. `OPENCODE_HOME/auth.json` (when `OPENCODE_HOME` is set)
2. `XDG_DATA_HOME/opencode/auth.json` (when `XDG_DATA_HOME` is set)
3. `~/.local/share/opencode/auth.json` (default)

When both Codex and opencode-codex credentials exist, Codex takes priority. To track several ChatGPT accounts at once, use Codex profiles (below) - they work with opencode-codex credentials too.

---

## Installation Scenarios

### Codex-only install

Set `CODEX_TOKEN` in `.env`, then start onWatch.

### Multi-provider install

If another provider key is already set, Codex can be enabled via auth-state auto-detection even without `CODEX_TOKEN`.

### Custom Codex home

Set `CODEX_HOME` to your custom Codex directory so onWatch reads `CODEX_HOME/auth.json`.

---

## Multi-Account Support (v2.11.12+)

Track multiple ChatGPT/Codex accounts simultaneously. Each account's quota data is stored and displayed separately.

### Save a Profile

```bash
onwatch codex profile save <profile-name>
```

This saves credentials from your current `~/.codex/auth.json` as a named profile in the onWatch data directory (`~/.onwatch/data/codex-profiles/<profile-name>.json`).

**First profile behavior:** When you save your first profile, onWatch renames the existing "default" account to your profile name, preserving all historical data.

**Duplicate prevention:** Each ChatGPT account can only have one profile. If you try to save a second profile for the same account, onWatch will error and suggest using `codex profile refresh` instead.

### Example: Adding Multiple Accounts

```bash
# Log into first account in Codex CLI, then save
onwatch codex profile save work-account

# Log into second account, then save
onwatch codex profile save personal-account
```

### Refresh Profile Credentials

To update credentials for an existing profile (e.g., after re-authenticating):

```bash
onwatch codex profile refresh <profile-name>
```

### Dashboard Usage

When multiple profiles exist:
- **Profile tabs** appear in the header next to provider tabs
- Click a profile tab to switch accounts
- All data (quotas, charts, cycles, logging history) updates for the selected account
- In **All** view, cards for each Codex account are shown with account name headers
- Deleted profiles are hidden from the dashboard by default

### Settings

In **Settings -> Providers**, each Codex account has its own toggle row:
- **Active profiles** - telemetry and dashboard toggles work normally
- **Deleted profiles** - shown with a "Deleted" badge, telemetry toggle disabled (credentials removed), dashboard toggle available for viewing historical data

### List Profiles

```bash
onwatch codex profile list
```

### Remove a Profile

```bash
onwatch codex profile delete <profile-name>
```

Deleting a profile removes credentials and stops polling. Historical telemetry data is preserved in the database and can be viewed by enabling the dashboard toggle in settings.

### How It Works

- Profiles are stored as JSON files in the data directory (`~/.onwatch/data/codex-profiles/`)
- In Docker, profiles are stored under `/data/codex-profiles/` (same volume as the database)
- Existing profiles at the legacy path (`~/.onwatch/codex-profiles/`) are auto-migrated on startup
- Each ChatGPT account is identified by its account ID (from the API/JWT) - this is the real identity, not the profile name
- Duplicate profiles for the same account are automatically deduplicated on startup (telemetry data merged, never deleted)
- Each profile gets its own polling agent
- Data is stored with account-specific IDs in SQLite
- Historical data is preserved per account

---

## Step 4: Restart onWatch

```bash
onwatch stop
onwatch
```

Or run in foreground:

```bash
onwatch --debug
```

---

## Step 5: Verify in Dashboard

Open `http://localhost:9211` and select the **Codex** tab.

You should see:
- **LLMs** utilization (rolling limit quota)
- **Review Requests** (code review quota)
- Reset timers, usage history, and projections
- Profile tabs (when multiple accounts are configured)

---

## Troubleshooting

### "No provider data appears in dashboard"

onWatch now starts even when no providers are configured.

To enable Codex tracking:
- Set `CODEX_TOKEN` in your `.env`, or use Codex auto-detection
- Open **Settings -> Providers**
- Enable Codex telemetry and dashboard visibility

### "Codex polling paused due to repeated auth failures"

Refresh your Codex login so `auth.json` has a new access token, then restart onWatch.

### Token security

- Keep `.env` out of version control
- onWatch only sends the token to Codex usage endpoints
- Usage history stays local in SQLite

---

## Auto quota-starter (Beta)

Codex limit windows only begin counting once you send your first message after a
reset. If you do not use Codex right after a reset, the fresh window (and its
reserve) sits unstarted. The auto quota-starter has onWatch send a tiny Codex
request to start the window for you.

There are two independent toggles, one per window:

- **Auto-start 5h window** — starts the 5-hour window when it is found unstarted.
- **Auto-start weekly window** — starts the 7-day window when it is found unstarted.

Both default to **Off**. Enable them in **Settings -> Providers -> Codex**.
Changes take effect without a daemon restart. Each configured Codex account
starts its own window.

How it works:

- **Detection.** An unstarted window reports its reset time as `now + full
  window`, so the countdown stays pinned at ~the full length (e.g. a 5h window
  shows ~4h59m and does not tick down). Once a turn is sent, the reset time
  becomes fixed and the countdown starts decreasing. After each poll onWatch
  checks the freshly polled reset time: if a window still looks unstarted and its
  toggle is on, it fires a starter.
- **The request.** A minimal call to the ChatGPT-backed Codex Responses endpoint
  (`/backend-api/codex/responses`) using your existing OAuth token, asking the
  model to reply with the short string `Quota Resumed`.
- **Why a full streamed turn (not a tiny request).** The window only anchors when
  a turn actually *completes* server-side. An earlier approach fired the request
  but read only the first bytes of the streamed response and closed the
  connection - that cancels the turn before `response.completed`, so the backend
  never counts it and the window keeps showing the full reset time. onWatch now
  reads the SSE stream to completion (through `response.completed`) so the turn
  commits and the window starts. The extra read costs only the few output tokens
  of "Quota Resumed" (see Cost below).
- **Rate cap.** Re-evaluated every poll (so a failed start retries at your
  polling cadence), but hard-capped to **5 pings per rolling 4 hours per window**.
  A successful start makes the window no longer look unstarted, so it normally
  fires once. The cap is a backstop against loops/failed starts.
- Failures are logged (`~/.onwatch/data/.onwatch.log`) and never retried beyond
  the cap.

### Cost

Each starter ping is one tiny model turn. Measured against gpt-5.5:

| Item | Tokens |
|------|--------|
| Input | ~44 |
| Output (incl. ~9 reasoning) | ~18 |
| **Total per ping** | **~62** |

So the quota/token cost is negligible:

- **Typical:** one ping per window start - up to ~5/day for the 5h window (only
  when it would otherwise sit unstarted) and ~1/week for the weekly window. That
  is roughly **~300 tokens/day** at most for the 5h window.
- **Worst case** (a start that never "takes" and keeps hitting the rate cap):
  5 pings per 4h per window, i.e. ~30/day for the 5h window - still under
  ~2,000 tokens/day.
- Against the tracked quota itself, a single start registers as **less than 1%**
  of the window.

Environment overrides (the dashboard toggles take precedence at runtime):

- `CODEX_AUTO_START_5H=true` / `CODEX_AUTO_START_7D=true` - default-on without the UI.
- `CODEX_STARTER_MODEL` - override the model used for the starter request (default
  `gpt-5.5`). ChatGPT-account Codex access supports only a small set of models
  (currently `gpt-5.5`, `gpt-5.4`, `gpt-5.4-mini`); set this if the default is
  rejected.

> Beta: the Codex Responses request shape can change upstream. If starter pings
> fail, check the logs and try a different `CODEX_STARTER_MODEL`.

---

## See Also

- [README](../README.md) — Quick start and provider overview
- [Development Guide](DEVELOPMENT.md) — Build and internals
