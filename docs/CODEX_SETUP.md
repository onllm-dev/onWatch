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
3. During runtime, keep checking auth state and refresh token usage automatically when credentials change.

This is aligned with the Anthropic provider behavior: explicit env token first, local auth-state detection as fallback, and runtime refresh for credential changes.

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

## See Also

- [README](../README.md) — Quick start and provider overview
- [Development Guide](DEVELOPMENT.md) — Build and internals
