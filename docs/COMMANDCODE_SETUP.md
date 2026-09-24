# Command Code Setup Guide

Track your **Command Code** credit balance, 5-hour and weekly rate-limit windows, and billing-period usage in onWatch.

Command Code (commandcode.ai) publishes an alpha JSON API that the `cmd` CLI itself uses - the same endpoints its `/usage` display reads. onWatch polls four read-only `GET` endpoints with the same API key, so **polling costs no credits**:

| Endpoint | What it returns |
|---|---|
| `/alpha/whoami` | Account login, user id, and the organisation id that scopes the billing lookups |
| `/alpha/billing/credits` | Credit balances (`monthlyCredits`, `purchasedCredits`, `freeCredits`) plus the `fiveHour` and `weekly` rate-limit windows with reset times |
| `/alpha/billing/subscriptions` | Plan id, subscription status, and the current billing-period bounds |
| `/alpha/usage/summary` | Billed cost, request count, and token totals for the billing period |

---

## Prerequisites

- A Command Code account with a plan that includes API access
- The `cmd` CLI installed and logged in (`cmd login`), **or** a `user_...` API key
- onWatch installed ([Quick Start](../README.md#quick-start))

---

## What Gets Tracked

Three quota windows, each with its own reset-cycle history, burn-rate insight, and notifications:

| Card | What it is | Reset |
|---|---|---|
| **5-Hour Credits** | Rolling five-hour rate-limit window (`used / cap` credits) | When the API reports a new `resetAt` |
| **Weekly Credits** | Weekly rate-limit window (`used / cap` credits) | Weekly `resetAt` |
| **Monthly Credits** | Billing-period credit balance | End of the subscription period |

The monthly card reconstructs the credits granted for the period as *remaining + billed*: 50.99 remaining plus 18.99 billed means 69.98 granted and 27% used. When a poll happens before the first usage summary lands the grant cannot be derived, so the card shows the credit balance itself instead of a misleading 0%.

Plan, subscription status, request count, token totals, and the account name appear on the insights panel.

---

## 1. Log in to Command Code

```bash
cmd login
```

`cmd` is the Command Code CLI binary - most onWatch users get here through it, not through pi or Oh My Pi. Logging in writes your credentials to `~/.commandcode/auth.json`. An explicit environment variable always takes priority over the stored login.

---

## 2. Configure onWatch

Use any one of the following.

### Option A - let onWatch find the key (recommended)

Command Code is auto-detected. onWatch reads the credential in this order:

1. `COMMAND_CODE_API_KEY` (or the legacy spelling `COMMANDCODE_API_KEY`) from the environment / `.env`
2. `~/.commandcode/auth.json` - written by `cmd login`
3. `~/.pi/agent/auth.json` - the [pi](https://github.com/earendil-works/pi) agent store, for users who authenticated Command Code inside pi
4. `~/.omp/agent/auth.json` - the Oh My Pi agent store, same arrangement as pi

Supported auth-file shapes:

```json
{ "apiKey": "user_..." }
```

```json
{ "commandcode": { "type": "oauth", "access": "user_..." } }
```

```json
{ "command-code": { "type": "api", "key": "user_..." } }
```

An auth file that is group- or world-readable is ignored: the file holds a bearer key.

To turn tracking off even when credentials are present, set:

```bash
COMMANDCODE_ENABLED=false
```

### Option B - `.env` with explicit key

Add to `~/.onwatch/.env` (best for Docker / headless hosts where no login session exists):

```bash
COMMAND_CODE_API_KEY=user_your_key_here
# Optional: override the API base URL (proxy or compatible endpoint)
# COMMANDCODE_BASE_URL=https://api.commandcode.ai
```

### Option C - setup wizard

```bash
onwatch setup
```

Choose "Command Code only" (or add it under "Multiple" / "All available"). When no login is detected the wizard accepts an optional key and verifies it with one read-only request.

### Option D - dashboard settings

Open Settings, pick the Command Code provider, and save an API key and/or base URL. Changes take effect after daemon restart.

---

## 3. Verify

```bash
# Poll once in the foreground and watch for the Command Code agent
onwatch --debug 2>&1 | grep -i "command code"
```

Then open the dashboard and select the **Command Code** tab. You should see the 5-Hour Credits, Weekly Credits, and Monthly Credits cards with reset countdowns. The menubar/tray companion picks up the provider automatically once it is configured.

---

## Environment variables

| Variable | Purpose |
|---|---|
| `COMMAND_CODE_API_KEY` | Command Code API key. Overrides any stored login. |
| `COMMANDCODE_API_KEY` | Legacy alias for the same key, accepted for compatibility. |
| `COMMANDCODE_ENABLED` | Set `false` to disable Command Code tracking entirely, even with credentials present. |
| `COMMANDCODE_BASE_URL` | Override the API base URL for proxy setups (default: `https://api.commandcode.ai`). |
| `COMMANDCODE_AUTH_PATH` | Override the auth-file path (default: `~/.commandcode/auth.json`, then the pi / OMP stores). |

---

## Cost and polling notes

- Every poll is a set of read-only `GET` requests to the billing endpoints, so it spends **no credits**.
- At the default 120 s interval that is about 720 poll cycles per day, each four small requests.
- To poll less often, raise `ONWATCH_POLL_INTERVAL` or pause the provider from the dashboard (telemetry toggle); polling and history resume when re-enabled.
- onWatch never writes to your Command Code credential files, and it never logs keys (values are redacted as `...` in logs and config dumps).

---

## Troubleshooting

| Symptom | Fix |
|---|---|
| Command Code tab missing | Run `cmd login` (the Command Code CLI), or set `COMMAND_CODE_API_KEY`, then restart the daemon. Check `onwatch --debug` for `Command Code API client configured`. |
| `401 invalid 'Authorization' header` | The stored key is stale or revoked. Log in again with the `cmd` CLI, or paste a fresh key in Settings. |
| `429 rate limited` | Back off and let the next poll retry; the daemon logs the event and keeps the last known values. |
| Monthly card shows a dollar figure but no percentage | The first usage summary has not landed yet, so the period grant cannot be derived. It fills in on the next poll. |
| `403` with a Cloudflare "error 1010" body | The API rejects some client signatures. onWatch sends a product `User-Agent` for this reason; if it still happens, the network path is being filtered - try a different egress. |
| Plan shows only `active` without a name | Command Code reported no `planId`; the account still tracks credits and windows normally. |
| Older cached credential not picked up after changing `.env` | Restart the daemon. Detection of a *new* key from Settings applies on save. |
