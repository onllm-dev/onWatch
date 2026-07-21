# OpenCode Go Setup Guide

Track OpenCode Go subscription quotas in onWatch.

OpenCode Go does not expose a public quota API. onWatch scrapes the authenticated OpenCode Go dashboard the same way other tooling does: with your workspace ID and browser `auth` cookie.

---

## Prerequisites

- An active [OpenCode Go](https://opencode.ai) subscription
- Access to the OpenCode Go dashboard in a browser
- onWatch installed ([Quick Start](../README.md#quick-start))

---

## How It Works

onWatch polls:

```text
https://opencode.ai/workspace/{workspaceId}/go
```

using your session cookie, then extracts utilization and reset countdowns for:

- **5-Hour** (rolling / session window)
- **Weekly**
- **Monthly** (when present on the dashboard)

Parsing tries SolidJS SSR hydration data first, then falls back to the newer `data-slot="usage-item"` HTML layout. Snapshots are stored locally in SQLite like every other provider.

This is separate from `OPENCODE_ENABLED`, which only feeds ChatGPT credentials from OpenCode into the **Codex** provider.

---

## 1. Find Your Workspace ID

1. Open https://opencode.ai and sign in
2. Open your OpenCode Go usage page. The URL looks like:

```text
https://opencode.ai/workspace/wrk_xxxxxxxx/go
```

3. Copy the `wrk_...` segment. That is your `OPENCODE_GO_WORKSPACE_ID`.

---

## 2. Copy the Auth Cookie

1. While signed in on opencode.ai, open your browser Developer Tools
2. Go to **Application** / **Storage** → **Cookies** → `https://opencode.ai`
3. Find the `auth` cookie
4. Copy its **value** (not the `auth=` name prefix)

Treat this cookie like a password. Logging out of OpenCode, rotating sessions, or clearing cookies will invalidate it.

---

## 3. Configure onWatch

Add both values to `~/.onwatch/.env` (or your project `.env`):

```bash
OPENCODE_GO_WORKSPACE_ID=wrk_xxxxxxxx
OPENCODE_GO_AUTH_COOKIE=your_auth_cookie_value
```

Both are required. If either is missing, the OpenCode Go provider stays disabled.

You can also set them in the dashboard:

1. Open **Settings → Providers → OpenCode Go**
2. Paste **Workspace ID** and **Auth Cookie**
3. Save

Dashboard values override `.env` for the running process. A daemon restart may still be needed depending on how the agent was started.

---

## 4. Reload / Restart

Reload providers from Settings if available, or restart onWatch:

```bash
onwatch stop
onwatch
```

Or verify in the foreground:

```bash
onwatch --debug
```

You should see the OpenCode agent start when both credentials are present.

---

## 5. Verify

- Open http://localhost:9211
- Switch to the **OpenCode** tab
- Confirm 5-Hour / Weekly cards populate (Monthly appears when OpenCode returns it)
- Charts, cycle overview, and insights begin filling after a few polls

---

## Dashboard

The OpenCode Go tab shows:

- Quota cards with utilization, remaining countdown, and status
- Historical chart across tracked windows
- Billing-cycle / usage-sample tables
- Burn-rate insights for the active windows

---

## Security Notes

- Never commit `.env` or paste the cookie into issue reports / logs
- onWatch redacts `auth_cookie` from `/api/settings` responses
- Scraped HTML is not written to logs
- All processing stays local on your machine

---

## Limitations & Notes

- This integration depends on undocumented dashboard HTML. OpenCode UI changes can break parsing until onWatch is updated.
- Auth failures and parse failures are surfaced as errors. onWatch does **not** invent fake currency quotas when scraping fails.
- Cookie lifetime is controlled by OpenCode. Expect to refresh the cookie after logout or session rotation.
- Workspace ID is required; onWatch does not auto-discover workspaces.

---

## Troubleshooting

### No OpenCode tab

- Confirm both `OPENCODE_GO_WORKSPACE_ID` and `OPENCODE_GO_AUTH_COOKIE` are set
- Restart onWatch and check `--debug` logs for missing-config messages
- In Settings → Providers, confirm OpenCode Go shows as configured / polling

### Unauthorized / forbidden / empty data

1. Re-copy a fresh `auth` cookie while signed in
2. Confirm the workspace ID matches the `/go` URL
3. Restart onWatch
4. Open the Go dashboard in your browser and verify the page still loads

### Parse failed / response format changed

OpenCode likely changed the dashboard markup. File an issue with:

- Approximate time of failure
- Whether the browser dashboard still shows 5h / weekly / monthly
- **Do not** attach cookies or full HTML dumps with session data

### Docker / headless

Pass both env vars into the container. There is no local cookie auto-detection path for OpenCode Go.

```bash
OPENCODE_GO_WORKSPACE_ID=wrk_xxxxxxxx
OPENCODE_GO_AUTH_COOKIE=your_auth_cookie_value
```

---

## Related

- Main README environment variable reference
- Codex + OpenCode ChatGPT auth (`OPENCODE_ENABLED`) is documented in [CODEX_SETUP.md](CODEX_SETUP.md)
