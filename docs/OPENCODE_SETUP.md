# OpenCode Go Setup Guide

Track OpenCode Go subscription quotas in onWatch.

OpenCode Go has no documented quota API. onWatch supports two modes:

- **Usage API (recommended).** A console service-account key reads the Go subscription's own meters from `/console/api/go/status`. No browser cookie, and it works with the current OpenCode console.
- **Dashboard scrape (legacy).** Your workspace ID and browser `auth` cookie are used to scrape `/workspace/{id}/go`. Used only when no usage API key is set. The dashboard moved to a new console, so scraping may no longer find the usage data.

---

## Usage API mode (recommended)

1. In the OpenCode console, open **Keys → Add Service Account** and create a key with **usage read** access. Keys look like `oc_sk_...`.
2. Set it (or paste it into **Settings → OpenCode Go → Usage API Key**):

   ```bash
   OPENCODE_GO_API_KEY=oc_sk_...
   ```

**Where the numbers come from.** `GET /console/api/go/status` returns the subscription's three meters, each with a used and a limit amount. They are the figures OpenCode enforces and shows on the Go page. onWatch displays used ÷ limit as the **5-Hour**, **Weekly** and **Monthly** cards, the same cards the scrape mode produces. Nothing is estimated, and there is no price table to keep in sync.

- **5-Hour** is a session window. While a session is open it carries its reset time. With no open session it shows 0% and no reset time.
- **Weekly** resets at the time the API reports.
- **Monthly** renews with the subscription period. Its reset is the period end the API reports, so no reset day needs to be configured.

The meters belong to the Go subscription the key resolves to. To track a different subscription, use a key from that subscription's workspace.

onWatch reuses a status response for 60 seconds, so a poll interval shorter than that does not add requests.

This endpoint is undocumented. The console usage export (`/console/api/v1/usage/export`) is not used: its Go rows carry no cost, and since 25 September 2026 it answers 403 to service-account keys while OpenCode migrates its usage records. See [anomalyco/opencode#50912](https://github.com/anomalyco/opencode/issues/50912), which also describes `go/status`.

---

## Dashboard scrape mode (legacy)

### Prerequisites

- An active [OpenCode Go](https://opencode.ai) subscription
- Access to the OpenCode Go dashboard in a browser
- onWatch installed ([Quick Start](../README.md#quick-start))

---

### How It Works

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

### 1. Find Your Workspace ID

Open https://opencode.ai and sign in, then use either method below.

#### From the browser URL

Open your OpenCode Go usage page. The URL looks like:

```text
https://opencode.ai/workspace/wrk_xxxxxxxx/go
```

Copy the `wrk_...` segment. That is your `OPENCODE_GO_WORKSPACE_ID`.

#### From the authenticated Go page

Copy the `auth` cookie value as described in the next section, then run:

```bash
curl -sS --compressed \
  -H 'Cookie: auth=<your-auth-cookie-value>' \
  https://opencode.ai/go |
  grep -oE 'wrk_[A-Za-z0-9]+' |
  sort -u
```

The authenticated `/go` page contains the workspace ID. The site root does not.
`https://opencode.ai/zen` can also expose it, but `/go` is preferred for an
OpenCode Go subscription.

If the command returns multiple workspace IDs, use the one whose
`https://opencode.ai/workspace/<workspace-id>/go` page shows your Go usage.

---

### 2. Copy the Auth Cookie

1. While signed in on opencode.ai, open your browser Developer Tools
2. Go to **Application** / **Storage** → **Cookies** → `https://opencode.ai`
3. Find the `auth` cookie
4. Copy its **value** (not the `auth=` name prefix)

Treat this cookie like a password. Logging out of OpenCode, rotating sessions, or clearing cookies will invalidate it.

---

### 3. Configure onWatch

Add both values to `~/.onwatch/.env` (or your project `.env`):

```bash
OPENCODE_GO_WORKSPACE_ID=wrk_xxxxxxxx
OPENCODE_GO_AUTH_COOKIE=your_auth_cookie_value
```

In scrape mode both are required. Without them (and without `OPENCODE_GO_API_KEY`) the OpenCode Go provider stays disabled.

You can also set them in the dashboard:

1. Open **Settings → Providers → OpenCode Go**
2. Paste **Workspace ID** and **Auth Cookie**
3. Save

Dashboard values override `.env` for the running process. A daemon restart may still be needed depending on how the agent was started.

---

### 4. Reload / Restart

Reload providers from Settings if available, or restart onWatch:

```bash
onwatch stop
onwatch
```

Or verify in the foreground:

```bash
onwatch --debug
```

You should see the OpenCode agent start once it is configured.

---

### 5. Verify

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
- onWatch redacts `api_key` and `auth_cookie` from `/api/settings` responses
- Scraped HTML and Go status responses are not written to logs
- All processing stays local on your machine

---

## Limitations & Notes

- Scrape mode depends on undocumented dashboard HTML. OpenCode UI changes can break parsing until onWatch is updated.
- Auth failures and parse failures are surfaced as errors. onWatch does **not** invent fake currency quotas when scraping fails.
- Usage API mode depends on the undocumented `go/status` response. If OpenCode changes its shape, onWatch reports a parse failure rather than showing wrong numbers.
- Cookie lifetime is controlled by OpenCode. Expect to refresh the cookie after logout or session rotation.
- In scrape mode the workspace ID is required; onWatch does not auto-discover workspaces.

---

## Troubleshooting

### No OpenCode tab

- Confirm `OPENCODE_GO_API_KEY` is set, or both `OPENCODE_GO_WORKSPACE_ID` and `OPENCODE_GO_AUTH_COOKIE`
- Restart onWatch and check `--debug` logs for missing-config messages
- In Settings → Providers, confirm OpenCode Go shows as configured / polling

### Unauthorized / forbidden / empty data

- Usage API mode: check the service-account key still exists and has usage read access. A 403 on a key that used to work usually means OpenCode changed what service-account keys may read. A 429 means the API is rate limiting; onWatch skips that poll and tries again on the next one.
- Scrape mode: re-copy a fresh `auth` cookie while signed in, confirm the workspace ID matches the `/go` URL, and check the Go dashboard still loads in your browser.
- Restart onWatch.

### Parse failed / response format changed

In usage API mode, OpenCode likely changed the `go/status` response. In scrape mode, it likely changed the dashboard markup. File an issue with:

- Approximate time of failure
- Whether the browser dashboard still shows 5h / weekly / monthly
- **Do not** attach keys, cookies, full HTML dumps or raw `go/status` responses (they contain account IDs)

### Docker / headless

Pass the env vars into the container. There is no local credential auto-detection for OpenCode Go.

```bash
OPENCODE_GO_API_KEY=oc_sk_...
# or, scrape mode:
OPENCODE_GO_WORKSPACE_ID=wrk_xxxxxxxx
OPENCODE_GO_AUTH_COOKIE=your_auth_cookie_value
```

---

## Related

- Main README environment variable reference
- Codex + OpenCode ChatGPT auth (`OPENCODE_ENABLED`) is documented in [CODEX_SETUP.md](CODEX_SETUP.md)
